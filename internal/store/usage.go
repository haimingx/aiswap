package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/haimingx/aiswap/internal/domain"
	"github.com/haimingx/aiswap/internal/fsutil"
	"github.com/haimingx/aiswap/internal/lock"
)

const (
	usageSchemaVersion   = 1
	DefaultServeTTL      = 180 * time.Second // Sustained-rate floor (<=20 req/hr per account)
	DefaultClaimTTL      = 90 * time.Second
	DefaultBackoffBase   = 30 * time.Second
	DefaultBackoffCap    = 10 * time.Minute
	DefaultRateLimitWait = 5 * time.Minute
)

type UsageRow struct {
	LastGood     *domain.QuotaReading `json:"lastGood,omitempty"`
	ClaimUntil   *time.Time           `json:"claimUntil,omitempty"`
	NextRetryAt  *time.Time           `json:"nextRetryAt,omitempty"`
	FailureCount int                  `json:"failureCount,omitempty"`
	LastError    string               `json:"lastError,omitempty"`
	LastErrorAt  *time.Time           `json:"lastErrorAt,omitempty"`
}

type usageFile struct {
	SchemaVersion int                  `json:"schemaVersion"`
	Rows          map[string]*UsageRow `json:"rows"`
}

// UsageStore coordinates quota fetches across CLI and menu bar processes
// using a leased cache file so concurrent surfaces never double-fetch or trigger 429 bursts.
type UsageStore struct {
	Root     string
	ServeTTL time.Duration
	ClaimTTL time.Duration
	Now      func() time.Time
}

func NewUsageStore(root string) *UsageStore {
	return &UsageStore{
		Root:     root,
		ServeTTL: DefaultServeTTL,
		ClaimTTL: DefaultClaimTTL,
		Now:      func() time.Time { return time.Now().UTC() },
	}
}

func (u *UsageStore) filePath() string {
	return filepath.Join(u.Root, "usage.json")
}

func (u *UsageStore) lockPath() string {
	return filepath.Join(u.Root, "usage.lock")
}

func (u *UsageStore) loadUnlocked() (*usageFile, error) {
	raw, err := os.ReadFile(u.filePath())
	if errors.Is(err, os.ErrNotExist) {
		return &usageFile{SchemaVersion: usageSchemaVersion, Rows: map[string]*UsageRow{}}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read usage.json: %w", err)
	}
	var f usageFile
	if err := json.Unmarshal(raw, &f); err != nil {
		// Corrupted cache should self-heal instead of blocking quota display.
		return &usageFile{SchemaVersion: usageSchemaVersion, Rows: map[string]*UsageRow{}}, nil
	}
	if f.Rows == nil {
		f.Rows = map[string]*UsageRow{}
	}
	return &f, nil
}

func (u *UsageStore) saveUnlocked(f *usageFile) error {
	f.SchemaVersion = usageSchemaVersion
	return fsutil.AtomicWriteJSON(u.filePath(), f, 0o600)
}

func (u *UsageStore) Snapshot(ctx context.Context) (map[string]*UsageRow, error) {
	unlock, err := lock.AcquireFileLock(ctx, u.lockPath())
	if err != nil {
		return nil, err
	}
	defer unlock()

	f, err := u.loadUnlocked()
	if err != nil {
		return nil, err
	}
	out := make(map[string]*UsageRow, len(f.Rows))
	for k, row := range f.Rows {
		if row != nil {
			cp := *row
			out[k] = &cp
		}
	}
	return out, nil
}

func (u *UsageStore) Get(ctx context.Context, key string) (*UsageRow, error) {
	unlock, err := lock.AcquireFileLock(ctx, u.lockPath())
	if err != nil {
		return nil, err
	}
	defer unlock()

	f, err := u.loadUnlocked()
	if err != nil {
		return nil, err
	}
	row, ok := f.Rows[key]
	if !ok {
		return nil, nil
	}
	cp := *row
	return &cp, nil
}

