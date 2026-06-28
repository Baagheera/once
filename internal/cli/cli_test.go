package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	once "github.com/Baagheera/once/internal/once"
)

func TestRunReplaysSavedResult(t *testing.T) {
	store := filepath.Join(t.TempDir(), "once.db")

	var out1, err1 bytes.Buffer
	code := Run([]string{"--store", store, "run", "--key", "demo", "--", shell(), shellFlag(), "echo first"}, &out1, &err1)
	if code != 0 {
		t.Fatalf("first run code = %d stderr = %s", code, err1.String())
	}
	if strings.TrimSpace(out1.String()) != "first" {
		t.Fatalf("first stdout = %q", out1.String())
	}

	var out2, err2 bytes.Buffer
	code = Run([]string{"--store", store, "run", "--key", "demo", "--", shell(), shellFlag(), "echo first"}, &out2, &err2)
	if code != 0 {
		t.Fatalf("second run code = %d stderr = %s", code, err2.String())
	}
	if strings.TrimSpace(out2.String()) != "first" {
		t.Fatalf("second stdout = %q", out2.String())
	}
}

func TestRunRejectsDifferentCommandForSameKey(t *testing.T) {
	store := filepath.Join(t.TempDir(), "once.db")

	var out1, err1 bytes.Buffer
	code := Run([]string{"--store", store, "run", "--key", "demo", "--", shell(), shellFlag(), "echo first"}, &out1, &err1)
	if code != 0 {
		t.Fatalf("first run code = %d stderr = %s", code, err1.String())
	}

	var out2, err2 bytes.Buffer
	code = Run([]string{"--store", store, "run", "--key", "demo", "--", shell(), shellFlag(), "echo second"}, &out2, &err2)
	if code == 0 {
		t.Fatalf("second run should fail")
	}
	if out2.Len() != 0 {
		t.Fatalf("stdout = %q", out2.String())
	}
	if !strings.Contains(err2.String(), "different command") {
		t.Fatalf("stderr = %q", err2.String())
	}
}

