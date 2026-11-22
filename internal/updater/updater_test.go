package updater

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestVerifyChecksumAndExtractTarGz(t *testing.T) {
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "aiswap-v0.1.1-linux-arm64.tar.gz")

	f, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	payload := []byte("#!/bin/sh\necho aiswap\n")
	if err := tw.WriteHeader(&tar.Header{Name: "aiswap", Mode: 0o755, Size: int64(len(payload)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(payload); err != nil {
		t.Fatal(err)
	}
	_ = tw.Close()
	_ = gz.Close()
	_ = f.Close()

	raw, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	checksumsPath := filepath.Join(dir, "checksums.txt")
	if err := os.WriteFile(checksumsPath, []byte(hex.EncodeToString(sum[:])+"  aiswap-v0.1.1-linux-arm64.tar.gz\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := VerifyChecksum(archivePath, checksumsPath, "aiswap-v0.1.1-linux-arm64.tar.gz"); err != nil {
		t.Fatalf("VerifyChecksum failed: %v", err)
	}
	if err := VerifyChecksum(archivePath, checksumsPath, "missing-asset.tar.gz"); err == nil {
		t.Fatal("expected error when asset is absent from checksums.txt")
	}
	got, err := extractBinaryFromTarGz(archivePath, "aiswap")
	if err != nil || string(got) != string(payload) {
		t.Fatalf("extractBinaryFromTarGz = (%q, %v), want (%q, nil)", string(got), err, string(payload))
	}
}

func TestIsNewerVersion(t *testing.T) {
	cases := []struct {
		cur, lat string
		want     bool
	}{
		{"v0.1.2", "v0.1.3", true},
		{"v0.1.3", "v0.1.3", false},
		{"v0.2.0", "v0.1.3", false},
		{"dev", "v0.1.3", true},
	}
	for _, tc := range cases {
		if got := isNewerVersion(tc.cur, tc.lat); got != tc.want {
			t.Errorf("isNewerVersion(%q, %q) = %v, want %v", tc.cur, tc.lat, got, tc.want)
		}
	}
}

func TestIsHomebrewManaged(t *testing.T) {
	if !isHomebrewManaged("/opt/homebrew/Cellar/aiswap/1.0.0/bin/aiswap") {
		t.Fatal("expected Homebrew Cellar path to be detected")
	}
	if isHomebrewManaged("/usr/local/bin/aiswap") {
		t.Fatal("expected plain /usr/local/bin path not to be detected as Cellar")
	}
}
