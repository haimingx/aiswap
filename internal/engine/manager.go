package engine

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/haimingx/aiswap/internal/contract"
	"github.com/haimingx/aiswap/internal/domain"
	"github.com/haimingx/aiswap/internal/lock"
	"github.com/haimingx/aiswap/internal/provider"
	"github.com/haimingx/aiswap/internal/store"
)

type Manager struct {
	Store    *store.Store
	Usage    *store.UsageStore
	Registry *provider.Registry
}

func NewManager(st *store.Store, reg *provider.Registry) *Manager {
	return &Manager{
		Store:    st,
		Usage:    store.NewUsageStore(st.Root),
		Registry: reg,
	}
}

type AddResult struct {
	Provider domain.ProviderName
	Slot     int
	Updated  bool
	Identity domain.Identity
	Alias    string
}

func (m *Manager) Add(ctx context.Context, pName domain.ProviderName, alias string) (*AddResult, error) {
	prov, err := m.Registry.Get(pName)
	if err != nil {
		return nil, err
	}
	var normAlias string
	if alias != "" {
		normAlias, err = domain.NormalizeAlias(alias)
		if err != nil {
			return nil, err
		}
	}

	unlockStore, err := lock.AcquireFileLock(ctx, m.Store.LockPath())
	if err != nil {
		return nil, err
	}
	defer unlockStore()

	unlockNative, err := prov.AcquireNativeLock(ctx)
	if err != nil {
		return nil, err
	}
	defer unlockNative()

	live, err := prov.ReadLive(ctx)
	if err != nil {
		return nil, fmt.Errorf("read active %s login: %w", pName, err)
	}
	id, err := prov.Identify(ctx, live)
	if err != nil {
		return nil, fmt.Errorf("identify %s login: %w", pName, err)
	}

	acctFile, err := m.Store.LoadAccounts()
	if err != nil {
		return nil, err
	}
	rec := m.Store.ProviderRecords(acctFile, pName)

	// Check alias collision against other slots.
	if normAlias != "" {
		for _, sl := range rec.Slots {
			if sl.Alias == normAlias && sl.Identity.Key() != id.Key() {
				return nil, fmt.Errorf("alias %q is already assigned to slot %d", normAlias, sl.Slot)
			}
		}
	}

	now := time.Now().UTC()
	existingIdx := -1
	for i := range rec.Slots {
		if rec.Slots[i].Identity.Key() == id.Key() {
			existingIdx = i
			break
		}
	}

	var slotNum int
	updated := false
	if existingIdx >= 0 {
		slotNum = rec.Slots[existingIdx].Slot
		rec.Slots[existingIdx].Identity = *id
		rec.Slots[existingIdx].UpdatedAt = now
		if normAlias != "" {
			rec.Slots[existingIdx].Alias = normAlias
		}
		updated = true
	} else {
		slotNum = m.Store.NextSlot(rec)
		rec.Slots = append(rec.Slots, domain.AccountSlot{
			Slot:      slotNum,
			Alias:     normAlias,
			Identity:  *id,
			AddedAt:   now,
			UpdatedAt: now,
		})
	}
	rec.ActiveSlot = slotNum

	if err := m.Store.SaveCredential(pName, slotNum, live); err != nil {
		return nil, fmt.Errorf("save backup credential: %w", err)
	}
	if err := m.Store.SaveAccounts(acctFile); err != nil {
		return nil, fmt.Errorf("save accounts index: %w", err)
	}

	finalAlias := normAlias
	if existingIdx >= 0 {
		finalAlias = rec.Slots[existingIdx].Alias
	}
	return &AddResult{
		Provider: pName,
		Slot:     slotNum,
		Updated:  updated,
		Identity: *id,
		Alias:    finalAlias,
	}, nil
}

