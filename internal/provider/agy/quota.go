package agy

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/haimingx/aiswap/internal/domain"
	"github.com/haimingx/aiswap/internal/provider"
)

const (
	defaultModelsURL       = "https://daily-cloudcode-pa.googleapis.com/v1internal:fetchAvailableModels"
	defaultQuotaSummaryURL = "https://daily-cloudcode-pa.googleapis.com/v1internal:retrieveUserQuotaSummary"
	defaultLoadAssistURL   = "https://daily-cloudcode-pa.googleapis.com/v1internal:loadCodeAssist"
)

func (p *Provider) httpClient() *http.Client {
	if p.HTTPClient != nil {
		return p.HTTPClient
	}
	return provider.DefaultHTTPClient
}

func (p *Provider) quotaURL() string {
	if p.QuotaEndpoint != "" {
		return p.QuotaEndpoint
	}
	return defaultModelsURL
}

func (p *Provider) userAgent() string {
	if envUA := strings.TrimSpace(os.Getenv("AISWAP_AGY_USER_AGENT")); envUA != "" {
		return envUA
	}
	if p.UserAgent != "" {
		return p.UserAgent
	}
	p.uaOnce.Do(func() {
		p.cachedUA = "antigravity"
		if bin, err := exec.LookPath("agy"); err == nil {
			c, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if out, err := exec.CommandContext(c, bin, "--version").Output(); err == nil {
				fields := strings.Fields(string(out))
				if len(fields) > 0 {
					p.cachedUA = "antigravity/" + fields[len(fields)-1]
				}
			}
		}
	})
	return p.cachedUA
}

func (p *Provider) loadAssistURL() string {
	if p.LoadAssistEndpoint != "" {
		return p.LoadAssistEndpoint
	}
	return defaultLoadAssistURL
}

func (p *Provider) quotaSummaryURL() string {
	if p.QuotaSummaryEndpoint != "" {
		return p.QuotaSummaryEndpoint
	}
	return defaultQuotaSummaryURL
}

type assistInfo struct {
	ProjectID string
	Plan      string
}

type agyFetchResult struct {
	Payload     *agyModelsPayload
	Info        assistInfo
	AccessToken string
	UserAgent   string
}

type agyModelsPayload struct {
	Models map[string]struct {
		DisplayName string `json:"displayName"`
		IsInternal  bool   `json:"isInternal"`
		QuotaInfo   *struct {
			RemainingFraction *float64 `json:"remainingFraction"`
			ResetTime         string   `json:"resetTime"`
		} `json:"quotaInfo"`
	} `json:"models"`
}

// fetchAssistInfo calls loadCodeAssist to obtain both the account's project ID
// and real subscription tier (e.g. Ultra / Pro / Free). Falls back to cached project ID on failure.
func (p *Provider) fetchAssistInfo(ctx context.Context, accessToken, ua string) assistInfo {
	var info assistInfo

	reqBody := []byte(`{"metadata":{"ideType":"IDE_UNSPECIFIED","platform":"PLATFORM_UNSPECIFIED","pluginType":"GEMINI"}}`)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.loadAssistURL(), bytes.NewReader(reqBody))
	if err == nil {
		req.Header.Set("Authorization", "Bearer "+accessToken)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", ua)
		if resp, doErr := p.httpClient().Do(req); doErr == nil {
			defer resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				var out struct {
					Project  string `json:"cloudaicompanionProject"`
					PaidTier *struct {
						ID   string `json:"id"`
						Name string `json:"name"`
					} `json:"paidTier"`
					CurrentTier *struct {
						ID   string `json:"id"`
						Name string `json:"name"`
					} `json:"currentTier"`
				}
				if json.NewDecoder(io.LimitReader(resp.Body, 1<<18)).Decode(&out) == nil {
					info.ProjectID = strings.TrimSpace(out.Project)
					if out.PaidTier != nil {
						info.Plan = formatAgyPlan(out.PaidTier.ID, out.PaidTier.Name)
					} else if out.CurrentTier != nil {
						info.Plan = formatAgyPlan(out.CurrentTier.ID, out.CurrentTier.Name)
					}
				}
			}
		}
	}

	if info.ProjectID == "" && p.DataDir != "" {
		if raw, err := os.ReadFile(filepath.Join(p.DataDir, "cache", "default_project_id.txt")); err == nil {
			info.ProjectID = strings.TrimSpace(string(raw))
		}
	}
	return info
}

