package engine

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/haimingx/aiswap/internal/domain"
	"github.com/haimingx/aiswap/internal/fsutil"
	"github.com/haimingx/aiswap/internal/keychain"
	"github.com/haimingx/aiswap/internal/provider"
	"github.com/haimingx/aiswap/internal/provider/agy"
	"github.com/haimingx/aiswap/internal/store"
)

func TestManagerAddSwitchAliasRemoveFlow(t *testing.T) {
	root := t.TempDir()
	agyDir := filepath.Join(root, "agy-live")
	st := store.New(filepath.Join(root, "aiswap-data"), keychain.NewMemoryStore())
	agyProv := &agy.Provider{
		DataDir: agyDir,
		Targets: []agy.SyncTarget{agy.NewCLITarget(agyDir)},
	}
	mgr := NewManager(st, provider.NewRegistry(agyProv))
	ctx := context.Background()

	writeAgy := func(email, token string) {
		raw := `{"token":{"access_token":"` + token + `","refresh_token":"rt-` + token + `"},"auth_method":"oauth","id_token_email":"` + email + `"}`
		if err := fsutil.AtomicWriteFile(filepath.Join(agyDir, "antigravity-oauth-token"), []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	// Add account 1 with alias "work"
	writeAgy("work@example.com", "tok1")
	r1, err := mgr.Add(ctx, domain.ProviderAgy, "work")
	if err != nil {
		t.Fatalf("Add 1 failed: %v", err)
	}
	if r1.Slot != 1 || r1.Updated || r1.Alias != "work" {
		t.Fatalf("unexpected add 1 result: %+v", r1)
	}

	// Simulate user logging into account 2 and adding it.
	writeAgy("personal@example.com", "tok2")
	r2, err := mgr.Add(ctx, domain.ProviderAgy, "me")
	if err != nil {
		t.Fatalf("Add 2 failed: %v", err)
	}
	if r2.Slot != 2 || r2.Updated {
		t.Fatalf("unexpected add 2 result: %+v", r2)
	}

	// Switch back to "work" by alias.
	sw, err := mgr.Switch(ctx, domain.ProviderAgy, "work")
	if err != nil {
		t.Fatalf("Switch to work failed: %v", err)
	}
	if !sw.Switched || sw.FromSlot != 2 || sw.ToSlot != 1 {
		t.Fatalf("unexpected switch result: %+v", sw)
	}

	// Verify live file now holds work@example.com.
	live, err := agyProv.ReadLive(ctx)
	if err != nil {
		t.Fatal(err)
	}
	id, err := agyProv.Identify(ctx, live)
	if err != nil {
		t.Fatal(err)
	}
	if id.Email != "work@example.com" {
		t.Fatalf("live email = %q, want work@example.com", id.Email)
	}

	// Rotate using "next" -> should land on slot 2.
	sw2, err := mgr.Switch(ctx, domain.ProviderAgy, "next")
	if err != nil {
		t.Fatalf("Switch next failed: %v", err)
	}
	if !sw2.Switched || sw2.ToSlot != 2 {
		t.Fatalf("expected rotate to slot 2, got %+v", sw2)
	}

	// Check Status JSON response.
	status, err := mgr.Status(ctx, domain.ProviderAgy)
	if err != nil {
		t.Fatalf("Status failed: %v", err)
	}
	if len(status.Providers) != 1 || status.Providers[0].ActiveSlot != 2 || !status.Providers[0].Managed {
		t.Fatalf("unexpected status: %+v", status.Providers)
	}

	// Disable slot 2 and verify Switch("best") picks enabled slot with lowest utilization.
	if _, err := mgr.SetDisabled(ctx, domain.ProviderAgy, "2", true); err != nil {
		t.Fatalf("SetDisabled failed: %v", err)
	}
	swBest, err := mgr.Switch(ctx, domain.ProviderAgy, "best")
	if err != nil {
		t.Fatalf("Switch best failed: %v", err)
	}
	if swBest.ToSlot != 1 {
		t.Fatalf("expected best to skip disabled slot 2 and pick slot 1, got %+v", swBest)
	}

	// Remove slot 1.
	removed, err := mgr.Remove(ctx, domain.ProviderAgy, "work")
	if err != nil {
		t.Fatalf("Remove failed: %v", err)
	}
	if removed.Slot != 1 {
		t.Fatalf("expected removed slot 1, got %d", removed.Slot)
	}
}