// Switch activates a target slot (number, alias, email, or "next"/"" to rotate).
func (m *Manager) Switch(ctx context.Context, pName domain.ProviderName, target string) (*contract.SwitchResponse, error) {
	prov, err := m.Registry.Get(pName)
	if err != nil {
		return nil, err
	}

	unlockStore, err := lock.AcquireFileLock(ctx, m.Store.LockPath())
	if err != nil {
		return nil, err
	}
	defer unlockStore()

	unlockNative, err := prov.AcquireNativeLock(ctx)
	if err != nil {
		return nil, err
	}
	defer unlockNative()

	acctFile, err := m.Store.LoadAccounts()
	if err != nil {
		return nil, err
	}
	rec := m.Store.ProviderRecords(acctFile, pName)
	if len(rec.Slots) == 0 {
		return nil, fmt.Errorf("no %s accounts managed yet; run 'aiswap add %s' first", pName, pName)
	}

	var targetSlot *domain.AccountSlot
	switch target {
	case "", "next":
		targetSlot, err = pickNextSlot(rec)
		if err != nil {
			return nil, err
		}
	case "best":
		targetSlot, err = m.pickBestSlot(ctx, pName, rec)
		if err != nil {
			return nil, err
		}
	default:
		targetSlot, _, err = store.ResolveSlot(rec, target)
		if err != nil {
			return nil, err
		}
	}

	// Capture current live credential for identity check, rollback, and safe outgoing backup.
	liveBefore, liveErr := prov.ReadLive(ctx)
	var liveBeforeID *domain.Identity
	if liveErr == nil && liveBefore != nil {
		liveBeforeID, _ = prov.Identify(ctx, liveBefore)
	}

	fromSlot := rec.ActiveSlot
	if fromSlot == targetSlot.Slot && liveBeforeID != nil && liveBeforeID.Key() == targetSlot.Identity.Key() {
		return &contract.SwitchResponse{
			SchemaVersion: contract.SchemaVersion,
			Provider:      pName,
			Switched:      false,
			FromSlot:      fromSlot,
			ToSlot:        targetSlot.Slot,
			ToEmail:       targetSlot.Identity.Email,
			Reason:        "already active",
		}, nil
	}

	targetCred, err := m.Store.LoadCredential(pName, targetSlot.Slot)
	if err != nil {
		return nil, fmt.Errorf("load target credential for %s slot %d: %w", pName, targetSlot.Slot, err)
	}

	if liveErr == nil && liveBefore != nil && liveBeforeID != nil {
		// Match live identity against managed slots (not stale rec.ActiveSlot) so rotated tokens
		// are always backed up to the actual outgoing slot.
		if outgoing := store.FindSlotByIdentity(rec, liveBeforeID.Key()); outgoing != nil {
			if err := m.Store.SaveCredential(pName, outgoing.Slot, liveBefore); err != nil {
				return nil, fmt.Errorf("backup outgoing slot %d: %w", outgoing.Slot, err)
			}
			outgoing.Identity = *liveBeforeID
			outgoing.UpdatedAt = time.Now().UTC()
		}
	}

	// Transactional write: write live credential, then update accounts.json; roll back on error.
	if err := prov.WriteLive(ctx, targetCred); err != nil {
		return nil, fmt.Errorf("write target credential: %w", err)
	}
	rec.ActiveSlot = targetSlot.Slot
	if err := m.Store.SaveAccounts(acctFile); err != nil {
		if liveBefore != nil {
			if rbErr := prov.WriteLive(ctx, liveBefore); rbErr != nil {
				return nil, fmt.Errorf("persist active slot: %w (rollback also failed: %v)", err, rbErr)
			}
			return nil, fmt.Errorf("persist active slot (rolled back live credential): %w", err)
		}
		return nil, fmt.Errorf("persist active slot: %w", err)
	}

	return &contract.SwitchResponse{
		SchemaVersion: contract.SchemaVersion,
		Provider:      pName,
		Switched:      true,
		FromSlot:      fromSlot,
		ToSlot:        targetSlot.Slot,
		ToEmail:       targetSlot.Identity.Email,
		Reason:        "switched",
	}, nil
}

func (m *Manager) Remove(ctx context.Context, pName domain.ProviderName, query string) (*domain.AccountSlot, error) {
	if _, err := m.Registry.Get(pName); err != nil {
		return nil, err
	}
	unlockStore, err := lock.AcquireFileLock(ctx, m.Store.LockPath())
	if err != nil {
		return nil, err
	}
	defer unlockStore()

	acctFile, err := m.Store.LoadAccounts()
	if err != nil {
		return nil, err
	}
	rec := m.Store.ProviderRecords(acctFile, pName)
	sl, idx, err := store.ResolveSlot(rec, query)
	if err != nil {
		return nil, err
	}
	removed := *sl
	rec.Slots = append(rec.Slots[:idx], rec.Slots[idx+1:]...)
	if rec.ActiveSlot == removed.Slot {
		rec.ActiveSlot = 0
	}
	if err := m.Store.SaveAccounts(acctFile); err != nil {
		return nil, fmt.Errorf("save accounts: %w", err)
	}
	if err := m.Store.DeleteCredential(pName, removed.Slot); err != nil {
		return nil, fmt.Errorf("delete stored credential: %w", err)
	}
	return &removed, nil
}

