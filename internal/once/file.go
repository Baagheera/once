package once

import (
	"fmt"
	"os"
	"path/filepath"
)

func RejectSymlinkPath(path string) error {
	clean := filepath.Clean(path)
	for _, name := range []string{clean, filepath.Dir(clean)} {
		info, err := os.Lstat(name)
		if err == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("refusing symlink path: %s", path)
			}
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func RestrictLocalFile(path string) error {
	return restrictLocalFile(path)
}
