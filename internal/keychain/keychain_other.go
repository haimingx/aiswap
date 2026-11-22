//go:build !darwin

package keychain

import "path/filepath"

func DefaultBackupStore(backupRoot string) Store {
	return &FileStore{Root: filepath.Join(backupRoot, "credentials")}
}
