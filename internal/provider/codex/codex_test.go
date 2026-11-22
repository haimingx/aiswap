package codex

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/haimingx/aiswap/internal/fsutil"
)

func makeFakeJWT(t *testing.T, claims map[string]any) string {
	t.Helper()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	raw, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	payload := base64.RawURLEncoding.EncodeToString(raw)
	return header + "." + payload + ".sig"
}

func TestCodexReadWriteAndIdentify(t *testing.T) {
	dir := t.TempDir()
	p := &Provider{HomeDir: dir}

	jwt := makeFakeJWT(t, map[string]any{
		"email": "dev@openai.test",
		"sub":   "auth0|123",
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_plan_type": "pro",
		},
	})
	authJSON := `{"auth_mode":"chatgpt","OPENAI_API_KEY":null,"tokens":{"id_token":"` + jwt + `","access_token":"at","refresh_token":"rt","account_id":"acct-1"}}`
	if err := fsutil.AtomicWriteFile(filepath.Join(dir, "auth.json"), []byte(authJSON), 0o600); err != nil {
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
	if id.Email != "dev@openai.test" || id.AccountID != "acct-1" || id.Plan != "Pro" {
		t.Fatalf("unexpected codex identity: %+v", id)
	}
}
