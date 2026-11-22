package domain

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

type ProviderName string

const (
	ProviderClaude ProviderName = "claude"
	ProviderCodex  ProviderName = "codex"
	ProviderAgy    ProviderName = "agy"
)

var AllProviders = []ProviderName{
	ProviderClaude,
	ProviderCodex,
	ProviderAgy,
}

func ParseProvider(raw string) (ProviderName, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "claude", "cc", "c":
		return ProviderClaude, nil
	case "codex", "cx", "x":
		return ProviderCodex, nil
	case "agy", "antigravity", "a":
		return ProviderAgy, nil
	default:
		return "", fmt.Errorf("unknown provider %q (expected: claude, codex, agy)", raw)
	}
}

var aliasRe = regexp.MustCompile(`^[a-z0-9_.-]+$`)

func NormalizeAlias(raw string) (string, error) {
	name := strings.ToLower(strings.TrimSpace(raw))
	if name == "" {
		return "", errors.New("alias cannot be empty")
	}
	if strings.HasPrefix(name, "-") {
		return "", fmt.Errorf("alias %q cannot start with '-'", raw)
	}
	allDigits := true
	for _, r := range name {
		if r < '0' || r > '9' {
			allDigits = false
			break
		}
	}
	if allDigits {
		return "", fmt.Errorf("alias %q cannot be purely numeric (reserved for slot numbers)", raw)
	}
	if !aliasRe.MatchString(name) {
		return "", fmt.Errorf("alias %q may only contain letters, digits, '-', '_', '.'", raw)
	}
	return name, nil
}

type Identity struct {
	Email     string `json:"email"`
	AccountID string `json:"accountId,omitempty"`
	OrgID     string `json:"orgId,omitempty"`
	OrgName   string `json:"orgName,omitempty"`
	Plan      string `json:"plan,omitempty"`
}

func (id Identity) Key() string {
	if id.AccountID != "" && id.OrgID != "" {
		return id.AccountID + "|" + id.OrgID
	}
	if id.AccountID != "" {
		return id.AccountID
	}
	return strings.ToLower(strings.TrimSpace(id.Email))
}

func (id Identity) DisplayLabel() string {
	if id.Email != "" && id.OrgName != "" {
		return fmt.Sprintf("%s [%s]", id.Email, id.OrgName)
	}
	if id.Email != "" {
		return id.Email
	}
	if id.AccountID != "" {
		return id.AccountID
	}
	return "unknown"
}

// LiveCredential holds the raw account-scoped bytes captured from or written to a tool.
type LiveCredential struct {
	// Raw is the primary credential JSON (e.g. .credentials.json, auth.json, antigravity-oauth-token).
	Raw []byte `json:"raw"`
	// Meta holds optional secondary account metadata (e.g. Claude's oauthAccount JSON object).
	Meta []byte `json:"meta,omitempty"`
	// Backend records where the credential came from ("file" or "keychain").
	Backend string `json:"backend,omitempty"`
}

type AccountSlot struct {
	Slot      int       `json:"slot"`
	Alias     string    `json:"alias,omitempty"`
	Identity  Identity  `json:"identity"`
	Disabled  bool      `json:"disabled,omitempty"`
	AddedAt   time.Time `json:"addedAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type QuotaStatus string

const (
	QuotaOK              QuotaStatus = "ok"
	QuotaUnavailable     QuotaStatus = "unavailable"
	QuotaTokenExpired    QuotaStatus = "token_expired"
	QuotaReloginRequired QuotaStatus = "relogin_required"
	QuotaRateLimited     QuotaStatus = "rate_limited"
)

type QuotaWindow struct {
	Name     string     `json:"name"`
	UsedPct  float64    `json:"usedPct"`
	ResetsAt *time.Time `json:"resetsAt,omitempty"`
}

type QuotaReading struct {
	Windows   []QuotaWindow `json:"windows,omitempty"`
	Plan      string        `json:"plan,omitempty"`
	FetchedAt time.Time     `json:"fetchedAt"`
}

type ModelInfo struct {
	ID          string   `json:"id"`
	DisplayName string   `json:"displayName"`
	Pool        string   `json:"pool,omitempty"`
	PoolUsage   string   `json:"poolUsage,omitempty"`
	UsedPct     *float64 `json:"usedPct,omitempty"`
}

type RefreshOutcome struct {
	Credential *LiveCredential
	// Permanent is true on invalid_grant or missing refresh token (requires re-login).
	Permanent bool
	Err       error
}
