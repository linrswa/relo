//go:build !windows

package upgrade

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

func replaceExecutable(target string, data []byte, mode fs.FileMode, verify func() error) error {
	if mode.Perm()&0111 == 0 {
		return fmt.Errorf("downloaded binary is not executable")
	}
	dir := filepath.Dir(target)
	staged, err := os.CreateTemp(dir, ".relo-upgrade-*")
	if err != nil {
		return err
	}
	stagedPath := staged.Name()
	keep := false
	defer func() {
		_ = staged.Close()
		if !keep {
			_ = os.Remove(stagedPath)
		}
	}()
	if _, err := staged.Write(data); err != nil {
		return err
	}
	if err := staged.Chmod(mode.Perm()); err != nil {
		return err
	}
	if err := staged.Sync(); err != nil {
		return err
	}
	if err := staged.Close(); err != nil {
		return err
	}
	if err := verify(); err != nil {
		return err
	}
	if err := os.Rename(stagedPath, target); err != nil {
		return err
	}
	keep = true
	if directory, err := os.Open(dir); err == nil {
		_ = directory.Sync()
		_ = directory.Close()
	}
	return nil
}
