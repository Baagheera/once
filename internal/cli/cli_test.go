package cli

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
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
	t.Setenv("ONCE_TEST_HELPER", "1")

	dir := t.TempDir()
	storePath := filepath.Join(dir, "once.db")
	marker := filepath.Join(dir, "side-effect")
	store, err := once.OpenSQLite(storePath)
	if err != nil {
		t.Fatal(err)
	}
	cmd := helperCommand("append", marker, "ran\n")
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
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("command ran for running key; marker stat err = %v", err)
	}
}

func TestRunStoresStartError(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "once.db")
	missing := filepath.Join(t.TempDir(), "missing-command")

	var out, errOut bytes.Buffer
	code := Run([]string{"--store", storePath, "run", "--key", "demo", "--", missing}, &out, &errOut)
	if code != 127 {
		t.Fatalf("run code = %d stderr = %s", code, errOut.String())
	}
	if out.Len() != 0 {
		t.Fatalf("stdout = %q", out.String())
	}

	store, err := once.OpenSQLite(storePath)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := store.Get("demo")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if rec.State != once.Failed {
		t.Fatalf("state = %s, want %s", rec.State, once.Failed)
	}
	if rec.ExitCode != 127 {
		t.Fatalf("exit code = %d, want 127", rec.ExitCode)
	}
	if rec.Error == "" {
		t.Fatal("missing stored start error")
	}

	out.Reset()
	errOut.Reset()
	code = Run([]string{"--store", storePath, "run", "--key", "demo", "--", missing}, &out, &errOut)
	if code != 127 {
		t.Fatalf("replay code = %d stderr = %s", code, errOut.String())
	}
}

func TestRunStoresKilledChildAsFailure(t *testing.T) {
	t.Setenv("ONCE_TEST_HELPER", "1")

	storePath := filepath.Join(t.TempDir(), "once.db")
	cmd := helperCommand("kill")

	var out, errOut bytes.Buffer
	code := Run(append([]string{"--store", storePath, "run", "--key", "demo", "--"}, cmd...), &out, &errOut)
	if code == 0 {
		t.Fatal("killed child should fail")
	}

	store, err := once.OpenSQLite(storePath)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := store.Get("demo")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if rec.State != once.Failed {
		t.Fatalf("state = %s, want %s", rec.State, once.Failed)
	}
	if rec.FinishedAt == nil {
		t.Fatal("missing finished_at")
	}

	out.Reset()
	errOut.Reset()
	replayCode := Run(append([]string{"--store", storePath, "run", "--key", "demo", "--"}, cmd...), &out, &errOut)
	if replayCode != code {
		t.Fatalf("replay code = %d, want %d", replayCode, code)
	}
}

func TestRunStoresAndReplaysLargeStdout(t *testing.T) {
	t.Setenv("ONCE_TEST_HELPER", "1")

	storePath := filepath.Join(t.TempDir(), "once.db")
	size := 1<<20 + 123
	cmd := helperCommand("stdout", strconv.Itoa(size))

	var out1, err1 bytes.Buffer
	code := Run(append([]string{"--store", storePath, "run", "--key", "demo", "--"}, cmd...), &out1, &err1)
	if code != 0 {
		t.Fatalf("first run code = %d stderr = %s", code, err1.String())
	}
	if out1.Len() != size {
		t.Fatalf("first stdout len = %d, want %d", out1.Len(), size)
	}

	var out2, err2 bytes.Buffer
	code = Run(append([]string{"--store", storePath, "run", "--key", "demo", "--"}, cmd...), &out2, &err2)
	if code != 0 {
		t.Fatalf("second run code = %d stderr = %s", code, err2.String())
	}
	if !bytes.Equal(out1.Bytes(), out2.Bytes()) {
		t.Fatal("replayed stdout differs from stored stdout")
	}
}