func TestStatus(t *testing.T) {
	store := filepath.Join(t.TempDir(), "once.db")

	var out, errOut bytes.Buffer
	code := Run([]string{"--store", store, "run", "--key", "demo", "--", shell(), shellFlag(), "echo ok"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("run code = %d stderr = %s", code, errOut.String())
	}

	out.Reset()
	errOut.Reset()
	code = Run([]string{"--store", store, "status", "demo"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("status code = %d stderr = %s", code, errOut.String())
	}
	if strings.TrimSpace(out.String()) != "succeeded" {
		t.Fatalf("status stdout = %q", out.String())
	}
}

func TestRunReplaysSavedFailure(t *testing.T) {
	store := filepath.Join(t.TempDir(), "once.db")

	var out1, err1 bytes.Buffer
	code := Run([]string{"--store", store, "run", "--key", "demo", "--", shell(), shellFlag(), failScript()}, &out1, &err1)
	if code != 9 {
		t.Fatalf("first run code = %d stderr = %s", code, err1.String())
	}

	var out2, err2 bytes.Buffer
	code = Run([]string{"--store", store, "run", "--key", "demo", "--", shell(), shellFlag(), failScript()}, &out2, &err2)
	if code != 9 {
		t.Fatalf("second run code = %d stderr = %s", code, err2.String())
	}
	if strings.TrimSpace(out2.String()) != "bad" {
		t.Fatalf("second stdout = %q", out2.String())
	}
}

func TestRunRejectsRunningKey(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "once.db")
	store, err := once.OpenSQLite(storePath)
	if err != nil {
		t.Fatal(err)
	}
	cmd := []string{shell(), shellFlag(), "echo no"}
	if _, _, err := store.Reserve("demo", cmd); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	code := Run(append([]string{"--store", storePath, "run", "--key", "demo", "--"}, cmd...), &out, &errOut)
	if code != 75 {
		t.Fatalf("run code = %d stderr = %s", code, errOut.String())
	}
	if out.Len() != 0 {
		t.Fatalf("stdout = %q", out.String())
	}
	if !strings.Contains(errOut.String(), "already running") {
		t.Fatalf("stderr = %q", errOut.String())
	}
}

func TestForgetRunningNeedsForce(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "once.db")
	store, err := once.OpenSQLite(storePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Reserve("demo", []string{"still", "running"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	code := Run([]string{"--store", storePath, "forget", "demo"}, &out, &errOut)
	if code == 0 {
		t.Fatal("forget should fail for running key")
	}
	if !strings.Contains(errOut.String(), "still running") {
		t.Fatalf("stderr = %q", errOut.String())
	}

	out.Reset()
	errOut.Reset()
	code = Run([]string{"--store", storePath, "forget", "--force", "demo"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("forget --force code = %d stderr = %s", code, errOut.String())
	}
}

func TestStoreWithoutCommandDoesNotPanic(t *testing.T) {
	var out, errOut bytes.Buffer
	code := Run([]string{"--store", filepath.Join(t.TempDir(), "once.db")}, &out, &errOut)
	if code != 2 {
		t.Fatalf("code = %d", code)
	}
}

func TestServeRejectsRemoteWithoutExplicitFlag(t *testing.T) {
	var out, errOut bytes.Buffer
	code := Run([]string{"serve", "--listen", "0.0.0.0:7410"}, &out, &errOut)
	if code != 2 {
		t.Fatalf("code = %d", code)
	}
	if !strings.Contains(errOut.String(), "requires --allow-remote") {
		t.Fatalf("stderr = %q", errOut.String())
	}
}

func TestServeRejectsUnsafeNoAuthOnRemote(t *testing.T) {
	var out, errOut bytes.Buffer
	code := Run([]string{"serve", "--listen", "0.0.0.0:7410", "--allow-remote", "--unsafe-no-auth"}, &out, &errOut)
	if code != 2 {
		t.Fatalf("code = %d", code)
	}
	if !strings.Contains(errOut.String(), "only allowed on loopback") {
		t.Fatalf("stderr = %q", errOut.String())
	}
}

func TestServeRejectsWeakToken(t *testing.T) {
	var out, errOut bytes.Buffer
	code := Run([]string{"--store", filepath.Join(t.TempDir(), "once.db"), "serve", "--token", "short"}, &out, &errOut)
	if code != 2 {
		t.Fatalf("code = %d", code)
	}
	if strings.Contains(out.String(), "short") {
		t.Fatalf("stdout leaked token: %q", out.String())
	}
	if !strings.Contains(errOut.String(), "at least 32 characters") {
		t.Fatalf("stderr = %q", errOut.String())
	}
}

func TestResolveAuthTokenCreatesTokenFile(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "once.db")

	token, tokenFile, err := resolveAuthToken("", "", false, storePath)
	if err != nil {
		t.Fatal(err)
	}
	if token == "" {
		t.Fatal("missing generated token")
	}
	if tokenFile != storePath+".token" {
		t.Fatalf("tokenFile = %q", tokenFile)
	}
	data, err := os.ReadFile(tokenFile)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(data)) != token {
		t.Fatal("token file does not contain generated token")
	}
}

func TestResolveAuthTokenRejectsTokenFileSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires privileges on some Windows installs")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	link := filepath.Join(dir, "token")
	if err := os.WriteFile(target, []byte(strings.Repeat("a", minAuthTokenLength)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, _, err := resolveAuthToken("", link, false, filepath.Join(dir, "once.db")); err == nil {
		t.Fatal("expected symlink token file to be rejected")
	}
}

func TestResolveAuthTokenRejectsSymlinkAncestor(t *testing.T) {
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

	tokenFile := filepath.Join(link, "sub", "once.token")
	if _, _, err := resolveAuthToken("", tokenFile, false, filepath.Join(dir, "once.db")); err == nil {
		t.Fatal("expected symlink ancestor to be rejected")
	}
}

func shell() string {
	if runtime.GOOS == "windows" {
		if comspec := os.Getenv("ComSpec"); comspec != "" {
			return comspec
		}
		return "cmd"
	}
	return "sh"
}

func shellFlag() string {
	if runtime.GOOS == "windows" {
		return "/C"
	}
	return "-c"
}

func failScript() string {
	if runtime.GOOS == "windows" {
		return "echo bad && exit /b 9"
	}
	return "echo bad; exit 9"
}
