package codex

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/haimingx/aiswap/internal/domain"
)

func TestCodexFetchQuota(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("ChatGPT-Account-Id") != "acct-1" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`{"rate_limit":{"primary_window":{"limit_window_seconds":18000,"used_percent":18.5,"reset_at":1795320000},"secondary_window":{"limit_window_seconds":604800,"used_percent":52.0,"reset_at":1795800000}}}`))
	}))
	defer srv.Close()

	p := &Provider{
		HTTPClient:    srv.Client(),
		UsageEndpoint: srv.URL,
	}
	cred := &domain.LiveCredential{
		Raw: []byte(`{"tokens":{"access_token":"at","account_id":"acct-1"}}`),
	}
	reading, err := p.FetchQuota(context.Background(), cred)
	if err != nil {
		t.Fatalf("FetchQuota failed: %v", err)
	}
	if len(reading.Windows) != 2 || reading.Windows[0].Name != "5h" || reading.Windows[0].UsedPct != 18.5 {
		t.Fatalf("unexpected codex windows: %+v", reading.Windows)
	}
}
