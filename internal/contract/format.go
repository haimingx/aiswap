package contract

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/haimingx/aiswap/internal/domain"
)

func FormatCountdown(d time.Duration) string {
	total := int(d.Seconds())
	if total <= 0 {
		return "0m"
	}
	days := total / 86400
	hours := (total % 86400) / 3600
	mins := (total % 3600) / 60
	if days > 0 {
		return fmt.Sprintf("%dd%dh", days, hours)
	}
	if hours > 0 {
		return fmt.Sprintf("%dh%dm", hours, mins)
	}
	return fmt.Sprintf("%dm", mins)
}

func FormatTokens(n int64) string {
	switch {
	case n >= 1_000_000_000:
		return fmt.Sprintf("%.2fB", float64(n)/1_000_000_000)
	case n >= 1_000_000:
		return fmt.Sprintf("%.2fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fK", float64(n)/1_000)
	default:
		return strconv.FormatInt(n, 10)
	}
}

func FormatUSD(usd *float64) string {
	if usd == nil {
		return "—"
	}
	v := *usd
	if v == 0 {
		return "$0.00"
	}
	if v < 0.01 {
		return fmt.Sprintf("$%.4f", v)
	}
	return fmt.Sprintf("$%.2f", v)
}

func CompactQuotaSummary(q *QuotaPayload) string {
	if q == nil || len(q.Windows) == 0 {
		return ""
	}
	parts := make([]string, 0, len(q.Windows))
	for _, w := range q.Windows {
		parts = append(parts, fmt.Sprintf("%s %.0f%%", w.Name, w.UsedPct))
	}
	return strings.Join(parts, " | ")
}

type ModelPoolGroup struct {
	Name    string
	Usage   string
	UsedPct *float64
	Models  []domain.ModelInfo
}

// GroupModelPools groups models by their shared quota pool name.
// Returns (groups, true) if any model declares a pool; otherwise (nil, false).
func GroupModelPools(models []domain.ModelInfo) ([]ModelPoolGroup, bool) {
	hasPools := false
	for _, m := range models {
		if m.Pool != "" {
			hasPools = true
			break
		}
	}
	if !hasPools {
		return nil, false
	}

	idxByPool := map[string]int{}
	groups := make([]ModelPoolGroup, 0, 2)
	for _, mi := range models {
		pool := mi.Pool
		if pool == "" {
			pool = "Other"
		}
		idx, ok := idxByPool[pool]
		if !ok {
			idx = len(groups)
			idxByPool[pool] = idx
			groups = append(groups, ModelPoolGroup{Name: pool})
		}
		groups[idx].Models = append(groups[idx].Models, mi)
		if mi.PoolUsage != "" && groups[idx].Usage == "" {
			groups[idx].Usage = mi.PoolUsage
		}
		if mi.UsedPct != nil {
			if groups[idx].UsedPct == nil || *mi.UsedPct > *groups[idx].UsedPct {
				v := *mi.UsedPct
				groups[idx].UsedPct = &v
			}
		}
	}
	for i := range groups {
		if groups[i].Usage == "" && groups[i].UsedPct != nil {
			groups[i].Usage = fmt.Sprintf("%.0f%%", *groups[i].UsedPct)
		}
	}
	return groups, true
}
