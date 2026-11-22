package analytics

import (
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/haimingx/aiswap/internal/domain"
	"github.com/haimingx/aiswap/internal/fsutil"
)

func TestCollectAllThreeProviders(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)

	claudeJSONL := `{"timestamp":"2026-11-22T09:00:00Z","requestId":"req-1","message":{"id":"msg-1","model":"claude-sonnet-4-5-20250929","usage":{"input_tokens":100,"output_tokens":10,"cache_read_input_tokens":500,"cache_creation_input_tokens":20}}}
{"timestamp":"2026-11-22T09:00:01Z","requestId":"req-1","message":{"id":"msg-1","model":"claude-sonnet-4-5-20250929","usage":{"input_tokens":100,"output_tokens":50,"cache_read_input_tokens":500,"cache_creation_input_tokens":20}}}
{"timestamp":"2026-11-22T09:02:00Z","message":{"id":"msg-ds-1","model":"deepseek-chat","usage":{"input_tokens":200,"output_tokens":10}}}
{"timestamp":"2026-11-22T09:02:01Z","message":{"id":"msg-ds-1","model":"deepseek-chat","usage":{"input_tokens":200,"output_tokens":40}}}
{"timestamp":"2026-11-22T09:03:00Z","message":{"id":"msg-syn","model":"<synthetic>","usage":{"input_tokens":999,"output_tokens":999}}}
{"timestamp":"2026-11-21T08:00:00Z","requestId":"req-2","message":{"id":"msg-2","model":"claude-opus-4-5","usage":{"input_tokens":200,"output_tokens":80,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}}}
`
	if err := fsutil.AtomicWriteFile(filepath.Join(dir, "claude", "proj-a", "sess.jsonl"), []byte(claudeJSONL), 0o600); err != nil {
		t.Fatal(err)
	}

	codexJSONL := `{"timestamp":"2026-11-22T09:05:00Z","type":"turn_context","payload":{"model":"gpt-5.3-codex"}}
{"timestamp":"2026-11-22T09:05:10Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":1000,"cached_input_tokens":800,"output_tokens":100},"last_token_usage":{"input_tokens":1000,"cached_input_tokens":800,"output_tokens":100}}}}
{"timestamp":"2026-11-22T09:05:11Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":1000,"cached_input_tokens":800,"output_tokens":100},"last_token_usage":{"input_tokens":1000,"cached_input_tokens":800,"output_tokens":100}}}}
{"timestamp":"2026-11-22T09:06:00Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":1500,"cached_input_tokens":1000,"output_tokens":200},"last_token_usage":{"input_tokens":500,"cached_input_tokens":200,"output_tokens":100}}}}
`
	if err := fsutil.AtomicWriteFile(filepath.Join(dir, "codex", "2026", "11", "sess.jsonl"), []byte(codexJSONL), 0o600); err != nil {
		t.Fatal(err)
	}

	agyJSONL := `{"created_at":"2026-11-22T09:10:00Z","input_tokens":400,"output_tokens":120,"cache_read_tokens":900,"model":"gemini-2.5-pro-medium"}
{"created_at":"2026-11-22T09:11:00Z","input_tokens":0,"output_tokens":0,"cache_read_tokens":0}
`
	if err := fsutil.AtomicWriteFile(filepath.Join(dir, "agy", "conv-1", ".system_generated", "logs", "transcript.jsonl"), []byte(agyJSONL), 0o600); err != nil {
		t.Fatal(err)
	}
	agyIDEJSONL := `{"created_at":"2026-11-22T09:15:00Z","input_tokens":100,"output_tokens":30,"cache_read_tokens":200}
`
	if err := fsutil.AtomicWriteFile(filepath.Join(dir, "agy-ide", "conv-2", ".system_generated", "logs", "transcript.jsonl"), []byte(agyIDEJSONL), 0o600); err != nil {
		t.Fatal(err)
	}

	c := &Collector{
		ClaudeProjectsDir: filepath.Join(dir, "claude"),
		CodexSessionsDir:  filepath.Join(dir, "codex"),
		AgyBrainDir:       filepath.Join(dir, "agy"),
		AgyIDEBrainDir:    filepath.Join(dir, "agy-ide"),
		Now:               func() time.Time { return now },
	}
	resp := c.Collect(7)
	if len(resp.Providers) != 3 {
		t.Fatalf("expected 3 providers, got %d", len(resp.Providers))
	}
	if resp.Pricing.Source != "builtin" || resp.Pricing.ModelCount == 0 {
		t.Fatalf("expected builtin pricing info, got %+v", resp.Pricing)
	}

	// Claude: 3 deduped requests (<synthetic> ignored, msg-ds-1 deduped by message.id)
	cl := resp.Providers[0]
	if cl.Totals.Requests != 3 || cl.Totals.InputTokens != 500 || cl.Totals.OutputTokens != 170 || cl.Totals.CacheReadTokens != 500 {
		t.Fatalf("unexpected claude stats: %+v", cl)
	}
	if len(cl.Models) != 3 {
		t.Fatalf("expected 3 claude models, got %+v", cl.Models)
	}
	if cl.Totals.EstimatedCostUSD == nil || *cl.Totals.EstimatedCostUSD <= 0 {
		t.Fatalf("expected positive claude cost, got %v", cl.Totals.EstimatedCostUSD)
	}

	// Codex: duplicate total_token_usage snapshot skipped -> 2 requests, 1500 input, 200 output, 1000 cached
	cx := resp.Providers[1]
	if cx.Totals.Requests != 2 || cx.Totals.InputTokens != 1500 || cx.Totals.OutputTokens != 200 || cx.Totals.CacheReadTokens != 1000 {
		t.Fatalf("unexpected codex stats: %+v", cx)
	}
	if len(cx.Models) != 1 || cx.Models[0].Model != "gpt-5.3-codex" || cx.Models[0].EstimatedCostUSD == nil {
		t.Fatalf("unexpected codex models: %+v", cx.Models)
	}
	// Non-cached input = 500 * 1.75/1M + 1000 * 0.175/1M + 200 * 14.0/1M = 0.00385
	wantCodexCost := (500*1.75 + 1000*0.175 + 200*14.0) / 1_000_000.0
	if cx.Totals.EstimatedCostUSD == nil || math.Abs(*cx.Totals.EstimatedCostUSD-wantCodexCost) > 1e-6 {
		t.Fatalf("codex cost = %v, want %.6f", cx.Totals.EstimatedCostUSD, wantCodexCost)
	}

	// AGY: gemini-2.5-pro-medium strips -medium and matches builtin gemini-2.5-pro; empty model becomes unpriced "unknown"
	ag := resp.Providers[2]
	if ag.Totals.Requests != 2 || ag.Totals.InputTokens != 500 || ag.Totals.OutputTokens != 150 || ag.Totals.CacheReadTokens != 1100 {
		t.Fatalf("unexpected agy stats: %+v", ag)
	}
	if len(ag.Models) != 2 || ag.Models[0].EstimatedCostUSD == nil || ag.Models[1].EstimatedCostUSD != nil {
		t.Fatalf("unexpected agy model pricing: %+v", ag.Models)
	}

	// Top-level totals aggregate all 3 providers.
	if resp.Totals.Requests != 7 || resp.Totals.EstimatedCostUSD == nil || *resp.Totals.EstimatedCostUSD <= 0 {
		t.Fatalf("unexpected top-level totals: %+v", resp.Totals)
	}
}