func (p *Provider) fetchAvailableModels(ctx context.Context, cred *domain.LiveCredential) (*agyFetchResult, error) {
	if cred == nil || len(cred.Raw) == 0 {
		return nil, &provider.QuotaError{Kind: "no-credential", Message: "empty credential"}
	}
	var tf oauthTokenFile
	if err := json.Unmarshal(cred.Raw, &tf); err != nil {
		return nil, &provider.QuotaError{Kind: "bad-credential", Message: err.Error()}
	}
	if tf.Token.AccessToken == "" {
		return nil, &provider.QuotaError{Kind: "no-token", Message: "no access_token in antigravity-oauth-token"}
	}

	ua := p.userAgent()
	info := p.fetchAssistInfo(ctx, tf.Token.AccessToken, ua)
	reqPayload := map[string]string{}
	if info.ProjectID != "" {
		reqPayload["project"] = info.ProjectID
	}
	reqBytes, _ := json.Marshal(reqPayload)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.quotaURL(), bytes.NewReader(reqBytes))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+tf.Token.AccessToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", ua)

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

	var payload agyModelsPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, &provider.QuotaError{Kind: "bad-response", Message: err.Error()}
	}
	return &agyFetchResult{
		Payload:     &payload,
		Info:        info,
		AccessToken: tf.Token.AccessToken,
		UserAgent:   ua,
	}, nil
}

