//go:build windows

package once

import (
	"fmt"
	"runtime"

	"golang.org/x/sys/windows"
)

func restrictLocalFile(path string) error {
	token, err := windows.OpenCurrentProcessToken()
	if err != nil {
		return fmt.Errorf("open process token: %w", err)
	}
	defer token.Close()

	user, err := token.GetTokenUser()
	if err != nil {
		return fmt.Errorf("get token user: %w", err)
	}

	acl, err := privateFileACL(user.User.Sid)
	runtime.KeepAlive(user)
	if err != nil {
		return fmt.Errorf("build acl: %w", err)
	}

	err = windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil,
		nil,
		acl,
		nil,
	)
	if err != nil {
		return fmt.Errorf("set acl: %w", err)
	}
	return nil
}

func privateFileACL(sid *windows.SID) (*windows.ACL, error) {
	return privateACL(sid, windows.NO_INHERITANCE)
}

func privateDirectoryACL(sid *windows.SID) (*windows.ACL, error) {
	return privateACL(sid, windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT)
}

func privateACL(sid *windows.SID, inheritance uint32) (*windows.ACL, error) {
	var pinner runtime.Pinner
	pinner.Pin(sid)
	defer pinner.Unpin()

	return windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{
		{
			AccessPermissions: windows.GENERIC_ALL,
			AccessMode:        windows.GRANT_ACCESS,
			Inheritance:       inheritance,
			Trustee: windows.TRUSTEE{
				TrusteeForm:  windows.TRUSTEE_IS_SID,
				TrusteeType:  windows.TRUSTEE_IS_USER,
				TrusteeValue: windows.TrusteeValueFromSID(sid),
			},
		},
	}, nil)
}
