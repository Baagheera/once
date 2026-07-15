//go:build windows

package once

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	windowsFileDeleteChild windows.ACCESS_MASK = 0x40

	windowsAccessAllowedObjectACEType         uint8 = 0x5
	windowsAccessAllowedCallbackACEType       uint8 = 0x9
	windowsAccessAllowedCallbackObjectACEType uint8 = 0xb

	windowsTrustedInstallerSID = "S-1-5-80-956008885-3418522649-1831038044-1853292631-2271478464"
)

const windowsImmediateDirectoryDangerousRights = windows.GENERIC_ALL |
	windows.GENERIC_WRITE |
	windows.MAXIMUM_ALLOWED |
	windows.FILE_WRITE_DATA |
	windows.FILE_APPEND_DATA |
	windowsFileDeleteChild |
	windows.DELETE |
	windows.WRITE_DAC |
	windows.WRITE_OWNER

const windowsAncestorDangerousRights = windows.GENERIC_ALL |
	windows.MAXIMUM_ALLOWED |
	windowsFileDeleteChild |
	windows.DELETE |
	windows.WRITE_DAC |
	windows.WRITE_OWNER

const windowsInheritedFileDangerousRights = windows.GENERIC_ALL |
	windows.GENERIC_READ |
	windows.GENERIC_WRITE |
	windows.MAXIMUM_ALLOWED |
	windows.FILE_READ_DATA |
	windows.FILE_WRITE_DATA |
	windows.FILE_APPEND_DATA |
	windows.DELETE |
	windows.WRITE_DAC |
	windows.WRITE_OWNER

type windowsPathACLPolicy struct {
	trusted      []*windows.SID
	placeholders []*windows.SID
}

type windowsParsedAllowACE struct {
	mask  windows.ACCESS_MASK
	flags uint8
	sid   *windows.SID
}

func rejectSharedWritableParent(path string) error {
	dir := filepath.Dir(filepath.Clean(path))
	if dir == "" {
		dir = "."
	}
	policy, err := newWindowsPathACLPolicy()
	if err != nil {
		return err
	}

	for _, name := range pathPrefixes(dir) {
		info, err := os.Stat(name)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("stat %s: %w", name, err)
		}
		if !info.IsDir() {
			return fmt.Errorf("%s is not a directory", name)
		}

		sd, err := windows.GetNamedSecurityInfo(
			name,
			windows.SE_FILE_OBJECT,
			windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION,
		)
		if err != nil {
			return fmt.Errorf("get %s security information: %w", name, err)
		}
		if err := validateWindowsDirectorySecurity(
			name,
			sd,
			filepath.Clean(name) == dir,
			policy,
		); err != nil {
			return err
		}
	}
	return nil
}

func newWindowsPathACLPolicy() (*windowsPathACLPolicy, error) {
	token, err := windows.OpenCurrentProcessToken()
	if err != nil {
		return nil, fmt.Errorf("open process token: %w", err)
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil {
		return nil, fmt.Errorf("get token user: %w", err)
	}
	userSID, err := user.User.Sid.Copy()
	runtime.KeepAlive(user)
	if err != nil {
		return nil, fmt.Errorf("copy token user SID: %w", err)
	}

	localSystemSID, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return nil, fmt.Errorf("create LocalSystem SID: %w", err)
	}
	administratorsSID, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return nil, fmt.Errorf("create Administrators SID: %w", err)
	}
	trustedInstallerSID, err := windows.StringToSid(windowsTrustedInstallerSID)
	if err != nil {
		return nil, fmt.Errorf("create TrustedInstaller SID: %w", err)
	}
	creatorOwnerSID, err := windows.CreateWellKnownSid(windows.WinCreatorOwnerSid)
	if err != nil {
		return nil, fmt.Errorf("create Creator Owner SID: %w", err)
	}
	ownerRightsSID, err := windows.CreateWellKnownSid(windows.WinCreatorOwnerRightsSid)
	if err != nil {
		return nil, fmt.Errorf("create Owner Rights SID: %w", err)
	}

	return &windowsPathACLPolicy{
		trusted: []*windows.SID{
			userSID,
			localSystemSID,
			administratorsSID,
			trustedInstallerSID,
		},
		placeholders: []*windows.SID{creatorOwnerSID, ownerRightsSID},
	}, nil
}

