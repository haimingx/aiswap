package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/haimingx/aiswap/internal/contract"
)

func (m *Model) renderStats() string {
	if m.stats == nil {
		return styleSubtitle.Render("Loading local usage logs…")
	}
	width := m.contentWidth()
	var blocks []string

	// Summary table card
	metaParts := []string{fmt.Sprintf("last %d days", m.statsDays)}
	if m.stats.Timezone != "" {
		metaParts = append(metaParts, m.stats.Timezone)
	}
	if m.stats.Pricing.Source != "" {
		metaParts = append(metaParts, "pricing: "+m.stats.Pricing.Source)
	}

	var rows []string
	rows = append(rows, lipgloss.NewStyle().Bold(true).Render(
		fmt.Sprintf("Local Offline Token Usage (%s)  %s",
			strings.Join(metaParts, " · "),
			styleSubtitle.Render("[d: 7d/30d · b: models · p: pricing]"),
		),
	))
	rows = append(rows, "")
	rows = append(rows, styleSubtitle.Render(fmt.Sprintf("%-14s %9s %11s %11s %11s %10s", "PROVIDER", "REQUESTS", "INPUT", "OUTPUT", "CACHE READ", "EST. COST")))

	for _, ps := range m.stats.Providers {
		t := ps.Totals
		rows = append(rows, fmt.Sprintf("%-14s %9d %11s %11s %11s %10s",
			providerLabel(string(ps.Provider)),
			t.Requests,
			contract.FormatTokens(t.InputTokens),
			contract.FormatTokens(t.OutputTokens),
			contract.FormatTokens(t.CacheReadTokens),
			contract.FormatUSD(t.EstimatedCostUSD),
		))
	}
	if m.stats.Totals.Requests > 0 {
		t := m.stats.Totals
		rows = append(rows, styleSubtitle.Render(fmt.Sprintf("%-14s %9d %11s %11s %11s %10s",
			"Total",
			t.Requests,
			contract.FormatTokens(t.InputTokens),
			contract.FormatTokens(t.OutputTokens),
			contract.FormatTokens(t.CacheReadTokens),
			contract.FormatUSD(t.EstimatedCostUSD),
		)))
	}
	blocks = append(blocks, styleCard.Width(width).Render(strings.Join(rows, "\n")))

	// Per-model breakdown card (toggled via 'b')
	if m.statsBreakdown {
		var modelLines []string
		for _, ps := range m.stats.Providers {
			if len(ps.Models) == 0 {
				continue
			}
			if len(modelLines) > 0 {
				modelLines = append(modelLines, "")
			}
			modelLines = append(modelLines, lipgloss.NewStyle().Bold(true).Render(providerLabel(string(ps.Provider))+" — By Model"))
			modelLines = append(modelLines, styleSubtitle.Render(fmt.Sprintf("  %-26s %7s %10s %10s %10s %10s", "MODEL", "REQS", "INPUT", "OUTPUT", "CACHE", "EST. COST")))
			limit := len(ps.Models)
			if limit > 8 {
				limit = 8
			}
			for _, mu := range ps.Models[:limit] {
				name := mu.Model
				if len(name) > 26 {
					name = name[:25] + "…"
				}
				modelLines = append(modelLines, fmt.Sprintf("  %-26s %7d %10s %10s %10s %10s",
					name,
					mu.Requests,
					contract.FormatTokens(mu.InputTokens),
					contract.FormatTokens(mu.OutputTokens),
					contract.FormatTokens(mu.CacheReadTokens),
					contract.FormatUSD(mu.EstimatedCostUSD),
				))
			}
		}
		if len(modelLines) > 0 {
			blocks = append(blocks, styleCard.Width(width).Render(strings.Join(modelLines, "\n")))
		}
	}

	// Daily bar chart per provider (non-empty only)
	chartBarWidth := width - 40
	if chartBarWidth > 36 {
		chartBarWidth = 36
	}
	if chartBarWidth < 10 {
		chartBarWidth = 10
	}

	for _, ps := range m.stats.Providers {
		if len(ps.Daily) == 0 {
			continue
		}
		var lines []string
		lines = append(lines, lipgloss.NewStyle().Bold(true).Render(providerLabel(string(ps.Provider))+" — Daily Input+Output Tokens"))
		// Show last 10 active days at most to keep screen clean.
		start := 0
		if len(ps.Daily) > 10 {
			start = len(ps.Daily) - 10
		}
		var maxDay int64 = 1
		for _, du := range ps.Daily[start:] {
			if t := du.InputTokens + du.OutputTokens; t > maxDay {
				maxDay = t
			}
		}
		for _, du := range ps.Daily[start:] {
			total := du.InputTokens + du.OutputTokens
			pct := (float64(total) / float64(maxDay)) * 100.0
			costSuffix := ""
			if du.EstimatedCostUSD != nil {
				costSuffix = " · " + contract.FormatUSD(du.EstimatedCostUSD)
			}
			lines = append(lines, fmt.Sprintf("  %s  %s  %8s (%d reqs%s)",
				du.Date,
				renderStatBar(total, pct, chartBarWidth),
				contract.FormatTokens(total),
				du.Requests,
				costSuffix,
			))
		}
		blocks = append(blocks, styleCard.Width(width).Render(strings.Join(lines, "\n")))
	}

	return strings.Join(blocks, "\n")
}
