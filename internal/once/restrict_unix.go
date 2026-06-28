//go:build !windows

package once

import "os"

func restrictLocalFile(path string) error {
	return os.Chmod(path, 0o600)
}
