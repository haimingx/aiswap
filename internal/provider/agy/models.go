package agy

import (
	"context"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/haimingx/aiswap/internal/domain"
)

func (p *Provider) ListModels(ctx context.Context, cred *domain.LiveCredential) ([]domain.ModelInfo, error) {
	// Fetch quota overlay (HTTP) and official `agy models` CLI list concurrently.
	var (
		res       *agyFetchResult
		fetchErr  error
		cliModels []domain.ModelInfo
		wg        sync.WaitGroup
	)

	wg.Add(1)
	go func() {
		defer wg.Done()
		res, fetchErr = p.fetchAvailableModels(ctx, cred)
	}()

	useCLI := p.ModelsCommand != nil || p.QuotaEndpoint == ""
	if useCLI {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cliModels = p.listModelsViaCLI(ctx)
		}()
	}
	wg.Wait()

	if fetchErr != nil && len(cliModels) == 0 {
		return nil, fetchErr
	}

	summaryUsage := map[string]string{}
	fallbackPct := map[string]float64{}
	fallbackPeriod := map[string]string{}
	now := time.Now().UTC()

	if res != nil {
		summary := p.fetchQuotaSummary(ctx, res.AccessToken, res.Info.ProjectID, res.UserAgent)
		for fam, wins := range summary {
			parts := make([]string, 0, len(wins))
			for _, w := range wins {
				label := strings.TrimPrefix(w.Name, fam+" ")
				parts = append(parts, fmt.Sprintf("%s %.0f%%", label, w.UsedPct))
			}
			summaryUsage[fam] = strings.Join(parts, " | ")
		}

		for id, m := range res.Payload.Models {
			if m.IsInternal || isAuxiliaryModelID(id) || m.QuotaInfo == nil {
				continue
			}
			fam := classifyModelFamily(id, m.DisplayName)
			if fam == "" {
				continue
			}
			if m.QuotaInfo.RemainingFraction != nil {
				u := clampUsedPct(*m.QuotaInfo.RemainingFraction)
				if cur, exists := fallbackPct[fam]; !exists || u > cur {
					fallbackPct[fam] = u
				}
			}
			if period := inferQuotaPeriod(m.QuotaInfo.ResetTime, now); period != "" && fallbackPeriod[fam] == "" {
				fallbackPeriod[fam] = period
			}
		}
	}

	applyPoolUsage := func(m *domain.ModelInfo) {
		fam := classifyModelFamily(m.ID, m.DisplayName)
		m.Pool = modelPoolName(m.ID, m.DisplayName)
		if u := summaryUsage[fam]; u != "" {
			m.PoolUsage = u
		} else if pct, ok := fallbackPct[fam]; ok {
			if pStr := fallbackPeriod[fam]; pStr != "" {
				m.PoolUsage = fmt.Sprintf("%s %.0f%%", pStr, pct)
			} else {
				m.PoolUsage = fmt.Sprintf("%.0f%%", pct)
			}
		}
	}

	// Prefer the official `agy models` output when available; fall back to HTTP fetchAvailableModels.
	if len(cliModels) > 0 {
		for i := range cliModels {
			applyPoolUsage(&cliModels[i])
		}
		sortModels(cliModels)
		return cliModels, nil
	}

	byBase := map[string]domain.ModelInfo{}
	for id, m := range res.Payload.Models {
		if m.IsInternal || isAuxiliaryModelID(id) {
			continue
		}
		rawName := m.DisplayName
		if rawName == "" {
			rawName = id
		}
		baseName := stripEffortSuffix(rawName)
		baseID := stripIDEffortSuffix(id)

		var usedPtr *float64
		if m.QuotaInfo != nil && m.QuotaInfo.RemainingFraction != nil {
			u := clampUsedPct(*m.QuotaInfo.RemainingFraction)
			usedPtr = &u
		}

		existing, exists := byBase[baseName]
		if !exists {
			byBase[baseName] = domain.ModelInfo{
				ID:          baseID,
				DisplayName: baseName,
				UsedPct:     usedPtr,
			}
			continue
		}
		if len(baseID) < len(existing.ID) {
			existing.ID = baseID
		}
		if usedPtr != nil && (existing.UsedPct == nil || *usedPtr > *existing.UsedPct) {
			existing.UsedPct = usedPtr
		}
		byBase[baseName] = existing
	}

	out := make([]domain.ModelInfo, 0, len(byBase))
	for _, item := range byBase {
		applyPoolUsage(&item)
		out = append(out, item)
	}
	sortModels(out)
	return out, nil
}

