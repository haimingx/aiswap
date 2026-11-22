package lock

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAcquireFileLockExclusive(t *testing.T) {
	p := filepath.Join(t.TempDir(), "store.lock")
	unlock, err := AcquireFileLock(context.Background(), p)
	if err != nil {
		t.Fatalf("first lock failed: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
	defer cancel()
	if _, err := AcquireFileLock(ctx, p); err == nil {
		t.Fatal("expected second lock to time out while first is held")
	}

	unlock()
	unlock2, err := AcquireFileLock(context.Background(), p)
	if err != nil {
		t.Fatalf("re-acquire after unlock failed: %v", err)
	}
	unlock2()
}

func TestAcquireDirLockAndStaleTakeover(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "cred.lock")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatalf("seed stale lock: %v", err)
	}
	old := time.Now().Add(-10 * time.Second)
	if err := os.Chtimes(dir, old, old); err != nil {
		t.Fatalf("chtimes stale lock: %v", err)
	}

	unlock, err := AcquireDirLock(context.Background(), dir, 2*time.Second, 500*time.Millisecond)
	if err != nil {
		t.Fatalf("expected stale lock takeover to succeed: %v", err)
	}
	defer unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if _, err := AcquireDirLock(ctx, dir, 2*time.Second, 500*time.Millisecond); err == nil {
		t.Fatal("expected live dir lock to block second waiter")
	}
}
