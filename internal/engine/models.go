package engine

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/haimingx/aiswap/internal/contract"
	"github.com/haimingx/aiswap/internal/domain"
	"github.com/haimingx/aiswap/internal/provider"
	"github.com/haimingx/aiswap/internal/store"
)

// ListModels fetches real-time available models concurrently for the active login
// of each requested provider (or all providers when filter is empty).
func (m *Manager) ListModels(ctx context.Context, filter ...domain.ProviderName) (*contract.ModelsResponse, error) {
	providers, err := m.resolveProviders(filter...)
	if err != nil {
		return nil, err
	}
	acctFile, _ := m.Store.LoadAccounts()

	resp := &contract.ModelsResponse{
		SchemaVersion: contract.SchemaVersion,
		GeneratedAt:   time.Now().UTC(),
		Providers:     make([]contract.ProviderModels, len(providers)),
	}

	var wg sync.WaitGroup
	for i, prov := range providers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			pm := contract.ProviderModels{
				Provider: prov.Name(),
				Models:   []domain.ModelInfo{},
			}
			live, err := prov.ReadLive(ctx)
			if err != nil {
				if !errors.Is(err, provider.ErrNotLoggedIn) {
					pm.Error = err.Error()
				}
				resp.Providers[i] = pm
				return
			}
			activeSlot := 0
			if id, idErr := prov.Identify(ctx, live); idErr == nil {
				pm.Email = id.Email
				pm.Plan = id.Plan
				if acctFile != nil {
					if matched := store.FindSlotByIdentity(acctFile.Providers[prov.Name()], id.Key()); matched != nil {
						activeSlot = matched.Slot
					}
				}
				if m.Usage != nil {
					if row, uErr := m.Usage.Get(ctx, quotaCacheKey(prov.Name(), id.Key())); uErr == nil && row != nil && row.LastGood != nil {
						stale := row.LastError != "" || m.Usage.Now().Sub(row.LastGood.FetchedAt) > m.Usage.ServeTTL
						pm.Quota = toQuotaPayload(row.LastGood, stale)
					}
				}
			}
			models, err := prov.ListModels(ctx, live)
			var qe *provider.QuotaError
			if err != nil && errors.As(err, &qe) && qe.Kind == "http-401" {
				if refreshed, rErr := m.safeRefreshOn401(ctx, prov, activeSlot, true, live); rErr == nil && refreshed != nil {
					models, err = prov.ListModels(ctx, refreshed)
				}
			}
			if err != nil {
				pm.Error = err.Error()
			} else {
				pm.Models = models
			}
			resp.Providers[i] = pm
		}()
	}
	wg.Wait()
	return resp, nil
}
