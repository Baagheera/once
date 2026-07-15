//go:build windows

package once

import (
	"testing"

	"golang.org/x/sys/windows"
)

func TestPrivateFileACLGrantsOnlyTheRequestedSID(t *testing.T) {
	sid, err := windows.CreateWellKnownSid(windows.WinWorldSid)
	if err != nil {
		t.Fatal(err)
	}

	acl, err := privateFileACL(sid)
	if err != nil {
		t.Fatal(err)
	}
	if acl.AceCount != 1 {
		t.Fatalf("ACE count = %d, want 1", acl.AceCount)
	}

	var ace *windows.ACCESS_ALLOWED_ACE
	if err := windows.GetAce(acl, 0, &ace); err != nil {
		t.Fatal(err)
	}
	parsed, relevant, err := parseWindowsAllowACE(ace)
	if err != nil {
		t.Fatal(err)
	}
	if !relevant {
		t.Fatal("ACL entry is not an allow ACE")
	}
	if parsed.mask != windows.GENERIC_ALL {
		t.Fatalf("access mask = %#x, want %#x", parsed.mask, windows.GENERIC_ALL)
	}
	if !parsed.sid.Equals(sid) {
		t.Fatal("ACL entry references a different SID")
	}
}
