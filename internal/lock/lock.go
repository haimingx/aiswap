package lock

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

// FileLock acquires an exclusive advisory flock on path, respecting ctx cancellation.
func AcquireFileLock(ctx context.Context, path string) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("mkdir lock parent %s: %w", path, err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open lock file %s: %w", path, err)
	}
	for {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() {
				_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
				_ = f.Close()
			}, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			_ = f.Close()
			return nil, fmt.Errorf("flock %s: %w", path, err)
		}
		select {
		case <-ctx.Done():
			_ = f.Close()
			return nil, fmt.Errorf("timeout waiting for lock %s: %w", path, ctx.Err())
		case <-time.After(40 * time.Millisecond):
		}
	}
}

// AcquireDirLock implements the npm proper-lockfile directory-mutex protocol
// used by Claude Code (.oauth_refresh.lock, ~/.claude.lock, ~/.claude.json.lock).
func AcquireDirLock(ctx context.Context, lockDir string, staleness, touchInterval time.Duration) (func(), error) {
	if staleness <= 0 || touchInterval <= 0 {
		return nil, fmt.Errorf("invalid dir lock durations: staleness=%v touchInterval=%v", staleness, touchInterval)
	}
	if err := os.MkdirAll(filepath.Dir(lockDir), 0o700); err != nil {
		return nil, fmt.Errorf("mkdir lock parent %s: %w", lockDir, err)
	}
	for {
		err := os.Mkdir(lockDir, 0o700)
		if err == nil {
			break
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("mkdir lock %s: %w", lockDir, err)
		}
		if info, statErr := os.Stat(lockDir); statErr == nil {
			if time.Since(info.ModTime()) > staleness {
				if removeIfStale(lockDir, staleness) {
					continue
				}
			}
		}
		jitter := time.Duration(100+rand.Intn(150)) * time.Millisecond
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("timeout waiting for dir lock %s: %w", lockDir, ctx.Err())
		case <-time.After(jitter):
		}
	}

	createdInfo, _ := os.Stat(lockDir)
	isOwned := func() bool {
		if createdInfo == nil {
			return true
		}
		curInfo, err := os.Stat(lockDir)
		return err == nil && os.SameFile(createdInfo, curInfo)
	}

	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(touchInterval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				if !isOwned() {
					return
				}
				now := time.Now()
				_ = os.Chtimes(lockDir, now, now)
			}
		}
	}()

	var unlockOnce sync.Once
	return func() {
		unlockOnce.Do(func() {
			close(stop)
			<-done
			if isOwned() {
				_ = os.Remove(lockDir)
			}
		})
	}, nil
}

// removeIfStale serializes stale-lock cleanup via a sibling .takeover dir
// so two waiters cannot delete a freshly acquired lock from the first waiter.
func removeIfStale(lockDir string, staleness time.Duration) bool {
	takeover := lockDir + ".takeover"
	if err := os.Mkdir(takeover, 0o700); err != nil {
		if info, statErr := os.Lstat(takeover); statErr == nil && time.Since(info.ModTime()) > 5*time.Second {
			_ = os.Remove(takeover)
		}
		return false
	}
	defer func() { _ = os.Remove(takeover) }()

	info, err := os.Stat(lockDir)
	if err != nil || time.Since(info.ModTime()) <= staleness {
		return false
	}
	return os.Remove(lockDir) == nil
}
