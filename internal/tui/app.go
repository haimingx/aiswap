package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/haimingx/aiswap/internal/analytics"
	"github.com/haimingx/aiswap/internal/contract"
	"github.com/haimingx/aiswap/internal/domain"
	"github.com/haimingx/aiswap/internal/engine"
)

type tabID int

const (
	tabAll tabID = iota
	tabClaude
	tabCodex
	tabAgy
	tabStats
	tabModels
)

const pollIntervalSecs = 30

type Options struct {
	WatchMode bool
	Version   string
}

type statusLoadedMsg struct {
	resp *contract.StatusResponse
	err  error
}

type statsLoadedMsg struct {
	resp *contract.StatsResponse
}

type pricingRefreshedMsg struct {
	count int
	err   error
}

type modelsLoadedMsg struct {
	resp *contract.ModelsResponse
	err  error
}

type actionDoneMsg struct {
	notice string
	err    error
}

type secondTickMsg struct {
	id int
}

type Model struct {
	mgr       *engine.Manager
	collector *analytics.Collector
	version   string

	tab            tabID
	watchMode      bool
	statsDays      int
	statsBreakdown bool
	cursor         int
	scrollOffset  int
	width         int
	height        int
	loading       bool
	nextPollIn    int
	tickID        int
	tickActive    bool
	status        *contract.StatusResponse
	stats         *contract.StatsResponse
	models        *contract.ModelsResponse
	modelsLoading bool
	lastUpdated   time.Time
	notice        string
	noticeIsErr   bool
	noticeTTL     int
	confirmRemove *rowTarget
	editingAlias  *rowTarget
	aliasInput    string
}

func NewModel(mgr *engine.Manager, collector *analytics.Collector, opts Options) Model {
	if collector == nil {
		collector = analytics.NewCollector()
	}
	return Model{
		mgr:        mgr,
		collector:  collector,
		version:    opts.Version,
		tab:        tabAll,
		watchMode:      opts.WatchMode,
		statsDays:      7,
		statsBreakdown: true,
		width:          100,
		height:     32,
		loading:    true,
		nextPollIn: pollIntervalSecs,
		tickActive: opts.WatchMode,
	}
}

func Run(mgr *engine.Manager, collector *analytics.Collector, opts Options) error {
	m := NewModel(mgr, collector, opts)
	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err := p.Run()
	return err
}

func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{
		m.fetchStatusCmd(false),
		m.fetchStatsCmd(m.statsDays),
	}
	if m.watchMode {
		cmds = append(cmds, secondTickCmd(m.tickID))
	}
	return tea.Batch(cmds...)
}

func secondTickCmd(id int) tea.Cmd {
	return tea.Tick(time.Second, func(time.Time) tea.Msg {
		return secondTickMsg{id: id}
	})
}

func (m *Model) setNotice(text string, isErr bool) tea.Cmd {
	m.notice = text
	m.noticeIsErr = isErr
	if isErr {
		m.noticeTTL = 8
	} else {
		m.noticeTTL = 4
	}
	return m.ensureTicker()
}

func (m *Model) clearNotice() {
	m.notice = ""
	m.noticeIsErr = false
	m.noticeTTL = 0
}

func (m *Model) ensureTicker() tea.Cmd {
	if m.tickActive {
		return nil
	}
	m.tickID++
	m.tickActive = true
	return secondTickCmd(m.tickID)
}

