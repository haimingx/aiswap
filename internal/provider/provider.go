package provider

import (
	"context"
	"errors"
	"fmt"

	"github.com/haimingx/aiswap/internal/domain"
)

var (
	ErrNotLoggedIn    = errors.New("no active login found")
	ErrNotImplemented = errors.New("not implemented")
)

// Provider abstracts a single AI coding tool's local credential, lock, and quota protocol.
type Provider interface {
	Name() domain.ProviderName
	IsInstalled() bool
	AcquireNativeLock(ctx context.Context) (unlock func(), err error)
	ReadLive(ctx context.Context) (*domain.LiveCredential, error)
	WriteLive(ctx context.Context, cred *domain.LiveCredential) error
	Identify(ctx context.Context, cred *domain.LiveCredential) (*domain.Identity, error)
	Refresh(ctx context.Context, cred *domain.LiveCredential) domain.RefreshOutcome
	FetchQuota(ctx context.Context, cred *domain.LiveCredential) (*domain.QuotaReading, error)
	ListModels(ctx context.Context, cred *domain.LiveCredential) ([]domain.ModelInfo, error)
}

type Registry struct {
	items map[domain.ProviderName]Provider
}

func NewRegistry(providers ...Provider) *Registry {
	m := make(map[domain.ProviderName]Provider, len(providers))
	for _, p := range providers {
		m[p.Name()] = p
	}
	return &Registry{items: m}
}

func (r *Registry) Get(name domain.ProviderName) (Provider, error) {
	p, ok := r.items[name]
	if !ok {
		return nil, fmt.Errorf("provider %q is not registered", name)
	}
	return p, nil
}

func (r *Registry) All() []Provider {
	out := make([]Provider, 0, len(domain.AllProviders))
	for _, name := range domain.AllProviders {
		if p, ok := r.items[name]; ok {
			out = append(out, p)
		}
	}
	return out
}
