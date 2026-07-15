//go:build windows

package testutil

import (
	"fmt"
	"os"
	"runtime"

	"golang.org/x/sys/windows"
)

// RunWithPrivateTemp gives a Windows test process a temporary directory whose
// children are accessible only to the current user.
func RunWithPrivateTemp(run func() int) int {
	root, err := newPrivateTempRoot()
	if err != nil {
		fmt.Fprintf(os.Stderr, "prepare Windows test temp root: %v\n", err)
		return 1
	}
	if err := os.Setenv("TEMP", root); err != nil {
		fmt.Fprintf(os.Stderr, "set TEMP: %v\n", err)
		_ = os.RemoveAll(root)
		return 1
	}
	if err := os.Setenv("TMP", root); err != nil {
		fmt.Fprintf(os.Stderr, "set TMP: %v\n", err)
		_ = os.RemoveAll(root)
		return 1
	}

	code := run()
	if err := os.RemoveAll(root); err != nil && code == 0 {
		fmt.Fprintf(os.Stderr, "remove Windows test temp root: %v\n", err)
		return 1
	}
	return code
}

func newPrivateTempRoot() (string, error) {
	localAppData := os.Getenv("LOCALAPPDATA")
	if localAppData == "" {
		return "", fmt.Errorf("LOCALAPPDATA is empty")
	}

	token, err := windows.OpenCurrentProcessToken()
	if err != nil {
		return "", fmt.Errorf("open process token: %w", err)
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil {
		return "", fmt.Errorf("get token user: %w", err)
	}
	var pinner runtime.Pinner
	pinner.Pin(user.User.Sid)
	defer pinner.Unpin()

	acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{
		{
			AccessPermissions: windows.GENERIC_ALL,
			AccessMode:        windows.GRANT_ACCESS,
			Inheritance:       windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT,
			Trustee: windows.TRUSTEE{
				TrusteeForm:  windows.TRUSTEE_IS_SID,
				TrusteeType:  windows.TRUSTEE_IS_USER,
				TrusteeValue: windows.TrusteeValueFromSID(user.User.Sid),
			},
		},
	}, nil)
	if err != nil {
		return "", fmt.Errorf("build test temp ACL: %w", err)
	}

	root, err := os.MkdirTemp(localAppData, "once-tests-")
	if err != nil {
		return "", err
	}
	if err := windows.SetNamedSecurityInfo(
		root,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil,
		nil,
		acl,
		nil,
	); err != nil {
		_ = os.RemoveAll(root)
		return "", fmt.Errorf("protect test temp root: %w", err)
	}
	runtime.KeepAlive(user)
	return root, nil
}
