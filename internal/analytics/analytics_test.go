package analytics

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/haimingx/aiswap/internal/fsutil"
)

func TestCollectAllThreeProviders(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)

	claudeJSONL := `{"timestamp":"2026-11-22T09:00:00Z","requestId":"req-1","message":{"model":"claude-sonnet","usage":{"input_tokens":100,"output_tokens":10,"cache_read_input_tokens":500,"cache_creation_input_tokens":20}}}
{"timestamp":"2026-11-22T09:00:01Z","requestId":"req-1","message":{"model":"claude-sonnet","usage":{"input_tokens":100,"output_tokens":50,"cache_read_input_tokens":500,"cache_creation_input_tokens":20}}}
{"timestamp":"2026-11-21T08:00:00Z","requestId":"req-2","message":{"model":"claude-opus","usage":{"input_tokens":200,"output_tokens":80,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}}}
`
	if err := fsutil.AtomicWriteFile(filepath.Join(dir, "claude", "proj-a", "sess.jsonl"), []byte(claudeJSONL), 0o600); err != nil {
		t.Fatal(err)
	}

	agyJSONL := `{"created_at":"2026-11-22T09:10:00Z","input_tokens":400,"output_tokens":120,"cache_read_tokens":900,"model":"gemini-pro"}
{"created_at":"2026-11-22T09:11:00Z","input_tokens":0,"output_tokens":0,"cache_read_tokens":0}
`
	if err := fsutil.AtomicWriteFile(filepath.Join(dir, "agy", "conv-1", ".system_generated", "logs", "transcript.jsonl"), []byte(agyJSONL), 0o600); err != nil {
		t.Fatal(err)
	}
	agyIDEJSONL := `{"created_at":"2026-11-22T09:15:00Z","input_tokens":100,"output_tokens":30,"cache_read_tokens":200}
`
	if err := fsutil.AtomicWriteFile(filepath.Join(dir, "agy-ide", "conv-2", ".system_generated", "logs", "transcript.jsonl"), []byte(agyIDEJSONL), 0o600); err != nil {
		t.Fatal(err)
	}

	c := &Collector{
		ClaudeProjectsDir: filepath.Join(dir, "claude"),
		CodexSessionsDir:  filepath.Join(dir, "missing"),
		AgyBrainDir:       filepath.Join(dir, "agy"),
		AgyIDEBrainDir:    filepath.Join(dir, "agy-ide"),
		Now:               func() time.Time { return now },
	}
	resp := c.Collect(7)
	if len(resp.Providers) != 3 {
		t.Fatalf("expected 3 providers, got %d", len(resp.Providers))
	}
	cl := resp.Providers[0]
	if cl.TotalRequests != 2 || cl.TotalInputTokens != 300 || cl.TotalOutputTokens != 130 || cl.TotalCacheReadTokens != 500 {
		t.Fatalf("unexpected claude stats: %+v", cl)
	}
	ag := resp.Providers[2]
	if ag.TotalRequests != 2 || ag.TotalInputTokens != 500 || ag.TotalOutputTokens != 150 || ag.TotalCacheReadTokens != 1100 {
		t.Fatalf("unexpected agy stats: %+v", ag)
	}
}
