//go:build !windows

package once

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRejectSharedWritableParentRejectsGroupWritableParent(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "shared")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o770); err != nil {
		t.Fatal(err)
	}

	if err := RejectSharedWritableParent(filepath.Join(dir, "once.db")); err == nil {
		t.Fatal("expected group-writable parent to be rejected")
	}
}

func TestRejectSharedWritableParentRejectsGroupWritableAncestor(t *testing.T) {
	root := filepath.Join(t.TempDir(), "shared")
	private := filepath.Join(root, "private")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(private, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0o770); err != nil {
		t.Fatal(err)
	}

	if err := RejectSharedWritableParent(filepath.Join(private, "once.db")); err == nil {
		t.Fatal("expected group-writable ancestor to be rejected")
	}
}

func TestRejectSharedWritableParentAllowsPrivateParent(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "private")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}

	if err := RejectSharedWritableParent(filepath.Join(dir, "once.db")); err != nil {
		t.Fatalf("RejectSharedWritableParent err = %v", err)
	}
}
