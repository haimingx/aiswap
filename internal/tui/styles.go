package tui

import (
	"math"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

var (
	// Emerald accent and lime success follow the logo palette; the light variants are
	// darkened so text keeps at least 4.5:1 contrast on white terminals.
	colorAccent   = lipgloss.AdaptiveColor{Light: "#087F4E", Dark: "#55E0A3"}
	colorSuccess  = lipgloss.AdaptiveColor{Light: "#4D7C0F", Dark: "#CEE432"}
	colorWarn     = lipgloss.AdaptiveColor{Light: "#D97706", Dark: "#FBBF24"}
	colorDanger   = lipgloss.AdaptiveColor{Light: "#DC2626", Dark: "#F87171"}
	colorMuted    = lipgloss.AdaptiveColor{Light: "#6B7280", Dark: "#9CA3AF"}
	colorBorder   = lipgloss.AdaptiveColor{Light: "#D1D5DB", Dark: "#374151"}
	colorSelect   = lipgloss.AdaptiveColor{Light: "#ECFDF5", Dark: "#14261D"}
	colorOnAccent = lipgloss.AdaptiveColor{Light: "#FFFFFF", Dark: "#0E1412"}

	styleTitle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorAccent)

	styleSubtitle = lipgloss.NewStyle().
			Foreground(colorMuted)

	styleTabActive = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorOnAccent).
			Background(colorAccent).
			Padding(0, 1)

	styleTabInactive = lipgloss.NewStyle().
				Foreground(colorMuted).
				Padding(0, 1)

	styleCard = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorBorder).
			Padding(0, 1)

	styleCardFocused = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(colorAccent).
				Padding(0, 1)

	styleBadgePlan = lipgloss.NewStyle().
			Foreground(colorAccent).
			Bold(true)

	styleBadgeWatch = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorOnAccent).
			Background(colorSuccess).
			Padding(0, 1)

	styleBadgeDisabled = lipgloss.NewStyle().
				Foreground(colorWarn)

	styleActiveMark = lipgloss.NewStyle().
			Foreground(colorSuccess).
			Bold(true)

	styleSelectedRow = lipgloss.NewStyle().
				Background(colorSelect).
				Bold(true)

	styleHelp = lipgloss.NewStyle().
			Foreground(colorMuted)

	styleStatusOK = lipgloss.NewStyle().
			Foreground(colorSuccess)

	styleStatusErr = lipgloss.NewStyle().
			Foreground(colorDanger)

	styleConfirm = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorWarn)

	styleInputPrompt = lipgloss.NewStyle().
				Bold(true).
				Foreground(colorAccent)
)

func renderBar(pct float64, width int) string {
	clamped := math.Max(0, math.Min(100, pct))
	barColor := colorSuccess
	if clamped >= 90 {
		barColor = colorDanger
	} else if clamped >= 75 {
		barColor = colorWarn
	}
	return renderBarColored(clamped, width, barColor)
}

func renderStatBar(totalTokens int64, pct float64, width int) string {
	clamped := math.Max(0, math.Min(100, pct))
	barColor := colorSuccess
	switch {
	case totalTokens >= 100_000_000 || (totalTokens >= 80_000_000 && clamped >= 80):
		barColor = colorDanger
	case totalTokens >= 50_000_000 || (totalTokens >= 20_000_000 && clamped >= 50):
		barColor = colorWarn
	}
	return renderBarColored(clamped, width, barColor)
}

func renderBarColored(pct float64, width int, barColor lipgloss.TerminalColor) string {
	if width < 6 {
		width = 6
	}
	clamped := math.Max(0, math.Min(100, pct))
	filled := int(math.Round((clamped / 100.0) * float64(width)))
	if filled > width {
		filled = width
	}
	fillStr := lipgloss.NewStyle().Foreground(barColor).Render(strings.Repeat("■", filled))
	emptyStr := lipgloss.NewStyle().Foreground(colorBorder).Render(strings.Repeat("■", width-filled))
	return fillStr + emptyStr
}

func providerLabel(name string) string {
	switch name {
	case "claude":
		return "Claude Code"
	case "codex":
		return "Codex CLI"
	case "agy":
		return "Antigravity"
	default:
		return strings.ToUpper(name)
	}
}

func truncateText(s string, max int) string {
	runes := []rune(s)
	if max <= 3 || len(runes) <= max {
		return s
	}
	return string(runes[:max-1]) + "…"
}
