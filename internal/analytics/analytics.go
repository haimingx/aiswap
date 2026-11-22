package analytics

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/haimingx/aiswap/internal/contract"
	"github.com/haimingx/aiswap/internal/domain"
	"github.com/haimingx/aiswap/internal/fsutil"
)

type Collector struct {
	ClaudeProjectsDir string
	CodexSessionsDir  string
	AgyBrainDir       string
	AgyIDEBrainDir    string
	PricingCachePath  string
	PricingURL        string
	Now               func() time.Time
}

func NewCollector() *Collector {
	home, _ := os.UserHomeDir()
	claudeDir := os.Getenv("CLAUDE_CONFIG_DIR")
	if claudeDir == "" {
		claudeDir = filepath.Join(home, ".claude")
	}
	codexDir := os.Getenv("CODEX_HOME")
	if codexDir == "" {
		codexDir = filepath.Join(home, ".codex")
	}
	agyDir := os.Getenv("AGY_DATA_DIR")
	if agyDir == "" {
		agyDir = filepath.Join(home, ".gemini", "antigravity-cli")
	}
	var pricingCache string
	if dataDir, err := fsutil.DataDir(); err == nil {
		pricingCache = filepath.Join(dataDir, "pricing_cache.json")
	}
	return &Collector{
		ClaudeProjectsDir: filepath.Join(claudeDir, "projects"),
		CodexSessionsDir:  filepath.Join(codexDir, "sessions"),
		AgyBrainDir:       filepath.Join(agyDir, "brain"),
		AgyIDEBrainDir:    filepath.Join(home, ".gemini", "antigravity", "brain"),
		PricingCachePath:  pricingCache,
		PricingURL:        DefaultPricingURL,
		Now:               time.Now,
	}
}

func (c *Collector) RefreshPricing(ctx context.Context) (*PricingBook, error) {
	return RefreshPricingCache(ctx, c.PricingURL, c.PricingCachePath)
}

func (c *Collector) Collect(days int) *contract.StatsResponse {
	if days <= 0 {
		days = 7
	}
	nowFn := c.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	now := nowFn()
	loc := now.Location()
	since := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, -days+1)
	book := LoadPricingBook(c.PricingCachePath)

	providers := []contract.ProviderStats{
		c.collectClaude(since, loc, book),
		c.collectCodex(since, loc, book),
		c.collectAgy(since, loc, book),
	}
	var totals contract.TokenUsage
	for _, ps := range providers {
		totals.Merge(ps.Totals)
	}
	totals.RoundCost()

	return &contract.StatsResponse{
		SchemaVersion: contract.SchemaVersion,
		GeneratedAt:   now,
		Days:          days,
		Timezone:      now.Format("MST"),
		Pricing: contract.PricingInfo{
			Source:     book.Source,
			ModelCount: book.Count(),
			UpdatedAt:  book.UpdatedAt,
		},
		Totals:    totals,
		Providers: providers,
	}
}

func (c *Collector) collectClaude(since time.Time, loc *time.Location, book *PricingBook) contract.ProviderStats {
	ps := contract.ProviderStats{Provider: domain.ProviderClaude}
	daily := map[string]*contract.DailyUsage{}
	models := map[string]*contract.ModelUsage{}
	type reqUsage struct {
		day        string
		model      string
		in         int64
		out        int64
		cacheRead  int64
		cacheWrite int64
	}
	byReq := map[string]reqUsage{}

	_ = filepath.WalkDir(c.ClaudeProjectsDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(d.Name(), ".jsonl") {
			return nil
		}
		if info, statErr := d.Info(); statErr == nil && info.ModTime().Before(since) {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer f.Close()

		scanJSONLLines(f, func(line []byte) {
			if !bytes.Contains(line, []byte(`"usage"`)) {
				return
			}
			var entry struct {
				Timestamp string `json:"timestamp"`
				RequestID string `json:"requestId"`
				Message   struct {
					ID    string `json:"id"`
					Model string `json:"model"`
					Usage *struct {
						InputTokens              int64 `json:"input_tokens"`
						OutputTokens             int64 `json:"output_tokens"`
						CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
						CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
					} `json:"usage"`
				} `json:"message"`
			}
			if err := json.Unmarshal(line, &entry); err != nil || entry.Message.Usage == nil {
				return
			}
			if entry.Message.Model == "<synthetic>" {
				return
			}
			ts, err := time.Parse(time.RFC3339Nano, entry.Timestamp)
			if err != nil || ts.Before(since) {
				return
			}
			day := ts.In(loc).Format("2006-01-02")
			model := normalizeModelName(entry.Message.Model)
			u := entry.Message.Usage

			dedupKey := claudeDedupKey(entry.RequestID, entry.Message.ID)
			if dedupKey == "" {
				cost, priced := book.EstimateCost(domain.ProviderClaude, model, u.InputTokens, u.OutputTokens, u.CacheReadInputTokens, u.CacheCreationInputTokens)
				addUsage(daily, models, day, model, u.InputTokens, u.OutputTokens, u.CacheReadInputTokens, u.CacheCreationInputTokens, cost, priced)
				return
			}
			// Streaming transcripts emit multiple rows per request/message; retain the max counts.
			cur := byReq[dedupKey]
			if cur.day == "" {
				cur.day = day
			}
			if cur.model == "" || cur.model == "unknown" {
				cur.model = model
			}
			if u.InputTokens > cur.in {
				cur.in = u.InputTokens
			}
			if u.OutputTokens > cur.out {
				cur.out = u.OutputTokens
			}
			if u.CacheReadInputTokens > cur.cacheRead {
				cur.cacheRead = u.CacheReadInputTokens
			}
			if u.CacheCreationInputTokens > cur.cacheWrite {
				cur.cacheWrite = u.CacheCreationInputTokens
			}
			byReq[dedupKey] = cur
		})
		return nil
	})

	for _, r := range byReq {
		cost, priced := book.EstimateCost(domain.ProviderClaude, r.model, r.in, r.out, r.cacheRead, r.cacheWrite)
		addUsage(daily, models, r.day, r.model, r.in, r.out, r.cacheRead, r.cacheWrite, cost, priced)
	}
	finalizeStats(&ps, daily, models)
	return ps
}

