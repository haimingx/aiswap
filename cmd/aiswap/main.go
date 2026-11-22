package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/haimingx/aiswap/internal/analytics"
	"github.com/haimingx/aiswap/internal/contract"
	"github.com/haimingx/aiswap/internal/domain"
	"github.com/haimingx/aiswap/internal/engine"
	"github.com/haimingx/aiswap/internal/fsutil"
	"github.com/haimingx/aiswap/internal/provider"
	"github.com/haimingx/aiswap/internal/provider/agy"
	"github.com/haimingx/aiswap/internal/provider/claude"
	"github.com/haimingx/aiswap/internal/provider/codex"
	"github.com/haimingx/aiswap/internal/store"
	"github.com/haimingx/aiswap/internal/tui"
	"github.com/haimingx/aiswap/internal/updater"
)

var version = "dev"

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) == 0 {
		if isTerminal(os.Stdin) && isTerminal(os.Stdout) {
			return runTUI(false)
		}
		printUsage()
		return 0
	}

	jsonOut := false
	refresh := false
	noQuota := false
	filtered := make([]string, 0, len(args))
	for _, a := range args {
		switch a {
		case "--json":
			jsonOut = true
		case "--refresh":
			refresh = true
		case "--no-quota":
			noQuota = true
		default:
			filtered = append(filtered, a)
		}
	}
	if len(filtered) == 0 {
		printUsage()
		return 0
	}

	cmd := filtered[0]
	rest := filtered[1:]

	switch cmd {
	case "version", "--version", "-v":
		fmt.Printf("aiswap %s\n", version)
		return 0
	case "help", "--help", "-h":
		printUsage()
		return 0
	case "tui":
		return runTUI(false)
	case "watch":
		return runTUI(true)
	case "upgrade", "update":
		return runUpgrade(rest, jsonOut)
	case "doctor":
		return runDoctor(jsonOut)
	case "completion":
		return runCompletion(rest)
	case "stats":
		days := 7
		breakdown := false
		refreshPricing := false
		for i := 0; i < len(rest); i++ {
			arg := rest[i]
			var rawVal string
			switch {
			case arg == "--breakdown" || arg == "-b":
				breakdown = true
				continue
			case arg == "--refresh-pricing":
				refreshPricing = true
				continue
			case arg == "--days":
				if i+1 >= len(rest) {
					return fail(jsonOut, "usage", errors.New("missing value for --days"))
				}
				rawVal = rest[i+1]
				i++
			case strings.HasPrefix(arg, "--days="):
				rawVal = strings.TrimPrefix(arg, "--days=")
			default:
				return fail(jsonOut, "usage", fmt.Errorf("unknown flag or argument %q for stats", arg))
			}
			n, err := strconv.Atoi(rawVal)
			if err != nil || n <= 0 {
				return fail(jsonOut, "usage", fmt.Errorf("invalid --days value %q (expected positive integer)", rawVal))
			}
			days = n
		}
		col := analytics.NewCollector()
		if refreshPricing {
			pctx, pcancel := context.WithTimeout(context.Background(), 15*time.Second)
			book, err := col.RefreshPricing(pctx)
			pcancel()
			if err != nil {
				return fail(jsonOut, "pricing_error", err)
			}
			if !jsonOut {
				fmt.Fprintf(os.Stderr, "Updated pricing cache (%d models)\n", book.Count())
			}
		}
		resp := col.Collect(days)
		if jsonOut {
			_ = contract.WriteJSON(os.Stdout, resp)
			return 0
		}
		printStats(resp, breakdown)
		return 0
	}

	mgr, err := buildManager()
	if err != nil {
		return fail(jsonOut, "init_error", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	switch cmd {
	case "models":
		var providers []domain.ProviderName
		if len(rest) > 0 {
			p, err := domain.ParseProvider(rest[0])
			if err != nil {
				return fail(jsonOut, "invalid_provider", err)
			}
			providers = append(providers, p)
		}
		resp, err := mgr.ListModels(ctx, providers...)
		if err != nil {
			return fail(jsonOut, "models_error", err)
		}
		if jsonOut {
			_ = contract.WriteJSON(os.Stdout, resp)
			return 0
		}
		printModels(resp)
		return 0

	case "status", "list", "ls":
		var providers []domain.ProviderName
		if len(rest) > 0 {
			p, err := domain.ParseProvider(rest[0])
			if err != nil {
				return fail(jsonOut, "invalid_provider", err)
			}
			providers = append(providers, p)
		}
		opts := engine.StatusOptions{
			FetchQuota: !noQuota,
			Force:      refresh,
		}
		resp, err := mgr.StatusWithOptions(ctx, opts, providers...)
		if err != nil {
			return fail(jsonOut, "status_error", err)
		}
		if jsonOut {
			_ = contract.WriteJSON(os.Stdout, resp)
			return 0
		}
		printStatus(resp)
		return 0

	case "add":
		if len(rest) < 1 {
			return fail(jsonOut, "usage", errors.New("usage: aiswap add <claude|codex|agy> [--alias name]"))
		}
		p, err := domain.ParseProvider(rest[0])
		if err != nil {
			return fail(jsonOut, "invalid_provider", err)
		}
		var alias string
		for i := 1; i < len(rest); i++ {
			arg := rest[i]
			switch {
			case arg == "--alias":
				if i+1 >= len(rest) {
					return fail(jsonOut, "usage", errors.New("missing value for --alias"))
				}
				alias = rest[i+1]
				i++
			case strings.HasPrefix(arg, "--alias="):
				alias = strings.TrimPrefix(arg, "--alias=")
			default:
				return fail(jsonOut, "usage", fmt.Errorf("unknown flag or argument %q for add", arg))
			}
		}
		res, err := mgr.Add(ctx, p, alias)
		if err != nil {
			return fail(jsonOut, "add_error", err)
		}
		action := "Added"
		if res.Updated {
			action = "Updated"
		}
		aliasNote := ""
		if res.Alias != "" {
			aliasNote = fmt.Sprintf(" (alias: %s)", res.Alias)
		}
		fmt.Printf("%s %s slot #%d: %s%s\n", action, res.Provider, res.Slot, res.Identity.DisplayLabel(), aliasNote)
		return 0

	case "switch", "sw":
		if len(rest) < 1 {
			return fail(jsonOut, "usage", errors.New("usage: aiswap switch <claude|codex|agy> [slot|alias|email|next|best]"))
		}
		p, err := domain.ParseProvider(rest[0])
		if err != nil {
			return fail(jsonOut, "invalid_provider", err)
		}
		target := "next"
		if len(rest) >= 2 {
			target = rest[1]
		}
		res, err := mgr.Switch(ctx, p, target)
		if err != nil {
			return fail(jsonOut, "switch_error", err)
		}
		if jsonOut {
			_ = contract.WriteJSON(os.Stdout, res)
			return 0
		}
		if !res.Switched {
			fmt.Printf("%s slot #%d (%s) is already active\n", res.Provider, res.ToSlot, res.ToEmail)
			return 0
		}
		fmt.Printf("Switched %s: #%d -> #%d (%s)\n", res.Provider, res.FromSlot, res.ToSlot, res.ToEmail)
		return 0

	case "remove", "rm":
		if len(rest) < 2 {
			return fail(jsonOut, "usage", errors.New("usage: aiswap remove <claude|codex|agy> <slot|alias|email>"))
		}
		p, err := domain.ParseProvider(rest[0])
		if err != nil {
			return fail(jsonOut, "invalid_provider", err)
		}
		removed, err := mgr.Remove(ctx, p, rest[1])
		if err != nil {
			return fail(jsonOut, "remove_error", err)
		}
		fmt.Printf("Removed %s slot #%d (%s)\n", p, removed.Slot, removed.Identity.DisplayLabel())
		return 0

	case "alias":
		if len(rest) < 3 {
			return fail(jsonOut, "usage", errors.New("usage: aiswap alias <claude|codex|agy> <slot|email> <alias|--unset>"))
		}
		p, err := domain.ParseProvider(rest[0])
		if err != nil {
			return fail(jsonOut, "invalid_provider", err)
		}
		newAlias := rest[2]
		if newAlias == "--unset" {
			newAlias = ""
		}
		updated, err := mgr.SetAlias(ctx, p, rest[1], newAlias)
		if err != nil {
			return fail(jsonOut, "alias_error", err)
		}
		if updated.Alias == "" {
			fmt.Printf("Cleared alias for %s slot #%d\n", p, updated.Slot)
		} else {
			fmt.Printf("Set alias for %s slot #%d -> %s\n", p, updated.Slot, updated.Alias)
		}
		return 0

	case "disable", "enable":
		if len(rest) < 2 {
			return fail(jsonOut, "usage", fmt.Errorf("usage: aiswap %s <claude|codex|agy> <slot|alias|email>", cmd))
		}
		p, err := domain.ParseProvider(rest[0])
		if err != nil {
			return fail(jsonOut, "invalid_provider", err)
		}
		disabled := cmd == "disable"
		updated, err := mgr.SetDisabled(ctx, p, rest[1], disabled)
		if err != nil {
			return fail(jsonOut, cmd+"_error", err)
		}
		if disabled {
			fmt.Printf("Disabled %s slot #%d (%s) from rotation\n", p, updated.Slot, updated.Identity.DisplayLabel())
		} else {
			fmt.Printf("Enabled %s slot #%d (%s) for rotation\n", p, updated.Slot, updated.Identity.DisplayLabel())
		}
		return 0

	default:
		return fail(jsonOut, "unknown_command", fmt.Errorf("unknown command %q (run 'aiswap help')", cmd))
	}
}

func buildManager() (*engine.Manager, error) {
	dataDir, err := fsutil.DataDir()
	if err != nil {
		return nil, err
	}
	st := store.New(dataDir, nil)
	reg := provider.NewRegistry(
		claude.New(),
		codex.New(),
		agy.New(),
	)
	return engine.NewManager(st, reg), nil
}

func printStatus(resp *contract.StatusResponse) {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	hasUnsaved := false
	for i, ps := range resp.Providers {
		if i > 0 {
			fmt.Fprintln(w)
		}
		state := "not logged in"
		if ps.Error != "" {
			state = "error: " + ps.Error
		} else if ps.LiveEmail != "" {
			plan := ""
			if ps.LivePlan != "" {
				plan = " · " + ps.LivePlan
			}
			managed := "unsaved"
			if ps.Managed {
				managed = fmt.Sprintf("slot #%d", ps.ActiveSlot)
			} else {
				hasUnsaved = true
			}
			quotaStr := formatQuota(ps.LiveQuota, ps.LiveQuotaError)
			if quotaStr != "" {
				state = fmt.Sprintf("%s%s (%s)\t%s", ps.LiveEmail, plan, managed, quotaStr)
			} else {
				state = fmt.Sprintf("%s%s (%s)", ps.LiveEmail, plan, managed)
			}
		} else if !ps.Installed {
			state = "not installed"
		}
		fmt.Fprintf(w, "[%s]\t%s\n", strings.ToUpper(string(ps.Provider)), state)

		for _, acct := range ps.Accounts {
			marker := " "
			if acct.Active {
				marker = "*"
			}
			alias := ""
			if acct.Alias != "" {
				alias = fmt.Sprintf(" (%s)", acct.Alias)
			}
			disabledTag := ""
			if acct.Disabled {
				disabledTag = " [off]"
			}
			plan := ""
			if acct.Plan != "" {
				plan = " · " + acct.Plan
			}
			quotaStr := formatQuota(acct.Quota, acct.QuotaError)
			fmt.Fprintf(w, "  %s #%d%s%s\t%s%s\t%s\n", marker, acct.Slot, alias, disabledTag, acct.Email, plan, quotaStr)
		}
	}
	_ = w.Flush()
	if hasUnsaved {
		fmt.Println("\nTip: run 'aiswap add <claude|codex|agy>' to save an active account into a slot.")
	}
}

func formatQuota(q *contract.QuotaPayload, errKind string) string {
	if q == nil || len(q.Windows) == 0 {
		if errKind != "" {
			return fmt.Sprintf("[%s]", errKind)
		}
		return ""
	}
	parts := make([]string, 0, len(q.Windows))
	for _, win := range q.Windows {
		s := fmt.Sprintf("%s: %.0f%%", win.Name, win.UsedPct)
		if win.UsedPct > 0 && win.ResetsAt != nil {
			rem := time.Until(*win.ResetsAt)
			if rem > 0 {
				s += fmt.Sprintf(" (%s)", contract.FormatCountdown(rem))
			}
		}
		parts = append(parts, s)
	}
	out := strings.Join(parts, " | ")
	if q.Stale {
		out += " [stale]"
	}
	return out
}

func printModels(resp *contract.ModelsResponse) {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	for i, pm := range resp.Providers {
		if i > 0 {
			fmt.Fprintln(w)
		}
		header := fmt.Sprintf("[%s]", strings.ToUpper(string(pm.Provider)))
		if pm.Email != "" {
			header += "  " + pm.Email
		}
		fmt.Fprintf(w, "%s\t(%d models)\n", header, len(pm.Models))
		if pm.Error != "" {
			fmt.Fprintf(w, "  error: %s\n", pm.Error)
			continue
		}
		groups, hasPools := contract.GroupModelPools(pm.Models)
		if !hasPools {
			poolLine := "  All Models Pool"
			if summary := contract.CompactQuotaSummary(pm.Quota); summary != "" {
				poolLine += fmt.Sprintf(" (shared · %s)", summary)
			}
			fmt.Fprintln(w, poolLine)
			for _, m := range pm.Models {
				fmt.Fprintf(w, "    %s\t%s\n", m.DisplayName, m.ID)
			}
			continue
		}
		for idx, g := range groups {
			if idx > 0 {
				fmt.Fprintln(w)
			}
			if g.Usage != "" {
				fmt.Fprintf(w, "  %s (shared · %s)\n", g.Name, g.Usage)
			} else {
				fmt.Fprintf(w, "  %s\n", g.Name)
			}
			for _, m := range g.Models {
				fmt.Fprintf(w, "    %s\t%s\n", m.DisplayName, m.ID)
			}
		}
	}
	_ = w.Flush()
}

func printStats(resp *contract.StatsResponse, breakdown bool) {
	meta := fmt.Sprintf("last %d days", resp.Days)
	if resp.Timezone != "" {
		meta += " · " + resp.Timezone
	}
	if resp.Pricing.Source != "" {
		meta += " · pricing: " + resp.Pricing.Source
	}
	fmt.Printf("Local usage stats (%s)\n\n", meta)

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "PROVIDER\tREQUESTS\tINPUT\tOUTPUT\tCACHE READ\tEST. COST")
	for _, ps := range resp.Providers {
		t := ps.Totals
		fmt.Fprintf(w, "%s\t%d\t%s\t%s\t%s\t%s\n",
			strings.ToUpper(string(ps.Provider)),
			t.Requests,
			contract.FormatTokens(t.InputTokens),
			contract.FormatTokens(t.OutputTokens),
			contract.FormatTokens(t.CacheReadTokens),
			contract.FormatUSD(t.EstimatedCostUSD),
		)
	}
	if resp.Totals.Requests > 0 {
		t := resp.Totals
		fmt.Fprintf(w, "TOTAL\t%d\t%s\t%s\t%s\t%s\n",
			t.Requests,
			contract.FormatTokens(t.InputTokens),
			contract.FormatTokens(t.OutputTokens),
			contract.FormatTokens(t.CacheReadTokens),
			contract.FormatUSD(t.EstimatedCostUSD),
		)
	}
	_ = w.Flush()

	if !breakdown {
		return
	}
	for _, ps := range resp.Providers {
		if len(ps.Models) == 0 {
			continue
		}
		fmt.Printf("\n[%s] Models\n", strings.ToUpper(string(ps.Provider)))
		mw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(mw, "  MODEL\tREQUESTS\tINPUT\tOUTPUT\tCACHE READ\tEST. COST")
		for _, m := range ps.Models {
			fmt.Fprintf(mw, "  %s\t%d\t%s\t%s\t%s\t%s\n",
				m.Model,
				m.Requests,
				contract.FormatTokens(m.InputTokens),
				contract.FormatTokens(m.OutputTokens),
				contract.FormatTokens(m.CacheReadTokens),
				contract.FormatUSD(m.EstimatedCostUSD),
			)
		}
		_ = mw.Flush()
	}
}

