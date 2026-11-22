package claude

import (
	"context"
	"encoding/json"
	"io"
	"net/http"

	"github.com/haimingx/aiswap/internal/domain"
	"github.com/haimingx/aiswap/internal/provider"
)

const defaultModelsAPIURL = "https://api.anthropic.com/v1/models?limit=50"

func (p *Provider) modelsURL() string {
	if p.ModelsEndpoint != "" {
		return p.ModelsEndpoint
	}
	return defaultModelsAPIURL
}

func (p *Provider) ListModels(ctx context.Context, cred *domain.LiveCredential) ([]domain.ModelInfo, error) {
	if cred == nil || len(cred.Raw) == 0 {
		return nil, &provider.QuotaError{Kind: "no-credential", Message: "empty credential"}
	}
	accessToken, _, err := extractClaudeOAuth(cred.Raw)
	if err != nil || accessToken == "" {
		return nil, &provider.QuotaError{Kind: "no-token", Message: "no OAuth access token"}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.modelsURL(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("anthropic-beta", oauthBetaHeader)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "aiswap/1.0")

	resp, err := p.httpClient().Do(req)
	if err != nil {
		return nil, &provider.QuotaError{Kind: "network", Message: err.Error()}
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, &provider.QuotaError{Kind: "network", Message: err.Error()}
	}
	if resp.StatusCode != http.StatusOK {
		return nil, provider.NewHTTPQuotaError(resp, body)
	}

	var payload struct {
		Data []struct {
			ID          string `json:"id"`
			DisplayName string `json:"display_name"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, &provider.QuotaError{Kind: "bad-response", Message: err.Error()}
	}

	out := make([]domain.ModelInfo, 0, len(payload.Data))
	for _, m := range payload.Data {
		name := m.DisplayName
		if name == "" {
			name = m.ID
		}
		out = append(out, domain.ModelInfo{
			ID:          m.ID,
			DisplayName: name,
		})
	}
	return out, nil
}
