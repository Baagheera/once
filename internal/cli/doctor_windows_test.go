//go:build windows

package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	once "github.com/Baagheera/once/internal/once"
	"golang.org/x/sys/windows"
)

func TestDoctorRejectsBroadWindowsTokenACL(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "once.db")
	store, err := once.OpenSQLite(storePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	token := strings.Repeat("a", minAuthTokenLength)
	tokenPath := storePath + ".token"
	if err := os.WriteFile(tokenPath, []byte(token+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	grantWindowsEveryoneAccess(t, tokenPath, windows.GENERIC_READ)

	var out, errOut bytes.Buffer
	code := Run([]string{"--store", storePath, "doctor"}, &out, &errOut)
	if code == 0 {
		t.Fatalf("doctor should fail for broad token ACL:\n%s", out.String())
	}
	combined := out.String() + errOut.String()
	if strings.Contains(combined, token) {
		t.Fatalf("doctor leaked token material:\n%s", combined)
	}
	if !strings.Contains(out.String(), "token file: fail") {
		t.Fatalf("stdout = %q", out.String())
	}
	if !strings.Contains(out.String(), "Everyone") {
		t.Fatalf("stdout = %q", out.String())
	}
}

func TestDoctorRejectsBroadWindowsTokenDACLWrite(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "once.db")
	store, err := once.OpenSQLite(storePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	token := strings.Repeat("b", minAuthTokenLength)
	tokenPath := storePath + ".token"
	if err := os.WriteFile(tokenPath, []byte(token+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	grantWindowsEveryoneAccess(t, tokenPath, windows.WRITE_DAC)

	var out, errOut bytes.Buffer
	code := Run([]string{"--store", storePath, "doctor"}, &out, &errOut)
	if code == 0 {
		t.Fatalf("doctor should fail for broad token DACL write:\n%s", out.String())
	}
	combined := out.String() + errOut.String()
	if strings.Contains(combined, token) {
		t.Fatalf("doctor leaked token material:\n%s", combined)
	}
	if !strings.Contains(out.String(), "token file: fail") {
		t.Fatalf("stdout = %q", out.String())
	}
	if !strings.Contains(out.String(), "Everyone") {
		t.Fatalf("stdout = %q", out.String())
	}
}

func grantWindowsEveryoneAccess(t *testing.T, path string, access windows.ACCESS_MASK) {
	t.Helper()

	token, err := windows.OpenCurrentProcessToken()
	if err != nil {
		t.Fatal(err)
	}
	defer token.Close()

	user, err := token.GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	everyone, err := windows.CreateWellKnownSid(windows.WinWorldSid)
	if err != nil {
		t.Fatal(err)
	}
	var pinner runtime.Pinner
	pinner.Pin(user.User.Sid)
	pinner.Pin(everyone)
	defer pinner.Unpin()

	acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{
		{
			AccessPermissions: windows.GENERIC_ALL,
			AccessMode:        windows.GRANT_ACCESS,
			Inheritance:       windows.NO_INHERITANCE,
			Trustee: windows.TRUSTEE{
				TrusteeForm:  windows.TRUSTEE_IS_SID,
				TrusteeType:  windows.TRUSTEE_IS_USER,
				TrusteeValue: windows.TrusteeValueFromSID(user.User.Sid),
			},
		},
		{
			AccessPermissions: access,
			AccessMode:        windows.GRANT_ACCESS,
			Inheritance:       windows.NO_INHERITANCE,
			Trustee: windows.TRUSTEE{
				TrusteeForm:  windows.TRUSTEE_IS_SID,
				TrusteeType:  windows.TRUSTEE_IS_WELL_KNOWN_GROUP,
				TrusteeValue: windows.TrusteeValueFromSID(everyone),
			},
		},
	}, nil)
	if err != nil {
		t.Fatal(err)
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
		t.Fatal(err)
	}
}
