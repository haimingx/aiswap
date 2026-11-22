package fsutil

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAtomicWriteFileAndJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "data.json")

	type sample struct {
		Name string `json:"name"`
	}
	if err := AtomicWriteJSON(path, sample{Name: "v1"}, 0o600); err != nil {
		t.Fatalf("AtomicWriteJSON failed: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat failed: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("perm = %o, want 0600", got)
	}
	if err := AtomicWriteJSON(path, sample{Name: "v2"}, 0o600); err != nil {
		t.Fatalf("overwrite failed: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	if string(raw) != "{\n  \"name\": \"v2\"\n}\n" {
		t.Fatalf("unexpected content: %q", string(raw))
	}
}

func TestDataDirOverride(t *testing.T) {
	want := t.TempDir()
	t.Setenv("AISWAP_DATA_DIR", want)
	got, err := DataDir()
	if err != nil {
		t.Fatalf("DataDir error: %v", err)
	}
	if got != want {
		t.Fatalf("DataDir = %q, want %q", got, want)
	}
}
