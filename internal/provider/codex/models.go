package codex

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/haimingx/aiswap/internal/domain"
	"github.com/haimingx/aiswap/internal/provider"
)

const defaultCodexModelsURL = "https://chatgpt.com/backend-api/codex/models"

func (p *Provider) modelsURL(clientVersion string) string {
	if p.ModelsEndpoint != "" {
		return p.ModelsEndpoint
	}
	if clientVersion != "" {
		return defaultCodexModelsURL + "?client_version=" + url.QueryEscape(clientVersion)
	}
	return defaultCodexModelsURL
}

func (p *Provider) detectClientVersion(ctx context.Context) string {
	if p.HomeDir != "" {
		if raw, err := os.ReadFile(filepath.Join(p.HomeDir, "models_cache.json")); err == nil {
			var cache struct {
				ClientVersion string `json:"client_version"`
			}
			if json.Unmarshal(raw, &cache) == nil && cache.ClientVersion != "" {
				return cache.ClientVersion
			}
		}
	}
	if bin, err := exec.LookPath("codex"); err == nil {
		c, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		if out, err := exec.CommandContext(c, bin, "--version").Output(); err == nil {
			fields := strings.Fields(string(out))
			if len(fields) > 0 {
				return fields[len(fields)-1]
			}
		}
	}
	return ""
}

func (p *Provider) ListModels(ctx context.Context, cred *domain.LiveCredential) ([]domain.ModelInfo, error) {
	if cred == nil || len(cred.Raw) == 0 {
		return nil, &provider.QuotaError{Kind: "no-credential", Message: "empty credential"}
	}
	var af authFile
	if err := json.Unmarshal(cred.Raw, &af); err != nil {
		return nil, &provider.QuotaError{Kind: "bad-credential", Message: err.Error()}
	}
	if af.Tokens.AccessToken == "" {
		return nil, &provider.QuotaError{Kind: "no-token", Message: "no OAuth access_token"}
	}

	cv := p.detectClientVersion(ctx)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.modelsURL(cv), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+af.Tokens.AccessToken)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("originator", "codex_cli_rs")
	if cv != "" {
		req.Header.Set("User-Agent", "codex_cli_rs/"+cv)
	} else {
		req.Header.Set("User-Agent", "codex_cli_rs")
	}
	if af.Tokens.AccountID != "" {
		req.Header.Set("ChatGPT-Account-Id", af.Tokens.AccountID)
	}

	resp, err := p.httpClient().Do(req)
	allowCacheFallback := true
	if err != nil {
		err = &provider.QuotaError{Kind: "network", Message: err.Error()}
	} else {
		defer resp.Body.Close()
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
		if readErr != nil {
			err = &provider.QuotaError{Kind: "network", Message: readErr.Error()}
		} else if resp.StatusCode == http.StatusOK {
			models, parseErr := parseCodexModels(body)
			if parseErr == nil {
				return models, nil
			}
			err = &provider.QuotaError{Kind: "bad-response", Message: parseErr.Error()}
		} else {
			if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
				allowCacheFallback = false
			}
			err = provider.NewHTTPQuotaError(resp, body)
		}
	}

	// Fallback to local models_cache.json only on transient/network errors, never auth failures.
	if allowCacheFallback && p.HomeDir != "" {
		if raw, rErr := os.ReadFile(filepath.Join(p.HomeDir, "models_cache.json")); rErr == nil {
			if models, parseErr := parseCodexModels(raw); parseErr == nil && len(models) > 0 {
				return models, nil
			}
		}
	}
	return nil, err
}

func parseCodexModels(body []byte) ([]domain.ModelInfo, error) {
	var payload struct {
		Models []struct {
			Slug        string `json:"slug"`
			DisplayName string `json:"display_name"`
			Visibility  string `json:"visibility"`
		} `json:"models"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	out := make([]domain.ModelInfo, 0, len(payload.Models))
	for _, m := range payload.Models {
		if m.Visibility != "" && m.Visibility != "list" {
			continue
		}
		name := m.DisplayName
		if name == "" {
			name = m.Slug
		}
		out = append(out, domain.ModelInfo{
			ID:          m.Slug,
			DisplayName: name,
		})
	}
	return out, nil
}
