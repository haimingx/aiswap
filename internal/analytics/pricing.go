package analytics

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/haimingx/aiswap/internal/domain"
	"github.com/haimingx/aiswap/internal/fsutil"
)

const DefaultPricingURL = "https://raw.githubusercontent.com/BerriAI/litellm/main/model_prices_and_context_window.json"

// ModelPrice holds USD rates per 1,000,000 tokens.
type ModelPrice struct {
	InputPerMillion      float64 `json:"inputPerMillion"`
	OutputPerMillion     float64 `json:"outputPerMillion"`
	CacheReadPerMillion  float64 `json:"cacheReadPerMillion,omitempty"`
	CacheWritePerMillion float64 `json:"cacheWritePerMillion,omitempty"`
}

type PricingBook struct {
	Source    string
	UpdatedAt *time.Time
	prices    map[string]ModelPrice
}

type pricingCacheFile struct {
	UpdatedAt time.Time             `json:"updatedAt"`
	SourceURL string                `json:"sourceUrl,omitempty"`
	Models    map[string]ModelPrice `json:"models"`
}

func (b *PricingBook) Count() int {
	if b == nil {
		return 0
	}
	return len(b.prices)
}

// builtinPrices provides zero-network fallback reference pricing (USD per 1M tokens).
func builtinPrices() map[string]ModelPrice {
	return map[string]ModelPrice{
		// Anthropic Claude
		"claude-opus-5-5":   {InputPerMillion: 4.0, OutputPerMillion: 20.0, CacheReadPerMillion: 0.2, CacheWritePerMillion: 5.0},
		"claude-sonnet-5-5": {InputPerMillion: 2.0, OutputPerMillion: 10.0, CacheReadPerMillion: 0.1, CacheWritePerMillion: 2.5},
		"claude-opus-4-6":   {InputPerMillion: 15.0, OutputPerMillion: 75.0, CacheReadPerMillion: 1.5, CacheWritePerMillion: 18.75},
		"claude-opus-4-5":   {InputPerMillion: 5.0, OutputPerMillion: 25.0, CacheReadPerMillion: 0.5, CacheWritePerMillion: 6.25},
		"claude-opus-4-1":   {InputPerMillion: 15.0, OutputPerMillion: 75.0, CacheReadPerMillion: 1.5, CacheWritePerMillion: 18.75},
		"claude-opus-4":     {InputPerMillion: 15.0, OutputPerMillion: 75.0, CacheReadPerMillion: 1.5, CacheWritePerMillion: 18.75},
		"claude-sonnet-4-6": {InputPerMillion: 3.0, OutputPerMillion: 15.0, CacheReadPerMillion: 0.3, CacheWritePerMillion: 3.75},
		"claude-sonnet-4-5": {InputPerMillion: 3.0, OutputPerMillion: 15.0, CacheReadPerMillion: 0.3, CacheWritePerMillion: 3.75},
		"claude-sonnet-4":   {InputPerMillion: 3.0, OutputPerMillion: 15.0, CacheReadPerMillion: 0.3, CacheWritePerMillion: 3.75},
		"claude-3-7-sonnet": {InputPerMillion: 3.0, OutputPerMillion: 15.0, CacheReadPerMillion: 0.3, CacheWritePerMillion: 3.75},
		"claude-3-5-sonnet": {InputPerMillion: 3.0, OutputPerMillion: 15.0, CacheReadPerMillion: 0.3, CacheWritePerMillion: 3.75},
		"claude-haiku-4-5":  {InputPerMillion: 1.0, OutputPerMillion: 5.0, CacheReadPerMillion: 0.1, CacheWritePerMillion: 1.25},
		"claude-3-5-haiku":  {InputPerMillion: 0.8, OutputPerMillion: 4.0, CacheReadPerMillion: 0.08, CacheWritePerMillion: 1.0},

		// OpenAI / Codex
		"gpt-6.1-sol":       {InputPerMillion: 2.0, OutputPerMillion: 10.0, CacheReadPerMillion: 0.1},
		"gpt-6-astra":       {InputPerMillion: 10.0, OutputPerMillion: 50.0, CacheReadPerMillion: 1.0},
		"gpt-6-luna":        {InputPerMillion: 0.1, OutputPerMillion: 0.5, CacheReadPerMillion: 0.01},
		"gpt-5.4":           {InputPerMillion: 2.5, OutputPerMillion: 15.0, CacheReadPerMillion: 0.25},
		"gpt-5.3-codex":     {InputPerMillion: 1.75, OutputPerMillion: 14.0, CacheReadPerMillion: 0.175},
		"gpt-5.2-codex":     {InputPerMillion: 1.75, OutputPerMillion: 14.0, CacheReadPerMillion: 0.175},
		"gpt-5.2":           {InputPerMillion: 1.75, OutputPerMillion: 14.0, CacheReadPerMillion: 0.175},
		"gpt-5.1-codex":     {InputPerMillion: 1.25, OutputPerMillion: 10.0, CacheReadPerMillion: 0.125},
		"gpt-5.1":           {InputPerMillion: 1.25, OutputPerMillion: 10.0, CacheReadPerMillion: 0.125},
		"gpt-5-codex":       {InputPerMillion: 1.25, OutputPerMillion: 10.0, CacheReadPerMillion: 0.125},
		"gpt-5-mini":        {InputPerMillion: 0.25, OutputPerMillion: 2.0, CacheReadPerMillion: 0.025},
		"gpt-5-nano":        {InputPerMillion: 0.05, OutputPerMillion: 0.4, CacheReadPerMillion: 0.005},
		"gpt-5":             {InputPerMillion: 1.25, OutputPerMillion: 10.0, CacheReadPerMillion: 0.125},
		"codex-mini-latest": {InputPerMillion: 1.5, OutputPerMillion: 6.0, CacheReadPerMillion: 0.375},
		"gpt-4.1":           {InputPerMillion: 2.0, OutputPerMillion: 8.0, CacheReadPerMillion: 0.5},
		"gpt-4.1-mini":      {InputPerMillion: 0.4, OutputPerMillion: 1.6, CacheReadPerMillion: 0.1},
		"gpt-4o":            {InputPerMillion: 2.5, OutputPerMillion: 10.0, CacheReadPerMillion: 1.25},
		"o3":                {InputPerMillion: 2.0, OutputPerMillion: 8.0, CacheReadPerMillion: 0.5},
		"o4-mini":           {InputPerMillion: 1.1, OutputPerMillion: 4.4, CacheReadPerMillion: 0.275},

		// Google Gemini
		"gemini-4-neon":        {InputPerMillion: 3.0, OutputPerMillion: 15.0, CacheReadPerMillion: 0.15},
		"gemini-4-carbon":      {InputPerMillion: 2.0, OutputPerMillion: 10.0, CacheReadPerMillion: 0.1},
		"gemini-4-argon":       {InputPerMillion: 2.0, OutputPerMillion: 10.0, CacheReadPerMillion: 0.1},
		"gemini-3-pro":         {InputPerMillion: 2.0, OutputPerMillion: 12.0, CacheReadPerMillion: 0.2},
		"gemini-3-flash":       {InputPerMillion: 0.5, OutputPerMillion: 3.0, CacheReadPerMillion: 0.05},
		"gemini-2.5-pro":       {InputPerMillion: 1.25, OutputPerMillion: 10.0, CacheReadPerMillion: 0.125},
		"gemini-2.5-flash":     {InputPerMillion: 0.3, OutputPerMillion: 2.5, CacheReadPerMillion: 0.03},
		"gemini-2.5-flash-lite": {InputPerMillion: 0.1, OutputPerMillion: 0.4, CacheReadPerMillion: 0.01},

		// DeepSeek (commonly used via Anthropic-compatible base URL in Claude Code)
		"deepseek-flash":    {InputPerMillion: 0.3, OutputPerMillion: 1.2, CacheReadPerMillion: 0.006},
		"deepseek-chat":     {InputPerMillion: 0.27, OutputPerMillion: 1.1, CacheReadPerMillion: 0.07},
		"deepseek-reasoner": {InputPerMillion: 0.55, OutputPerMillion: 2.19, CacheReadPerMillion: 0.14},
	}
}

