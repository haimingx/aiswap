package claude

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/haimingx/aiswap/internal/domain"
)

func TestClaudeFetchQuotaAndRefresh(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/usage":
			if r.Header.Get("Authorization") != "Bearer at-1" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(`{"five_hour":{"utilization":25.5,"resets_at":"2026-11-22T09:00:00Z"},"seven_day":{"utilization":40.0,"resets_at":"2026-11-27T00:00:00Z"}}`))
		case "/token":
			_, _ = w.Write([]byte(`{"access_token":"at-new","refresh_token":"rt-new","expires_in":3600}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	p := &Provider{
		HTTPClient:    srv.Client(),
		UsageEndpoint: srv.URL + "/usage",
		TokenEndpoint: srv.URL + "/token",
	}
	cred := &domain.LiveCredential{
		Raw: []byte(`{"claudeAiOauth":{"accessToken":"at-1","refreshToken":"rt-1"}}`),
	}

	reading, err := p.FetchQuota(context.Background(), cred)
	if err != nil {
		t.Fatalf("FetchQuota failed: %v", err)
	}
	if len(reading.Windows) != 2 || reading.Windows[0].UsedPct != 25.5 || reading.Windows[1].UsedPct != 40.0 {
		t.Fatalf("unexpected quota windows: %+v", reading.Windows)
	}

	out := p.Refresh(context.Background(), cred)
	if out.Err != nil || out.Credential == nil {
		t.Fatalf("Refresh failed: %+v", out)
	}
	at, rt, err := extractClaudeOAuth(out.Credential.Raw)
	if err != nil || at != "at-new" || rt != "rt-new" {
		t.Fatalf("rotated tokens = (%q, %q, %v), want (at-new, rt-new, nil)", at, rt, err)
	}
}
