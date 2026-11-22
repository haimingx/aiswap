package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/haimingx/aiswap/internal/contract"
	"github.com/haimingx/aiswap/internal/domain"
)

func TestModelNavigationWideLayoutAndInlineAlias(t *testing.T) {
	m := NewModel(nil, nil, Options{Version: "v0.2.0"})
	m.loading = false
	m.width = 130
	m.height = 36
	m.status = &contract.StatusResponse{
		SchemaVersion: contract.SchemaVersion,
		GeneratedAt:   time.Now().UTC(),
		Providers: []contract.ProviderStatus{
			{
				Provider:   domain.ProviderClaude,
				Installed:  true,
				ActiveSlot: 1,
				LiveEmail:  "work@example.com",
				LivePlan:   "Pro",
				Managed:    true,
				LiveQuota: &contract.QuotaPayload{
					Windows: []domain.QuotaWindow{{Name: "5h", UsedPct: 42}},
				},
				Accounts: []contract.AccountRow{
					{Slot: 1, Alias: "work", Email: "work@example.com", Plan: "Pro", Active: true},
					{Slot: 2, Alias: "me", Email: "me@example.com", Plan: "Max 5x", Disabled: true},
				},
			},
			{
				Provider:  domain.ProviderCodex,
				Installed: true,
				LiveEmail: "dev@openai.test",
				LivePlan:  "Plus",
			},
			{
				Provider:  domain.ProviderAgy,
				Installed: true,
				LiveEmail: "u@gmail.com",
				LivePlan:  "Ultra",
			},
		},
	}
	m.stats = &contract.StatsResponse{
		SchemaVersion: contract.SchemaVersion,
		Days:          7,
		Timezone:      "UTC",
		Pricing:       contract.PricingInfo{Source: "builtin", ModelCount: 10},
		Totals:        contract.TokenUsage{Requests: 100, InputTokens: 2_500_000, OutputTokens: 500_000},
		Providers: []contract.ProviderStats{
			{Provider: domain.ProviderClaude, Totals: contract.TokenUsage{Requests: 100, InputTokens: 2_500_000, OutputTokens: 500_000}},
		},
	}

	view := m.View()
	if !strings.Contains(view, "Claude Code") || !strings.Contains(view, "Codex CLI") || !strings.Contains(view, "Antigravity") {
		t.Fatalf("expected 3-column panoramic view, got:\n%s", view)
	}
	if !strings.Contains(view, "[off]") {
		t.Fatalf("expected disabled slot badge [off], got:\n%s", view)
	}

	// Open inline alias editor on slot #1 and cancel with Esc
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
	m = next.(Model)
	if m.editingAlias == nil || !strings.Contains(m.View(), "Alias for claude #1") {
		t.Fatalf("expected inline alias prompt, got:\n%s", m.View())
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)
	if m.editingAlias != nil {
		t.Fatal("expected Esc to exit inline alias editor")
	}

	// Toggle watch mode
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'w'}})
	m = next.(Model)
	if !m.watchMode || !strings.Contains(m.View(), "WATCH") {
		t.Fatal("expected watch mode to be active")
	}

	// Switch to Stats tab and toggle 7d -> 30d
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})
	m = next.(Model)
	if m.tab != tabStats || !strings.Contains(m.View(), "Local Offline Token Usage") {
		t.Fatalf("expected stats tab view, got:\n%s", m.View())
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	m = next.(Model)
	if m.statsDays != 30 {
		t.Fatalf("statsDays = %d, want 30", m.statsDays)
	}

	// Switch to Models tab and verify account actions are blocked
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'6'}})
	m = next.(Model)
	if m.tab != tabModels {
		t.Fatalf("expected tabModels, got %v", m.tab)
	}
	for _, r := range []rune{'e', 'x', 'd', 'b', 'a'} {
		next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = next.(Model)
		if cmd != nil || m.editingAlias != nil || m.confirmRemove != nil {
			t.Fatalf("key %q should be ignored on Models tab", string(r))
		}
	}

	// Verify success notice auto-dismisses on tab switch and via TTL ticks
	next, _ = m.Update(actionDoneMsg{notice: "Saved agy slot #1"})
	m = next.(Model)
	if m.notice == "" || m.noticeTTL != 4 {
		t.Fatalf("expected active notice with TTL 4, got notice=%q ttl=%d", m.notice, m.noticeTTL)
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})
	m = next.(Model)
	if m.notice != "" {
		t.Fatalf("expected non-error notice to clear on tab switch, got %q", m.notice)
	}
}