// LoadPricingBook loads the built-in offline pricing table and overlays any locally cached online pricing.
func LoadPricingBook(cachePath string) *PricingBook {
	book := &PricingBook{
		Source: "builtin",
		prices: builtinPrices(),
	}
	if cachePath == "" {
		return book
	}
	raw, err := os.ReadFile(cachePath)
	if err != nil {
		return book
	}
	var cached pricingCacheFile
	if err := json.Unmarshal(raw, &cached); err != nil || len(cached.Models) == 0 {
		return book
	}
	for k, v := range cached.Models {
		book.prices[strings.ToLower(k)] = v
	}
	book.Source = "cached"
	if !cached.UpdatedAt.IsZero() {
		ts := cached.UpdatedAt
		book.UpdatedAt = &ts
	}
	return book
}

// RefreshPricingCache downloads the latest LiteLLM pricing table, filters relevant LLM models,
// and saves a compact local cache for offline use.
func RefreshPricingCache(ctx context.Context, url, cachePath string) (*PricingBook, error) {
	if url == "" {
		url = DefaultPricingURL
	}
	if cachePath == "" {
		return nil, fmt.Errorf("pricing cache path is empty")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "aiswap")

	client := &http.Client{Timeout: 12 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch pricing table: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch pricing table: HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 24<<20))
	if err != nil {
		return nil, fmt.Errorf("read pricing table: %w", err)
	}

	models, err := parseLiteLLMPricing(body)
	if err != nil {
		return nil, err
	}
	if len(models) == 0 {
		return nil, fmt.Errorf("pricing table contained no matching models")
	}

	now := time.Now().UTC()
	payload, err := json.MarshalIndent(pricingCacheFile{
		UpdatedAt: now,
		SourceURL: url,
		Models:    models,
	}, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := fsutil.AtomicWriteFile(cachePath, payload, 0o600); err != nil {
		return nil, fmt.Errorf("save pricing cache: %w", err)
	}
	return LoadPricingBook(cachePath), nil
}

