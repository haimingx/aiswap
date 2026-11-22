package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/haimingx/aiswap/internal/analytics"
	"github.com/haimingx/aiswap/internal/domain"
	"github.com/haimingx/aiswap/internal/engine"
	"github.com/haimingx/aiswap/internal/fsutil"
	"github.com/haimingx/aiswap/internal/keychain"
	"github.com/haimingx/aiswap/internal/provider"
	"github.com/haimingx/aiswap/internal/store"
	"github.com/haimingx/aiswap/internal/tui"
)

type demoAccount struct {
	id     domain.Identity
	alias  string
	off    bool
	quota  domain.QuotaReading
	models []domain.ModelInfo
}

type demoProvider struct {
	name     domain.ProviderName
	mu       sync.Mutex
	activeID string
	accounts map[string]demoAccount
}

func (d *demoProvider) Name() domain.ProviderName { return d.name }
func (d *demoProvider) IsInstalled() bool         { return true }
func (d *demoProvider) AcquireNativeLock(context.Context) (func(), error) {
	return func() {}, nil
}

func (d *demoProvider) ReadLive(context.Context) (*domain.LiveCredential, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	raw, _ := json.Marshal(map[string]string{"id": d.activeID})
	return &domain.LiveCredential{Raw: raw, Backend: "memory"}, nil
}

func (d *demoProvider) WriteLive(_ context.Context, cred *domain.LiveCredential) error {
	var payload struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(cred.Raw, &payload); err != nil {
		return err
	}
	d.mu.Lock()
	d.activeID = payload.ID
	d.mu.Unlock()
	return nil
}

func (d *demoProvider) Identify(_ context.Context, cred *domain.LiveCredential) (*domain.Identity, error) {
	var payload struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(cred.Raw, &payload); err != nil {
		return nil, err
	}
	acct, ok := d.accounts[payload.ID]
	if !ok {
		return nil, provider.ErrNotLoggedIn
	}
	id := acct.id
	return &id, nil
}

func (d *demoProvider) FetchQuota(_ context.Context, cred *domain.LiveCredential) (*domain.QuotaReading, error) {
	id, err := d.Identify(context.Background(), cred)
	if err != nil {
		return nil, err
	}
	acct := d.accounts[id.Email]
	q := acct.quota
	q.FetchedAt = time.Now().UTC()
	return &q, nil
}

func (d *demoProvider) Refresh(context.Context, *domain.LiveCredential) domain.RefreshOutcome {
	return domain.RefreshOutcome{Err: provider.ErrNotImplemented}
}

func (d *demoProvider) ListModels(_ context.Context, cred *domain.LiveCredential) ([]domain.ModelInfo, error) {
	id, err := d.Identify(context.Background(), cred)
	if err != nil {
		return nil, err
	}
	return d.accounts[id.Email].models, nil
}

func ptrTime(d time.Duration) *time.Time {
	t := time.Now().UTC().Add(d)
	return &t
}