func (p *Provider) listModelsViaCLI(ctx context.Context) []domain.ModelInfo {
	var raw []byte
	var err error
	if p.ModelsCommand != nil {
		raw, err = p.ModelsCommand(ctx)
	} else {
		bin, lookErr := exec.LookPath("agy")
		if lookErr != nil {
			return nil
		}
		c, cancel := context.WithTimeout(ctx, 8*time.Second)
		defer cancel()
		raw, err = exec.CommandContext(c, bin, "models").Output()
	}
	if err != nil || len(raw) == 0 {
		return nil
	}
	return parseCLIModelsOutput(raw)
}

func parseCLIModelsOutput(raw []byte) []domain.ModelInfo {
	byBase := map[string]domain.ModelInfo{}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		id, name, ok := strings.Cut(line, "\t")
		if !ok {
			continue
		}
		id = strings.TrimSpace(id)
		name = strings.TrimSpace(name)
		if id == "" || isAuxiliaryModelID(id) {
			continue
		}
		if name == "" {
			name = id
		}
		baseName := stripEffortSuffix(name)
		baseID := stripIDEffortSuffix(id)
		existing, exists := byBase[baseName]
		if !exists || len(baseID) < len(existing.ID) {
			byBase[baseName] = domain.ModelInfo{
				ID:          baseID,
				DisplayName: baseName,
			}
		}
	}
	out := make([]domain.ModelInfo, 0, len(byBase))
	for _, item := range byBase {
		out = append(out, item)
	}
	return out
}

func sortModels(out []domain.ModelInfo) {
	sort.Slice(out, func(i, j int) bool {
		ri := modelFamilyRank(out[i].ID, out[i].DisplayName)
		rj := modelFamilyRank(out[j].ID, out[j].DisplayName)
		if ri != rj {
			return ri < rj
		}
		return out[i].DisplayName < out[j].DisplayName
	})
}

func isAuxiliaryModelID(id string) bool {
	lower := strings.ToLower(id)
	return strings.HasPrefix(lower, "tab_") ||
		strings.HasPrefix(lower, "chat_") ||
		strings.HasSuffix(lower, "-tiered")
}

func stripEffortSuffix(name string) string {
	for _, suffix := range []string{" (XHigh)", " (Low)", " (Medium)", " (High)", " (Thinking)"} {
		if strings.HasSuffix(name, suffix) {
			return strings.TrimSuffix(name, suffix)
		}
	}
	return name
}

func stripIDEffortSuffix(id string) string {
	for _, suffix := range []string{"-xhigh", "-low", "-medium", "-high", "-thinking"} {
		if strings.HasSuffix(id, suffix) {
			return strings.TrimSuffix(id, suffix)
		}
	}
	return id
}

func modelFamilyRank(id, displayName string) int {
	lower := strings.ToLower(displayName)
	idLower := strings.ToLower(id)
	switch {
	case strings.HasPrefix(lower, "gemini"):
		return 0
	case strings.HasPrefix(idLower, "gemini") || strings.HasPrefix(lower, "orchestrator"):
		return 1
	case strings.HasPrefix(lower, "claude"):
		return 2
	default:
		return 3
	}
}

func modelPoolName(id, displayName string) string {
	if classifyModelFamily(id, displayName) == "Gemini" {
		return "Gemini Pool"
	}
	return "Claude / GPT Pool"
}