func (m Model) fetchStatusCmd(force bool) tea.Cmd {
	mgr := m.mgr
	return func() tea.Msg {
		if mgr == nil {
			return statusLoadedMsg{}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		resp, err := mgr.StatusWithOptions(ctx, engine.StatusOptions{
			FetchQuota: true,
			Force:      force,
		})
		return statusLoadedMsg{resp: resp, err: err}
	}
}

func (m Model) fetchStatsCmd(days int) tea.Cmd {
	col := m.collector
	return func() tea.Msg {
		if col == nil {
			return statsLoadedMsg{}
		}
		return statsLoadedMsg{resp: col.Collect(days)}
	}
}

func (m Model) refreshPricingCmd() tea.Cmd {
	col := m.collector
	return func() tea.Msg {
		if col == nil {
			return pricingRefreshedMsg{err: fmt.Errorf("analytics collector unavailable")}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		book, err := col.RefreshPricing(ctx)
		if err != nil {
			return pricingRefreshedMsg{err: err}
		}
		return pricingRefreshedMsg{count: book.Count()}
	}
}

func (m Model) fetchModelsCmd() tea.Cmd {
	mgr := m.mgr
	return func() tea.Msg {
		if mgr == nil {
			return modelsLoadedMsg{}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		resp, err := mgr.ListModels(ctx)
		return modelsLoadedMsg{resp: resp, err: err}
	}
}

func (m Model) switchCmd(p domain.ProviderName, target string) tea.Cmd {
	mgr := m.mgr
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		res, err := mgr.Switch(ctx, p, target)
		if err != nil {
			return actionDoneMsg{err: err}
		}
		if !res.Switched {
			return actionDoneMsg{notice: fmt.Sprintf("%s slot #%d is already active", p, res.ToSlot)}
		}
		return actionDoneMsg{notice: fmt.Sprintf("Switched %s -> #%d (%s)", p, res.ToSlot, res.ToEmail)}
	}
}

func (m Model) addCurrentCmd(p domain.ProviderName) tea.Cmd {
	mgr := m.mgr
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		res, err := mgr.Add(ctx, p, "")
		if err != nil {
			return actionDoneMsg{err: err}
		}
		verb := "Saved"
		if res.Updated {
			verb = "Updated"
		}
		return actionDoneMsg{notice: fmt.Sprintf("%s %s slot #%d (%s)", verb, p, res.Slot, res.Identity.DisplayLabel())}
	}
}

func (m Model) removeCmd(p domain.ProviderName, slot int) tea.Cmd {
	mgr := m.mgr
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		rem, err := mgr.Remove(ctx, p, fmt.Sprintf("%d", slot))
		if err != nil {
			return actionDoneMsg{err: err}
		}
		return actionDoneMsg{notice: fmt.Sprintf("Removed %s slot #%d (%s)", p, rem.Slot, rem.Identity.DisplayLabel())}
	}
}

func (m Model) setAliasCmd(p domain.ProviderName, slot int, alias string) tea.Cmd {
	mgr := m.mgr
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		updated, err := mgr.SetAlias(ctx, p, fmt.Sprintf("%d", slot), alias)
		if err != nil {
			return actionDoneMsg{err: err}
		}
		if updated.Alias == "" {
			return actionDoneMsg{notice: fmt.Sprintf("Cleared alias for %s #%d", p, slot)}
		}
		return actionDoneMsg{notice: fmt.Sprintf("Alias for %s #%d -> %s", p, slot, updated.Alias)}
	}
}

