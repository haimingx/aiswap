package agy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/haimingx/aiswap/internal/domain"
)

func TestAgyFetchQuotaAggregatesFamiliesAndPeriods(t *testing.T) {
	fiveH := time.Now().UTC().Add(4 * time.Hour).Format(time.RFC3339)
	sevenD := time.Now().UTC().Add(6 * 24 * time.Hour).Format(time.RFC3339)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/assist":
			_, _ = w.Write([]byte(`{"cloudaicompanionProject":"proj-123","paidTier":{"id":"g1-ultra-tier","name":"Google AI Ultra"}}`))
		case "/models":
			_, _ = w.Write([]byte(`{
				"models": {
					"gemini-2.5-pro": {"displayName": "Gemini 2.5 Pro", "isInternal": false, "quotaInfo": {"remainingFraction": 0.75, "resetTime": "` + fiveH + `"}},
					"gemini-3-flash": {"displayName": "Gemini 3 Flash", "isInternal": false, "quotaInfo": {"remainingFraction": 0.90, "resetTime": "` + fiveH + `"}},
					"claude-sonnet-5-5-medium": {"displayName": "Claude Sonnet 5.5", "isInternal": false, "quotaInfo": {"remainingFraction": 0.50, "resetTime": "` + sevenD + `"}},
					"internal-model": {"displayName": "Internal", "isInternal": true, "quotaInfo": {"remainingFraction": 0.0}}
				}
			}`))
		case "/summary":
			_, _ = w.Write([]byte(`{
				"groups": [
					{
						"displayName": "Claude and GPT models",
						"buckets": [
							{"window": "weekly", "remainingFraction": 0.50, "resetTime": "` + sevenD + `"},
							{"window": "5h", "remainingFraction": 1.0, "resetTime": "` + fiveH + `"}
						]
					}
				]
			}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	p := &Provider{
		DataDir:              t.TempDir(),
		HTTPClient:           srv.Client(),
		QuotaEndpoint:        srv.URL + "/models",
		QuotaSummaryEndpoint: srv.URL + "/summary",
		LoadAssistEndpoint:   srv.URL + "/assist",
	}
	cred := &domain.LiveCredential{
		Raw: []byte(`{"token":{"access_token":"at"},"id_token_email":"u@gmail.com"}`),
	}
	reading, err := p.FetchQuota(context.Background(), cred)
	if err != nil {
		t.Fatalf("FetchQuota failed: %v", err)
	}
	if reading.Plan != "Ultra" {
		t.Fatalf("Plan = %q, want Ultra", reading.Plan)
	}
	if len(reading.Windows) != 3 {
		t.Fatalf("expected 3 pool windows (Gemini 5h + Claude 5h + Claude 7d), got %+v", reading.Windows)
	}
	if reading.Windows[0].Name != "Gemini 5h" || reading.Windows[0].UsedPct != 25.0 {
		t.Fatalf("unexpected Gemini window: %+v", reading.Windows[0])
	}
	if reading.Windows[1].Name != "Claude 5h" || reading.Windows[1].UsedPct != 0.0 {
		t.Fatalf("unexpected Claude 5h window: %+v", reading.Windows[1])
	}
	if reading.Windows[2].Name != "Claude 7d" || reading.Windows[2].UsedPct != 50.0 {
		t.Fatalf("unexpected Claude 7d window: %+v", reading.Windows[2])
	}

	models, err := p.ListModels(context.Background(), cred)
	if err != nil {
		t.Fatalf("ListModels failed: %v", err)
	}
	var claudeUsage, geminiUsage string
	for _, m := range models {
		if m.Pool == "Claude / GPT Pool" {
			claudeUsage = m.PoolUsage
		}
		if m.Pool == "Gemini Pool" {
			geminiUsage = m.PoolUsage
		}
	}
	if claudeUsage != "5h 0% | 7d 50%" {
		t.Fatalf("Claude PoolUsage = %q, want %q", claudeUsage, "5h 0% | 7d 50%")
	}
	if geminiUsage != "5h 25%" {
		t.Fatalf("Gemini PoolUsage = %q, want %q", geminiUsage, "5h 25%")
	}

	// Verify official `agy models` CLI output takes precedence and overlays pool usage.
	p.ModelsCommand = func(ctx context.Context) ([]byte, error) {
		return []byte(
			"gemini-4-neon-xhigh\tGemini 4 Neon (XHigh)\n" +
				"gemini-4-neon-low\tGemini 4 Neon (Low)\n" +
				"gemini-pro-orchestrator-neon\tOrchestrator (Neon)\n" +
				"claude-opus-5-5-high\tClaude Opus 5.5 (High)\n",
		), nil
	}
	cliModels, err := p.ListModels(context.Background(), cred)
	if err != nil {
		t.Fatalf("ListModels with CLI failed: %v", err)
	}
	if len(cliModels) != 3 {
		t.Fatalf("expected 3 deduplicated CLI models, got %+v", cliModels)
	}
	if cliModels[0].DisplayName != "Gemini 4 Neon" || cliModels[0].Pool != "Gemini Pool" || cliModels[0].PoolUsage != "5h 25%" {
		t.Fatalf("unexpected CLI Gemini model: %+v", cliModels[0])
	}
	if cliModels[1].DisplayName != "Orchestrator (Neon)" || cliModels[1].Pool != "Gemini Pool" || cliModels[1].PoolUsage != "5h 25%" {
		t.Fatalf("unexpected CLI Orchestrator model: %+v", cliModels[1])
	}
	if cliModels[2].DisplayName != "Claude Opus 5.5" || cliModels[2].Pool != "Claude / GPT Pool" || cliModels[2].PoolUsage != "5h 0% | 7d 50%" {
		t.Fatalf("unexpected CLI Claude model: %+v", cliModels[2])
	}
}
