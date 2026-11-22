package claude

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/haimingx/aiswap/internal/domain"
	"github.com/haimingx/aiswap/internal/fsutil"
	"github.com/haimingx/aiswap/internal/keychain"
	"github.com/haimingx/aiswap/internal/lock"
	"github.com/haimingx/aiswap/internal/provider"
)

const (
	defaultKeychainService = "Claude Code-credentials"
	credLockStaleness      = 60 * time.Second
	configLockStaleness    = 10 * time.Second
	lockTouchInterval      = 3 * time.Second
)

// Machine-shared sibling keys in Claude's credentials JSON that must always come from live state.
var sharedCredentialKeys = map[string]bool{
	"mcpOAuth":             true,
	"mcpOAuthClientConfig": true,
	"mcpXaaIdp":            true,
	"mcpXaaIdpConfig":      true,
	"pluginSecrets":        true,
}

type Provider struct {
	ConfigDir      string         // Defaults to $CLAUDE_CONFIG_DIR or ~/.claude
	GlobalConfig   string         // Defaults to ~/.claude.json (or $CLAUDE_CONFIG_DIR/.claude.json when overridden)
	Keychain       keychain.Store // Non-nil on macOS (or fake in tests)
	UseKeychain    bool
	HTTPClient     *http.Client
	UsageEndpoint  string
	TokenEndpoint  string
	ModelsEndpoint string
}

func New() *Provider {
	home, _ := os.UserHomeDir()
	cfgDir := os.Getenv("CLAUDE_CONFIG_DIR")
	globalCfg := filepath.Join(home, ".claude.json")
	if cfgDir == "" {
		cfgDir = filepath.Join(home, ".claude")
	} else {
		cfgDir = filepath.Clean(cfgDir)
		globalCfg = filepath.Join(cfgDir, ".claude.json")
	}
	useKC := runtime.GOOS == "darwin"
	var kc keychain.Store
	if useKC {
		kc = keychain.DefaultBackupStore("")
	}
	return &Provider{
		ConfigDir:    cfgDir,
		GlobalConfig: globalCfg,
		Keychain:     kc,
		UseKeychain:  useKC,
	}
}

func (p *Provider) Name() domain.ProviderName { return domain.ProviderClaude }

func (p *Provider) credentialsFilePath() string {
	return filepath.Join(p.ConfigDir, ".credentials.json")
}

func (p *Provider) keychainService() string {
	if secDir := os.Getenv("CLAUDE_SECURESTORAGE_CONFIG_DIR"); secDir != "" {
		return suffixedKeychainService(filepath.Clean(secDir))
	}
	if cfgDir := os.Getenv("CLAUDE_CONFIG_DIR"); cfgDir != "" {
		return suffixedKeychainService(filepath.Clean(cfgDir))
	}
	if home, err := os.UserHomeDir(); err == nil && p.ConfigDir != "" {
		defaultDir := filepath.Clean(filepath.Join(home, ".claude"))
		if cleaned := filepath.Clean(p.ConfigDir); cleaned != defaultDir {
			return suffixedKeychainService(cleaned)
		}
	}
	return defaultKeychainService
}

func (p *Provider) keychainAccount() string {
	if u := os.Getenv("USER"); u != "" {
		return u
	}
	if cur, err := user.Current(); err == nil && cur.Username != "" {
		return cur.Username
	}
	return ""
}

func suffixedKeychainService(dir string) string {
	sum := sha256.Sum256([]byte(dir))
	return defaultKeychainService + "-" + hex.EncodeToString(sum[:])[:8]
}

func (p *Provider) IsInstalled() bool {
	if _, err := os.Stat(p.ConfigDir); err == nil {
		return true
	}
	if _, err := os.Stat(p.GlobalConfig); err == nil {
		return true
	}
	return false
}