func validateWindowsDirectorySecurity(
	name string,
	sd *windows.SECURITY_DESCRIPTOR,
	immediate bool,
	policy *windowsPathACLPolicy,
) error {
	if sd == nil {
		return fmt.Errorf("%s has no security descriptor", name)
	}
	owner, _, err := sd.Owner()
	if err != nil {
		return fmt.Errorf("get %s owner: %w", name, err)
	}
	if owner == nil || !owner.IsValid() {
		return fmt.Errorf("%s has no valid owner", name)
	}
	if !policy.contains(policy.trusted, owner) {
		return fmt.Errorf("%s owner is not trusted", name)
	}

	dacl, _, err := sd.DACL()
	if err != nil {
		return fmt.Errorf("get %s DACL: %w", name, err)
	}
	if dacl == nil {
		return fmt.Errorf("%s has a nil DACL", name)
	}

	for i := uint16(0); i < dacl.AceCount; i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, uint32(i), &ace); err != nil {
			return fmt.Errorf("get %s DACL ACE %d: %w", name, i, err)
		}
		parsed, relevant, err := parseWindowsAllowACE(ace)
		if err != nil {
			return fmt.Errorf("validate %s DACL ACE %d: %w", name, i, err)
		}
		if !relevant {
			continue
		}

		if policy.contains(policy.placeholders, parsed.sid) {
			inheritFlags := uint8(windows.OBJECT_INHERIT_ACE | windows.CONTAINER_INHERIT_ACE)
			if parsed.flags&windows.INHERIT_ONLY_ACE == 0 || parsed.flags&inheritFlags == 0 {
				return fmt.Errorf("%s DACL has an effective owner placeholder ACE", name)
			}
			continue
		}
		if policy.contains(policy.trusted, parsed.sid) {
			continue
		}

		if parsed.flags&windows.INHERIT_ONLY_ACE == 0 {
			dangerous := windowsAncestorDangerousRights
			if immediate {
				dangerous = windowsImmediateDirectoryDangerousRights
			}
			if parsed.mask&dangerous != 0 {
				return fmt.Errorf("%s DACL grants untrusted directory modification rights", name)
			}
		}
		if immediate &&
			parsed.flags&windows.OBJECT_INHERIT_ACE != 0 &&
			parsed.mask&windowsInheritedFileDangerousRights != 0 {
			return fmt.Errorf("%s DACL grants untrusted inheritable file rights", name)
		}
	}

	return nil
}

func (p *windowsPathACLPolicy) contains(set []*windows.SID, sid *windows.SID) bool {
	for _, candidate := range set {
		if sid.Equals(candidate) {
			return true
		}
	}
	return false
}

func parseWindowsAllowACE(ace *windows.ACCESS_ALLOWED_ACE) (windowsParsedAllowACE, bool, error) {
	if ace == nil {
		return windowsParsedAllowACE{}, false, fmt.Errorf("nil ACE")
	}
	header := ace.Header
	callback := false
	sidOffset := 0
	switch header.AceType {
	case windows.ACCESS_ALLOWED_ACE_TYPE:
		sidOffset = 8
	case windowsAccessAllowedCallbackACEType:
		callback = true
		sidOffset = 8
	case windowsAccessAllowedObjectACEType, windowsAccessAllowedCallbackObjectACEType:
		callback = header.AceType == windowsAccessAllowedCallbackObjectACEType
		if header.AceSize < 12 {
			return windowsParsedAllowACE{}, true, fmt.Errorf("object allow ACE is too short")
		}
		raw := unsafe.Slice((*byte)(unsafe.Pointer(ace)), int(header.AceSize))
		objectFlags := binary.LittleEndian.Uint32(raw[8:12])
		if objectFlags&^(uint32(windows.ACE_OBJECT_TYPE_PRESENT)|uint32(windows.ACE_INHERITED_OBJECT_TYPE_PRESENT)) != 0 {
			return windowsParsedAllowACE{}, true, fmt.Errorf("object allow ACE has unknown flags")
		}
		sidOffset = 12
		if objectFlags&uint32(windows.ACE_OBJECT_TYPE_PRESENT) != 0 {
			sidOffset += 16
		}
		if objectFlags&uint32(windows.ACE_INHERITED_OBJECT_TYPE_PRESENT) != 0 {
			sidOffset += 16
		}
	default:
		return windowsParsedAllowACE{}, false, nil
	}

	if int(header.AceSize) < sidOffset+8 {
		return windowsParsedAllowACE{}, true, fmt.Errorf("allow ACE is too short for a SID")
	}
	raw := unsafe.Slice((*byte)(unsafe.Pointer(ace)), int(header.AceSize))
	sidBytes := raw[sidOffset:]
	sidLength := 8 + 4*int(sidBytes[1])
	if sidLength > len(sidBytes) {
		return windowsParsedAllowACE{}, true, fmt.Errorf("allow ACE SID exceeds ACE size")
	}
	if !callback && sidOffset+sidLength != len(raw) {
		return windowsParsedAllowACE{}, true, fmt.Errorf("allow ACE has an invalid size")
	}
	sid := (*windows.SID)(unsafe.Pointer(&sidBytes[0]))
	if !sid.IsValid() {
		return windowsParsedAllowACE{}, true, fmt.Errorf("allow ACE has an invalid SID")
	}

	parsed := windowsParsedAllowACE{
		mask:  windows.ACCESS_MASK(binary.LittleEndian.Uint32(raw[4:8])),
		flags: header.AceFlags,
		sid:   sid,
	}
	runtime.KeepAlive(ace)
	return parsed, true, nil
}
