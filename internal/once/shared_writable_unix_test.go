//go:build !windows

package once

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSharedWritableParentModeSafety(t *testing.T) {
	tests := []struct {
		name      string
		mode      os.FileMode
		immediate bool
		want      bool
	}{
		{name: "private immediate parent", mode: 0o700, immediate: true, want: false},
		{name: "non-sticky writable ancestor", mode: 0o770, immediate: false, want: true},
		{name: "sticky writable immediate parent", mode: os.ModeSticky | 0o777, immediate: true, want: true},
		{name: "sticky writable ancestor", mode: os.ModeSticky | 0o777, immediate: false, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sharedWritableParentIsUnsafe(tt.mode, tt.immediate); got != tt.want {
				t.Fatalf("sharedWritableParentIsUnsafe(%v, %v) = %v, want %v", tt.mode, tt.immediate, got, tt.want)
			}
		})
	}
}

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

func TestRejectSharedWritableParentRejectsStickyWritableImmediateParent(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "shared")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, os.ModeSticky|0o777); err != nil {
		t.Fatal(err)
	}

	if err := RejectSharedWritableParent(filepath.Join(dir, "once.db")); err == nil {
		t.Fatal("expected sticky writable immediate parent to be rejected")
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

func TestRejectSharedWritableParentAllowsStickyWritableAncestorAbovePrivateParent(t *testing.T) {
	shared := filepath.Join(t.TempDir(), "shared")
	private := filepath.Join(shared, "private")
	if err := os.Mkdir(shared, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(private, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(shared, os.ModeSticky|0o777); err != nil {
		t.Fatal(err)
	}

	if err := RejectSharedWritableParent(filepath.Join(private, "once.db")); err != nil {
		t.Fatalf("RejectSharedWritableParent err = %v", err)
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
