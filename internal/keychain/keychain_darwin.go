//go:build darwin

package keychain

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

const (
	securityInteractiveMaxLine = 3800
	securityCmdTimeout         = 15 * time.Second
)

// MacOSStore wraps the native macOS `security` CLI for Keychain access.
type MacOSStore struct{}

func DefaultBackupStore(backupRoot string) Store {
	return &MacOSStore{}
}

func (m *MacOSStore) Backend() string { return "keychain" }

func (m *MacOSStore) Get(service, account string) ([]byte, error) {
	args := []string{"find-generic-password", "-s", service, "-w"}
	if account != "" {
		args = append(args, "-a", account)
	}
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), securityCmdTimeout)
		cmd := exec.CommandContext(ctx, "security", args...)
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		err := cmd.Run()
		cancel()
		if err == nil {
			out := strings.TrimSuffix(stdout.String(), "\n")
			return decodeSecurityOutput(out), nil
		}
		if strings.Contains(stderr.String(), "could not be found") {
			return nil, ErrNotFound
		}
		lastErr = fmt.Errorf("security get %s/%s: %w (%s)", service, account, err, strings.TrimSpace(stderr.String()))
		if attempt == 0 {
			time.Sleep(250 * time.Millisecond)
		}
	}
	return nil, lastErr
}

func (m *MacOSStore) Set(service, account string, secret []byte) error {
	if strings.ContainsAny(service, "\r\n\"") || strings.ContainsAny(account, "\r\n\"") {
		return fmt.Errorf("invalid keychain service or account name")
	}
	hexVal := hex.EncodeToString(secret)
	input := fmt.Sprintf("add-generic-password -U -s %q -a %q -X %s\n", service, account, hexVal)

	ctx, cancel := context.WithTimeout(context.Background(), securityCmdTimeout)
	defer cancel()

	var cmd *exec.Cmd
	usedInteractive := len(input) <= securityInteractiveMaxLine
	// Prefer `security -i` stdin to avoid exposing secrets in argv when the command
	// fits within Apple `security -i`'s 4 KiB line buffer; fall back to direct args
	// for larger payloads (e.g. Codex JWT credentials).
	if usedInteractive {
		cmd = exec.CommandContext(ctx, "security", "-i")
		cmd.Stdin = strings.NewReader(input)
	} else {
		cmd = exec.CommandContext(ctx, "security", "add-generic-password", "-U", "-s", service, "-a", account, "-X", hexVal)
	}

	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	msg := strings.TrimSpace(stderr.String())
	// `security -i` exits 0 at EOF even when an interactive command fails and writes to stderr.
	if err != nil || (usedInteractive && msg != "") {
		if len(msg) > 120 {
			msg = msg[:120] + "…"
		}
		if err == nil {
			return fmt.Errorf("security set %s/%s: %s", service, account, msg)
		}
		return fmt.Errorf("security set %s/%s: %w (%s)", service, account, err, msg)
	}
	return nil
}

func (m *MacOSStore) Delete(service, account string) error {
	args := []string{"delete-generic-password", "-s", service}
	if account != "" {
		args = append(args, "-a", account)
	}
	ctx, cancel := context.WithTimeout(context.Background(), securityCmdTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "security", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if strings.Contains(stderr.String(), "could not be found") {
			return nil
		}
		return fmt.Errorf("security delete %s/%s: %w (%s)", service, account, err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// `security -w` emits hex when the stored item contains newlines, control chars, or non-ASCII bytes.
func decodeSecurityOutput(out string) []byte {
	if len(out) > 0 && len(out)%2 == 0 {
		if decoded, err := hex.DecodeString(out); err == nil && len(decoded) > 0 {
			if decoded[0] == '{' || strings.HasPrefix(string(decoded), "sk-") || hasNonPrintableASCII(decoded) {
				return decoded
			}
		}
	}
	return []byte(out)
}

func hasNonPrintableASCII(b []byte) bool {
	for _, c := range b {
		if c < 0x20 || c > 0x7e {
			return true
		}
	}
	return false
}
