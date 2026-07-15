//go:build windows

package once

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"golang.org/x/sys/windows"
)

const testFileDeleteChild windows.ACCESS_MASK = 0x40
const testUntrustedSID = "S-1-5-21-424242-424242-424242-424242"

func TestMain(m *testing.M) {
	root, err := newWindowsTestTempRoot()
	if err != nil {
		fmt.Fprintf(os.Stderr, "prepare Windows test temp root: %v\n", err)
		os.Exit(1)
	}
	if err := os.Setenv("TEMP", root); err != nil {
		fmt.Fprintf(os.Stderr, "set TEMP: %v\n", err)
		_ = os.RemoveAll(root)
		os.Exit(1)
	}
	if err := os.Setenv("TMP", root); err != nil {
		fmt.Fprintf(os.Stderr, "set TMP: %v\n", err)
		_ = os.RemoveAll(root)
		os.Exit(1)
	}

	code := m.Run()
	if err := os.RemoveAll(root); err != nil && code == 0 {
		fmt.Fprintf(os.Stderr, "remove Windows test temp root: %v\n", err)
		code = 1
	}
	os.Exit(code)
}

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

func TestRejectSharedWritableParentRejectsDangerousCustomSID(t *testing.T) {
	sid, err := windows.StringToSid(testUntrustedSID)
	if err != nil {
		t.Fatal(err)
	}
	dir := newACLTestDirectory(t, windows.EXPLICIT_ACCESS{
		AccessPermissions: windows.FILE_WRITE_DATA,
		AccessMode:        windows.GRANT_ACCESS,
		Inheritance:       windows.NO_INHERITANCE,
		Trustee:           sidTrustee(sid, windows.TRUSTEE_IS_USER),
	})
	runtime.KeepAlive(sid)

	if err := RejectSharedWritableParent(filepath.Join(dir, "once.db")); err == nil {
		t.Fatal("expected dangerous rights for a custom SID to be rejected")
	}
}

func TestRejectSharedWritableParentRejectsUnsafeAncestorAbovePrivateParent(t *testing.T) {
	ancestor := filepath.Join(t.TempDir(), "unsafe")
	private := filepath.Join(ancestor, "private")
	if err := os.MkdirAll(private, 0o700); err != nil {
		t.Fatal(err)
	}

	trustee, sid := wellKnownGroupTrustee(t, windows.WinBuiltinUsersSid)
	setTestDirectoryDACL(t, ancestor, windows.EXPLICIT_ACCESS{
		AccessPermissions: testFileDeleteChild,
		AccessMode:        windows.GRANT_ACCESS,
		Inheritance:       windows.NO_INHERITANCE,
		Trustee:           trustee,
	})
	setTestDirectoryDACL(t, private)
	runtime.KeepAlive(sid)

	if err := RejectSharedWritableParent(filepath.Join(private, "once.db")); err == nil {
		t.Fatal("expected an unsafe ancestor above a private parent to be rejected")
	}
}

func TestRejectSharedWritableParentRejectsUnsafeAncestorBeforeMissingParent(t *testing.T) {
	ancestor := filepath.Join(t.TempDir(), "unsafe")
	if err := os.Mkdir(ancestor, 0o700); err != nil {
		t.Fatal(err)
	}

	trustee, sid := wellKnownGroupTrustee(t, windows.WinBuiltinUsersSid)
	setTestDirectoryDACL(t, ancestor, windows.EXPLICIT_ACCESS{
		AccessPermissions: testFileDeleteChild,
		AccessMode:        windows.GRANT_ACCESS,
		Inheritance:       windows.NO_INHERITANCE,
		Trustee:           trustee,
	})
	runtime.KeepAlive(sid)

	path := filepath.Join(ancestor, "missing", "once.db")
	if err := RejectSharedWritableParent(path); err == nil {
		t.Fatal("expected an unsafe existing ancestor before a missing parent to be rejected")
	}
}

func TestRejectSharedWritableParentAllowsCreateRightsOnAncestor(t *testing.T) {
	ancestor := filepath.Join(t.TempDir(), "create-allowed")
	private := filepath.Join(ancestor, "private")
	if err := os.MkdirAll(private, 0o700); err != nil {
		t.Fatal(err)
	}

	trustee, sid := wellKnownGroupTrustee(t, windows.WinBuiltinUsersSid)
	setTestDirectoryDACL(t, ancestor, windows.EXPLICIT_ACCESS{
		AccessPermissions: windows.FILE_APPEND_DATA,
		AccessMode:        windows.GRANT_ACCESS,
		Inheritance:       windows.NO_INHERITANCE,
		Trustee:           trustee,
	})
	setTestDirectoryDACL(t, private)
	runtime.KeepAlive(sid)

	if err := RejectSharedWritableParent(filepath.Join(private, "once.db")); err != nil {
		t.Fatalf("RejectSharedWritableParent err = %v", err)
	}
}