func parseLiteLLMPricing(data []byte) (map[string]ModelPrice, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse pricing JSON: %w", err)
	}

	out := make(map[string]ModelPrice, 128)
	for key, val := range raw {
		if key == "sample_spec" {
			continue
		}
		var entry struct {
			Mode                      string  `json:"mode"`
			InputCostPerToken         float64 `json:"input_cost_per_token"`
			OutputCostPerToken        float64 `json:"output_cost_per_token"`
			CacheReadInputTokenCost   float64 `json:"cache_read_input_token_cost"`
			CacheCreationInputTokCost float64 `json:"cache_creation_input_token_cost"`
		}
		if err := json.Unmarshal(val, &entry); err != nil {
			continue
		}
		if entry.Mode != "" && entry.Mode != "chat" && entry.Mode != "responses" {
			continue
		}
		if entry.InputCostPerToken <= 0 && entry.OutputCostPerToken <= 0 {
			continue
		}
		canonical := stripProviderPrefix(strings.ToLower(strings.TrimSpace(key)))
		if !isTrackedModelFamily(canonical) {
			continue
		}
		// Prefer unprefixed canonical entries over provider-prefixed duplicates.
		if _, exists := out[canonical]; exists && strings.Contains(key, "/") {
			continue
		}
		out[canonical] = ModelPrice{
			InputPerMillion:      entry.InputCostPerToken * 1_000_000,
			OutputPerMillion:     entry.OutputCostPerToken * 1_000_000,
			CacheReadPerMillion:  entry.CacheReadInputTokenCost * 1_000_000,
			CacheWritePerMillion: entry.CacheCreationInputTokCost * 1_000_000,
		}
	}
	return out, nil
}

func isTrackedModelFamily(name string) bool {
	for _, prefix := range []string{"claude-", "gpt-", "o1", "o3", "o4", "codex-", "gemini-", "deepseek-"} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

var (
	reEffortSuffix = regexp.MustCompile(`-(low|medium|high|max|minimal|xhigh|thinking)$`)
	reDateSuffix   = regexp.MustCompile(`-(\d{8}|\d{4}-\d{2}-\d{2}|latest|preview)$`)
)

func stripProviderPrefix(name string) string {
	for _, p := range []string{"anthropic/", "openai/", "google/", "gemini/", "vertex_ai/", "deepseek/", "bedrock/"} {
		if strings.HasPrefix(name, p) {
			return strings.TrimPrefix(name, p)
		}
	}
	return name
}

// Lookup resolves a model name to its pricing using exact, normalized, and bounded prefix matching.
func (b *PricingBook) Lookup(model string) (ModelPrice, bool) {
	if b == nil || len(b.prices) == 0 {
		return ModelPrice{}, false
	}
	norm := stripProviderPrefix(strings.ToLower(strings.TrimSpace(model)))
	if norm == "" || norm == "unknown" {
		return ModelPrice{}, false
	}

	candidates := []string{norm}
	if stripped := reEffortSuffix.ReplaceAllString(norm, ""); stripped != norm {
		candidates = append(candidates, stripped)
	}
	for _, c := range append([]string(nil), candidates...) {
		if d := reDateSuffix.ReplaceAllString(c, ""); d != c {
			candidates = append(candidates, d)
		}
	}

	for _, c := range candidates {
		if p, ok := b.prices[c]; ok {
			return p, true
		}
	}

	// Longest boundary-aligned prefix match (e.g. "claude-sonnet-4-5-20250929" -> "claude-sonnet-4-5").
	bestLen := 0
	var best ModelPrice
	for _, c := range candidates {
		for k, p := range b.prices {
			if len(k) < 6 || len(k) <= bestLen || !strings.HasPrefix(c, k) {
				continue
			}
			if len(c) == len(k) || c[len(k)] == '-' || c[len(k)] == '.' || c[len(k)] == ':' {
				bestLen = len(k)
				best = p
			}
		}
	}
	if bestLen > 0 {
		return best, true
	}
	return ModelPrice{}, false
}

// EstimateCost computes estimated USD cost for a single usage record.
// For Codex (OpenAI), input_tokens includes cached_input_tokens, so non-cached input is max(0, in-cacheRead).
func (b *PricingBook) EstimateCost(provider domain.ProviderName, model string, in, out, cacheRead, cacheWrite int64) (float64, bool) {
	p, ok := b.Lookup(model)
	if !ok {
		return 0, false
	}
	nonCachedIn := in
	if provider == domain.ProviderCodex {
		nonCachedIn = in - cacheRead
		if nonCachedIn < 0 {
			nonCachedIn = 0
		}
	}
	cost := (float64(nonCachedIn)*p.InputPerMillion +
		float64(out)*p.OutputPerMillion +
		float64(cacheRead)*p.CacheReadPerMillion +
		float64(cacheWrite)*p.CacheWritePerMillion) / 1_000_000.0
	return cost, true
}
