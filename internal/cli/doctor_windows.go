//go:build windows

package cli

import (
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

func doctorFilePermissionProblem(path string, _ os.FileInfo) string {
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return fmt.Sprintf("%s ACLs could not be verified: %v", path, err)
	}
	if sd == nil {
		return fmt.Sprintf("%s has no security descriptor", path)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return fmt.Sprintf("%s DACL could not be verified: %v", path, err)
	}
	if dacl == nil {
		return fmt.Sprintf("%s has a null DACL", path)
	}

	name, err := aclBroadFileAccess(dacl)
	if err != nil {
		return fmt.Sprintf("%s ACLs could not be verified: %v", path, err)
	}
	if name != "" {
		return fmt.Sprintf("%s grants broad file access to %s", path, name)
	}
	return ""
}

func doctorDirectoryPermissionProblem(_ string, _ os.FileInfo) string {
	return ""
}

func aclBroadFileAccess(dacl *windows.ACL) (string, error) {
	broadSIDs, err := broadWindowsSIDs()
	if err != nil {
		return "", err
	}
	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			return "", err
		}
		if ace == nil || ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			continue
		}
		if ace.Header.AceFlags&windows.INHERIT_ONLY_ACE != 0 {
			continue
		}
		if !allowsSensitiveFileAccess(ace.Mask) {
			continue
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		for _, broad := range broadSIDs {
			if windows.EqualSid(sid, broad.sid) {
				return broad.name, nil
			}
		}
	}
	return "", nil
}

func allowsSensitiveFileAccess(mask windows.ACCESS_MASK) bool {
	const access = windows.GENERIC_ALL |
		windows.GENERIC_READ |
		windows.GENERIC_WRITE |
		windows.MAXIMUM_ALLOWED |
		windows.FILE_GENERIC_READ |
		windows.FILE_GENERIC_WRITE |
		windows.FILE_READ_DATA |
		windows.FILE_WRITE_DATA |
		windows.FILE_APPEND_DATA |
		windows.WRITE_DAC |
		windows.WRITE_OWNER
	return mask&access != 0
}

type namedSID struct {
	name string
	sid  *windows.SID
}

func broadWindowsSIDs() ([]namedSID, error) {
	specs := []struct {
		name string
		typ  windows.WELL_KNOWN_SID_TYPE
	}{
		{name: "Everyone", typ: windows.WinWorldSid},
		{name: "Authenticated Users", typ: windows.WinAuthenticatedUserSid},
		{name: "Users", typ: windows.WinBuiltinUsersSid},
	}

	sids := make([]namedSID, 0, len(specs))
	for _, spec := range specs {
		sid, err := windows.CreateWellKnownSid(spec.typ)
		if err != nil {
			return nil, err
		}
		sids = append(sids, namedSID{name: spec.name, sid: sid})
	}
	return sids, nil
}