func (m *Manager) SetAlias(ctx context.Context, pName domain.ProviderName, query, alias string) (*domain.AccountSlot, error) {
	if _, err := m.Registry.Get(pName); err != nil {
		return nil, err
	}
	var norm string
	if alias != "" {
		var err error
		norm, err = domain.NormalizeAlias(alias)
		if err != nil {
			return nil, err
		}
	}

	unlockStore, err := lock.AcquireFileLock(ctx, m.Store.LockPath())
	if err != nil {
		return nil, err
	}
	defer unlockStore()

	acctFile, err := m.Store.LoadAccounts()
	if err != nil {
		return nil, err
	}
	rec := m.Store.ProviderRecords(acctFile, pName)
	sl, _, err := store.ResolveSlot(rec, query)
	if err != nil {
		return nil, err
	}
	if norm != "" {
		for _, other := range rec.Slots {
			if other.Slot != sl.Slot && other.Alias == norm {
				return nil, fmt.Errorf("alias %q is already assigned to slot %d", norm, other.Slot)
			}
		}
	}
	sl.Alias = norm
	sl.UpdatedAt = time.Now().UTC()
	if err := m.Store.SaveAccounts(acctFile); err != nil {
		return nil, err
	}
	cp := *sl
	return &cp, nil
}

func (m *Manager) SetDisabled(ctx context.Context, pName domain.ProviderName, query string, disabled bool) (*domain.AccountSlot, error) {
	if _, err := m.Registry.Get(pName); err != nil {
		return nil, err
	}
	unlockStore, err := lock.AcquireFileLock(ctx, m.Store.LockPath())
	if err != nil {
		return nil, err
	}
	defer unlockStore()

	acctFile, err := m.Store.LoadAccounts()
	if err != nil {
		return nil, err
	}
	rec := m.Store.ProviderRecords(acctFile, pName)
	sl, _, err := store.ResolveSlot(rec, query)
	if err != nil {
		return nil, err
	}
	sl.Disabled = disabled
	sl.UpdatedAt = time.Now().UTC()
	if err := m.Store.SaveAccounts(acctFile); err != nil {
		return nil, err
	}
	cp := *sl
	return &cp, nil
}

func (m *Manager) Status(ctx context.Context, filter ...domain.ProviderName) (*contract.StatusResponse, error) {
	return m.StatusWithOptions(ctx, StatusOptions{FetchQuota: false}, filter...)
}

func (m *Manager) resolveProviders(filter ...domain.ProviderName) ([]provider.Provider, error) {
	if len(filter) == 0 {
		return m.Registry.All(), nil
	}
	providers := make([]provider.Provider, 0, len(filter))
	for _, name := range filter {
		p, err := m.Registry.Get(name)
		if err != nil {
			return nil, err
		}
		providers = append(providers, p)
	}
	return providers, nil
}

