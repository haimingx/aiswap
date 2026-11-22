package claude

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/haimingx/aiswap/internal/fsutil"
)

func TestClaudeReadWritePreservesSharedKeysAndConfig(t *testing.T) {
	dir := t.TempDir()
	cfgDir := filepath.Join(dir, ".claude")
	globalCfg := filepath.Join(dir, ".claude.json")

	p := &Provider{
		ConfigDir:    cfgDir,
		GlobalConfig: globalCfg,
		UseKeychain:  false,
	}

	// Seed live credentials with both account-scoped claudeAiOauth and machine-shared mcpOAuth.
	liveCreds := `{"claudeAiOauth":{"accessToken":"at-a","refreshToken":"rt-a","rateLimitTier":"default_claude_max_5x"},"mcpOAuth":{"stripe|123":{"accessToken":"mcp-live-secret"}}}`
	if err := fsutil.AtomicWriteFile(filepath.Join(cfgDir, ".credentials.json"), []byte(liveCreds), 0o600); err != nil {
		t.Fatal(err)
	}
	liveGlobal := `{"theme":"dark","oauthAccount":{"accountUuid":"u-a","emailAddress":"a@example.com","organizationUuid":"org-a","organizationName":"OrgA"}}`
	if err := fsutil.AtomicWriteFile(globalCfg, []byte(liveGlobal), 0o600); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	capturedA, err := p.ReadLive(ctx)
	if err != nil {
		t.Fatalf("ReadLive failed: %v", err)
	}

	// Captured backup must strip mcpOAuth.
	var backupObj map[string]any
	if err := json.Unmarshal(capturedA.Raw, &backupObj); err != nil {
		t.Fatal(err)
	}
	if _, exists := backupObj["mcpOAuth"]; exists {
		t.Fatal("backup credential should not contain machine-shared mcpOAuth")
	}

	idA, err := p.Identify(ctx, capturedA)
	if err != nil {
		t.Fatalf("Identify failed: %v", err)
	}
	if idA.Email != "a@example.com" || idA.Plan != "Max 5x" || idA.OrgName != "OrgA" {
		t.Fatalf("unexpected identity: %+v", idA)
	}

	// Write account B; live mcpOAuth and unrelated .claude.json keys ("theme":"dark") must survive.
	credB := capturedA
	credB.Raw = []byte(`{"claudeAiOauth":{"accessToken":"at-b","refreshToken":"rt-b"}}`)
	credB.Meta = []byte(`{"accountUuid":"u-b","emailAddress":"b@example.com"}`)

	if err := p.WriteLive(ctx, credB); err != nil {
		t.Fatalf("WriteLive failed: %v", err)
	}

	afterCredsRaw, err := os.ReadFile(filepath.Join(cfgDir, ".credentials.json"))
	if err != nil {
		t.Fatal(err)
	}
	var afterCreds map[string]any
	if err := json.Unmarshal(afterCredsRaw, &afterCreds); err != nil {
		t.Fatal(err)
	}
	if _, ok := afterCreds["mcpOAuth"]; !ok {
		t.Fatal("expected live mcpOAuth to be preserved after WriteLive")
	}

	afterGlobalRaw, err := os.ReadFile(globalCfg)
	if err != nil {
		t.Fatal(err)
	}
	var afterGlobal map[string]any
	if err := json.Unmarshal(afterGlobalRaw, &afterGlobal); err != nil {
		t.Fatal(err)
	}
	if afterGlobal["theme"] != "dark" {
		t.Fatalf("expected unrelated global config key 'theme' to survive, got %v", afterGlobal["theme"])
	}
}
