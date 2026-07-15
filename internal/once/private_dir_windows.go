//go:build windows

package once

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

func mkdirAllPrivate(path string) error {
	info, err := os.Stat(path)
	if err == nil {
		if info.IsDir() {
			return nil
		}
		return &os.PathError{Op: "mkdir", Path: path, Err: windows.ERROR_DIRECTORY}
	}
	if !os.IsNotExist(err) {
		return err
	}

	prefixes := pathPrefixes(filepath.Clean(path))
	firstMissing := len(prefixes)
	for i, name := range prefixes {
		info, err := os.Stat(name)
		if err == nil {
			if !info.IsDir() {
				return &os.PathError{Op: "mkdir", Path: name, Err: windows.ERROR_DIRECTORY}
			}
			continue
		}
		if !os.IsNotExist(err) {
			return err
		}
		firstMissing = i
		break
	}
	if firstMissing == len(prefixes) {
		return nil
	}

	token, err := windows.OpenCurrentProcessToken()
	if err != nil {
		return fmt.Errorf("open process token: %w", err)
	}
	defer token.Close()

	user, err := token.GetTokenUser()
	if err != nil {
		return fmt.Errorf("get token user: %w", err)
	}
	acl, err := privateDirectoryACL(user.User.Sid)
	runtime.KeepAlive(user)
	if err != nil {
		return fmt.Errorf("build directory acl: %w", err)
	}

	absoluteSD, err := windows.NewSecurityDescriptor()
	if err != nil {
		return fmt.Errorf("create directory security descriptor: %w", err)
	}
	if err := absoluteSD.SetDACL(acl, true, false); err != nil {
		return fmt.Errorf("set directory dacl: %w", err)
	}
	if err := absoluteSD.SetControl(windows.SE_DACL_PROTECTED, windows.SE_DACL_PROTECTED); err != nil {
		return fmt.Errorf("protect directory dacl: %w", err)
	}
	securityDescriptor, err := absoluteSD.ToSelfRelative()
	if err != nil {
		return fmt.Errorf("make directory security descriptor self-relative: %w", err)
	}
	securityAttributes := &windows.SecurityAttributes{
		Length:             uint32(unsafe.Sizeof(windows.SecurityAttributes{})),
		SecurityDescriptor: securityDescriptor,
	}

	for _, name := range prefixes[firstMissing:] {
		namep, err := windows.UTF16PtrFromString(name)
		if err != nil {
			return &os.PathError{Op: "mkdir", Path: name, Err: err}
		}
		if err := windows.CreateDirectory(namep, securityAttributes); err != nil {
			if errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
				info, statErr := os.Stat(name)
				if statErr == nil && info.IsDir() {
					continue
				}
				if statErr != nil {
					return statErr
				}
				return &os.PathError{Op: "mkdir", Path: name, Err: windows.ERROR_DIRECTORY}
			}
			return &os.PathError{Op: "mkdir", Path: name, Err: err}
		}
	}

	runtime.KeepAlive(acl)
	runtime.KeepAlive(absoluteSD)
	runtime.KeepAlive(securityDescriptor)
	runtime.KeepAlive(securityAttributes)
	return nil
}
