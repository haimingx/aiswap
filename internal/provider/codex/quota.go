package codex

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/haimingx/aiswap/internal/domain"
	"github.com/haimingx/aiswap/internal/provider"
)

const defaultUsageURL = "https://chatgpt.com/backend-api/wham/usage"

func (p *Provider) httpClient() *http.Client {
	if p.HTTPClient != nil {
		return p.HTTPClient
	}
	return provider.DefaultHTTPClient
}

func (p *Provider) usageURL() string {
	if p.UsageEndpoint != "" {
		return p.UsageEndpoint
	}
	return defaultUsageURL
}

func (p *Provider) FetchQuota(ctx context.Context, cred *domain.LiveCredential) (*domain.QuotaReading, error) {
	if cred == nil || len(cred.Raw) == 0 {
		return nil, &provider.QuotaError{Kind: "no-credential", Message: "empty credential"}
	}
	var af authFile
	if err := json.Unmarshal(cred.Raw, &af); err != nil {
		return nil, &provider.QuotaError{Kind: "bad-credential", Message: err.Error()}
	}
	if af.Tokens.AccessToken == "" {
		return nil, &provider.QuotaError{Kind: "no-token", Message: "no OAuth access_token in auth.json"}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.usageURL(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+af.Tokens.AccessToken)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("originator", "codex_cli_rs")
	req.Header.Set("User-Agent", "aiswap/1.0")
	if af.Tokens.AccountID != "" {
		req.Header.Set("ChatGPT-Account-Id", af.Tokens.AccountID)
	}

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

	type rawWin struct {
		LimitWindowSeconds int64    `json:"limit_window_seconds"`
		UsedPercent        *float64 `json:"used_percent"`
		ResetAt            int64    `json:"reset_at"`
	}
	var payload struct {
		RateLimit struct {
			PrimaryWindow   *rawWin `json:"primary_window"`
			SecondaryWindow *rawWin `json:"secondary_window"`
		} `json:"rate_limit"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, &provider.QuotaError{Kind: "bad-response", Message: err.Error()}
	}

	var windows []domain.QuotaWindow
	addWin := func(fallbackName string, rw *rawWin) {
		if rw == nil || rw.UsedPercent == nil {
			return
		}
		name := fallbackName
		if rw.LimitWindowSeconds == 18000 {
			name = "5h"
		} else if rw.LimitWindowSeconds == 604800 {
			name = "7d"
		}
		w := domain.QuotaWindow{Name: name, UsedPct: *rw.UsedPercent}
		if rw.ResetAt > 0 {
			ts := time.Unix(rw.ResetAt, 0).UTC()
			w.ResetsAt = &ts
		}
		windows = append(windows, w)
	}
	addWin("5h", payload.RateLimit.PrimaryWindow)
	addWin("7d", payload.RateLimit.SecondaryWindow)

	return &domain.QuotaReading{
		Windows:   windows,
		FetchedAt: time.Now().UTC(),
	}, nil
}
