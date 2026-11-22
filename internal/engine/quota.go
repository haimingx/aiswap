package engine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/haimingx/aiswap/internal/contract"
	"github.com/haimingx/aiswap/internal/domain"
	"github.com/haimingx/aiswap/internal/lock"
	"github.com/haimingx/aiswap/internal/provider"
)

var errPermanentRefresh = errors.New("permanent refresh failure")

type StatusOptions struct {
	FetchQuota bool
	Force      bool
}

// ConsumeRefreshGate serializes OAuth refresh per (provider, slot) so a one-time
// refresh token is never double-posted, and guarantees the successor token is
// persisted or stashed in unclaimed/ even if normal save fails.
func (m *Manager) ConsumeRefreshGate(ctx context.Context, pName domain.ProviderName, slot int) (*domain.LiveCredential, error) {
	return m.consumeRefreshGate(ctx, pName, slot, nil)
}

func (m *Manager) consumeRefreshGate(ctx context.Context, pName domain.ProviderName, slot int, staleCred *domain.LiveCredential) (*domain.LiveCredential, error) {
	prov, err := m.Registry.Get(pName)
	if err != nil {
		return nil, err
	}
	unlockStore, err := lock.AcquireFileLock(ctx, m.Store.LockPath())
	if err != nil {
		return nil, err
	}
	defer unlockStore()

	gateLock := filepath.Join(m.Store.Root, fmt.Sprintf("refresh-%s-%d.lock", pName, slot))
	unlock, err := lock.AcquireFileLock(ctx, gateLock)
	if err != nil {
		return nil, err
	}
	defer unlock()

	cred, err := m.Store.LoadCredential(pName, slot)
	if err != nil {
		return nil, fmt.Errorf("load credential for refresh: %w", err)
	}
	// Another waiter already rotated this slot's credential while we waited on the lock.
	if staleCred != nil && !bytes.Equal(cred.Raw, staleCred.Raw) {
		return cred, nil
	}
	outcome := prov.Refresh(ctx, cred)
	if outcome.Err != nil {
		if outcome.Permanent {
			return nil, fmt.Errorf("%w: %v", errPermanentRefresh, outcome.Err)
		}
		return nil, outcome.Err
	}
	if outcome.Credential == nil {
		return nil, errors.New("refresh returned nil credential without error")
	}

	// Successor token MUST never be dropped.
	if err := m.Store.SaveCredential(pName, slot, outcome.Credential); err != nil {
		stashPath, stashErr := m.Store.StashUnclaimed(pName, slot, "save_failed_after_refresh", outcome.Credential)
		if stashErr != nil {
			return outcome.Credential, fmt.Errorf("save rotated token failed (%w) and stash failed (%v)", err, stashErr)
		}
		return outcome.Credential, fmt.Errorf("save rotated token failed (%w); stashed safely at %s", err, stashPath)
	}
	return outcome.Credential, nil
}

func (m *Manager) collectQuota(
	ctx context.Context,
	prov provider.Provider,
	cacheKey string,
	cred *domain.LiveCredential,
	slot int, // 0 if unmanaged live account
	isActive bool,
	force bool,
) (domain.QuotaStatus, *contract.QuotaPayload, string, string) {
	if m.Usage == nil || cred == nil {
		return domain.QuotaUnavailable, nil, "", ""
	}
	shouldFetch, cached, err := m.Usage.Reserve(ctx, cacheKey, force)
	if err != nil {
		return domain.QuotaUnavailable, nil, err.Error(), ""
	}

	if shouldFetch {
		reading, fetchErr := prov.FetchQuota(ctx, cred)
		// On http-401, attempt one safe OAuth refresh under the proper lock, then retry quota once.
		var qe *provider.QuotaError
		var permanentRefresh bool
		if fetchErr != nil && errors.As(fetchErr, &qe) && qe.Kind == "http-401" {
			refreshed, rErr := m.safeRefreshOn401(ctx, prov, slot, isActive, cred)
			if rErr == nil && refreshed != nil {
				reading, fetchErr = prov.FetchQuota(ctx, refreshed)
			} else if errors.Is(rErr, errPermanentRefresh) {
				permanentRefresh = true
			}
		}

		if fetchErr == nil && reading != nil {
			recCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			_ = m.Usage.RecordSuccess(recCtx, cacheKey, reading)
			cancel()
			return domain.QuotaOK, toQuotaPayload(reading, false), "", reading.Plan
		}

		// Do not poison backoff on caller cancellation; release the in-flight lease cleanly.
		if ctx.Err() != nil {
			relCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			_ = m.Usage.ReleaseClaim(relCtx, cacheKey)
			cancel()
			if cached != nil && cached.LastGood != nil {
				return domain.QuotaOK, toQuotaPayload(cached.LastGood, true), "", cached.LastGood.Plan
			}
			return domain.QuotaUnavailable, nil, "", ""
		}

		errKind := "error"
		var retryAfter time.Duration
		if permanentRefresh {
			errKind = "relogin_required"
		} else if errors.As(fetchErr, &qe) {
			errKind = qe.Kind
			retryAfter = qe.RetryAfter
		} else if fetchErr != nil {
			errKind = fetchErr.Error()
		}
		recCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = m.Usage.RecordFailure(recCtx, cacheKey, errKind, retryAfter)
		cancel()

		status := mapErrorToQuotaStatus(errKind)
		if cached != nil && cached.LastGood != nil {
			return status, toQuotaPayload(cached.LastGood, true), errKind, cached.LastGood.Plan
		}
		return status, nil, errKind, ""
	}

	if cached != nil && cached.LastGood != nil {
		stale := cached.LastError != "" || m.Usage.Now().Sub(cached.LastGood.FetchedAt) > m.Usage.ServeTTL
		status := domain.QuotaOK
		if cached.LastError != "" {
			status = mapErrorToQuotaStatus(cached.LastError)
		}
		return status, toQuotaPayload(cached.LastGood, stale), cached.LastError, cached.LastGood.Plan
	}
	if cached != nil && cached.LastError != "" {
		return mapErrorToQuotaStatus(cached.LastError), nil, cached.LastError, ""
	}
	return domain.QuotaUnavailable, nil, "", ""
}