func claudeDedupKey(requestID, messageID string) string {
	switch {
	case requestID != "" && messageID != "":
		return requestID + ":" + messageID
	case requestID != "":
		return requestID
	case messageID != "":
		return messageID
	default:
		return ""
	}
}

func (c *Collector) collectCodex(since time.Time, loc *time.Location, book *PricingBook) contract.ProviderStats {
	ps := contract.ProviderStats{Provider: domain.ProviderCodex}
	daily := map[string]*contract.DailyUsage{}
	models := map[string]*contract.ModelUsage{}

	_ = filepath.WalkDir(c.CodexSessionsDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(d.Name(), ".jsonl") {
			return nil
		}
		if info, statErr := d.Info(); statErr == nil && info.ModTime().Before(since) {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer f.Close()

		currentModel := "unknown"
		type totalKey struct {
			in, out, cached int64
		}
		var prevTotal *totalKey

		scanJSONLLines(f, func(line []byte) {
			isContext := bytes.Contains(line, []byte(`"turn_context"`)) || bytes.Contains(line, []byte(`"session_meta"`))
			isToken := bytes.Contains(line, []byte(`"token_count"`)) || bytes.Contains(line, []byte(`"last_token_usage"`))
			if !isContext && !isToken {
				return
			}
			var entry struct {
				Timestamp string `json:"timestamp"`
				Type      string `json:"type"`
				Payload   struct {
					Model string `json:"model"`
					Info  *struct {
						TotalTokenUsage *struct {
							InputTokens       int64 `json:"input_tokens"`
							CachedInputTokens int64 `json:"cached_input_tokens"`
							OutputTokens      int64 `json:"output_tokens"`
						} `json:"total_token_usage"`
						LastTokenUsage *struct {
							InputTokens       int64 `json:"input_tokens"`
							CachedInputTokens int64 `json:"cached_input_tokens"`
							OutputTokens      int64 `json:"output_tokens"`
						} `json:"last_token_usage"`
					} `json:"info"`
				} `json:"payload"`
			}
			if err := json.Unmarshal(line, &entry); err != nil {
				return
			}
			if (entry.Type == "turn_context" || entry.Type == "session_meta") && entry.Payload.Model != "" {
				currentModel = normalizeModelName(entry.Payload.Model)
				return
			}
			if entry.Payload.Info == nil || entry.Payload.Info.LastTokenUsage == nil {
				return
			}
			// Skip consecutive duplicate snapshots where cumulative total_token_usage has not advanced.
			if tot := entry.Payload.Info.TotalTokenUsage; tot != nil {
				k := totalKey{in: tot.InputTokens, out: tot.OutputTokens, cached: tot.CachedInputTokens}
				if prevTotal != nil && *prevTotal == k {
					return
				}
				prevTotal = &k
			}
			ts, err := time.Parse(time.RFC3339Nano, entry.Timestamp)
			if err != nil || ts.Before(since) {
				return
			}
			u := entry.Payload.Info.LastTokenUsage
			cost, priced := book.EstimateCost(domain.ProviderCodex, currentModel, u.InputTokens, u.OutputTokens, u.CachedInputTokens, 0)
			addUsage(daily, models, ts.In(loc).Format("2006-01-02"), currentModel, u.InputTokens, u.OutputTokens, u.CachedInputTokens, 0, cost, priced)
		})
		return nil
	})

	finalizeStats(&ps, daily, models)
	return ps
}