// AcquireNativeLock takes Claude Code's locks in its exact order:
// 1. <config-home>/.oauth_refresh.lock (stale 60s)
// 2. <config-home>.lock (stale 60s)
// 3. <global-config>.lock (stale 10s)
func (p *Provider) AcquireNativeLock(ctx context.Context) (func(), error) {
	cfgDir := filepath.Clean(p.ConfigDir)
	oauthLockPath := filepath.Join(cfgDir, ".oauth_refresh.lock")
	legacyLockPath := filepath.Join(filepath.Dir(cfgDir), filepath.Base(cfgDir)+".lock")
	configLockPath := p.GlobalConfig + ".lock"

	u1, err := lock.AcquireDirLock(ctx, oauthLockPath, credLockStaleness, lockTouchInterval)
	if err != nil {
		return nil, fmt.Errorf("claude oauth_refresh lock: %w", err)
	}
	u2, err := lock.AcquireDirLock(ctx, legacyLockPath, credLockStaleness, lockTouchInterval)
	if err != nil {
		u1()
		return nil, fmt.Errorf("claude legacy credential lock: %w", err)
	}
	u3, err := lock.AcquireDirLock(ctx, configLockPath, configLockStaleness, lockTouchInterval)
	if err != nil {
		u2()
		u1()
		return nil, fmt.Errorf("claude config lock: %w", err)
	}
	return func() {
		u3()
		u2()
		u1()
	}, nil
}

func (p *Provider) readRawLive() ([]byte, string, error) {
	if p.UseKeychain && p.Keychain != nil {
		svc := p.keychainService()
		acct := p.keychainAccount()
		kcData, err := p.Keychain.Get(svc, acct)
		if errors.Is(err, keychain.ErrNotFound) && acct != "" {
			kcData, err = p.Keychain.Get(svc, "")
		}
		if err == nil && len(kcData) > 0 {
			return kcData, "keychain", nil
		}
		if err != nil && !errors.Is(err, keychain.ErrNotFound) {
			return nil, "keychain", fmt.Errorf("read claude keychain: %w", err)
		}
	}
	data, err := os.ReadFile(p.credentialsFilePath())
	if errors.Is(err, os.ErrNotExist) {
		return nil, "file", provider.ErrNotLoggedIn
	}
	if err != nil {
		return nil, "file", fmt.Errorf("read %s: %w", p.credentialsFilePath(), err)
	}
	return data, "file", nil
}

func (p *Provider) ReadLive(ctx context.Context) (*domain.LiveCredential, error) {
	raw, backend, err := p.readRawLive()
	if err != nil {
		return nil, err
	}

	// Strip machine-shared sibling keys (mcpOAuth, pluginSecrets, etc.) from the captured snapshot
	// so backups carry only account-scoped credentials.
	cleaned, err := stripSharedKeys(raw)
	if err != nil {
		return nil, err
	}

	meta, err := p.readOAuthAccountMeta()
	if err != nil {
		return nil, err
	}

	return &domain.LiveCredential{
		Raw:     cleaned,
		Meta:    meta,
		Backend: backend,
	}, nil
}

func (p *Provider) WriteLive(ctx context.Context, cred *domain.LiveCredential) error {
	if cred == nil || len(cred.Raw) == 0 {
		return errors.New("empty claude credential")
	}

	// Capture prior live payload for shared-key merge and rollback if meta write fails.
	priorRaw, _, readErr := p.readRawLive()
	if readErr != nil && !errors.Is(readErr, provider.ErrNotLoggedIn) {
		return fmt.Errorf("read live claude credential before write: %w", readErr)
	}

	merged, err := mergeSharedKeys(cred.Raw, priorRaw)
	if err != nil {
		return err
	}

	writePrimary := func(payload []byte) error {
		if p.UseKeychain && p.Keychain != nil {
			if err := p.Keychain.Set(p.keychainService(), p.keychainAccount(), payload); err != nil {
				return fmt.Errorf("write claude keychain: %w", err)
			}
			if _, err := os.Stat(p.credentialsFilePath()); err == nil {
				_ = fsutil.AtomicWriteFile(p.credentialsFilePath(), payload, 0o600)
			}
			return nil
		}
		if err := fsutil.AtomicWriteFile(p.credentialsFilePath(), payload, 0o600); err != nil {
			return fmt.Errorf("write %s: %w", p.credentialsFilePath(), err)
		}
		return nil
	}

	if err := writePrimary(merged); err != nil {
		return err
	}

	if err := p.writeOAuthAccountMeta(cred.Meta); err != nil {
		if len(priorRaw) > 0 {
			_ = writePrimary(priorRaw)
		}
		return err
	}
	return nil
}

