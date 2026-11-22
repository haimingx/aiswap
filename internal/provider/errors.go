package provider

import (
	"bytes"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// DefaultHTTPClient is shared across providers when no custom HTTPClient is injected.
var DefaultHTTPClient = &http.Client{Timeout: 10 * time.Second}

// QuotaError carries structured failure metadata for UsageStore backoff decisions.
type QuotaError struct {
	Kind       string        // e.g. "http-429", "http-401", "timeout", "network", "bad-response"
	StatusCode int
	RetryAfter time.Duration
	Message    string
}

func (e *QuotaError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("%s: %s", e.Kind, e.Message)
	}
	return e.Kind
}

func NewHTTPQuotaError(resp *http.Response, body []byte) *QuotaError {
	return &QuotaError{
		Kind:       fmt.Sprintf("http-%d", resp.StatusCode),
		StatusCode: resp.StatusCode,
		RetryAfter: ParseRetryAfter(resp.Header),
		Message:    string(bytes.TrimSpace(body[:min(len(body), 200)])),
	}
}

func ParseRetryAfter(h http.Header) time.Duration {
	raw := strings.TrimSpace(h.Get("Retry-After"))
	if raw == "" {
		return 0
	}
	if secs, err := strconv.ParseFloat(raw, 64); err == nil && secs > 0 {
		return time.Duration(secs * float64(time.Second))
	}
	if t, err := http.ParseTime(raw); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}
