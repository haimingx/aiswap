package keychain

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/haimingx/aiswap/internal/fsutil"
)

var ErrNotFound = errors.New("credential not found")

type Store interface {
	Get(service, account string) ([]byte, error)
	Set(service, account string, secret []byte) error
	Delete(service, account string) error
	Backend() string
}

// FileStore persists secrets as 0600 files under a root directory (Linux default / fallback).
type FileStore struct {
	Root string
}

func (f *FileStore) Backend() string { return "file" }

func (f *FileStore) secretPath(service, account string) string {
	safe := func(s string) string {
		r := strings.NewReplacer("/", "_", "\\", "_", "..", "_", " ", "_")
		return r.Replace(s)
	}
	return filepath.Join(f.Root, safe(service), safe(account)+".secret")
}

func (f *FileStore) Get(service, account string) ([]byte, error) {
	data, err := os.ReadFile(f.secretPath(service, account))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("read secret %s/%s: %w", service, account, err)
	}
	return data, nil
}

func (f *FileStore) Set(service, account string, secret []byte) error {
	return fsutil.AtomicWriteFile(f.secretPath(service, account), secret, 0o600)
}

func (f *FileStore) Delete(service, account string) error {
	err := os.Remove(f.secretPath(service, account))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// MemoryStore is an in-memory Store for unit tests (never touches OS keychain or disk).
type MemoryStore struct {
	mu    sync.RWMutex
	items map[string][]byte
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{items: make(map[string][]byte)}
}

func (m *MemoryStore) Backend() string { return "memory" }

func (m *MemoryStore) Get(service, account string) ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	v, ok := m.items[service+"\x00"+account]
	if !ok {
		return nil, ErrNotFound
	}
	cp := make([]byte, len(v))
	copy(cp, v)
	return cp, nil
}

func (m *MemoryStore) Set(service, account string, secret []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]byte, len(secret))
	copy(cp, secret)
	m.items[service+"\x00"+account] = cp
	return nil
}

func (m *MemoryStore) Delete(service, account string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.items, service+"\x00"+account)
	return nil
}
