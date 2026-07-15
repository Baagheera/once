//go:build windows

package once

import (
	"fmt"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

const windowsFileDeleteChild windows.ACCESS_MASK = 0x40

func rejectSharedWritableParent(path string) error {
	dir := filepath.Dir(filepath.Clean(path))
	if dir == "" {
		dir = "."
	}
	info, err := os.Stat(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("stat %s: %w", dir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", dir)
	}

	sd, err := windows.GetNamedSecurityInfo(
		dir,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION,
	)
	if err != nil {
		return fmt.Errorf("get %s security information: %w", dir, err)
	}
	if sd == nil {
		return fmt.Errorf("%s has no security descriptor", dir)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return fmt.Errorf("get %s DACL: %w", dir, err)
	}
	if dacl == nil {
		return fmt.Errorf("%s has a nil DACL", dir)
	}

	broadSIDs := make([]*windows.SID, 0, 3)
	for _, sidType := range []windows.WELL_KNOWN_SID_TYPE{
		windows.WinWorldSid,
		windows.WinAuthenticatedUserSid,
		windows.WinBuiltinUsersSid,
	} {
		sid, err := windows.CreateWellKnownSid(sidType)
		if err != nil {
			return fmt.Errorf("create well-known SID: %w", err)
		}
		broadSIDs = append(broadSIDs, sid)
	}

	const directoryRights = windows.GENERIC_ALL |
		windows.GENERIC_WRITE |
		windows.MAXIMUM_ALLOWED |
		windows.FILE_WRITE_DATA |
		windows.FILE_APPEND_DATA |
		windowsFileDeleteChild |
		windows.DELETE |
		windows.WRITE_DAC |
		windows.WRITE_OWNER
	const childFileRights = windows.GENERIC_ALL |
		windows.GENERIC_READ |
		windows.GENERIC_WRITE |
		windows.MAXIMUM_ALLOWED |
		windows.FILE_READ_DATA |
		windows.FILE_WRITE_DATA |
		windows.FILE_APPEND_DATA |
		windows.DELETE |
		windows.WRITE_DAC |
		windows.WRITE_OWNER

	for i := uint16(0); i < dacl.AceCount; i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, uint32(i), &ace); err != nil {
			return fmt.Errorf("get %s DACL ACE %d: %w", dir, i, err)
		}
		if ace == nil {
			return fmt.Errorf("get %s DACL ACE %d: nil ACE", dir, i)
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			continue
		}

		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		isBroad := false
		for _, broadSID := range broadSIDs {
			if sid.Equals(broadSID) {
				isBroad = true
				break
			}
		}
		if !isBroad {
			continue
		}

		flags := ace.Header.AceFlags
		if flags&windows.INHERIT_ONLY_ACE == 0 && ace.Mask&directoryRights != 0 {
			return fmt.Errorf("%s DACL grants broad directory write rights", dir)
		}
		if flags&windows.OBJECT_INHERIT_ACE != 0 && ace.Mask&childFileRights != 0 {
			return fmt.Errorf("%s DACL grants broad inheritable child-file rights", dir)
		}
	}

	return nil
}
