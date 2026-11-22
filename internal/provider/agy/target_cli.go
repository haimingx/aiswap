package agy

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/haimingx/aiswap/internal/fsutil"
	"github.com/haimingx/aiswap/internal/keychain"
	"github.com/haimingx/aiswap/internal/provider"
)

const (
	macosKeychainService  = "gemini"
	macosKeychainAccount  = "antigravity"
	goKeyringBase64Prefix = "go-keyring-base64:"
)

type CLITarget struct {
	DataDir     string
	Keychain    keychain.Store
	UseKeychain bool
}

func NewCLITarget(dataDir string) *CLITarget {
	useKC := runtime.GOOS == "darwin"
	var kc keychain.Store
	if useKC {
		kc = keychain.DefaultBackupStore("")
	}
	return &CLITarget{
		DataDir:     dataDir,
		Keychain:    kc,
		UseKeychain: useKC,
	}
}

func (c *CLITarget) TargetName() string { return "cli" }

func (c *CLITarget) tokenPath() string {
	return filepath.Join(c.DataDir, "antigravity-oauth-token")
}

func (c *CLITarget) IsInstalled() bool {
	_, err := os.Stat(c.DataDir)
	return err == nil
}

func (c *CLITarget) Read() ([]byte, string, error) {
	if c.UseKeychain && c.Keychain != nil {
		raw, err := c.Keychain.Get(macosKeychainService, macosKeychainAccount)
		if err == nil && len(raw) > 0 {
			decoded, dErr := decodeKeyringPayload(raw)
			if dErr != nil {
				return nil, "keychain", dErr
			}
			return decoded, "keychain", nil
		}
		if err != nil && !errors.Is(err, keychain.ErrNotFound) {
			return nil, "keychain", err
		}
	}
	raw, err := os.ReadFile(c.tokenPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil, "file", provider.ErrNotLoggedIn
	}
	return raw, "file", err
}

func (c *CLITarget) Write(raw []byte) error {
	if c.UseKeychain && c.Keychain != nil {
		encoded := encodeKeyringPayload(raw)
		if err := c.Keychain.Set(macosKeychainService, macosKeychainAccount, encoded); err != nil {
			return err
		}
		// Keep legacy file in sync if it already exists on disk.
		if _, err := os.Stat(c.tokenPath()); err == nil {
			_ = fsutil.AtomicWriteFile(c.tokenPath(), raw, 0o600)
		}
		return nil
	}
	return fsutil.AtomicWriteFile(c.tokenPath(), raw, 0o600)
}

// agy CLI uses zalando/go-keyring on macOS, which wraps multi-line/non-ASCII payloads
// with a "go-keyring-base64:" prefix.
func decodeKeyringPayload(raw []byte) ([]byte, error) {
	trimmed := bytes.TrimSpace(raw)
	prefix := []byte(goKeyringBase64Prefix)
	if !bytes.HasPrefix(trimmed, prefix) {
		return raw, nil
	}
	encoded := string(bytes.TrimPrefix(trimmed, prefix))
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		decoded, err = base64.RawStdEncoding.DecodeString(encoded)
		if err != nil {
			return nil, fmt.Errorf("decode go-keyring-base64 payload: %w", err)
		}
	}
	return decoded, nil
}

func encodeKeyringPayload(raw []byte) []byte {
	return []byte(goKeyringBase64Prefix + base64.StdEncoding.EncodeToString(raw))
}