func TestRejectSharedWritableParentAllowsHarmlessReadExecuteRights(t *testing.T) {
	trustee, sid := wellKnownGroupTrustee(t, windows.WinBuiltinUsersSid)
	dir := newACLTestDirectory(t, windows.EXPLICIT_ACCESS{
		AccessPermissions: windows.GENERIC_READ | windows.GENERIC_EXECUTE,
		AccessMode:        windows.GRANT_ACCESS,
		Inheritance:       windows.NO_INHERITANCE,
		Trustee:           trustee,
	})
	runtime.KeepAlive(sid)

	if err := RejectSharedWritableParent(filepath.Join(dir, "once.db")); err != nil {
		t.Fatalf("RejectSharedWritableParent err = %v", err)
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

func TestValidateWindowsDirectorySecurityRejectsOwnerMismatch(t *testing.T) {
	policy, err := newWindowsPathACLPolicy()
	if err != nil {
		t.Fatal(err)
	}
	userSID, err := currentProcessUserSID()
	if err != nil {
		t.Fatal(err)
	}
	sd, err := windows.SecurityDescriptorFromString(
		"O:" + testUntrustedSID + "D:P(A;;FA;;;" + userSID.String() + ")",
	)
	if err != nil {
		t.Fatal(err)
	}
	runtime.KeepAlive(userSID)

	if err := validateWindowsDirectorySecurity("synthetic", sd, true, policy); err == nil {
		t.Fatal("expected an untrusted directory owner to be rejected")
	}
}

func TestValidateWindowsDirectorySecurityRejectsRelevantAllowACETypes(t *testing.T) {
	tests := []struct {
		name    string
		aceSDDL string
		aceType uint8
	}{
		{name: "standard", aceSDDL: "(A;;GW;;;" + testUntrustedSID + ")", aceType: 0x0},
		{name: "callback", aceSDDL: "(A;;GW;;;" + testUntrustedSID + ")", aceType: 0x9},
		{name: "object", aceSDDL: "(OA;;GW;;;" + testUntrustedSID + ")", aceType: 0x5},
		{name: "callback object", aceSDDL: "(OA;;GW;;;" + testUntrustedSID + ")", aceType: 0xb},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			policy, err := newWindowsPathACLPolicy()
			if err != nil {
				t.Fatal(err)
			}
			sd := testSecurityDescriptor(t, tt.aceSDDL)
			setFirstTestACEType(t, sd, tt.aceType)

			if err := validateWindowsDirectorySecurity("synthetic", sd, true, policy); err == nil {
				t.Fatalf("expected dangerous allow ACE type %#x to be rejected", tt.aceType)
			}
		})
	}
}

func TestValidateWindowsDirectorySecurityRejectsMalformedRelevantAllowACE(t *testing.T) {
	policy, err := newWindowsPathACLPolicy()
	if err != nil {
		t.Fatal(err)
	}
	sd := testSecurityDescriptor(t, "(A;;GW;;;"+testUntrustedSID+")")
	dacl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	var ace *windows.ACCESS_ALLOWED_ACE
	if err := windows.GetAce(dacl, 0, &ace); err != nil {
		t.Fatal(err)
	}
	ace.Header.AceSize = 8

	if err := validateWindowsDirectorySecurity("synthetic", sd, true, policy); err == nil {
		t.Fatal("expected malformed allow ACE to be rejected")
	}
}

func TestValidateWindowsDirectorySecurityAllowsSafeCreatorOwnerPlaceholder(t *testing.T) {
	policy, err := newWindowsPathACLPolicy()
	if err != nil {
		t.Fatal(err)
	}
	sd := testSecurityDescriptor(t, "(A;OICIIO;GA;;;CO)")

	if err := validateWindowsDirectorySecurity("synthetic", sd, true, policy); err != nil {
		t.Fatalf("validateWindowsDirectorySecurity err = %v", err)
	}
}

func TestValidateWindowsDirectorySecurityRejectsEffectiveCreatorOwnerPlaceholder(t *testing.T) {
	policy, err := newWindowsPathACLPolicy()
	if err != nil {
		t.Fatal(err)
	}
	sd := testSecurityDescriptor(t, "(A;;GR;;;CO)")

	if err := validateWindowsDirectorySecurity("synthetic", sd, true, policy); err == nil {
		t.Fatal("expected an effective Creator Owner placeholder to be rejected")
	}
}

