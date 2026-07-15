package once

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

func TestPathPrefixesRelativeIncludesCurrentDirectory(t *testing.T) {
	want := []string{".", "nested", filepath.Join("nested", "private")}
	if got := pathPrefixes(filepath.Join("nested", "private")); !reflect.DeepEqual(got, want) {
		t.Fatalf("pathPrefixes() = %q, want %q", got, want)
	}
}

func TestRejectSymlinkPathRejectsNestedAncestor(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires privileges on some Windows installs")
	}

	dir := t.TempDir()
	real := filepath.Join(dir, "real")
	if err := os.MkdirAll(filepath.Join(real, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}

	if err := RejectSymlinkPath(filepath.Join(link, "sub", "once.db")); err == nil {
		t.Fatal("expected nested symlink ancestor to be rejected")
	}
}