func (c *Collector) collectAgy(since time.Time, loc *time.Location, book *PricingBook) contract.ProviderStats {
	ps := contract.ProviderStats{Provider: domain.ProviderAgy}
	daily := map[string]*contract.DailyUsage{}
	models := map[string]*contract.ModelUsage{}

	var matches []string
	seenDir := map[string]bool{}
	for _, brainDir := range []string{c.AgyBrainDir, c.AgyIDEBrainDir} {
		if brainDir == "" || seenDir[brainDir] {
			continue
		}
		seenDir[brainDir] = true
		m, _ := filepath.Glob(filepath.Join(brainDir, "*", ".system_generated", "logs", "transcript.jsonl"))
		matches = append(matches, m...)
	}
	for _, path := range matches {
		if info, err := os.Stat(path); err == nil && info.ModTime().Before(since) {
			continue
		}
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		scanJSONLLines(f, func(line []byte) {
			if !bytes.Contains(line, []byte(`"input_tokens"`)) {
				return
			}
			var entry struct {
				CreatedAt       string `json:"created_at"`
				Model           string `json:"model"`
				InputTokens     int64  `json:"input_tokens"`
				OutputTokens    int64  `json:"output_tokens"`
				CacheReadTokens int64  `json:"cache_read_tokens"`
			}
			if err := json.Unmarshal(line, &entry); err != nil {
				return
			}
			if entry.InputTokens == 0 && entry.OutputTokens == 0 && entry.CacheReadTokens == 0 {
				return
			}
			ts, err := time.Parse(time.RFC3339Nano, entry.CreatedAt)
			if err != nil || ts.Before(since) {
				return
			}
			model := normalizeModelName(entry.Model)
			cost, priced := book.EstimateCost(domain.ProviderAgy, model, entry.InputTokens, entry.OutputTokens, entry.CacheReadTokens, 0)
			addUsage(daily, models, ts.In(loc).Format("2006-01-02"), model, entry.InputTokens, entry.OutputTokens, entry.CacheReadTokens, 0, cost, priced)
		})
		_ = f.Close()
	}

	finalizeStats(&ps, daily, models)
	return ps
}

func normalizeModelName(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "unknown"
	}
	return s
}

// scanJSONLLines reads newline-delimited JSON without aborting on arbitrarily large lines.
func scanJSONLLines(r io.Reader, fn func(line []byte)) {
	br := bufio.NewReaderSize(r, 64*1024)
	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 {
			fn(bytes.TrimRight(line, "\r\n"))
		}
		if errors.Is(err, io.EOF) || err != nil {
			return
		}
	}
}

func addUsage(
	daily map[string]*contract.DailyUsage,
	models map[string]*contract.ModelUsage,
	dayKey, model string,
	in, out, cacheRead, cacheWrite int64,
	cost float64,
	priced bool,
) {
	var costPtr *float64
	if priced {
		v := cost
		costPtr = &v
	}

	du := daily[dayKey]
	if du == nil {
		du = &contract.DailyUsage{Date: dayKey}
		daily[dayKey] = du
	}
	du.Add(in, out, cacheRead, cacheWrite, costPtr)

	mu := models[model]
	if mu == nil {
		mu = &contract.ModelUsage{Model: model}
		models[model] = mu
	}
	mu.Add(in, out, cacheRead, cacheWrite, costPtr)
}

func finalizeStats(ps *contract.ProviderStats, daily map[string]*contract.DailyUsage, models map[string]*contract.ModelUsage) {
	days := make([]contract.DailyUsage, 0, len(daily))
	for _, du := range daily {
		du.RoundCost()
		days = append(days, *du)
		ps.Totals.Merge(du.TokenUsage)
	}
	ps.Totals.RoundCost()
	sort.Slice(days, func(i, j int) bool { return days[i].Date < days[j].Date })
	ps.Daily = days

	modelList := make([]contract.ModelUsage, 0, len(models))
	for _, mu := range models {
		mu.RoundCost()
		modelList = append(modelList, *mu)
	}
	sort.Slice(modelList, func(i, j int) bool {
		ti := modelList[i].InputTokens + modelList[i].OutputTokens + modelList[i].CacheReadTokens + modelList[i].CacheWriteTokens
		tj := modelList[j].InputTokens + modelList[j].OutputTokens + modelList[j].CacheReadTokens + modelList[j].CacheWriteTokens
		if ti != tj {
			return ti > tj
		}
		return modelList[i].Model < modelList[j].Model
	})
	ps.Models = modelList
}