func (m Model) toggleDisabledCmd(p domain.ProviderName, slot int, nextDisabled bool) tea.Cmd {
	mgr := m.mgr
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err := mgr.SetDisabled(ctx, p, fmt.Sprintf("%d", slot), nextDisabled)
		if err != nil {
			return actionDoneMsg{err: err}
		}
		if nextDisabled {
			return actionDoneMsg{notice: fmt.Sprintf("Disabled %s #%d from auto-rotation", p, slot)}
		}
		return actionDoneMsg{notice: fmt.Sprintf("Enabled %s #%d for rotation", p, slot)}
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case secondTickMsg:
		if msg.id != m.tickID {
			return m, nil
		}
		if m.noticeTTL > 0 {
			m.noticeTTL--
			if m.noticeTTL == 0 {
				m.clearNotice()
			}
		}
		var cmds []tea.Cmd
		if m.watchMode {
			m.nextPollIn--
			if m.nextPollIn <= 0 {
				m.nextPollIn = pollIntervalSecs
				cmds = append(cmds, m.fetchStatusCmd(false))
			}
		}
		if m.watchMode || m.noticeTTL > 0 {
			cmds = append(cmds, secondTickCmd(m.tickID))
		} else {
			m.tickActive = false
		}
		return m, tea.Batch(cmds...)

	case statusLoadedMsg:
		m.loading = false
		if msg.err != nil {
			return m, m.setNotice(msg.err.Error(), true)
		}
		if msg.resp != nil {
			m.status = msg.resp
			m.lastUpdated = time.Now()
			m.clampCursor()
			if !m.noticeIsErr && strings.HasPrefix(m.notice, "Refreshing") {
				m.clearNotice()
			}
		}
		return m, nil

	case statsLoadedMsg:
		if msg.resp != nil {
			m.stats = msg.resp
		}
		return m, nil

	case pricingRefreshedMsg:
		if msg.err != nil {
			return m, m.setNotice(msg.err.Error(), true)
		}
		return m, tea.Batch(
			m.setNotice(fmt.Sprintf("Updated pricing table (%d models)", msg.count), false),
			m.fetchStatsCmd(m.statsDays),
		)

	case modelsLoadedMsg:
		m.modelsLoading = false
		if msg.err != nil {
			return m, m.setNotice(msg.err.Error(), true)
		}
		if msg.resp != nil {
			m.models = msg.resp
			if !m.noticeIsErr && strings.HasPrefix(m.notice, "Refreshing") {
				m.clearNotice()
			}
		}
		return m, nil

	case actionDoneMsg:
		if msg.err != nil {
			return m, m.setNotice(msg.err.Error(), true)
		}
		m.models = nil // Invalidate cached active-account models after account mutations.
		m.loading = true
		return m, tea.Batch(m.setNotice(msg.notice, false), m.fetchStatusCmd(false))

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	// Inline alias editor mode
	if m.editingAlias != nil {
		switch msg.Type {
		case tea.KeyCtrlC:
			return m, tea.Quit
		case tea.KeyEsc:
			m.editingAlias = nil
			m.aliasInput = ""
			return m, m.setNotice("Cancelled alias edit", false)
		case tea.KeyEnter:
			target := *m.editingAlias
			val := strings.TrimSpace(m.aliasInput)
			m.editingAlias = nil
			m.aliasInput = ""
			return m, m.setAliasCmd(target.Provider, target.Slot, val)
		case tea.KeyBackspace, tea.KeyDelete:
			if runes := []rune(m.aliasInput); len(runes) > 0 {
				m.aliasInput = string(runes[:len(runes)-1])
			}
			return m, nil
		case tea.KeyRunes:
			m.aliasInput += string(msg.Runes)
			return m, nil
		}
		return m, nil
	}

	// Inline confirmation bar for destructive remove
	if m.confirmRemove != nil {
		target := *m.confirmRemove
		m.confirmRemove = nil
		if key == "y" || key == "Y" {
			return m, m.removeCmd(target.Provider, target.Slot)
		}
		return m, m.setNotice("Cancelled remove", false)
	}

	switch key {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "esc":
		m.clearNotice()
		return m, nil
	case "1":
		return m, m.switchTab(tabAll)
	case "2":
		return m, m.switchTab(tabClaude)
	case "3":
		return m, m.switchTab(tabCodex)
	case "4":
		return m, m.switchTab(tabAgy)
	case "5", "u":
		return m, m.switchTab(tabStats)
	case "6", "m":
		return m, m.switchTab(tabModels)
	case "tab", "right", "l":
		return m, m.switchTab((m.tab + 1) % 6)
	case "shift+tab", "left", "h":
		return m, m.switchTab((m.tab + 5) % 6)
	case "w":
		m.watchMode = !m.watchMode
		var cmds []tea.Cmd
		if m.watchMode {
			m.nextPollIn = pollIntervalSecs
			if cmd := m.ensureTicker(); cmd != nil {
				cmds = append(cmds, cmd)
			}
			if !m.isAccountTab() {
				if cmd := m.switchTab(tabAll); cmd != nil {
					cmds = append(cmds, cmd)
				}
			}
		}
		return m, tea.Batch(cmds...)
	case "d":
		if m.tab == tabStats {
			if m.statsDays == 7 {
				m.statsDays = 30
			} else {
				m.statsDays = 7
			}
			return m, m.fetchStatsCmd(m.statsDays)
		}
		if m.isAccountTab() {
			if t, ok := m.selectedTarget(); ok && t.Slot > 0 {
				return m, m.toggleDisabledCmd(t.Provider, t.Slot, !t.Disabled)
			}
		}
	case "e":
		if m.isAccountTab() {
			if t, ok := m.selectedTarget(); ok && t.Slot > 0 {
				cp := t
				m.editingAlias = &cp
				m.aliasInput = t.Alias
			}
		}
	case "b":
		if m.tab == tabStats {
			m.statsBreakdown = !m.statsBreakdown
			m.scrollOffset = 0
			return m, nil
		}
		if m.isAccountTab() {
			if t, ok := m.selectedTarget(); ok {
				return m, m.switchCmd(t.Provider, "best")
			}
		}
	case "p":
		if m.tab == tabStats {
			return m, tea.Batch(m.setNotice("Refreshing online pricing table…", false), m.refreshPricingCmd())
		}
	case "r":
		if m.tab == tabModels {
			m.modelsLoading = true
			return m, tea.Batch(m.setNotice("Refreshing available models…", false), m.fetchModelsCmd())
		}
		m.loading = true
		m.nextPollIn = pollIntervalSecs
		return m, tea.Batch(m.setNotice("Refreshing quotas…", false), m.fetchStatusCmd(true), m.fetchStatsCmd(m.statsDays))
	case "j", "down":
		if !m.isAccountTab() {
			m.scrollBy(1)
			return m, nil
		}
		targets := m.buildTargets()
		if len(targets) > 0 {
			m.cursor = (m.cursor + 1) % len(targets)
		}
	case "k", "up":
		if !m.isAccountTab() {
			m.scrollBy(-1)
			return m, nil
		}
		targets := m.buildTargets()
		if len(targets) > 0 {
			m.cursor = (m.cursor - 1 + len(targets)) % len(targets)
		}
	case "pgdown":
		if !m.isAccountTab() {
			m.scrollBy(8)
		}
	case "pgup":
		if !m.isAccountTab() {
			m.scrollBy(-8)
		}
	case "enter":
		if !m.isAccountTab() {
			return m, nil
		}
		if t, ok := m.selectedTarget(); ok {
			if t.Slot == 0 {
				return m, m.addCurrentCmd(t.Provider)
			}
			return m, m.switchCmd(t.Provider, fmt.Sprintf("%d", t.Slot))
		}
	case "a":
		if m.isAccountTab() {
			if t, ok := m.selectedTarget(); ok {
				return m, m.addCurrentCmd(t.Provider)
			}
		}
	case "x":
		if m.isAccountTab() {
			if t, ok := m.selectedTarget(); ok && t.Slot > 0 {
				cp := t
				m.confirmRemove = &cp
			}
		}
	}
	return m, nil
}

