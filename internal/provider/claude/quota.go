package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/haimingx/aiswap/internal/domain"
	"github.com/haimingx/aiswap/internal/provider"
)

const (
	defaultUsageURL    = "https://api.anthropic.com/api/oauth/usage"
	defaultTokenURL    = "https://platform.claude.com/v1/oauth/token"
	defaultOAuthClient = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"
	oauthBetaHeader    = "oauth-2025-04-20"
)

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

func (p *Provider) tokenURL() string {
	if p.TokenEndpoint != "" {
		return p.TokenEndpoint
	}
	return defaultTokenURL
}

func extractClaudeOAuth(raw []byte) (accessToken, refreshToken string, err error) {
	var obj struct {
		ClaudeAiOauth struct {
			AccessToken  string `json:"accessToken"`
			RefreshToken string `json:"refreshToken"`
		} `json:"claudeAiOauth"`
	}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return "", "", fmt.Errorf("parse claude credentials: %w", err)
	}
	return obj.ClaudeAiOauth.AccessToken, obj.ClaudeAiOauth.RefreshToken, nil
}

func (p *Provider) FetchQuota(ctx context.Context, cred *domain.LiveCredential) (*domain.QuotaReading, error) {
	if cred == nil || len(cred.Raw) == 0 {
		return nil, &provider.QuotaError{Kind: "no-credential", Message: "empty credential"}
	}
	accessToken, _, err := extractClaudeOAuth(cred.Raw)
	if err != nil || accessToken == "" {
		return nil, &provider.QuotaError{Kind: "no-token", Message: "no OAuth access token"}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.usageURL(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("anthropic-beta", oauthBetaHeader)
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

	type rawWindow struct {
		Utilization *float64 `json:"utilization"`
		ResetsAt    string   `json:"resets_at"`
	}
	var payload struct {
		FiveHour *rawWindow `json:"five_hour"`
		SevenDay *rawWindow `json:"seven_day"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, &provider.QuotaError{Kind: "bad-response", Message: err.Error()}
	}

	var windows []domain.QuotaWindow
	addWindow := func(name string, rw *rawWindow) {
		if rw == nil || rw.Utilization == nil {
			return
		}
		w := domain.QuotaWindow{Name: name, UsedPct: *rw.Utilization}
		if rw.ResetsAt != "" {
			if ts, err := time.Parse(time.RFC3339, rw.ResetsAt); err == nil {
				w.ResetsAt = &ts
			}
		}
		windows = append(windows, w)
	}
	addWindow("5h", payload.FiveHour)
	addWindow("7d", payload.SevenDay)

	return &domain.QuotaReading{
		Windows:   windows,
		FetchedAt: time.Now().UTC(),
	}, nil
}

func (p *Provider) Refresh(ctx context.Context, cred *domain.LiveCredential) domain.RefreshOutcome {
	if cred == nil || len(cred.Raw) == 0 {
		return domain.RefreshOutcome{Permanent: true, Err: fmt.Errorf("empty credential")}
	}
	_, refreshToken, err := extractClaudeOAuth(cred.Raw)
	if err != nil || refreshToken == "" {
		return domain.RefreshOutcome{Permanent: true, Err: fmt.Errorf("no refresh token")}
	}

	reqBody, _ := json.Marshal(map[string]string{
		"grant_type":    "refresh_token",
		"refresh_token": refreshToken,
		"client_id":     defaultOAuthClient,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.tokenURL(), bytes.NewReader(reqBody))
	if err != nil {
		return domain.RefreshOutcome{Err: err}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "aiswap/1.0")

	resp, err := p.httpClient().Do(req)
	if err != nil {
		return domain.RefreshOutcome{Err: err}
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		var errBody struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(body, &errBody)
		permanent := errBody.Error == "invalid_grant" || resp.StatusCode == http.StatusBadRequest
		return domain.RefreshOutcome{
			Permanent: permanent,
			Err:       fmt.Errorf("claude oauth refresh http-%d (%s)", resp.StatusCode, errBody.Error),
		}
	}

	var tokResp struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &tokResp); err != nil || tokResp.AccessToken == "" {
		return domain.RefreshOutcome{Err: fmt.Errorf("invalid refresh response")}
	}

	var full map[string]json.RawMessage
	if err := json.Unmarshal(cred.Raw, &full); err != nil {
		return domain.RefreshOutcome{Err: err}
	}
	oauthMap := map[string]any{}
	if rawOAuth, ok := full["claudeAiOauth"]; ok && len(rawOAuth) > 0 {
		_ = json.Unmarshal(rawOAuth, &oauthMap)
	}
	oauthMap["accessToken"] = tokResp.AccessToken
	if tokResp.RefreshToken != "" {
		oauthMap["refreshToken"] = tokResp.RefreshToken
	}
	if tokResp.ExpiresIn > 0 {
		oauthMap["expiresAt"] = time.Now().Add(time.Duration(tokResp.ExpiresIn) * time.Second).UnixMilli()
	}
	encodedOAuth, err := json.Marshal(oauthMap)
	if err != nil {
		return domain.RefreshOutcome{Err: err}
	}
	full["claudeAiOauth"] = encodedOAuth

	rotatedRaw, err := json.Marshal(full)
	if err != nil {
		return domain.RefreshOutcome{Err: err}
	}
	return domain.RefreshOutcome{
		Credential: &domain.LiveCredential{
			Raw:     rotatedRaw,
			Meta:    cred.Meta,
			Backend: cred.Backend,
		},
	}
}