// Reserve checks whether key needs a fresh network fetch.
// If shouldFetch is true, a ClaimUntil lease is written before releasing the lock.
func (u *UsageStore) Reserve(ctx context.Context, key string, force bool) (shouldFetch bool, cached *UsageRow, err error) {
	unlock, err := lock.AcquireFileLock(ctx, u.lockPath())
	if err != nil {
		return false, nil, err
	}
	defer unlock()

	f, err := u.loadUnlocked()
	if err != nil {
		return false, nil, err
	}
	now := u.Now()
	row := f.Rows[key]
	if row == nil {
		row = &UsageRow{}
		f.Rows[key] = row
	}
	cp := *row

	// Respect active in-flight lease from another process even on force.
	if row.ClaimUntil != nil && now.Before(*row.ClaimUntil) {
		return false, &cp, nil
	}
	if !force {
		if row.NextRetryAt != nil && now.Before(*row.NextRetryAt) {
			return false, &cp, nil
		}
		if row.LastGood != nil && now.Sub(row.LastGood.FetchedAt) < u.ServeTTL {
			return false, &cp, nil
		}
	}

	claimUntil := now.Add(u.ClaimTTL)
	row.ClaimUntil = &claimUntil
	if err := u.saveUnlocked(f); err != nil {
		return false, &cp, err
	}
	return true, &cp, nil
}

func (u *UsageStore) RecordSuccess(ctx context.Context, key string, reading *domain.QuotaReading) error {
	unlock, err := lock.AcquireFileLock(ctx, u.lockPath())
	if err != nil {
		return err
	}
	defer unlock()

	f, err := u.loadUnlocked()
	if err != nil {
		return err
	}
	f.Rows[key] = &UsageRow{
		LastGood: reading,
	}
	return u.saveUnlocked(f)
}

// ReleaseClaim clears an active in-flight lease without recording a failure or backoff
// (used when a fetch is canceled by caller context).
func (u *UsageStore) ReleaseClaim(ctx context.Context, key string) error {
	unlock, err := lock.AcquireFileLock(ctx, u.lockPath())
	if err != nil {
		return err
	}
	defer unlock()

	f, err := u.loadUnlocked()
	if err != nil {
		return err
	}
	if row := f.Rows[key]; row != nil && row.ClaimUntil != nil {
		row.ClaimUntil = nil
		return u.saveUnlocked(f)
	}
	return nil
}

// RecordFailure preserves LastGood (stale-on-error) and schedules exponential or Retry-After backoff.
func (u *UsageStore) RecordFailure(ctx context.Context, key, errKind string, retryAfter time.Duration) error {
	unlock, err := lock.AcquireFileLock(ctx, u.lockPath())
	if err != nil {
		return err
	}
	defer unlock()

	f, err := u.loadUnlocked()
	if err != nil {
		return err
	}
	now := u.Now()
	row := f.Rows[key]
	if row == nil {
		row = &UsageRow{}
		f.Rows[key] = row
	}
	row.ClaimUntil = nil
	row.FailureCount++
	row.LastError = errKind
	row.LastErrorAt = &now

	wait := computeBackoff(errKind, row.FailureCount, retryAfter)
	next := now.Add(wait)
	row.NextRetryAt = &next
	return u.saveUnlocked(f)
}

func computeBackoff(errKind string, failures int, retryAfter time.Duration) time.Duration {
	if errKind == "http-429" {
		if retryAfter > DefaultRateLimitWait {
			return retryAfter
		}
		return DefaultRateLimitWait
	}
	if retryAfter > 0 {
		return retryAfter
	}
	shift := failures - 1
	if shift < 0 {
		shift = 0
	}
	if shift > 10 {
		shift = 10
	}
	wait := DefaultBackoffBase << shift
	if wait > DefaultBackoffCap {
		return DefaultBackoffCap
	}
	return wait
}
