//go:build !windows

package once

import "os"

func mkdirAllPrivate(path string) error {
	return os.MkdirAll(path, 0o700)
}
