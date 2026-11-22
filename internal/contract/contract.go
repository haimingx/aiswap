package contract

import (
	"encoding/json"
	"io"
	"time"

	"github.com/haimingx/aiswap/internal/domain"
)

const SchemaVersion = 1

type ErrorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type ErrorResponse struct {
	SchemaVersion int         `json:"schemaVersion"`
	Error         ErrorDetail `json:"error"`
}

type QuotaPayload struct {
	Windows   []domain.QuotaWindow `json:"windows,omitempty"`
	FetchedAt time.Time            `json:"fetchedAt"`
	Stale     bool                 `json:"stale"`
}

type AccountRow struct {
	Slot        int                `json:"slot"`
	Alias       string             `json:"alias,omitempty"`
	Email       string             `json:"email"`
	OrgName     string             `json:"orgName,omitempty"`
	Plan        string             `json:"plan,omitempty"`
	Active      bool               `json:"active"`
	Disabled    bool               `json:"disabled,omitempty"`
	QuotaStatus domain.QuotaStatus `json:"quotaStatus"`
	Quota       *QuotaPayload      `json:"quota,omitempty"`
	QuotaError  string             `json:"quotaError,omitempty"`
}

type ProviderStatus struct {
	Provider        domain.ProviderName `json:"provider"`
	Installed       bool                `json:"installed"`
	ActiveSlot      int                 `json:"activeSlot,omitempty"`
	LiveEmail       string              `json:"liveEmail,omitempty"`
	LivePlan        string              `json:"livePlan,omitempty"`
	LiveQuotaStatus domain.QuotaStatus  `json:"liveQuotaStatus,omitempty"`
	LiveQuota       *QuotaPayload       `json:"liveQuota,omitempty"`
	LiveQuotaError  string              `json:"liveQuotaError,omitempty"`
	Managed         bool                `json:"managed"`
	Accounts        []AccountRow        `json:"accounts"`
	Error           string              `json:"error,omitempty"`
}

type DailyUsage struct {
	Date             string `json:"date"`
	Requests         int64  `json:"requests"`
	InputTokens      int64  `json:"inputTokens"`
	OutputTokens     int64  `json:"outputTokens"`
	CacheReadTokens  int64  `json:"cacheReadTokens,omitempty"`
	CacheWriteTokens int64  `json:"cacheWriteTokens,omitempty"`
}

type ProviderStats struct {
	Provider              domain.ProviderName `json:"provider"`
	TotalRequests         int64               `json:"totalRequests"`
	TotalInputTokens      int64               `json:"totalInputTokens"`
	TotalOutputTokens     int64               `json:"totalOutputTokens"`
	TotalCacheReadTokens  int64               `json:"totalCacheReadTokens,omitempty"`
	TotalCacheWriteTokens int64               `json:"totalCacheWriteTokens,omitempty"`
	Daily                 []DailyUsage        `json:"daily,omitempty"`
}

type StatsResponse struct {
	SchemaVersion int             `json:"schemaVersion"`
	GeneratedAt   time.Time       `json:"generatedAt"`
	Days          int             `json:"days"`
	Providers     []ProviderStats `json:"providers"`
}

type ProviderModels struct {
	Provider domain.ProviderName `json:"provider"`
	Email    string              `json:"email,omitempty"`
	Plan     string              `json:"plan,omitempty"`
	Quota    *QuotaPayload       `json:"quota,omitempty"`
	Models   []domain.ModelInfo  `json:"models"`
	Error    string              `json:"error,omitempty"`
}

type ModelsResponse struct {
	SchemaVersion int              `json:"schemaVersion"`
	GeneratedAt   time.Time        `json:"generatedAt"`
	Providers     []ProviderModels `json:"providers"`
}

type StatusResponse struct {
	SchemaVersion int              `json:"schemaVersion"`
	GeneratedAt   time.Time        `json:"generatedAt"`
	Providers     []ProviderStatus `json:"providers"`
}

type SwitchResponse struct {
	SchemaVersion int                 `json:"schemaVersion"`
	Provider      domain.ProviderName `json:"provider"`
	Switched      bool                `json:"switched"`
	FromSlot      int                 `json:"fromSlot,omitempty"`
	ToSlot        int                 `json:"toSlot"`
	ToEmail       string              `json:"toEmail,omitempty"`
	Reason        string              `json:"reason,omitempty"`
}

func WriteJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func WriteError(w io.Writer, code, message string) error {
	return WriteJSON(w, ErrorResponse{
		SchemaVersion: SchemaVersion,
		Error: ErrorDetail{
			Code:    code,
			Message: message,
		},
	})
}
