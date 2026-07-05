package once

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

func RejectSymlinkPath(path string) error {
	clean := filepath.Clean(path)
	for _, name := range pathPrefixes(clean) {
		info, err := os.Lstat(name)
		if err == nil {
			if info.Mode()&os.ModeSymlink != 0 && !allowedSystemSymlinkAncestor(name) {
				return fmt.Errorf("refusing symlink path: %s", path)
			}
		} else if !os.IsNotExist(err) {
			return err
		} else {
			return nil
		}
	}
	return nil
}

func RestrictLocalFile(path string) error {
	return restrictLocalFile(path)
}

func RejectSharedWritableParent(path string) error {
	return rejectSharedWritableParent(path)
}

func pathPrefixes(path string) []string {
	clean := filepath.Clean(path)
	volume := filepath.VolumeName(clean)
	rest := strings.TrimPrefix(clean, volume)
	separator := string(os.PathSeparator)

	var current string
	var prefixes []string
	if strings.HasPrefix(rest, separator) {
		current = volume + separator
		rest = strings.TrimPrefix(rest, separator)
		prefixes = append(prefixes, current)
	} else if volume != "" {
		current = volume
		prefixes = append(prefixes, current)
	}

	for _, part := range strings.Split(rest, separator) {
		if part == "" || part == "." {
			continue
		}
		if current == "" {
			current = part
		} else if strings.HasSuffix(current, separator) {
			current += part
		} else {
			current = filepath.Join(current, part)
		}
		prefixes = append(prefixes, current)
	}
	if len(prefixes) == 0 {
		return []string{clean}
	}
	return prefixes
}

func allowedSystemSymlinkAncestor(path string) bool {
	if runtime.GOOS != "darwin" {
		return false
	}
	switch filepath.Clean(path) {
	case "/etc", "/tmp", "/var":
		return true
	default:
		return false
	}
}