func TestCollectLocalTimezoneBucketing(t *testing.T) {
	dir := t.TempDir()
	cst := time.FixedZone("CST", 8*3600)
	// 2026-11-22 10:00 CST
	now := time.Date(2026, 11, 22, 10, 0, 0, 0, cst)
	// 2026-11-21 16:30 UTC == 2026-11-22 00:30 CST (must bucket into 2026-11-22, not 2026-11-21)
	agyJSONL := `{"created_at":"2026-11-21T16:30:00Z","input_tokens":100,"output_tokens":20,"model":"gemini-2.5-pro"}
`
	if err := fsutil.AtomicWriteFile(filepath.Join(dir, "agy", "c1", ".system_generated", "logs", "transcript.jsonl"), []byte(agyJSONL), 0o600); err != nil {
		t.Fatal(err)
	}

	c := &Collector{
		ClaudeProjectsDir: filepath.Join(dir, "missing-c"),
		CodexSessionsDir:  filepath.Join(dir, "missing-x"),
		AgyBrainDir:       filepath.Join(dir, "agy"),
		Now:               func() time.Time { return now },
	}
	resp := c.Collect(1)
	if resp.Timezone != "CST" {
		t.Fatalf("expected CST timezone, got %q", resp.Timezone)
	}
	ag := resp.Providers[2]
	if len(ag.Daily) != 1 || ag.Daily[0].Date != "2026-11-22" {
		t.Fatalf("expected CST day 2026-11-22, got %+v", ag.Daily)
	}
}

func TestRefreshPricingCacheAndOverlay(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{
			"sample_spec": {"mode": "chat", "input_cost_per_token": 1},
			"claude-opus-5-5": {
				"mode": "chat",
				"input_cost_per_token": 0.000010,
				"output_cost_per_token": 0.000050,
				"cache_read_input_token_cost": 0.000001
			},
			"gpt-6-astra": {
				"mode": "responses",
				"input_cost_per_token": 0.000002,
				"output_cost_per_token": 0.000010,
				"cache_read_input_token_cost": 0.0000002
			},
			"text-embedding-3-large": {
				"mode": "embedding",
				"input_cost_per_token": 0.00000013
			}
		}`))
	}))
	defer srv.Close()

	cachePath := filepath.Join(t.TempDir(), "pricing_cache.json")
	book, err := RefreshPricingCache(context.Background(), srv.URL, cachePath)
	if err != nil {
		t.Fatalf("RefreshPricingCache failed: %v", err)
	}
	if book.Source != "cached" || book.UpdatedAt == nil {
		t.Fatalf("expected cached pricing book with timestamp, got source=%q updatedAt=%v", book.Source, book.UpdatedAt)
	}

	// Online model with effort suffix resolves via cache.
	cost, ok := book.EstimateCost(domain.ProviderAgy, "claude-opus-5-5-medium", 1_000_000, 100_000, 500_000, 0)
	if !ok {
		t.Fatalf("expected claude-opus-5-5-medium to resolve from refreshed cache")
	}
	want := 10.0 + 5.0 + 0.5
	if math.Abs(cost-want) > 1e-6 {
		t.Fatalf("cost = %.6f, want %.6f", cost, want)
	}

	// Non-chat embedding model must be filtered out.
	if _, ok := book.Lookup("text-embedding-3-large"); ok {
		t.Fatalf("expected embedding model to be filtered out")
	}
}