func main() {
	tmpDir, err := os.MkdirTemp("", "aiswap-demo-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "demo init: %v\n", err)
		os.Exit(1)
	}
	defer os.RemoveAll(tmpDir)

	claudeModels := []domain.ModelInfo{
		{ID: "claude-opus-5-5", DisplayName: "Claude Opus 5.5"},
		{ID: "claude-sonnet-5-5", DisplayName: "Claude Sonnet 5.5"},
		{ID: "claude-haiku-5-5", DisplayName: "Claude Haiku 5.5"},
		{ID: "claude-opus-4-6", DisplayName: "Claude Opus 4.6"},
		{ID: "claude-sonnet-4-6", DisplayName: "Claude Sonnet 4.6"},
	}
	codexModels := []domain.ModelInfo{
		{ID: "gpt-6.2-sol", DisplayName: "GPT-6.2-Sol"},
		{ID: "gpt-6.2-astra", DisplayName: "GPT-6.2-Astra"},
		{ID: "gpt-6.1-sol", DisplayName: "GPT-6.1-Sol"},
		{ID: "gpt-6-astra", DisplayName: "GPT-6-Astra"},
		{ID: "gpt-5.6-sol", DisplayName: "GPT-5.6-Sol"},
	}
	agyModels := []domain.ModelInfo{
		{ID: "gemini-4-neon", DisplayName: "Gemini 4 Neon", Pool: "Gemini Pool", PoolUsage: "5h 4%"},
		{ID: "gemini-4-carbon", DisplayName: "Gemini 4 Carbon", Pool: "Gemini Pool", PoolUsage: "5h 4%"},
		{ID: "gemini-4-argon", DisplayName: "Gemini 4 Argon", Pool: "Gemini Pool", PoolUsage: "5h 4%"},
		{ID: "gemini-3.8-flash", DisplayName: "Gemini 3.8 Flash", Pool: "Gemini Pool", PoolUsage: "5h 4%"},
		{ID: "gemini-pro-orchestrator-neon", DisplayName: "Orchestrator (Neon)", Pool: "Gemini Pool", PoolUsage: "5h 4%"},
		{ID: "claude-opus-5-5", DisplayName: "Claude Opus 5.5", Pool: "Claude / GPT Pool", PoolUsage: "5h 0% | 7d 50%"},
		{ID: "claude-sonnet-5-5", DisplayName: "Claude Sonnet 5.5", Pool: "Claude / GPT Pool", PoolUsage: "5h 0% | 7d 50%"},
		{ID: "gpt-oss-120b", DisplayName: "GPT-OSS 120B", Pool: "Claude / GPT Pool", PoolUsage: "5h 0% | 7d 50%"},
	}

	claudeProv := &demoProvider{
		name:     domain.ProviderClaude,
		activeID: "work@acme.dev",
		accounts: map[string]demoAccount{
			"work@acme.dev": {
				id:    domain.Identity{Email: "work@acme.dev", Plan: "Max 5x"},
				alias: "work",
				quota: domain.QuotaReading{
					Plan: "Max 5x",
					Windows: []domain.QuotaWindow{
						{Name: "5h", UsedPct: 24, ResetsAt: ptrTime(3*time.Hour + 12*time.Minute)},
						{Name: "7d", UsedPct: 42, ResetsAt: ptrTime(4*24*time.Hour + 8*time.Hour)},
					},
				},
				models: claudeModels,
			},
			"me@acme.dev": {
				id:    domain.Identity{Email: "me@acme.dev", Plan: "Pro"},
				alias: "me",
				quota: domain.QuotaReading{
					Plan: "Pro",
					Windows: []domain.QuotaWindow{
						{Name: "5h", UsedPct: 8, ResetsAt: ptrTime(4*time.Hour + 30*time.Minute)},
						{Name: "7d", UsedPct: 15, ResetsAt: ptrTime(5 * 24 * time.Hour)},
					},
				},
				models: claudeModels,
			},
			"lab@acme.dev": {
				id:    domain.Identity{Email: "lab@acme.dev", Plan: "Pro"},
				alias: "lab",
				off:   true,
				quota: domain.QuotaReading{
					Plan: "Pro",
					Windows: []domain.QuotaWindow{
						{Name: "5h", UsedPct: 0},
						{Name: "7d", UsedPct: 5},
					},
				},
				models: claudeModels,
			},
		},
	}

	codexProv := &demoProvider{
		name:     domain.ProviderCodex,
		activeID: "dev@studio.io",
		accounts: map[string]demoAccount{
			"dev@studio.io": {
				id:    domain.Identity{Email: "dev@studio.io", Plan: "Pro"},
				alias: "main",
				quota: domain.QuotaReading{
					Plan: "Pro",
					Windows: []domain.QuotaWindow{
						{Name: "5h", UsedPct: 12, ResetsAt: ptrTime(4*time.Hour + 5*time.Minute)},
						{Name: "7d", UsedPct: 85, ResetsAt: ptrTime(2*24*time.Hour + 3*time.Hour)},
					},
				},
				models: codexModels,
			},
			"alt@studio.io": {
				id:    domain.Identity{Email: "alt@studio.io", Plan: "Plus"},
				alias: "alt",
				quota: domain.QuotaReading{
					Plan: "Plus",
					Windows: []domain.QuotaWindow{
						{Name: "5h", UsedPct: 0},
						{Name: "7d", UsedPct: 18, ResetsAt: ptrTime(5 * 24 * time.Hour)},
					},
				},
				models: codexModels,
			},
		},
	}

	agyProv := &demoProvider{
		name:     domain.ProviderAgy,
		activeID: "me@gmail.com",
		accounts: map[string]demoAccount{
			"me@gmail.com": {
				id:    domain.Identity{Email: "me@gmail.com", Plan: "Ultra"},
				alias: "pro",
				quota: domain.QuotaReading{
					Plan: "Ultra",
					Windows: []domain.QuotaWindow{
						{Name: "Gemini 5h", UsedPct: 4, ResetsAt: ptrTime(3*time.Hour + 6*time.Minute)},
						{Name: "Claude 5h", UsedPct: 0},
						{Name: "Claude 7d", UsedPct: 50, ResetsAt: ptrTime(6*24*time.Hour + 17*time.Hour)},
					},
				},
				models: agyModels,
			},
		},
	}

	st := store.New(filepath.Join(tmpDir, "data"), keychain.NewMemoryStore())
	reg := provider.NewRegistry(claudeProv, codexProv, agyProv)
	mgr := engine.NewManager(st, reg)

	// Pre-register slots cleanly through the real Manager.
	ctx := context.Background()
	seed := func(dp *demoProvider, order []string) {
		for _, email := range order {
			acct := dp.accounts[email]
			dp.activeID = email
			res, _ := mgr.Add(ctx, dp.name, acct.alias)
			if acct.off && res != nil {
				_, _ = mgr.SetDisabled(ctx, dp.name, fmt.Sprintf("%d", res.Slot), true)
			}
		}
		dp.activeID = order[0]
		_, _ = mgr.Switch(ctx, dp.name, "1")
	}
	seed(claudeProv, []string{"work@acme.dev", "me@acme.dev", "lab@acme.dev"})
	seed(codexProv, []string{"dev@studio.io", "alt@studio.io"})
	seed(agyProv, []string{"me@gmail.com"})

	collector := seedDemoAnalytics(filepath.Join(tmpDir, "logs"))
	if err := tui.Run(mgr, collector, tui.Options{Version: "v1.0.9"}); err != nil {
		fmt.Fprintf(os.Stderr, "demo tui: %v\n", err)
		os.Exit(1)
	}
}

