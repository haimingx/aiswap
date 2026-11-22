package codex

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/haimingx/aiswap/internal/domain"
	"github.com/haimingx/aiswap/internal/fsutil"
	"github.com/haimingx/aiswap/internal/lock"
	"github.com/haimingx/aiswap/internal/provider"
)

type Provider struct {
	HomeDir        string // Defaults to $CODEX_HOME or ~/.codex
	HTTPClient     *http.Client
	UsageEndpoint  string
	ModelsEndpoint string
}

func New() *Provider {
	dir := os.Getenv("CODEX_HOME")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".codex")
	}
	return &Provider{HomeDir: dir}
}

func (p *Provider) Name() domain.ProviderName { return domain.ProviderCodex }

func (p *Provider) authPath() string {
	return filepath.Join(p.HomeDir, "auth.json")
}

func (p *Provider) IsInstalled() bool {
	_, err := os.Stat(p.HomeDir)
	return err == nil
}

func (p *Provider) AcquireNativeLock(ctx context.Context) (func(), error) {
	u1, err := lock.AcquireFileLock(ctx, filepath.Join(p.HomeDir, "auth.json.lock"))
	if err != nil {
		return nil, fmt.Errorf("codex auth.json.lock: %w", err)
	}
	u2, err := lock.AcquireFileLock(ctx, filepath.Join(p.HomeDir, "auth.lock"))
	if err != nil {
		u1()
		return nil, fmt.Errorf("codex auth.lock: %w", err)
	}
	return func() {
		u2()
		u1()
	}, nil
}

func (p *Provider) ReadLive(ctx context.Context) (*domain.LiveCredential, error) {
	raw, err := os.ReadFile(p.authPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil, provider.ErrNotLoggedIn
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", p.authPath(), err)
	}
	return &domain.LiveCredential{
		Raw:     raw,
		Backend: "file",
	}, nil
}

func (p *Provider) WriteLive(ctx context.Context, cred *domain.LiveCredential) error {
	if cred == nil || len(cred.Raw) == 0 {
		return errors.New("empty codex credential")
	}
	return fsutil.AtomicWriteFile(p.authPath(), cred.Raw, 0o600)
}

type authFile struct {
	AuthMode     string  `json:"auth_mode"`
	OpenAIAPIKey *string `json:"OPENAI_API_KEY"`
	Tokens       struct {
		IDToken      string `json:"id_token"`
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		AccountID    string `json:"account_id"`
	} `json:"tokens"`
}

func (p *Provider) Identify(ctx context.Context, cred *domain.LiveCredential) (*domain.Identity, error) {
	if cred == nil || len(cred.Raw) == 0 {
		return nil, errors.New("empty codex credential")
	}
	var af authFile
	if err := json.Unmarshal(cred.Raw, &af); err != nil {
		return nil, fmt.Errorf("parse codex auth.json: %w", err)
	}

	id := &domain.Identity{
		AccountID: af.Tokens.AccountID,
	}

	if af.Tokens.IDToken != "" {
		claims, err := parseJWTClaims(af.Tokens.IDToken)
		if err == nil {
			if email, ok := claims["email"].(string); ok {
				id.Email = email
			}
			if id.AccountID == "" {
				if sub, ok := claims["sub"].(string); ok {
					id.AccountID = sub
				}
			}
			if authObj, ok := claims["https://api.openai.com/auth"].(map[string]any); ok {
				if plan, ok := authObj["chatgpt_plan_type"].(string); ok {
					id.Plan = formatCodexPlan(plan)
				}
				if orgID, ok := authObj["chatgpt_account_id"].(string); ok && id.AccountID == "" {
					id.AccountID = orgID
				}
			}
		}
	}

	if id.Email == "" && id.AccountID == "" {
		if af.OpenAIAPIKey != nil && *af.OpenAIAPIKey != "" {
			sum := sha256.Sum256([]byte(*af.OpenAIAPIKey))
			id.AccountID = "apikey-" + hex.EncodeToString(sum[:])[:8]
			id.Email = "api-key@codex.local"
			id.Plan = "API Key"
			return id, nil
		}
		return nil, errors.New("cannot determine codex account identity from auth.json")
	}
	return id, nil
}

func (p *Provider) Refresh(ctx context.Context, cred *domain.LiveCredential) domain.RefreshOutcome {
	return domain.RefreshOutcome{Err: provider.ErrNotImplemented}
}

// parseJWTClaims decodes the payload segment of a JWT without signature verification
// (used purely to read local display identity already trusted by Codex CLI).
func parseJWTClaims(jwt string) (map[string]any, error) {
	parts := strings.Split(jwt, ".")
	if len(parts) < 2 {
		return nil, errors.New("invalid jwt format")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("decode jwt payload: %w", err)
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, fmt.Errorf("unmarshal jwt claims: %w", err)
	}
	return claims, nil
}

func formatCodexPlan(plan string) string {
	switch strings.ToLower(plan) {
	case "plus":
		return "Plus"
	case "pro":
		return "Pro"
	case "team":
		return "Team"
	case "enterprise":
		return "Enterprise"
	case "free":
		return "Free"
	default:
		return plan
	}
}