func (p *Provider) Identify(ctx context.Context, cred *domain.LiveCredential) (*domain.Identity, error) {
	if cred == nil {
		return nil, errors.New("nil credential")
	}
	id := &domain.Identity{}

	if len(cred.Meta) > 0 {
		var oa struct {
			AccountUUID             string `json:"accountUuid"`
			EmailAddress            string `json:"emailAddress"`
			OrganizationUUID        string `json:"organizationUuid"`
			OrganizationName        string `json:"organizationName"`
			OrganizationRateLimit   string `json:"organizationRateLimitTier"`
			OrganizationType        string `json:"organizationType"`
		}
		if err := json.Unmarshal(cred.Meta, &oa); err == nil {
			id.AccountID = oa.AccountUUID
			id.Email = oa.EmailAddress
			id.OrgID = oa.OrganizationUUID
			id.OrgName = oa.OrganizationName
			if oa.OrganizationRateLimit != "" {
				id.Plan = formatClaudePlan(oa.OrganizationRateLimit)
			}
		}
	}

	if len(cred.Raw) > 0 {
		var rawObj struct {
			ClaudeAiOauth struct {
				SubscriptionType string `json:"subscriptionType"`
				RateLimitTier    string `json:"rateLimitTier"`
			} `json:"claudeAiOauth"`
		}
		if err := json.Unmarshal(cred.Raw, &rawObj); err == nil {
			if id.Plan == "" {
				if rawObj.ClaudeAiOauth.RateLimitTier != "" {
					id.Plan = formatClaudePlan(rawObj.ClaudeAiOauth.RateLimitTier)
				} else if rawObj.ClaudeAiOauth.SubscriptionType != "" {
					id.Plan = rawObj.ClaudeAiOauth.SubscriptionType
				}
			}
		}
	}

	if id.Email == "" && id.AccountID == "" {
		return nil, errors.New("cannot determine claude account identity from local credentials")
	}
	return id, nil
}

func (p *Provider) readOAuthAccountMeta() ([]byte, error) {
	raw, err := os.ReadFile(p.GlobalConfig)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", p.GlobalConfig, err)
	}
	var cfg map[string]json.RawMessage
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", p.GlobalConfig, err)
	}
	oa, ok := cfg["oauthAccount"]
	if !ok || string(oa) == "null" {
		return nil, nil
	}
	return oa, nil
}

func (p *Provider) writeOAuthAccountMeta(meta []byte) error {
	cfg := make(map[string]json.RawMessage)
	if raw, err := os.ReadFile(p.GlobalConfig); err == nil {
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return fmt.Errorf("parse %s: %w", p.GlobalConfig, err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read %s: %w", p.GlobalConfig, err)
	}
	if len(meta) == 0 {
		if _, exists := cfg["oauthAccount"]; !exists {
			return nil
		}
		delete(cfg, "oauthAccount")
	} else {
		cfg["oauthAccount"] = json.RawMessage(meta)
	}
	return fsutil.AtomicWriteJSON(p.GlobalConfig, cfg, 0o600)
}

func stripSharedKeys(raw []byte) ([]byte, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return raw, nil // Preserve non-JSON (e.g. raw API key) as-is.
	}
	for k := range sharedCredentialKeys {
		delete(obj, k)
	}
	return json.Marshal(obj)
}

func mergeSharedKeys(targetRaw, liveRaw []byte) ([]byte, error) {
	var target map[string]json.RawMessage
	if err := json.Unmarshal(targetRaw, &target); err != nil {
		return targetRaw, nil
	}
	for k := range sharedCredentialKeys {
		delete(target, k)
	}
	if len(liveRaw) > 0 {
		var live map[string]json.RawMessage
		if err := json.Unmarshal(liveRaw, &live); err == nil {
			for k := range sharedCredentialKeys {
				if v, ok := live[k]; ok {
					target[k] = v
				}
			}
		}
	}
	return json.Marshal(target)
}

func formatClaudePlan(tier string) string {
	t := strings.ToLower(tier)
	switch {
	case strings.Contains(t, "max_20x"):
		return "Max 20x"
	case strings.Contains(t, "max_5x"):
		return "Max 5x"
	case strings.Contains(t, "ultra"):
		return "Ultra"
	case strings.Contains(t, "team"):
		return "Team"
	case strings.Contains(t, "pro"):
		return "Pro"
	case t == "default_claude_ai":
		return "Pro"
	default:
		return tier
	}
}
