package fsutil

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// DataDir returns the root directory for aiswap state.
// AISWAP_DATA_DIR overrides the platform default (also used by tests).
func DataDir() (string, error) {
	if override := os.Getenv("AISWAP_DATA_DIR"); override != "" {
		return filepath.Clean(override), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, "Library", "Application Support", "aiswap"), nil
	}
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		return filepath.Join(xdg, "aiswap"), nil
	}
	return filepath.Join(home, ".local", "share", "aiswap"), nil
}

// AtomicWriteFile writes data to a sibling temp file, fsyncs it, enforces perm, and renames it into place.
func AtomicWriteFile(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".aiswap-tmp-*")
	if err != nil {
		return fmt.Errorf("create temp in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()

	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod temp %s: %w", tmpName, err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp %s: %w", tmpName, err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync temp %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename %s -> %s: %w", tmpName, path, err)
	}
	cleanup = false
	return nil
}

func AtomicWriteJSON(path string, v any, perm os.FileMode) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal json for %s: %w", path, err)
	}
	data = append(data, '\n')
	return AtomicWriteFile(path, data, perm)
}
