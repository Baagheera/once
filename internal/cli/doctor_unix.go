//go:build !windows

package cli

import (
	"fmt"
	"os"
)

func doctorFilePermissionProblem(path string, info os.FileInfo) string {
	if info.Mode().Perm()&0o077 == 0 {
		return ""
	}
	return fmt.Sprintf("%s permissions %04o are wider than 0600", path, info.Mode().Perm())
}
