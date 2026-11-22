package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/haimingx/aiswap/internal/contract"
	"github.com/haimingx/aiswap/internal/domain"
)

// rowTarget maps a flat cursor index to either a saved slot or an unsaved live provider login.
type rowTarget struct {
	Provider domain.ProviderName
	Slot     int // 0 means unsaved live login
	Alias    string
	Email    string
	Active   bool
	Disabled bool
}

func (m *Model) visibleProviders() []contract.ProviderStatus {
	if m.status == nil {
		return nil
	}
	switch m.tab {
	case tabClaude:
		return filterProviders(m.status.Providers, domain.ProviderClaude)
	case tabCodex:
		return filterProviders(m.status.Providers, domain.ProviderCodex)
	case tabAgy:
		return filterProviders(m.status.Providers, domain.ProviderAgy)
	default:
		return m.status.Providers
	}
}

func filterProviders(all []contract.ProviderStatus, target domain.ProviderName) []contract.ProviderStatus {
	for _, ps := range all {
		if ps.Provider == target {
			return []contract.ProviderStatus{ps}
		}
	}
	return nil
}

func (m *Model) buildTargets() []rowTarget {
	var out []rowTarget
	for _, ps := range m.visibleProviders() {
		if len(ps.Accounts) == 0 {
			out = append(out, rowTarget{
				Provider: ps.Provider,
				Slot:     0,
				Email:    ps.LiveEmail,
				Active:   ps.LiveEmail != "",
			})
			continue
		}
		for _, acct := range ps.Accounts {
			out = append(out, rowTarget{
				Provider: ps.Provider,
				Slot:     acct.Slot,
				Alias:    acct.Alias,
				Email:    acct.Email,
				Active:   acct.Active,
				Disabled: acct.Disabled,
			})
		}
	}
	return out
}

func (m *Model) renderOverview() (string, int) {
	providers := m.visibleProviders()
	if len(providers) == 0 {
		if m.loading {
			return styleSubtitle.Render("Loading accounts and quotas…"), -1
		}
		return styleSubtitle.Render("No providers detected."), -1
	}

	// Wide-terminal panoramic 3-column layout when viewing all 3 providers.
	wideThreeCol := m.tab == tabAll && len(providers) == 3 && m.width >= 118
	var cardWidth, barWidth int
	if wideThreeCol {
		cardWidth = (m.width - 10) / 3
		if cardWidth > 44 {
			cardWidth = 44
		}
		barWidth = cardWidth - 26
		if barWidth < 8 {
			barWidth = 8
		}
	} else {
		cardWidth = m.contentWidth()
		barWidth = 22
		if cardWidth < 60 {
			barWidth = 10
		}
	}

	var cards []string
	rowIdx := 0
	lineCursor := 0
	selectedLine := -1

	for _, ps := range providers {
		var lines []string

		// Card header: provider title + plan badge
		head := lipgloss.NewStyle().Bold(true).Render(providerLabel(string(ps.Provider)))
		if ps.LivePlan != "" {
			head += "  " + styleBadgePlan.Render(ps.LivePlan)
		}
		lines = append(lines, head)

		// Active identity subtitle (clean, single-line, truncated for narrow cards)
		if ps.LiveEmail != "" {
			tag := "unsaved"
			if ps.Managed {
				tag = fmt.Sprintf("#%d", ps.ActiveSlot)
			}
			emailLine := truncateText(ps.LiveEmail, cardWidth-14)
			lines = append(lines, styleSubtitle.Render(fmt.Sprintf("%s (%s)", emailLine, tag)))
		} else if !ps.Installed {
			lines = append(lines, styleSubtitle.Render("not installed"))
		} else {
			lines = append(lines, styleSubtitle.Render("not logged in"))
		}

		// Primary quota bars for active account
		if ps.LiveQuota != nil && len(ps.LiveQuota.Windows) > 0 {
			for _, w := range ps.LiveQuota.Windows {
				lines = append(lines, renderQuotaLine(w, barWidth, ps.LiveQuota.Stale, wideThreeCol))
			}
		} else if ps.LiveQuotaError != "" {
			lines = append(lines, styleSubtitle.Render("quota: "+ps.LiveQuotaError))
		}

		// Compact slot list (zero redundant repetition of full window strings)
		lines = append(lines, "")
		if len(ps.Accounts) == 0 {
			cursor := "  "
			if rowIdx == m.cursor {
				cursor = "▸ "
				if !wideThreeCol {
					selectedLine = lineCursor + 1 + len(lines)
				}
			}
			hint := "press 'a' to save current"
			if ps.LiveEmail == "" {
				hint = "log in via CLI first"
			}
			rowLine := cursor + styleSubtitle.Render(hint)
			if rowIdx == m.cursor {
				rowLine = styleSelectedRow.Render(rowLine)
			}
			lines = append(lines, rowLine)
			rowIdx++
		} else {
			for _, acct := range ps.Accounts {
				selected := rowIdx == m.cursor
				if selected && !wideThreeCol {
					selectedLine = lineCursor + 1 + len(lines)
				}
				lines = append(lines, renderAccountRow(acct, selected, cardWidth-4))
				rowIdx++
			}
		}

		box := styleCard
		if m.providerHasCursor(ps, rowIdx) {
			box = styleCardFocused
		}
		rendered := box.Width(cardWidth).Render(strings.Join(lines, "\n"))
		cards = append(cards, rendered)
		if !wideThreeCol {
			lineCursor += strings.Count(rendered, "\n") + 1
		}
	}

	if wideThreeCol {
		return lipgloss.JoinHorizontal(lipgloss.Top, cards[0], " ", cards[1], " ", cards[2]), -1
	}
	return strings.Join(cards, "\n"), selectedLine
}