// fetchQuotaSummary queries retrieveUserQuotaSummary for per-pool 5h + 7d (weekly) windows.
// Returns nil on error so callers can cleanly fall back to fetchAvailableModels' binding window.
func (p *Provider) fetchQuotaSummary(ctx context.Context, accessToken, projectID, ua string) map[string][]domain.QuotaWindow {
	reqPayload := map[string]string{}
	if projectID != "" {
		reqPayload["project"] = projectID
	}
	reqBytes, _ := json.Marshal(reqPayload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.quotaSummaryURL(), bytes.NewReader(reqBytes))
	if err != nil {
		return nil
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", ua)

	resp, err := p.httpClient().Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil
	}

	var out struct {
		Groups []struct {
			DisplayName string `json:"displayName"`
			Buckets     []struct {
				Window            string   `json:"window"`
				ResetTime         string   `json:"resetTime"`
				RemainingFraction *float64 `json:"remainingFraction"`
			} `json:"buckets"`
		} `json:"groups"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<18)).Decode(&out); err != nil {
		return nil
	}

	result := map[string][]domain.QuotaWindow{}
	for _, g := range out.Groups {
		fam := classifyModelFamily("", g.DisplayName)
		if fam == "" {
			continue
		}
		var win5h, win7d *domain.QuotaWindow
		for _, b := range g.Buckets {
			if b.RemainingFraction == nil {
				continue
			}
			var label string
			switch strings.ToLower(strings.TrimSpace(b.Window)) {
			case "5h":
				label = "5h"
			case "weekly", "7d":
				label = "7d"
			default:
				continue
			}
			w := &domain.QuotaWindow{
				Name:    fam + " " + label,
				UsedPct: clampUsedPct(*b.RemainingFraction),
			}
			if b.ResetTime != "" {
				if ts, err := time.Parse(time.RFC3339, b.ResetTime); err == nil {
					w.ResetsAt = &ts
				}
			}
			if label == "5h" {
				win5h = w
			} else {
				win7d = w
			}
		}
		var wins []domain.QuotaWindow
		if win5h != nil {
			wins = append(wins, *win5h)
		}
		if win7d != nil {
			wins = append(wins, *win7d)
		}
		if len(wins) > 0 {
			result[fam] = wins
		}
	}
	return result
}

func clampUsedPct(remainingFraction float64) float64 {
	used := (1.0 - remainingFraction) * 100.0
	if used < 0 {
		return 0
	}
	if used > 100 {
		return 100
	}
	return used
}

func inferQuotaPeriod(resetTime string, now time.Time) string {
	if resetTime == "" {
		return ""
	}
	ts, err := time.Parse(time.RFC3339, resetTime)
	if err != nil {
		return ""
	}
	rem := ts.Sub(now)
	if rem > 24*time.Hour {
		return "7d"
	}
	if rem > 0 {
		return "5h"
	}
	return ""
}

func formatAgyPlan(id, name string) string {
	s := strings.ToLower(id + " " + name)
	switch {
	case strings.Contains(s, "ultra"):
		return "Ultra"
	case strings.Contains(s, "pro"):
		return "Pro"
	case strings.Contains(s, "enterprise"):
		return "Enterprise"
	case strings.Contains(s, "standard"):
		return "Standard"
	case strings.Contains(s, "free"):
		return "Free"
	case name != "":
		return name
	default:
		return id
	}
}

func (p *Provider) FetchQuota(ctx context.Context, cred *domain.LiveCredential) (*domain.QuotaReading, error) {
	res, err := p.fetchAvailableModels(ctx, cred)
	if err != nil {
		return nil, err
	}
	summary := p.fetchQuotaSummary(ctx, res.AccessToken, res.Info.ProjectID, res.UserAgent)

	familyOrder := []string{"Gemini", "Claude"}
	pools := map[string]*domain.QuotaWindow{}

	for modelID, m := range res.Payload.Models {
		if m.IsInternal || m.DisplayName == "" || m.QuotaInfo == nil || m.QuotaInfo.RemainingFraction == nil {
			continue
		}
		family := classifyModelFamily(modelID, m.DisplayName)
		if family == "" {
			continue
		}
		used := clampUsedPct(*m.QuotaInfo.RemainingFraction)
		var resetPtr *time.Time
		if m.QuotaInfo.ResetTime != "" {
			if ts, err := time.Parse(time.RFC3339, m.QuotaInfo.ResetTime); err == nil {
				resetPtr = &ts
			}
		}

		cur := pools[family]
		if cur == nil {
			pools[family] = &domain.QuotaWindow{Name: family, UsedPct: used, ResetsAt: resetPtr}
		} else {
			if used > cur.UsedPct {
				cur.UsedPct = used
			}
			if resetPtr != nil && (cur.ResetsAt == nil || resetPtr.Before(*cur.ResetsAt)) {
				cur.ResetsAt = resetPtr
			}
		}
	}

	now := time.Now().UTC()
	windows := make([]domain.QuotaWindow, 0, 4)
	for _, fam := range familyOrder {
		if sumWins, ok := summary[fam]; ok && len(sumWins) > 0 {
			windows = append(windows, sumWins...)
			continue
		}
		if w, ok := pools[fam]; ok {
			if w.ResetsAt != nil {
				if period := inferQuotaPeriod(w.ResetsAt.Format(time.RFC3339), now); period != "" {
					w.Name = fam + " " + period
				}
			}
			windows = append(windows, *w)
		}
	}

	return &domain.QuotaReading{
		Windows:   windows,
		Plan:      res.Info.Plan,
		FetchedAt: now,
	}, nil
}

func classifyModelFamily(modelID, displayName string) string {
	s := strings.ToLower(modelID + " " + displayName)
	switch {
	case strings.Contains(s, "claude") || strings.Contains(s, "gpt"):
		return "Claude"
	case strings.Contains(s, "gemini"):
		return "Gemini"
	default:
		return ""
	}
}