// safeRefreshOn401 refreshes an expired credential without colliding with the running CLI.
func (m *Manager) safeRefreshOn401(
	ctx context.Context,
	prov provider.Provider,
	slot int,
	isActive bool,
	cred *domain.LiveCredential,
) (*domain.LiveCredential, error) {
	if isActive || slot == 0 {
		// Preserve global lock order (StoreLock -> NativeLock) when updating a managed slot.
		if slot > 0 {
			unlockStore, err := lock.AcquireFileLock(ctx, m.Store.LockPath())
			if err != nil {
				return nil, err
			}
			defer unlockStore()
		}
		unlockNative, err := prov.AcquireNativeLock(ctx)
		if err != nil {
			return nil, err
		}
		defer unlockNative()

		var expectedKey string
		if cred != nil {
			if id, idErr := prov.Identify(ctx, cred); idErr == nil && id != nil {
				expectedKey = id.Key()
			}
		}

		latest, rErr := prov.ReadLive(ctx)
		if rErr != nil || latest == nil {
			return nil, fmt.Errorf("read live credential under lock: %w", rErr)
		}
		if expectedKey != "" {
			if latestID, idErr := prov.Identify(ctx, latest); idErr == nil && latestID != nil && latestID.Key() != expectedKey {
				// Another process switched the live account while we waited on the lock.
				return nil, errors.New("live account changed during refresh")
			}
		}
		if cred != nil && !bytes.Equal(latest.Raw, cred.Raw) {
			// Another process already rotated the same live account's credential.
			return latest, nil
		}
		out := prov.Refresh(ctx, latest)
		if out.Err != nil {
			if out.Permanent {
				return nil, fmt.Errorf("%w: %v", errPermanentRefresh, out.Err)
			}
			return nil, out.Err
		}
		if out.Credential == nil {
			return nil, errors.New("refresh returned nil credential without error")
		}
		if wErr := prov.WriteLive(ctx, out.Credential); wErr != nil {
			_, _ = m.Store.StashUnclaimed(prov.Name(), slot, "write_live_failed_after_refresh", out.Credential)
			return out.Credential, wErr
		}
		if slot > 0 {
			if sErr := m.Store.SaveCredential(prov.Name(), slot, out.Credential); sErr != nil {
				_, _ = m.Store.StashUnclaimed(prov.Name(), slot, "save_failed_after_live_refresh", out.Credential)
			}
		}
		return out.Credential, nil
	}
	return m.consumeRefreshGate(ctx, prov.Name(), slot, cred)
}

func mapErrorToQuotaStatus(errKind string) domain.QuotaStatus {
	switch errKind {
	case "http-429":
		return domain.QuotaRateLimited
	case "http-401":
		return domain.QuotaTokenExpired
	case "relogin_required":
		return domain.QuotaReloginRequired
	default:
		return domain.QuotaUnavailable
	}
}

func toQuotaPayload(r *domain.QuotaReading, stale bool) *contract.QuotaPayload {
	if r == nil {
		return nil
	}
	return &contract.QuotaPayload{
		Windows:   r.Windows,
		FetchedAt: r.FetchedAt,
		Stale:     stale,
	}
}

func quotaCacheKey(p domain.ProviderName, idKey string) string {
	return string(p) + ":" + idKey
}
