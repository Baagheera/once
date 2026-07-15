//go:build windows

package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	once "github.com/Baagheera/once/internal/once"
)

func TestRunCreatesPrivateStoreDirectory(t *testing.T) {
	t.Setenv("ONCE_TEST_HELPER", "1")

	root := missingLocalAppDataChild(t)
	storePath := filepath.Join(root, "once.db")
	command := helperCommand("stdout", "6")

	var stdout, stderr bytes.Buffer
	code := Run(
		append([]string{"--store", storePath, "run", "--key", "demo", "--"}, command...),
		&stdout,
		&stderr,
	)
	if code != 0 {
		t.Fatalf("Run code = %d, stderr = %q", code, stderr.String())
	}
	if stdout.String() != "xxxxxx" {
		t.Fatalf("stdout = %q, want %q", stdout.String(), "xxxxxx")
	}
	if info, err := os.Stat(storePath); err != nil {
		t.Fatalf("stat store: %v", err)
	} else if !info.Mode().IsRegular() {
		t.Fatalf("store mode = %v, want regular file", info.Mode())
	}
	if err := once.RejectSharedWritableParent(storePath); err != nil {
		t.Fatalf("RejectSharedWritableParent: %v", err)
	}
}

func TestResolveAuthTokenCreatesPrivateNestedDirectory(t *testing.T) {
	root := missingLocalAppDataChild(t)
	tokenPath := filepath.Join(root, "nested", "once.token")

	token, resolvedPath, err := resolveAuthToken("", tokenPath, false, filepath.Join(root, "once.db"))
	if err != nil {
		t.Fatal(err)
	}
	if token == "" {
		t.Fatal("missing generated token")
	}
	if resolvedPath != tokenPath {
		t.Fatalf("resolved token path = %q, want %q", resolvedPath, tokenPath)
	}
	if info, err := os.Stat(tokenPath); err != nil {
		t.Fatalf("stat token: %v", err)
	} else if !info.Mode().IsRegular() {
		t.Fatalf("token mode = %v, want regular file", info.Mode())
	}
	if err := once.RejectSharedWritableParent(tokenPath); err != nil {
		t.Fatalf("RejectSharedWritableParent: %v", err)
	}
}

func missingLocalAppDataChild(t *testing.T) string {
	t.Helper()

	localAppData := os.Getenv("LOCALAPPDATA")
	if localAppData == "" {
		t.Fatal("LOCALAPPDATA is empty")
	}
	token, err := once.NewAttemptToken()
	if err != nil {
		t.Fatalf("generate test path token: %v", err)
	}
	root := filepath.Join(localAppData, "once-cli-test-"+token)
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("test path is not initially missing: %s (stat error: %v)", root, err)
	}
	t.Cleanup(func() {
		rel, err := filepath.Rel(localAppData, root)
		if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			t.Errorf("refusing to remove test path outside LOCALAPPDATA: %s", root)
			return
		}
		if err := os.RemoveAll(root); err != nil {
			t.Errorf("remove test path %s: %v", root, err)
		}
	})
	return root
}