func TestListShowsRecords(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "once.db")

	var out, errOut bytes.Buffer
	code := Run([]string{"--store", storePath, "run", "--key", "done", "--", shell(), shellFlag(), "echo ok"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("run code = %d stderr = %s", code, errOut.String())
	}
	out.Reset()
	errOut.Reset()
	code = Run([]string{"--store", storePath, "run", "--key", "bad", "--", shell(), shellFlag(), failScript()}, &out, &errOut)
	if code != 9 {
		t.Fatalf("fail run code = %d stderr = %s", code, errOut.String())
	}

	store, err := once.OpenSQLite(storePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Reserve("stuck", []string{"send", "email"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	errOut.Reset()
	code = Run([]string{"--store", storePath, "list"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("list code = %d stderr = %s", code, errOut.String())
	}
	listed := out.String()
	for _, want := range []string{"KEY", "STATE", "EXIT", "UPDATED", "COMMAND", "done", "succeeded", "bad", "failed", "stuck", "running"} {
		if !strings.Contains(listed, want) {
			t.Fatalf("list output missing %q:\n%s", want, listed)
		}
	}

	out.Reset()
	errOut.Reset()
	code = Run([]string{"--store", storePath, "list", "--state", "running", "--limit", "1"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("filtered list code = %d stderr = %s", code, errOut.String())
	}
	filtered := out.String()
	if !strings.Contains(filtered, "stuck") || strings.Contains(filtered, "done") || strings.Contains(filtered, "bad") {
		t.Fatalf("filtered list output = %q", filtered)
	}
}

func TestExportJSONLRedactsOutputByDefault(t *testing.T) {
	t.Setenv("ONCE_TEST_HELPER", "1")

	storePath := filepath.Join(t.TempDir(), "once.db")
	cmd := helperCommand("stdout", "6")

	var out, errOut bytes.Buffer
	code := Run(append([]string{"--store", storePath, "run", "--key", "demo", "--"}, cmd...), &out, &errOut)
	if code != 0 {
		t.Fatalf("run code = %d stderr = %s", code, errOut.String())
	}

	out.Reset()
	errOut.Reset()
	code = Run([]string{"--store", storePath, "export"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("export code = %d stderr = %s", code, errOut.String())
	}
	docs := decodeExport(t, out.String())
	if len(docs) != 1 {
		t.Fatalf("exported records = %d, want 1", len(docs))
	}
	if docs[0]["key"] != "demo" || docs[0]["state"] != "succeeded" {
		t.Fatalf("export doc = %#v", docs[0])
	}
	if _, ok := docs[0]["stdout_b64"]; ok {
		t.Fatalf("stdout_b64 should be omitted by default: %#v", docs[0])
	}
	if _, ok := docs[0]["stderr_b64"]; ok {
		t.Fatalf("stderr_b64 should be omitted by default: %#v", docs[0])
	}

	out.Reset()
	errOut.Reset()
	code = Run([]string{"--store", storePath, "export", "--include-output"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("export --include-output code = %d stderr = %s", code, errOut.String())
	}
	docs = decodeExport(t, out.String())
	if docs[0]["stdout_b64"] != base64.StdEncoding.EncodeToString([]byte("xxxxxx")) {
		t.Fatalf("stdout_b64 = %#v", docs[0]["stdout_b64"])
	}
	if value, ok := docs[0]["stderr_b64"]; !ok || value != "" {
		t.Fatalf("stderr_b64 = %#v, present=%v", value, ok)
	}
}

func TestListAndExportRejectInvalidFilters(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "once.db")

	var out, errOut bytes.Buffer
	code := Run([]string{"--store", storePath, "list", "--state", "waiting"}, &out, &errOut)
	if code != 2 {
		t.Fatalf("list code = %d", code)
	}
	if !strings.Contains(errOut.String(), "--state") {
		t.Fatalf("list stderr = %q", errOut.String())
	}

	out.Reset()
	errOut.Reset()
	code = Run([]string{"--store", storePath, "export", "--limit", "-1"}, &out, &errOut)
	if code != 2 {
		t.Fatalf("export code = %d", code)
	}
	if !strings.Contains(errOut.String(), "--limit") {
		t.Fatalf("export stderr = %q", errOut.String())
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

func helperCommand(args ...string) []string {
	cmd := []string{os.Args[0], "-test.run=TestHelperProcess", "--"}
	return append(cmd, args...)
}

func TestHelperProcess(t *testing.T) {
	if os.Getenv("ONCE_TEST_HELPER") != "1" {
		return
	}

	args := os.Args
	for len(args) > 0 && args[0] != "--" {
		args = args[1:]
	}
	if len(args) < 2 {
		os.Exit(2)
	}
	args = args[1:]

	switch args[0] {
	case "append":
		if len(args) < 3 {
			os.Exit(2)
		}
		file, err := os.OpenFile(args[1], os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			os.Exit(3)
		}
		if _, err := file.WriteString(args[2]); err != nil {
			_ = file.Close()
			os.Exit(3)
		}
		if err := file.Close(); err != nil {
			os.Exit(3)
		}
		os.Exit(0)
	case "kill":
		proc, err := os.FindProcess(os.Getpid())
		if err != nil {
			os.Exit(3)
		}
		_ = proc.Kill()
		os.Exit(3)
	case "stdout":
		if len(args) != 2 {
			os.Exit(2)
		}
		size, err := strconv.Atoi(args[1])
		if err != nil {
			os.Exit(2)
		}
		chunk := strings.Repeat("x", 8192)
		for size > 0 {
			n := len(chunk)
			if size < n {
				n = size
			}
			if _, err := os.Stdout.WriteString(chunk[:n]); err != nil {
				os.Exit(3)
			}
			size -= n
		}
		os.Exit(0)
	default:
		os.Exit(2)
	}
}

func decodeExport(t *testing.T, output string) []map[string]any {
	t.Helper()

	lines := strings.Split(strings.TrimSpace(output), "\n")
	var docs []map[string]any
	for _, line := range lines {
		if line == "" {
			continue
		}
		var doc map[string]any
		if err := json.Unmarshal([]byte(line), &doc); err != nil {
			t.Fatalf("decode %q: %v", line, err)
		}
		docs = append(docs, doc)
	}
	return docs
}