func (m *Model) providerHasCursor(ps contract.ProviderStatus, endIdx int) bool {
	count := len(ps.Accounts)
	if count == 0 {
		count = 1
	}
	startIdx := endIdx - count
	return m.cursor >= startIdx && m.cursor < endIdx
}

func renderQuotaLine(w domain.QuotaWindow, barWidth int, stale, compact bool) string {
	labelW := 12
	if compact {
		labelW = 10
	}
	name := truncateText(w.Name, labelW)
	label := fmt.Sprintf("%-*s", labelW, name)
	bar := renderBar(w.UsedPct, barWidth)
	pct := fmt.Sprintf(" %3.0f%%", w.UsedPct)
	reset := ""
	if w.UsedPct > 0 && w.ResetsAt != nil {
		if rem := time.Until(*w.ResetsAt); rem > 0 {
			if compact {
				reset = styleSubtitle.Render(" " + contract.FormatCountdown(rem))
			} else {
				reset = styleSubtitle.Render("  resets " + contract.FormatCountdown(rem))
			}
		}
	}
	staleTag := ""
	if stale {
		staleTag = styleSubtitle.Render(" *")
	}
	return label + " " + bar + pct + reset + staleTag
}

func renderAccountRow(acct contract.AccountRow, selected bool, maxWidth int) string {
	cursor := "  "
	if selected {
		cursor = "▸ "
	}
	mark := "○"
	if acct.Active {
		mark = styleActiveMark.Render("●")
	}

	name := acct.Email
	if acct.Alias != "" {
		name = acct.Alias
	}
	suffix := ""
	if acct.Disabled {
		suffix = " " + styleBadgeDisabled.Render("[off]")
	} else if pct, ok := bindingUsedPct(acct.Quota); ok {
		suffix = " " + styleSubtitle.Render(fmt.Sprintf("%3.0f%%", pct))
	}

	nameMax := maxWidth - 14
	if nameMax < 10 {
		nameMax = 10
	}
	line := fmt.Sprintf("%s%s #%d %s%s", cursor, mark, acct.Slot, truncateText(name, nameMax), suffix)
	if selected {
		return styleSelectedRow.Render(line)
	}
	if acct.Disabled {
		return styleSubtitle.Render(line)
	}
	return line
}

func bindingUsedPct(q *contract.QuotaPayload) (float64, bool) {
	if q == nil || len(q.Windows) == 0 {
		return 0, false
	}
	maxPct := q.Windows[0].UsedPct
	for _, w := range q.Windows[1:] {
		if w.UsedPct > maxPct {
			maxPct = w.UsedPct
		}
	}
	return maxPct, true
}