func seedDemoAnalytics(root string) *analytics.Collector {
	now := time.Now().UTC()
	var claudeLines, codexLines, agyLines string
	codexDaily := []struct {
		daysAgo int
		in      int64
		out     int64
		cache   int64
		reqs    int
	}{
		{6, 2_400_000, 120_000, 1_800_000, 42},
		{5, 22_000_000, 680_000, 19_000_000, 165},
		{4, 74_000_000, 1_400_000, 68_000_000, 340},
		{3, 52_000_000, 920_000, 47_000_000, 280},
		{2, 9_500_000, 210_000, 8_100_000, 84},
		{1, 104_000_000, 2_100_000, 96_000_000, 460},
		{0, 18_500_000, 410_000, 15_200_000, 112},
	}
	for i, d := range codexDaily {
		ts := now.AddDate(0, 0, -d.daysAgo).Format(time.RFC3339)
		codexLines += fmt.Sprintf(`{"timestamp":%q,"payload":{"info":{"last_token_usage":{"input_tokens":%d,"cached_input_tokens":%d,"output_tokens":%d}}}}`+"\n", ts, d.in, d.cache, d.out)
		claudeLines += fmt.Sprintf(`{"timestamp":%q,"requestId":"req-%d","message":{"usage":{"input_tokens":%d,"output_tokens":%d,"cache_read_input_tokens":%d}}}`+"\n", ts, i, d.in/4, d.out/2, d.cache)
		agyLines += fmt.Sprintf(`{"created_at":%q,"input_tokens":%d,"output_tokens":%d,"cache_read_tokens":%d}`+"\n", ts, d.in/8, d.out/3, d.cache/2)
	}
	_ = fsutil.AtomicWriteFile(filepath.Join(root, "claude", "proj", "session.jsonl"), []byte(claudeLines), 0o600)
	_ = fsutil.AtomicWriteFile(filepath.Join(root, "codex", "2026", "11", "22", "rollout.jsonl"), []byte(codexLines), 0o600)
	_ = fsutil.AtomicWriteFile(filepath.Join(root, "agy", "conv-1", ".system_generated", "logs", "transcript.jsonl"), []byte(agyLines), 0o600)

	return &analytics.Collector{
		ClaudeProjectsDir: filepath.Join(root, "claude"),
		CodexSessionsDir:  filepath.Join(root, "codex"),
		AgyBrainDir:       filepath.Join(root, "agy"),
		Now:               func() time.Time { return now },
	}
}
