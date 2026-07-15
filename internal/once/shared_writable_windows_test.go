//go:build windows

package once

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"golang.org/x/sys/windows"
)

const testFileDeleteChild windows.ACCESS_MASK = 0x40

func TestRejectSharedWritableParentRejectsBroadDirectoryRights(t *testing.T) {
	tests := []struct {
		name    string
		sidType windows.WELL_KNOWN_SID_TYPE
		mask    windows.ACCESS_MASK
	}{
		{name: "everyone can add files", sidType: windows.WinWorldSid, mask: windows.FILE_WRITE_DATA},
		{name: "authenticated users can delete children", sidType: windows.WinAuthenticatedUserSid, mask: testFileDeleteChild},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			trustee, sid := wellKnownGroupTrustee(t, tt.sidType)
			dir := newACLTestDirectory(t, windows.EXPLICIT_ACCESS{
				AccessPermissions: tt.mask,
				AccessMode:        windows.GRANT_ACCESS,
				Inheritance:       windows.NO_INHERITANCE,
				Trustee:           trustee,
			})
			runtime.KeepAlive(sid)

			if err := RejectSharedWritableParent(filepath.Join(dir, "once.db")); err == nil {
				t.Fatal("expected broad directory rights to be rejected")
			}
		})
	}
}

func TestRejectSharedWritableParentRejectsBroadInheritedChildRights(t *testing.T) {
	trustee, sid := wellKnownGroupTrustee(t, windows.WinBuiltinUsersSid)
	dir := newACLTestDirectory(t, windows.EXPLICIT_ACCESS{
		AccessPermissions: windows.FILE_READ_DATA | windows.FILE_WRITE_DATA,
		AccessMode:        windows.GRANT_ACCESS,
		Inheritance:       windows.OBJECT_INHERIT_ACE | windows.INHERIT_ONLY_ACE,
		Trustee:           trustee,
	})
	runtime.KeepAlive(sid)

	if err := RejectSharedWritableParent(filepath.Join(dir, "once.db")); err == nil {
		t.Fatal("expected broad inheritable child-file rights to be rejected")
	}
}

func TestRejectSharedWritableParentAllowsPrivateDirectory(t *testing.T) {
	dir := t.TempDir()

	if err := RejectSharedWritableParent(filepath.Join(dir, "once.db")); err != nil {
		t.Fatalf("RejectSharedWritableParent err = %v", err)
	}
}

func TestRejectSharedWritableParentAllowsMissingImmediateParent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing", "once.db")

	if err := RejectSharedWritableParent(path); err != nil {
		t.Fatalf("RejectSharedWritableParent err = %v", err)
	}
}

func newACLTestDirectory(t *testing.T, extra ...windows.EXPLICIT_ACCESS) string {
	t.Helper()

	dir := filepath.Join(t.TempDir(), "private")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}

	token, err := windows.OpenCurrentProcessToken()
	if err != nil {
		t.Fatal(err)
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}

	ownerEntry := windows.EXPLICIT_ACCESS{
		AccessPermissions: windows.GENERIC_ALL,
		AccessMode:        windows.GRANT_ACCESS,
		Inheritance:       windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  windows.TRUSTEE_IS_USER,
			TrusteeValue: windows.TrusteeValueFromSID(user.User.Sid),
		},
	}
	privateACL, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{ownerEntry}, nil)
	if err != nil {
		t.Fatal(err)
	}
	entries := append([]windows.EXPLICIT_ACCESS{ownerEntry}, extra...)
	testACL, err := windows.ACLFromEntries(entries, nil)
	if err != nil {
		t.Fatal(err)
	}
	setDirectoryDACL(t, dir, testACL)
	runtime.KeepAlive(user)
	t.Cleanup(func() {
		if err := windows.SetNamedSecurityInfo(
			dir,
			windows.SE_FILE_OBJECT,
			windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
			nil,
			nil,
			privateACL,
			nil,
		); err != nil && !os.IsNotExist(err) {
			t.Errorf("restore private directory ACL: %v", err)
		}
	})
	return dir
}

func wellKnownGroupTrustee(t *testing.T, sidType windows.WELL_KNOWN_SID_TYPE) (windows.TRUSTEE, *windows.SID) {
	t.Helper()

	sid, err := windows.CreateWellKnownSid(sidType)
	if err != nil {
		t.Fatal(err)
	}
	return windows.TRUSTEE{
		TrusteeForm:  windows.TRUSTEE_IS_SID,
		TrusteeType:  windows.TRUSTEE_IS_WELL_KNOWN_GROUP,
		TrusteeValue: windows.TrusteeValueFromSID(sid),
	}, sid
}

func setDirectoryDACL(t *testing.T, dir string, acl *windows.ACL) {
	t.Helper()

	if err := windows.SetNamedSecurityInfo(
		dir,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil,
		nil,
		acl,
		nil,
	); err != nil {
		t.Fatal(err)
	}
}
