package agy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"

	"github.com/haimingx/aiswap/internal/domain"
	"github.com/haimingx/aiswap/internal/lock"
	"github.com/haimingx/aiswap/internal/provider"
)

// SyncTarget decouples AGY's shared account/quota logic from where credentials live.
type SyncTarget interface {
	TargetName() string
	IsInstalled() bool
	Read() ([]byte, string, error)
	Write(raw []byte) error
}

type Provider struct {
	DataDir              string // Defaults to $AGY_DATA_DIR or ~/.gemini/antigravity-cli
	Targets              []SyncTarget
	HTTPClient           *http.Client
	QuotaEndpoint        string
	QuotaSummaryEndpoint string
	LoadAssistEndpoint   string
	UserAgent            string
	ModelsCommand        func(ctx context.Context) ([]byte, error) // Optional override for `agy models` in tests

	uaOnce   sync.Once
	cachedUA string
}

func New() *Provider {
	dir := os.Getenv("AGY_DATA_DIR")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".gemini", "antigravity-cli")
	}
	return &Provider{
		DataDir: dir,
		Targets: []SyncTarget{NewCLITarget(dir)},
	}
}

func (p *Provider) Name() domain.ProviderName { return domain.ProviderAgy }

func (p *Provider) IsInstalled() bool {
	for _, t := range p.Targets {
		if t.IsInstalled() {
			return true
		}
	}
	return false
}

func (p *Provider) AcquireNativeLock(ctx context.Context) (func(), error) {
	return lock.AcquireFileLock(ctx, filepath.Join(p.DataDir, ".aiswap-agy.lock"))
}

func (p *Provider) ReadLive(ctx context.Context) (*domain.LiveCredential, error) {
	for _, t := range p.Targets {
		raw, backend, err := t.Read()
		if errors.Is(err, provider.ErrNotLoggedIn) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read agy %s target: %w", t.TargetName(), err)
		}
		return &domain.LiveCredential{
			Raw:     raw,
			Backend: backend,
		}, nil
	}
	return nil, provider.ErrNotLoggedIn
}

func (p *Provider) WriteLive(ctx context.Context, cred *domain.LiveCredential) error {
	if cred == nil || len(cred.Raw) == 0 {
		return errors.New("empty agy credential")
	}
	written := 0
	for _, t := range p.Targets {
		if !t.IsInstalled() && len(p.Targets) > 1 {
			continue
		}
		if err := t.Write(cred.Raw); err != nil {
			return fmt.Errorf("write agy %s target: %w", t.TargetName(), err)
		}
		written++
	}
	if written == 0 {
		return errors.New("no installed agy target available")
	}
	return nil
}

type oauthTokenFile struct {
	Token struct {
		AccessToken  string `json:"access_token"`
		TokenType    string `json:"token_type"`
		RefreshToken string `json:"refresh_token"`
		Expiry       string `json:"expiry"`
		ExpiresIn    int    `json:"expires_in"`
	} `json:"token"`
	AuthMethod   string `json:"auth_method"`
	IDTokenEmail string `json:"id_token_email"`
}

func (p *Provider) Identify(ctx context.Context, cred *domain.LiveCredential) (*domain.Identity, error) {
	if cred == nil || len(cred.Raw) == 0 {
		return nil, errors.New("empty agy credential")
	}
	var tf oauthTokenFile
	if err := json.Unmarshal(cred.Raw, &tf); err != nil {
		return nil, fmt.Errorf("parse agy oauth token: %w", err)
	}
	if tf.IDTokenEmail == "" {
		return nil, errors.New("cannot determine agy account email from oauth token file")
	}
	return &domain.Identity{
		Email: tf.IDTokenEmail,
	}, nil
}

func (p *Provider) Refresh(ctx context.Context, cred *domain.LiveCredential) domain.RefreshOutcome {
	return domain.RefreshOutcome{Err: provider.ErrNotImplemented}
}
