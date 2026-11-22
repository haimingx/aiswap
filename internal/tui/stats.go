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
	var rows []string
	rows = append(rows, lipgloss.NewStyle().Bold(true).Render(
		fmt.Sprintf("Local Offline Token Usage (last %d days)  %s",
			m.statsDays,
			styleSubtitle.Render("[press 'd' to toggle 7d / 30d]"),
		),
	))
	rows = append(rows, "")
	rows = append(rows, styleSubtitle.Render(fmt.Sprintf("%-14s %10s %12s %12s %12s", "PROVIDER", "REQUESTS", "INPUT", "OUTPUT", "CACHE READ")))

	for _, ps := range m.stats.Providers {
		rows = append(rows, fmt.Sprintf("%-14s %10d %12s %12s %12s",
			providerLabel(string(ps.Provider)),
			ps.TotalRequests,
			contract.FormatTokens(ps.TotalInputTokens),
			contract.FormatTokens(ps.TotalOutputTokens),
			contract.FormatTokens(ps.TotalCacheReadTokens),
		))
	}
	blocks = append(blocks, styleCard.Width(width).Render(strings.Join(rows, "\n")))

	// Daily bar chart per provider (non-empty only)
	chartBarWidth := width - 38
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
			lines = append(lines, fmt.Sprintf("  %s  %s  %8s (%d reqs)",
				du.Date,
				renderStatBar(total, pct, chartBarWidth),
				contract.FormatTokens(total),
				du.Requests,
			))
		}
		blocks = append(blocks, styleCard.Width(width).Render(strings.Join(lines, "\n")))
	}

	return strings.Join(blocks, "\n")
}