func (m Model) isAccountTab() bool {
	return m.tab != tabStats && m.tab != tabModels
}

func (m *Model) switchTab(next tabID) tea.Cmd {
	m.tab = next
	m.cursor = 0
	m.scrollOffset = 0
	if !m.noticeIsErr {
		m.clearNotice()
	}
	if next == tabStats {
		return m.fetchStatsCmd(m.statsDays)
	}
	if next == tabModels && m.models == nil && !m.modelsLoading {
		m.modelsLoading = true
		return m.fetchModelsCmd()
	}
	return nil
}

func (m *Model) scrollBy(delta int) {
	m.scrollOffset += delta
	if m.scrollOffset < 0 {
		m.scrollOffset = 0
		return
	}
	maxOffset := m.maxScrollOffset()
	if m.scrollOffset > maxOffset {
		m.scrollOffset = maxOffset
	}
}

func (m Model) bodyLineLimit(headerCount, footerCount int) int {
	// Reserve 4 lines for the two "\n\n" separators around body + 1 line for helpText.
	return m.height - headerCount - footerCount - 5
}

func (m Model) maxScrollOffset() int {
	footerExtra := 0
	if m.editingAlias != nil || m.confirmRemove != nil || m.notice != "" {
		footerExtra = 1
	}
	maxBodyLines := m.bodyLineLimit(2, footerExtra)
	if maxBodyLines <= 4 {
		return 0
	}
	var body string
	switch m.tab {
	case tabStats:
		body = m.renderStats()
	case tabModels:
		body = m.renderModels()
	default:
		return 0
	}
	total := len(strings.Split(body, "\n"))
	if total <= maxBodyLines {
		return 0
	}
	return total - maxBodyLines
}

func (m *Model) selectedTarget() (rowTarget, bool) {
	targets := m.buildTargets()
	if m.cursor >= 0 && m.cursor < len(targets) {
		return targets[m.cursor], true
	}
	return rowTarget{}, false
}

func (m *Model) clampCursor() {
	n := len(m.buildTargets())
	if n == 0 {
		m.cursor = 0
		return
	}
	if m.cursor >= n {
		m.cursor = n - 1
	}
}

