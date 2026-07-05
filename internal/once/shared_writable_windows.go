//go:build windows

package once

func rejectSharedWritableParent(path string) error {
	return nil
}
