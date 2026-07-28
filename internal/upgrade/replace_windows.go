//go:build windows

package upgrade

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

func replaceExecutable(target string, data []byte, mode fs.FileMode, verify func() error) error {
	dir := filepath.Dir(target)
	staged, err := os.CreateTemp(dir, ".relo-upgrade-*.exe")
	if err != nil {
		return err
	}
	stagedPath := staged.Name()
	defer func() {
		_ = staged.Close()
		_ = os.Remove(stagedPath)
	}()
	if _, err := staged.Write(data); err != nil {
		return err
	}
	if err := staged.Sync(); err != nil {
		return err
	}
	if err := staged.Close(); err != nil {
		return err
	}

	backupFile, err := os.CreateTemp(dir, ".relo-upgrade-backup-*.exe")
	if err != nil {
		return err
	}
	backup := backupFile.Name()
	if err := backupFile.Close(); err != nil {
		return err
	}
	if err := os.Remove(backup); err != nil {
		return err
	}
	if err := verify(); err != nil {
		return err
	}
	if err := os.Rename(target, backup); err != nil {
		return err
	}
	if err := os.Rename(stagedPath, target); err != nil {
		if rollbackErr := os.Rename(backup, target); rollbackErr != nil {
			return fmt.Errorf("install new executable: %w; rollback failed: %v; original executable remains at %s", err, rollbackErr, backup)
		}
		return fmt.Errorf("install new executable (original restored): %w", err)
	}
	// Windows may keep the old running executable locked. Remove it when the OS
	// permits; otherwise the uniquely named backup remains for manual cleanup.
	_ = os.Remove(backup)
	return nil
}