func (m *Model) contentWidth() int {
	w := m.width - 4
	if w < 44 {
		w = 44
	}
	if w > 110 {
		w = 110
	}
	return w
}

func (m Model) View() string {
	var headerLines []string

	// Top header
	modeTag := ""
	if m.watchMode {
		modeTag = "  " + styleBadgeWatch.Render(fmt.Sprintf("WATCH · %ds", m.nextPollIn))
	}
	updatedTag := ""
	if m.loading {
		updatedTag = styleSubtitle.Render("refreshing…")
	} else if !m.lastUpdated.IsZero() {
		updatedTag = styleSubtitle.Render("updated " + m.lastUpdated.Format("15:04:05"))
	}
	headerLines = append(headerLines, fmt.Sprintf("%s %s%s   %s",
		styleTitle.Render("aiswap"),
		styleSubtitle.Render(m.version),
		modeTag,
		updatedTag,
	))

	// Tabs
	tabs := []struct {
		id    tabID
		label string
	}{
		{tabAll, "1 All"},
		{tabClaude, "2 Claude"},
		{tabCodex, "3 Codex"},
		{tabAgy, "4 AGY"},
		{tabStats, "5 Stats"},
		{tabModels, "6 Models"},
	}
	var tabParts []string
	for _, t := range tabs {
		if t.id == m.tab {
			tabParts = append(tabParts, styleTabActive.Render(t.label))
		} else {
			tabParts = append(tabParts, styleTabInactive.Render(t.label))
		}
	}
	headerLines = append(headerLines, strings.Join(tabParts, " "))
	header := strings.Join(headerLines, "\n")

	// Body with smooth viewport scrolling so tall content is never silently cut off.
	var body string
	selectedLine := -1
	switch m.tab {
	case tabStats:
		body = m.renderStats()
	case tabModels:
		body = m.renderModels()
	default:
		body, selectedLine = m.renderOverview()
	}

	// Build contextual footer lines first to know exact reserved height.
	var footerLines []string
	if m.editingAlias != nil {
		footerLines = append(footerLines, styleInputPrompt.Render(
			fmt.Sprintf("Alias for %s #%d (empty clears): %s█", m.editingAlias.Provider, m.editingAlias.Slot, m.aliasInput),
		))
	} else if m.confirmRemove != nil {
		footerLines = append(footerLines, styleConfirm.Render(fmt.Sprintf("Remove %s slot #%d (%s)? [y/N]",
			m.confirmRemove.Provider, m.confirmRemove.Slot, m.confirmRemove.Email)))
	} else if m.notice != "" {
		if m.noticeIsErr {
			footerLines = append(footerLines, styleStatusErr.Render("✗ "+m.notice))
		} else {
			footerLines = append(footerLines, styleStatusOK.Render("✓ "+m.notice))
		}
	}

	var helpText string
	switch m.tab {
	case tabStats:
		helpText = "j/k: scroll  d: 7d/30d  b: models  p: pricing  r: refresh  1-6/Tab: views  q: quit"
	case tabModels:
		helpText = "j/k: scroll  r: refresh  1-6/Tab: views  q: quit"
	default:
		helpText = "j/k: select  Enter: switch  b: best  e: alias  d: toggle  a: save  x: rm  w: watch  r: refresh  q: quit"
	}

	maxBodyLines := m.bodyLineLimit(len(headerLines), len(footerLines))
	if maxBodyLines > 4 {
		bodyLines := strings.Split(body, "\n")
		total := len(bodyLines)
		if total > maxBodyLines {
			offset := m.scrollOffset
			maxOffset := total - maxBodyLines
			if m.isAccountTab() && selectedLine >= 0 {
				// Auto-scroll account viewports so the selected row stays visible.
				offset = selectedLine - maxBodyLines/2
			}
			if offset > maxOffset {
				offset = maxOffset
			}
			if offset < 0 {
				offset = 0
			}
			end := offset + maxBodyLines
			body = strings.Join(bodyLines[offset:end], "\n")
			helpText += fmt.Sprintf("   [lines %d-%d/%d ↑↓]", offset+1, end, total)
		}
	}

	footerLines = append(footerLines, styleHelp.Render(helpText))
	footer := strings.Join(footerLines, "\n")

	return header + "\n\n" + body + "\n\n" + footer
}
