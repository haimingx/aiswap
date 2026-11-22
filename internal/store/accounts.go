package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/haimingx/aiswap/internal/domain"
	"github.com/haimingx/aiswap/internal/fsutil"
	"github.com/haimingx/aiswap/internal/keychain"
)

const accountsSchemaVersion = 1

type ProviderRecords struct {
	ActiveSlot int                  `json:"activeSlot,omitempty"`
	Slots      []domain.AccountSlot `json:"slots"`
}

type AccountsFile struct {
	SchemaVersion int                                        `json:"schemaVersion"`
	UpdatedAt     time.Time                                  `json:"updatedAt"`
	Providers     map[domain.ProviderName]*ProviderRecords   `json:"providers"`
}

type Store struct {
	Root    string
	Secrets keychain.Store
}

func New(root string, secrets keychain.Store) *Store {
	if secrets == nil {
		secrets = keychain.DefaultBackupStore(root)
	}
	return &Store{Root: root, Secrets: secrets}
}

func (s *Store) LockPath() string {
	return filepath.Join(s.Root, "aiswap.lock")
}

func (s *Store) accountsPath() string {
	return filepath.Join(s.Root, "accounts.json")
}

func (s *Store) LoadAccounts() (*AccountsFile, error) {
	raw, err := os.ReadFile(s.accountsPath())
	if errors.Is(err, os.ErrNotExist) {
		return &AccountsFile{
			SchemaVersion: accountsSchemaVersion,
			Providers:     make(map[domain.ProviderName]*ProviderRecords),
		}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read accounts.json: %w", err)
	}
	var f AccountsFile
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("parse accounts.json: %w", err)
	}
	if f.Providers == nil {
		f.Providers = make(map[domain.ProviderName]*ProviderRecords)
	}
	return &f, nil
}

func (s *Store) SaveAccounts(f *AccountsFile) error {
	f.SchemaVersion = accountsSchemaVersion
	f.UpdatedAt = time.Now().UTC()
	for _, rec := range f.Providers {
		sort.Slice(rec.Slots, func(i, j int) bool {
			return rec.Slots[i].Slot < rec.Slots[j].Slot
		})
	}
	return fsutil.AtomicWriteJSON(s.accountsPath(), f, 0o600)
}

func (s *Store) ProviderRecords(f *AccountsFile, p domain.ProviderName) *ProviderRecords {
	rec, ok := f.Providers[p]
	if !ok || rec == nil {
		rec = &ProviderRecords{Slots: []domain.AccountSlot{}}
		f.Providers[p] = rec
	}
	return rec
}

func (s *Store) NextSlot(rec *ProviderRecords) int {
	used := make(map[int]bool, len(rec.Slots))
	for _, sl := range rec.Slots {
		used[sl.Slot] = true
	}
	for i := 1; ; i++ {
		if !used[i] {
			return i
		}
	}
}

// FindSlotByIdentity locates a managed slot matching the given identity key.
func FindSlotByIdentity(rec *ProviderRecords, idKey string) *domain.AccountSlot {
	if rec == nil || idKey == "" {
		return nil
	}
	for i := range rec.Slots {
		if rec.Slots[i].Identity.Key() == idKey {
			return &rec.Slots[i]
		}
	}
	return nil
}

// ResolveSlot finds a slot by numeric slot id, alias, or exact email (case-insensitive).
func ResolveSlot(rec *ProviderRecords, query string) (*domain.AccountSlot, int, error) {
	q := strings.TrimSpace(query)
	if q == "" {
		return nil, -1, errors.New("empty account identifier")
	}
	if num, err := strconv.Atoi(q); err == nil {
		for i := range rec.Slots {
			if rec.Slots[i].Slot == num {
				return &rec.Slots[i], i, nil
			}
		}
		return nil, -1, fmt.Errorf("slot %d not found", num)
	}
	lower := strings.ToLower(q)
	for i := range rec.Slots {
		if rec.Slots[i].Alias != "" && strings.ToLower(rec.Slots[i].Alias) == lower {
			return &rec.Slots[i], i, nil
		}
	}
	var matches []int
	for i := range rec.Slots {
		if strings.ToLower(rec.Slots[i].Identity.Email) == lower {
			matches = append(matches, i)
		}
	}
	if len(matches) == 1 {
		return &rec.Slots[matches[0]], matches[0], nil
	}
	if len(matches) > 1 {
		return nil, -1, fmt.Errorf("multiple accounts match email %q; specify slot number or alias", q)
	}
	return nil, -1, fmt.Errorf("account %q not found", q)
}

func secretService(p domain.ProviderName) string {
	return "aiswap." + string(p)
}

func secretAccount(slot int) string {
	return fmt.Sprintf("slot-%d", slot)
}

func (s *Store) SaveCredential(p domain.ProviderName, slot int, cred *domain.LiveCredential) error {
	data, err := json.Marshal(cred)
	if err != nil {
		return fmt.Errorf("marshal credential envelope: %w", err)
	}
	return s.Secrets.Set(secretService(p), secretAccount(slot), data)
}

func (s *Store) LoadCredential(p domain.ProviderName, slot int) (*domain.LiveCredential, error) {
	data, err := s.Secrets.Get(secretService(p), secretAccount(slot))
	if err != nil {
		return nil, err
	}
	var cred domain.LiveCredential
	if err := json.Unmarshal(data, &cred); err != nil {
		return nil, fmt.Errorf("parse credential envelope for %s slot %d: %w", p, slot, err)
	}
	return &cred, nil
}

func (s *Store) DeleteCredential(p domain.ProviderName, slot int) error {
	return s.Secrets.Delete(secretService(p), secretAccount(slot))
}

// StashUnclaimed persists a rotated credential when normal slot save fails so a one-time refresh token is never lost.
func (s *Store) StashUnclaimed(p domain.ProviderName, slot int, reason string, cred *domain.LiveCredential) (string, error) {
	dir := filepath.Join(s.Root, "unclaimed")
	name := fmt.Sprintf("%s-slot%d-%d.json", p, slot, time.Now().UTC().UnixNano())
	path := filepath.Join(dir, name)
	payload := struct {
		Provider  domain.ProviderName `json:"provider"`
		Slot      int                 `json:"slot"`
		Reason    string              `json:"reason"`
		StashedAt time.Time           `json:"stashedAt"`
		Raw       []byte              `json:"raw"`
		Meta      []byte              `json:"meta,omitempty"`
	}{
		Provider:  p,
		Slot:      slot,
		Reason:    reason,
		StashedAt: time.Now().UTC(),
		Raw:       cred.Raw,
		Meta:      cred.Meta,
	}
	if err := fsutil.AtomicWriteJSON(path, payload, 0o600); err != nil {
		return "", err
	}
	return path, nil
}