func (m *Manager) StatusWithOptions(ctx context.Context, opts StatusOptions, filter ...domain.ProviderName) (*contract.StatusResponse, error) {
	acctFile, err := m.Store.LoadAccounts()
	if err != nil {
		return nil, err
	}

	providers, err := m.resolveProviders(filter...)
	if err != nil {
		return nil, err
	}

	resp := &contract.StatusResponse{
		SchemaVersion: contract.SchemaVersion,
		GeneratedAt:   time.Now().UTC(),
		Providers:     make([]contract.ProviderStatus, len(providers)),
	}

	type quotaTask struct {
		provIdx  int
		acctIdx  int // -1 means unmanaged live account
		prov     provider.Provider
		cacheKey string
		cred     *domain.LiveCredential
		slot     int
		isActive bool
	}
	var tasks []quotaTask

	for i, prov := range providers {
		ps := contract.ProviderStatus{
			Provider:  prov.Name(),
			Installed: prov.IsInstalled(),
			Accounts:  make([]contract.AccountRow, 0),
		}
		rec := m.Store.ProviderRecords(acctFile, prov.Name())

		var liveCred *domain.LiveCredential
		var liveID *domain.Identity
		if live, err := prov.ReadLive(ctx); err == nil && live != nil {
			liveCred = live
			if id, idErr := prov.Identify(ctx, live); idErr == nil {
				liveID = id
				ps.LiveEmail = id.Email
				ps.LivePlan = id.Plan
			} else {
				ps.Error = idErr.Error()
			}
		} else if err != nil && !errors.Is(err, provider.ErrNotLoggedIn) {
			ps.Error = err.Error()
		}

		activeSlot := rec.ActiveSlot
		if liveID != nil {
			if matched := store.FindSlotByIdentity(rec, liveID.Key()); matched != nil {
				activeSlot = matched.Slot
				ps.Managed = true
			} else if opts.FetchQuota {
				tasks = append(tasks, quotaTask{
					provIdx:  i,
					acctIdx:  -1,
					prov:     prov,
					cacheKey: quotaCacheKey(prov.Name(), liveID.Key()),
					cred:     liveCred,
					slot:     0,
					isActive: true,
				})
			}
		}
		ps.ActiveSlot = activeSlot

		for j, sl := range rec.Slots {
			isActive := sl.Slot == activeSlot && ps.Managed
			row := contract.AccountRow{
				Slot:        sl.Slot,
				Alias:       sl.Alias,
				Email:       sl.Identity.Email,
				OrgName:     sl.Identity.OrgName,
				Plan:        sl.Identity.Plan,
				Active:      isActive,
				Disabled:    sl.Disabled,
				QuotaStatus: domain.QuotaUnavailable,
			}
			ps.Accounts = append(ps.Accounts, row)

			if opts.FetchQuota {
				var cred *domain.LiveCredential
				if isActive && liveCred != nil {
					cred = liveCred
				} else {
					var loadErr error
					cred, loadErr = m.Store.LoadCredential(prov.Name(), sl.Slot)
					if loadErr != nil {
						ps.Accounts[j].QuotaError = loadErr.Error()
						continue
					}
				}
				tasks = append(tasks, quotaTask{
					provIdx:  i,
					acctIdx:  j,
					prov:     prov,
					cacheKey: quotaCacheKey(prov.Name(), sl.Identity.Key()),
					cred:     cred,
					slot:     sl.Slot,
					isActive: isActive,
				})
			}
		}
		resp.Providers[i] = ps
	}

	// Execute quota fetches concurrently with bounded parallelism (4 workers).
	if len(tasks) > 0 {
		sem := make(chan struct{}, 4)
		var wg sync.WaitGroup
		for _, t := range tasks {
			wg.Add(1)
			go func() {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()

				qStatus, qPayload, qErr, qPlan := m.collectQuota(ctx, t.prov, t.cacheKey, t.cred, t.slot, t.isActive, opts.Force)
				ps := &resp.Providers[t.provIdx]
				if t.acctIdx < 0 {
					ps.LiveQuotaStatus = qStatus
					ps.LiveQuota = qPayload
					ps.LiveQuotaError = qErr
					if qPlan != "" {
						ps.LivePlan = qPlan
					}
					return
				}
				row := &ps.Accounts[t.acctIdx]
				row.QuotaStatus = qStatus
				row.Quota = qPayload
				row.QuotaError = qErr
				if qPlan != "" {
					row.Plan = qPlan
				}
				if t.isActive {
					ps.LiveQuotaStatus = qStatus
					ps.LiveQuota = qPayload
					ps.LiveQuotaError = qErr
					if qPlan != "" {
						ps.LivePlan = qPlan
					}
				}
			}()
		}
		wg.Wait()
	}

	return resp, nil
}

// pickBestSlot chooses the enabled slot with the lowest binding (highest-window) utilization
// from UsageStore cache; falls back to next-slot rotation when no quota readings exist.
func (m *Manager) pickBestSlot(ctx context.Context, pName domain.ProviderName, rec *store.ProviderRecords) (*domain.AccountSlot, error) {
	var best *domain.AccountSlot
	bestUsed := math.MaxFloat64
	anyMeasured := false

	var snap map[string]*store.UsageRow
	if m.Usage != nil {
		snap, _ = m.Usage.Snapshot(ctx)
	}

	for i := range rec.Slots {
		sl := &rec.Slots[i]
		if sl.Disabled {
			continue
		}
		if snap != nil {
			if row := snap[quotaCacheKey(pName, sl.Identity.Key())]; row != nil && row.LastGood != nil && len(row.LastGood.Windows) > 0 {
				binding := 0.0
				for _, w := range row.LastGood.Windows {
					if w.UsedPct > binding {
						binding = w.UsedPct
					}
				}
				anyMeasured = true
				if binding < bestUsed {
					bestUsed = binding
					best = sl
				}
			}
		}
	}
	if anyMeasured && best != nil {
		return best, nil
	}
	return pickNextSlot(rec)
}

func pickNextSlot(rec *store.ProviderRecords) (*domain.AccountSlot, error) {
	var enabled []int
	for i := range rec.Slots {
		if !rec.Slots[i].Disabled {
			enabled = append(enabled, i)
		}
	}
	if len(enabled) == 0 {
		return nil, errors.New("no enabled accounts available for rotation")
	}
	if len(enabled) == 1 {
		return &rec.Slots[enabled[0]], nil
	}
	for pos, idx := range enabled {
		if rec.Slots[idx].Slot == rec.ActiveSlot {
			nextIdx := enabled[(pos+1)%len(enabled)]
			return &rec.Slots[nextIdx], nil
		}
	}
	return &rec.Slots[enabled[0]], nil
}
