package agy

import (
	"context"
	"encoding/base64"
	"path/filepath"
	"strings"
	"testing"

	"github.com/haimingx/aiswap/internal/domain"
	"github.com/haimingx/aiswap/internal/fsutil"
	"github.com/haimingx/aiswap/internal/keychain"
)

func TestAgyCLITargetReadWriteIdentify(t *testing.T) {
	dir := t.TempDir()
	p := &Provider{
		DataDir: dir,
		Targets: []SyncTarget{&CLITarget{DataDir: dir, UseKeychain: false}},
	}

	raw := `{"token":{"access_token":"at","token_type":"Bearer","refresh_token":"rt","expiry":"2026-12-01T00:00:00Z","expires_in":3600},"auth_method":"oauth","id_token_email":"user@gmail.com"}`
	if err := fsutil.AtomicWriteFile(filepath.Join(dir, "antigravity-oauth-token"), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	live, err := p.ReadLive(ctx)
	if err != nil {
		t.Fatalf("ReadLive failed: %v", err)
	}
	id, err := p.Identify(ctx, live)
	if err != nil {
		t.Fatalf("Identify failed: %v", err)
	}
	if id.Email != "user@gmail.com" {
		t.Fatalf("unexpected agy identity: %+v", id)
	}
}

func TestAgyCLITargetKeychainMode(t *testing.T) {
	mem := keychain.NewMemoryStore()
	raw := `{"token":{"access_token":"at-kc","refresh_token":"rt-kc"},"id_token_email":"mac@gmail.com"}`
	encoded := goKeyringBase64Prefix + base64.StdEncoding.EncodeToString([]byte(raw))
	if err := mem.Set(macosKeychainService, macosKeychainAccount, []byte(encoded)); err != nil {
		t.Fatal(err)
	}
	target := &CLITarget{
		DataDir:     t.TempDir(),
		Keychain:    mem,
		UseKeychain: true,
	}
	p := &Provider{DataDir: target.DataDir, Targets: []SyncTarget{target}}
	live, err := p.ReadLive(context.Background())
	if err != nil {
		t.Fatalf("ReadLive from keychain failed: %v", err)
	}
	id, err := p.Identify(context.Background(), live)
	if err != nil || id.Email != "mac@gmail.com" {
		t.Fatalf("unexpected keychain identity: %+v, err=%v", id, err)
	}

	// Verify WriteLive re-encodes with go-keyring-base64: prefix for agy compatibility.
	updated := `{"token":{"access_token":"at-kc-2","refresh_token":"rt-kc-2"},"id_token_email":"mac2@gmail.com"}`
	if err := p.WriteLive(context.Background(), &domain.LiveCredential{Raw: []byte(updated)}); err != nil {
		t.Fatalf("WriteLive to keychain failed: %v", err)
	}
	stored, err := mem.Get(macosKeychainService, macosKeychainAccount)
	if err != nil {
		t.Fatalf("keychain Get failed: %v", err)
	}
	if !strings.HasPrefix(string(stored), goKeyringBase64Prefix) {
		t.Fatalf("expected stored payload to have %q prefix, got %q", goKeyringBase64Prefix, string(stored))
	}
	live2, err := p.ReadLive(context.Background())
	if err != nil {
		t.Fatalf("ReadLive roundtrip failed: %v", err)
	}
	id2, err := p.Identify(context.Background(), live2)
	if err != nil || id2.Email != "mac2@gmail.com" {
		t.Fatalf("unexpected roundtrip identity: %+v, err=%v", id2, err)
	}
}
