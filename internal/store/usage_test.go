package store

import (
	"context"
	"testing"
	"time"

	"github.com/haimingx/aiswap/internal/domain"
)

func TestUsageStoreLeaseFreshnessAndStaleOnError(t *testing.T) {
	now := time.Date(2026, 11, 22, 5, 0, 0, 0, time.UTC)
	us := NewUsageStore(t.TempDir())
	us.Now = func() time.Time { return now }
	ctx := context.Background()

	// 1. First reserve must claim lease.
	fetch, _, err := us.Reserve(ctx, "claude:u1", false)
	if err != nil || !fetch {
		t.Fatalf("first Reserve = (%v, %v), want (true, nil)", fetch, err)
	}

	// 2. Concurrent caller sees active lease and skips fetch.
	fetch2, _, err := us.Reserve(ctx, "claude:u1", false)
	if err != nil || fetch2 {
		t.Fatalf("second Reserve during lease = (%v, %v), want (false, nil)", fetch2, err)
	}

	// 3. Record success -> served from cache within ServeTTL.
	reading := &domain.QuotaReading{
		Windows:   []domain.QuotaWindow{{Name: "5h", UsedPct: 30}},
		FetchedAt: now,
	}
	if err := us.RecordSuccess(ctx, "claude:u1", reading); err != nil {
		t.Fatal(err)
	}
	now = now.Add(60 * time.Second)
	fetch3, cached, err := us.Reserve(ctx, "claude:u1", false)
	if err != nil || fetch3 || cached == nil || cached.LastGood == nil || cached.LastGood.Windows[0].UsedPct != 30 {
		t.Fatalf("expected fresh cached reading, got fetch=%v cached=%+v err=%v", fetch3, cached, err)
	}

	// 4. After TTL expires, failure must preserve LastGood and back off on 429.
	now = now.Add(200 * time.Second)
	fetch4, _, err := us.Reserve(ctx, "claude:u1", false)
	if err != nil || !fetch4 {
		t.Fatalf("expected fetch after TTL, got fetch=%v err=%v", fetch4, err)
	}
	if err := us.RecordFailure(ctx, "claude:u1", "http-429", 0); err != nil {
		t.Fatal(err)
	}
	fetch5, staleRow, err := us.Reserve(ctx, "claude:u1", false)
	if err != nil || fetch5 {
		t.Fatalf("expected backoff after 429, got fetch=%v err=%v", fetch5, err)
	}
	if staleRow == nil || staleRow.LastGood == nil || staleRow.LastGood.Windows[0].UsedPct != 30 {
		t.Fatalf("expected LastGood preserved on error, got %+v", staleRow)
	}
}