func BenchmarkOpenSQLiteExistingStore(b *testing.B) {
	path := filepath.Join(b.TempDir(), "once.db")
	store, err := OpenSQLite(path)
	if err != nil {
		b.Fatal(err)
	}
	if err := store.Close(); err != nil {
		b.Fatal(err)
	}

	store, err = OpenSQLite(path)
	if err != nil {
		b.Fatal(err)
	}
	if err := store.Close(); err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		store, err := OpenSQLite(path)
		if err != nil {
			b.Fatal(err)
		}
		if err := store.Close(); err != nil {
			b.Fatal(err)
		}
	}
}

func newACLTestDirectory(t *testing.T, extra ...windows.EXPLICIT_ACCESS) string {
	t.Helper()

	dir := filepath.Join(t.TempDir(), "private")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}

	setTestDirectoryDACL(t, dir, extra...)
	return dir
}

func wellKnownGroupTrustee(t *testing.T, sidType windows.WELL_KNOWN_SID_TYPE) (windows.TRUSTEE, *windows.SID) {
	t.Helper()

	sid, err := windows.CreateWellKnownSid(sidType)
	if err != nil {
		t.Fatal(err)
	}
	return sidTrustee(sid, windows.TRUSTEE_IS_WELL_KNOWN_GROUP), sid
}

func newWindowsTestTempRoot() (string, error) {
	localAppData := os.Getenv("LOCALAPPDATA")
	if localAppData == "" {
		return "", fmt.Errorf("LOCALAPPDATA is empty")
	}
	root, err := os.MkdirTemp(localAppData, "once-tests-")
	if err != nil {
		return "", err
	}
	acl, sids, err := protectedTestDirectoryACL(nil)
	if err != nil {
		_ = os.RemoveAll(root)
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
		return "", err
	}
	runtime.KeepAlive(sids)
	return root, nil
}

func setTestDirectoryDACL(t *testing.T, dir string, extra ...windows.EXPLICIT_ACCESS) {
	t.Helper()
	acl, sids, err := protectedTestDirectoryACL(extra)
	if err != nil {
		t.Fatal(err)
	}
	setDirectoryDACL(t, dir, acl)
	runtime.KeepAlive(sids)
}

func protectedTestDirectoryACL(extra []windows.EXPLICIT_ACCESS) (*windows.ACL, []*windows.SID, error) {
	userSID, err := currentProcessUserSID()
	if err != nil {
		return nil, nil, err
	}
	systemSID, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return nil, nil, err
	}
	adminSID, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return nil, nil, err
	}
	sids := []*windows.SID{userSID, systemSID, adminSID}
	entries := make([]windows.EXPLICIT_ACCESS, 0, len(sids)+len(extra))
	for i, sid := range sids {
		trusteeType := windows.TRUSTEE_TYPE(windows.TRUSTEE_IS_WELL_KNOWN_GROUP)
		if i == 0 {
			trusteeType = windows.TRUSTEE_IS_USER
		}
		entries = append(entries, windows.EXPLICIT_ACCESS{
			AccessPermissions: windows.GENERIC_ALL,
			AccessMode:        windows.GRANT_ACCESS,
			Inheritance:       windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT,
			Trustee:           sidTrustee(sid, trusteeType),
		})
	}
	entries = append(entries, extra...)
	acl, err := windows.ACLFromEntries(entries, nil)
	runtime.KeepAlive(sids)
	if err != nil {
		return nil, nil, err
	}
	return acl, sids, nil
}

func currentProcessUserSID() (*windows.SID, error) {
	token, err := windows.OpenCurrentProcessToken()
	if err != nil {
		return nil, err
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil {
		return nil, err
	}
	sid, err := user.User.Sid.Copy()
	runtime.KeepAlive(user)
	return sid, err
}

func sidTrustee(sid *windows.SID, trusteeType windows.TRUSTEE_TYPE) windows.TRUSTEE {
	return windows.TRUSTEE{
		TrusteeForm:  windows.TRUSTEE_IS_SID,
		TrusteeType:  trusteeType,
		TrusteeValue: windows.TrusteeValueFromSID(sid),
	}
}

func testSecurityDescriptor(t *testing.T, aceSDDL string) *windows.SECURITY_DESCRIPTOR {
	t.Helper()
	userSID, err := currentProcessUserSID()
	if err != nil {
		t.Fatal(err)
	}
	sd, err := windows.SecurityDescriptorFromString("O:" + userSID.String() + "D:P" + aceSDDL)
	if err != nil {
		t.Fatal(err)
	}
	runtime.KeepAlive(userSID)
	return sd
}

func setFirstTestACEType(t *testing.T, sd *windows.SECURITY_DESCRIPTOR, aceType uint8) {
	t.Helper()
	dacl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	var ace *windows.ACCESS_ALLOWED_ACE
	if err := windows.GetAce(dacl, 0, &ace); err != nil {
		t.Fatal(err)
	}
	ace.Header.AceType = aceType
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