func fail(jsonOut bool, code string, err error) int {
	if jsonOut {
		_ = contract.WriteError(os.Stdout, code, err.Error())
	} else {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
	}
	return 1
}

func runUpgrade(args []string, jsonOut bool) int {
	checkOnly := false
	for _, a := range args {
		if a == "--check" {
			checkOnly = true
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	u := updater.New(os.Getenv("AISWAP_REPO"))
	if checkOnly {
		res, err := u.Check(ctx, version)
		if err != nil {
			return fail(jsonOut, "upgrade_check_error", err)
		}
		if res.UpdateAvail {
			fmt.Printf("Update available: %s -> %s (run 'aiswap upgrade')\n", res.CurrentVersion, res.LatestVersion)
		} else {
			fmt.Printf("aiswap %s is up to date\n", res.CurrentVersion)
		}
		return 0
	}
	res, err := u.Upgrade(ctx, version, "")
	if err != nil {
		return fail(jsonOut, "upgrade_error", err)
	}
	if !res.UpdateAvail {
		fmt.Printf("aiswap %s is already up to date\n", res.CurrentVersion)
		return 0
	}
	fmt.Printf("Upgraded aiswap: %s -> %s\n", res.CurrentVersion, res.LatestVersion)
	return 0
}

func runDoctor(jsonOut bool) int {
	exe, _ := os.Executable()
	dataDir, dataErr := fsutil.DataDir()

	mgr, err := buildManager()
	if err != nil {
		return fail(jsonOut, "doctor_error", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := mgr.StatusWithOptions(ctx, engine.StatusOptions{FetchQuota: false})
	if err != nil {
		return fail(jsonOut, "doctor_error", err)
	}
	if jsonOut {
		_ = contract.WriteJSON(os.Stdout, resp)
		return 0
	}

	fmt.Printf("aiswap doctor\n")
	fmt.Printf("  version:   %s\n", version)
	fmt.Printf("  binary:    %s\n", exe)
	if dataErr != nil {
		fmt.Printf("  data dir:  error (%v)\n", dataErr)
	} else {
		fmt.Printf("  data dir:  %s\n", dataDir)
	}
	fmt.Println("\nProviders:")
	for _, ps := range resp.Providers {
		state := "not installed"
		if ps.Error != "" {
			state = "error: " + ps.Error
		} else if ps.LiveEmail != "" {
			state = fmt.Sprintf("ok (%s, %d saved slot(s))", ps.LiveEmail, len(ps.Accounts))
		} else if ps.Installed {
			state = fmt.Sprintf("installed, not logged in (%d saved slot(s))", len(ps.Accounts))
		}
		fmt.Printf("  %-8s %s\n", string(ps.Provider), state)
	}
	return 0
}

func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

func runTUI(watchMode bool) int {
	mgr, err := buildManager()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	if err := tui.Run(mgr, analytics.NewCollector(), tui.Options{
		WatchMode: watchMode,
		Version:   version,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	return 0
}

func printUsage() {
	fmt.Print(`aiswap — lightweight local account switcher & quota monitor for Claude, Codex & AGY
Zero proxy, config swap only.

Usage:
  aiswap         (interactive TUI dashboard when attached to a terminal)
  aiswap tui     (open interactive dashboard)
  aiswap watch   (open live quota monitor)
  aiswap status     [claude|codex|agy] [--refresh] [--no-quota] [--json]
  aiswap list       [claude|codex|agy] [--refresh] [--no-quota] [--json]
  aiswap models     [claude|codex|agy] [--json]
  aiswap add     <claude|codex|agy> [--alias name]
  aiswap switch     <claude|codex|agy> [slot|alias|email|next|best] [--json]
  aiswap remove     <claude|codex|agy> <slot|alias|email>
  aiswap alias      <claude|codex|agy> <slot|email> <alias|--unset>
  aiswap disable    <claude|codex|agy> <slot|alias|email>
  aiswap enable     <claude|codex|agy> <slot|alias|email>
  aiswap stats      [--days 7] [--breakdown|-b] [--refresh-pricing] [--json]
  aiswap doctor
  aiswap upgrade    [--check]
  aiswap completion <zsh|bash|fish>
  aiswap version

Provider aliases: c|cc (claude), x|cx (codex), a|antigravity (agy)
`)
}
