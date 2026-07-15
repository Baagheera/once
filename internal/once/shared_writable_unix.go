//go:build !windows

package once

import (
	"fmt"
	"os"
	"path/filepath"
)

func rejectSharedWritableParent(path string) error {
	dir := filepath.Dir(filepath.Clean(path))
	if dir == "" {
		dir = "."
	}

	for _, name := range pathPrefixes(dir) {
		info, err := os.Stat(name)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return fmt.Errorf("%s is not a directory", name)
		}
		if sharedWritableParentIsUnsafe(info.Mode(), filepath.Clean(name) == dir) {
			return fmt.Errorf("%s permissions %04o allow group or other writes", name, info.Mode().Perm())
		}
	}
	return nil
}

func sharedWritableParentIsUnsafe(mode os.FileMode, immediate bool) bool {
	return mode.Perm()&0o022 != 0 && (immediate || mode&os.ModeSticky == 0)
}
