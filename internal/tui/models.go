package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/haimingx/aiswap/internal/contract"
)

func (m *Model) renderModels() string {
	if m.modelsLoading {
		return styleSubtitle.Render("Fetching live available models for active accounts…")
	}
	if m.models == nil || len(m.models.Providers) == 0 {
		return styleSubtitle.Render("Press 'r' to load available models.")
	}

	// Enable 3-column panoramic layout on standard 96+ col terminals so all 3 providers fit on one screen.
	wideThreeCol := len(m.models.Providers) == 3 && m.width >= 96
	cardWidth := m.contentWidth()
	if wideThreeCol {
		cardWidth = (m.width - 8) / 3
		if cardWidth > 42 {
			cardWidth = 42
		}
	}

	var cards []string
	for _, pm := range m.models.Providers {
		var lines []string
		head := lipgloss.NewStyle().Bold(true).Render(providerLabel(string(pm.Provider)))
		head += "  " + styleSubtitle.Render(fmt.Sprintf("(%d)", len(pm.Models)))
		lines = append(lines, head)
		if pm.Email != "" {
			lines = append(lines, styleSubtitle.Render(truncateText(pm.Email, cardWidth-4)))
		}
		lines = append(lines, "")

		if pm.Error != "" {
			lines = append(lines, styleStatusErr.Render("error: "+truncateText(pm.Error, cardWidth-10)))
		} else if len(pm.Models) == 0 {
			lines = append(lines, styleSubtitle.Render("no models available"))
		} else if groups, hasPools := contract.GroupModelPools(pm.Models); hasPools {
			for idx, g := range groups {
				if idx > 0 {
					lines = append(lines, "")
				}
				header := styleBadgePlan.Render(g.Name)
				if g.Usage != "" {
					header += styleSubtitle.Render(" · " + g.Usage)
				}
				lines = append(lines, header)
				for _, mi := range g.Models {
					lines = append(lines, "  "+truncateText(mi.DisplayName, cardWidth-5))
				}
			}
		} else {
			poolHeader := styleBadgePlan.Render("All Models Pool")
			if summary := contract.CompactQuotaSummary(pm.Quota); summary != "" {
				poolHeader += styleSubtitle.Render(" · " + summary)
			}
			lines = append(lines, poolHeader)
			for _, mi := range pm.Models {
				lines = append(lines, "  "+truncateText(mi.DisplayName, cardWidth-5))
			}
		}
		cards = append(cards, styleCard.Width(cardWidth).Render(strings.Join(lines, "\n")))
	}

	if wideThreeCol {
		return lipgloss.JoinHorizontal(lipgloss.Top, cards[0], " ", cards[1], " ", cards[2])
	}
	return strings.Join(cards, "\n")
}
