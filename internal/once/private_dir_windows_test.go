//go:build windows

package once

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"golang.org/x/sys/windows"
)

func TestMkdirAllPrivateCreatesProtectedDirectories(t *testing.T) {
	ancestor := newReadInheritingTestDirectory(t)

	first := filepath.Join(ancestor, "first")
	nested := filepath.Join(first, "nested")
	if err := MkdirAllPrivate(nested); err != nil {
		t.Fatal(err)
	}

	policy, err := newWindowsPathACLPolicy()
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{first, nested} {
		sd, err := windows.GetNamedSecurityInfo(
			dir,
			windows.SE_FILE_OBJECT,
			windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION,
		)
		if err != nil {
			t.Fatal(err)
		}
		if err := validateWindowsDirectorySecurity(dir, sd, true, policy); err != nil {
			t.Fatalf("created directory is not private: %v", err)
		}
		control, _, err := sd.Control()
		if err != nil {
			t.Fatal(err)
		}
		if control&windows.SE_DACL_PROTECTED == 0 {
			t.Fatalf("%s DACL is not protected", dir)
		}
		dacl, _, err := sd.DACL()
		if err != nil {
			t.Fatal(err)
		}
		if dacl.AceCount != 1 {
			t.Fatalf("%s ACE count = %d, want 1", dir, dacl.AceCount)
		}
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, 0, &ace); err != nil {
			t.Fatal(err)
		}
		parsed, relevant, err := parseWindowsAllowACE(ace)
		if err != nil {
			t.Fatal(err)
		}
		if !relevant {
			t.Fatalf("%s ACL entry is not an allow ACE", dir)
		}
		wantInheritance := uint8(windows.CONTAINER_INHERIT_ACE | windows.OBJECT_INHERIT_ACE)
		if parsed.flags&wantInheritance != wantInheritance {
			t.Fatalf("%s ACE flags = %#x, want container and object inheritance", dir, parsed.flags)
		}
	}

	if err := RejectSharedWritableParent(filepath.Join(nested, "once.db")); err != nil {
		t.Fatalf("RejectSharedWritableParent: %v", err)
	}
}

func TestMkdirAllPrivateDoesNotChangeExistingDirectory(t *testing.T) {
	ancestor := newReadInheritingTestDirectory(t)

	existing := filepath.Join(ancestor, "existing")
	if err := os.Mkdir(existing, 0o700); err != nil {
		t.Fatal(err)
	}
	storePath := filepath.Join(existing, "once.db")
	if err := RejectSharedWritableParent(storePath); err == nil {
		t.Fatal("test directory unexpectedly passed before MkdirAllPrivate")
	}

	if err := MkdirAllPrivate(existing); err != nil {
		t.Fatal(err)
	}
	if err := RejectSharedWritableParent(storePath); err == nil {
		t.Fatal("MkdirAllPrivate changed an existing directory ACL")
	}
}

func TestMkdirAllPrivateConcurrentCreation(t *testing.T) {
	ancestor := newReadInheritingTestDirectory(t)
	nested := filepath.Join(ancestor, "concurrent", "nested")

	const creators = 8
	start := make(chan struct{})
	results := make(chan error, creators)
	for range creators {
		go func() {
			<-start
			results <- MkdirAllPrivate(nested)
		}()
	}
	close(start)
	for range creators {
		if err := <-results; err != nil {
			t.Fatalf("MkdirAllPrivate: %v", err)
		}
	}

	if err := RejectSharedWritableParent(filepath.Join(nested, "once.db")); err != nil {
		t.Fatalf("RejectSharedWritableParent: %v", err)
	}
}

func TestOpenSQLiteCreatesPrivateDirectory(t *testing.T) {
	ancestor := newReadInheritingTestDirectory(t)

	storePath := filepath.Join(ancestor, "store", "once.db")
	store, err := OpenSQLite(storePath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	if err := RejectSharedWritableParent(storePath); err != nil {
		t.Fatalf("RejectSharedWritableParent: %v", err)
	}
	if _, fresh, err := store.Reserve("demo", []string{"true"}); err != nil {
		t.Fatal(err)
	} else if !fresh {
		t.Fatal("first reserve was not fresh")
	}
}

func TestOpenSQLiteRejectsUnsafeAncestorBeforeCreatingDirectory(t *testing.T) {
	trustee, sid := wellKnownGroupTrustee(t, windows.WinBuiltinUsersSid)
	pinner := pinSIDs(sid)
	defer pinner.Unpin()
	ancestor := newACLTestDirectory(t, windows.EXPLICIT_ACCESS{
		AccessPermissions: testFileDeleteChild,
		AccessMode:        windows.GRANT_ACCESS,
		Inheritance:       windows.NO_INHERITANCE,
		Trustee:           trustee,
	})
	defer runtime.KeepAlive(sid)

	missing := filepath.Join(ancestor, "missing")
	store, err := OpenSQLite(filepath.Join(missing, "once.db"))
	if err == nil {
		_ = store.Close()
		t.Fatal("OpenSQLite succeeded below an unsafe ancestor")
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatalf("missing store directory was created: %v", err)
	}
}

func newReadInheritingTestDirectory(t *testing.T) string {
	t.Helper()

	trustee, sid := wellKnownGroupTrustee(t, windows.WinBuiltinUsersSid)
	pinner := pinSIDs(sid)
	defer pinner.Unpin()
	ancestor := newACLTestDirectory(t, windows.EXPLICIT_ACCESS{
		AccessPermissions: windows.GENERIC_READ | windows.GENERIC_EXECUTE,
		AccessMode:        windows.GRANT_ACCESS,
		Inheritance:       windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT,
		Trustee:           trustee,
	})
	runtime.KeepAlive(sid)
	return ancestor
}
