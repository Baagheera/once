package cli

import (
	"bytes"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

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

func TestVersion(t *testing.T) {
	oldVersion := version
	version = "v1.2.3"
	t.Cleanup(func() {
		version = oldVersion
	})

	var out, errOut bytes.Buffer
	code := Run([]string{"version"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("version code = %d stderr = %s", code, errOut.String())
	}
	if out.String() != "once v1.2.3\n" {
		t.Fatalf("version stdout = %q", out.String())
	}
	if errOut.Len() != 0 {
		t.Fatalf("stderr = %q", errOut.String())
	}
}

func TestVersionRejectsArguments(t *testing.T) {
	var out, errOut bytes.Buffer
	code := Run([]string{"version", "extra"}, &out, &errOut)
	if code != 2 {
		t.Fatalf("version code = %d stderr = %s", code, errOut.String())
	}
	if out.Len() != 0 {
		t.Fatalf("stdout = %q", out.String())
	}
	if !strings.Contains(errOut.String(), "version does not take positional arguments") {
		t.Fatalf("stderr = %q", errOut.String())
	}
}

func TestHelpIncludesVersion(t *testing.T) {
	var out, errOut bytes.Buffer
	code := Run([]string{"help"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("help code = %d stderr = %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "once version") {
		t.Fatalf("help output missing version command:\n%s", out.String())
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

func TestRunCompletesBeforeTimeout(t *testing.T) {
	t.Setenv("ONCE_TEST_HELPER", "1")

	dir := t.TempDir()
	storePath := filepath.Join(dir, "once.db")
	marker := filepath.Join(dir, "side-effect")
	cmd := helperCommand("append", marker, "ran\n")

	var out, errOut bytes.Buffer
	code := Run(append([]string{"--store", storePath, "run", "--key", "demo", "--timeout", "1s", "--"}, cmd...), &out, &errOut)
	if code != 0 {
		t.Fatalf("run code = %d stderr = %s", code, errOut.String())
	}
	data, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "ran\n" {
		t.Fatalf("marker = %q", data)
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
	if rec.State != once.Succeeded {
		t.Fatalf("state = %s, want %s", rec.State, once.Succeeded)
	}
}

func TestRunTimesOutAndReplaysFailure(t *testing.T) {
	t.Setenv("ONCE_TEST_HELPER", "1")

	storePath := filepath.Join(t.TempDir(), "once.db")
	cmd := helperCommand("sleep", "200ms")

	var out1, err1 bytes.Buffer
	code := Run(append([]string{"--store", storePath, "run", "--key", "demo", "--timeout", "20ms", "--"}, cmd...), &out1, &err1)
	if code != 124 {
		t.Fatalf("run code = %d stderr = %s", code, err1.String())
	}
	if out1.Len() != 0 {
		t.Fatalf("stdout = %q", out1.String())
	}
	if !strings.Contains(err1.String(), "timed out") {
		t.Fatalf("stderr = %q", err1.String())
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
	if rec.ExitCode != 124 {
		t.Fatalf("exit code = %d, want 124", rec.ExitCode)
	}
	if !strings.Contains(rec.Error, "timed out") {
		t.Fatalf("stored error = %q", rec.Error)
	}

	var out2, err2 bytes.Buffer
	replayCode := Run(append([]string{"--store", storePath, "run", "--key", "demo", "--timeout", "1s", "--"}, cmd...), &out2, &err2)
	if replayCode != 124 {
		t.Fatalf("replay code = %d stderr = %s", replayCode, err2.String())
	}
	if out2.Len() != 0 {
		t.Fatalf("replay stdout = %q", out2.String())
	}
	if !strings.Contains(err2.String(), "timed out") {
		t.Fatalf("replay stderr = %q", err2.String())
	}
}

func TestRunRejectsNegativeTimeout(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "once.db")

	var out, errOut bytes.Buffer
	code := Run([]string{"--store", storePath, "run", "--key", "demo", "--timeout", "-1s", "--", "ignored"}, &out, &errOut)
	if code != 2 {
		t.Fatalf("run code = %d stderr = %s", code, errOut.String())
	}
	if out.Len() != 0 {
		t.Fatalf("stdout = %q", out.String())
	}
	if !strings.Contains(errOut.String(), "non-negative") {
		t.Fatalf("stderr = %q", errOut.String())
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

func TestRunCompletesWithinMaxOutputBytes(t *testing.T) {
	t.Setenv("ONCE_TEST_HELPER", "1")

	storePath := filepath.Join(t.TempDir(), "once.db")
	cmd := helperCommand("stdout", "32")

	var out, errOut bytes.Buffer
	code := Run(append([]string{"--store", storePath, "run", "--key", "demo", "--max-output-bytes", "64", "--"}, cmd...), &out, &errOut)
	if code != 0 {
		t.Fatalf("run code = %d stderr = %s", code, errOut.String())
	}
	if out.Len() != 32 {
		t.Fatalf("stdout len = %d, want 32", out.Len())
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
	if rec.State != once.Succeeded {
		t.Fatalf("state = %s, want %s", rec.State, once.Succeeded)
	}
}

func TestRunLimitsStoredOutput(t *testing.T) {
	t.Setenv("ONCE_TEST_HELPER", "1")

	storePath := filepath.Join(t.TempDir(), "once.db")
	cmd := helperCommand("stdout", "4096")

	var out1, err1 bytes.Buffer
	code := Run(append([]string{"--store", storePath, "run", "--key", "demo", "--max-output-bytes", "128", "--"}, cmd...), &out1, &err1)
	if code != 125 {
		t.Fatalf("run code = %d stderr = %s", code, err1.String())
	}
	if out1.Len() != 128 {
		t.Fatalf("stdout len = %d, want 128", out1.Len())
	}
	if !strings.Contains(err1.String(), "max-output-bytes") {
		t.Fatalf("stderr = %q", err1.String())
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
	if rec.ExitCode != 125 {
		t.Fatalf("exit code = %d, want 125", rec.ExitCode)
	}
	if len(rec.Stdout) != 128 {
		t.Fatalf("stored stdout len = %d, want 128", len(rec.Stdout))
	}
	if !strings.Contains(rec.Error, "max-output-bytes") {
		t.Fatalf("stored error = %q", rec.Error)
	}

	var out2, err2 bytes.Buffer
	replayCode := Run(append([]string{"--store", storePath, "run", "--key", "demo", "--max-output-bytes", "4096", "--"}, cmd...), &out2, &err2)
	if replayCode != 125 {
		t.Fatalf("replay code = %d stderr = %s", replayCode, err2.String())
	}
	if !bytes.Equal(out1.Bytes(), out2.Bytes()) {
		t.Fatal("replayed stdout differs from stored stdout")
	}
	if !strings.Contains(err2.String(), "max-output-bytes") {
		t.Fatalf("replay stderr = %q", err2.String())
	}
}

func TestRunMaxOutputBytesZeroStoresNoOutput(t *testing.T) {
	t.Setenv("ONCE_TEST_HELPER", "1")

	storePath := filepath.Join(t.TempDir(), "once.db")
	cmd := helperCommand("stdout", "32")

	var out1, err1 bytes.Buffer
	code := Run(append([]string{"--store", storePath, "run", "--key", "demo", "--max-output-bytes", "0", "--"}, cmd...), &out1, &err1)
	if code != 125 {
		t.Fatalf("run code = %d stderr = %s", code, err1.String())
	}
	if out1.Len() != 0 {
		t.Fatalf("stdout len = %d, want 0", out1.Len())
	}
	if !strings.Contains(err1.String(), "max-output-bytes") {
		t.Fatalf("stderr = %q", err1.String())
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
	if rec.ExitCode != 125 {
		t.Fatalf("exit code = %d, want 125", rec.ExitCode)
	}
	if len(rec.Stdout) != 0 {
		t.Fatalf("stored stdout len = %d, want 0", len(rec.Stdout))
	}
	if !strings.Contains(rec.Error, "max-output-bytes") {
		t.Fatalf("stored error = %q", rec.Error)
	}

	var out2, err2 bytes.Buffer
	replayCode := Run(append([]string{"--store", storePath, "run", "--key", "demo", "--"}, cmd...), &out2, &err2)
	if replayCode != 125 {
		t.Fatalf("replay code = %d stderr = %s", replayCode, err2.String())
	}
	if out2.Len() != 0 {
		t.Fatalf("replay stdout len = %d, want 0", out2.Len())
	}
	if !strings.Contains(err2.String(), "max-output-bytes") {
		t.Fatalf("replay stderr = %q", err2.String())
	}
}

func TestRunMaxOutputBytesIsSharedAcrossStreams(t *testing.T) {
	t.Setenv("ONCE_TEST_HELPER", "1")

	storePath := filepath.Join(t.TempDir(), "once.db")
	cmd := helperCommand("stdout-stderr", "80", "80")

	var out, errOut bytes.Buffer
	code := Run(append([]string{"--store", storePath, "run", "--key", "demo", "--max-output-bytes", "100", "--"}, cmd...), &out, &errOut)
	if code != 125 {
		t.Fatalf("run code = %d stderr = %s", code, errOut.String())
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
	if got := len(rec.Stdout) + len(rec.Stderr); got != 100 {
		t.Fatalf("stored output len = %d, want 100", got)
	}
	if rec.ExitCode != 125 {
		t.Fatalf("exit code = %d, want 125", rec.ExitCode)
	}
}

func TestRunRejectsNegativeMaxOutputBytes(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "once.db")

	var out, errOut bytes.Buffer
	code := Run([]string{"--store", storePath, "run", "--key", "demo", "--max-output-bytes", "-1", "--", "ignored"}, &out, &errOut)
	if code != 2 {
		t.Fatalf("run code = %d stderr = %s", code, errOut.String())
	}
	if out.Len() != 0 {
		t.Fatalf("stdout = %q", out.String())
	}
	if !strings.Contains(errOut.String(), "non-negative") {
		t.Fatalf("stderr = %q", errOut.String())
	}
}

func TestDoctorReportsHealthyStore(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "once.db")
	store, err := once.OpenSQLite(storePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	code := Run([]string{"--store", storePath, "doctor"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("doctor code = %d stdout = %s stderr = %s", code, out.String(), errOut.String())
	}
	output := out.String()
	for _, want := range []string{"store path: ok", "sqlite open: ok", "sqlite schema: ok", "doctor: ok"} {
		if !strings.Contains(output, want) {
			t.Fatalf("doctor output missing %q:\n%s", want, output)
		}
	}
}

func TestDoctorReportsHealthyStoreAsJSON(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "once.db")
	store, err := once.OpenSQLite(storePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	code := Run([]string{"--store", storePath, "doctor", "--json"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("doctor code = %d stdout = %s stderr = %s", code, out.String(), errOut.String())
	}
	if errOut.Len() != 0 {
		t.Fatalf("stderr = %q", errOut.String())
	}

	doc := decodeDoctorJSON(t, out.String())
	if !doc.OK {
		t.Fatalf("doctor json ok = false: %#v", doc)
	}
	requireDoctorJSONCheck(t, doc, "store path", "ok")
	requireDoctorJSONCheck(t, doc, "sqlite open", "ok")
	requireDoctorJSONCheck(t, doc, "sqlite schema", "ok")
	if strings.Contains(out.String(), "doctor: ok") {
		t.Fatalf("doctor json should not include text summary:\n%s", out.String())
	}
}

func TestDoctorDoesNotCreateMissingStore(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "missing")
	storePath := filepath.Join(dir, "once.db")

	var out, errOut bytes.Buffer
	code := Run([]string{"--store", storePath, "doctor"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("doctor code = %d stdout = %s stderr = %s", code, out.String(), errOut.String())
	}
	output := out.String()
	for _, want := range []string{"store parent: warn", "store file: warn", "sqlite open: skip", "doctor: ok"} {
		if !strings.Contains(output, want) {
			t.Fatalf("doctor output missing %q:\n%s", want, output)
		}
	}
	if _, err := os.Stat(storePath); !os.IsNotExist(err) {
		t.Fatalf("doctor created store or returned unexpected stat error: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("doctor created parent dir or returned unexpected stat error: %v", err)
	}
}

func TestDoctorFailsForBroadStoreParent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows ACL checks use a separate path")
	}

	dir := filepath.Join(t.TempDir(), "shared")
	if err := os.Mkdir(dir, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o777); err != nil {
		t.Fatal(err)
	}
	storePath := filepath.Join(dir, "once.db")

	var out, errOut bytes.Buffer
	code := Run([]string{"--store", storePath, "doctor"}, &out, &errOut)
	if code != 1 {
		t.Fatalf("doctor code = %d stdout = %s stderr = %s", code, out.String(), errOut.String())
	}
	output := out.String()
	if !strings.Contains(output, "store parent: fail") {
		t.Fatalf("doctor output missing parent failure:\n%s", output)
	}
	if !strings.Contains(output, "allow group or other writes") {
		t.Fatalf("doctor output missing permission detail:\n%s", output)
	}
}

func TestDoctorFailsForBroadCurrentDirectoryStoreParent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows ACL checks use a separate path")
	}

	dir := filepath.Join(t.TempDir(), "shared")
	if err := os.Mkdir(dir, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o777); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	var out, errOut bytes.Buffer
	code := Run([]string{"--store", "once.db", "doctor"}, &out, &errOut)
	if code != 1 {
		t.Fatalf("doctor code = %d stdout = %s stderr = %s", code, out.String(), errOut.String())
	}
	output := out.String()
	if !strings.Contains(output, "store parent: fail") {
		t.Fatalf("doctor output missing parent failure:\n%s", output)
	}
	if !strings.Contains(output, "allow group or other writes") {
		t.Fatalf("doctor output missing permission detail:\n%s", output)
	}
}

func TestDoctorRejectsSQLiteDSNPath(t *testing.T) {
	var out, errOut bytes.Buffer
	code := Run([]string{"--store", "file:once.db?mode=ro", "doctor"}, &out, &errOut)
	if code == 0 {
		t.Fatalf("doctor should reject SQLite DSN paths:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "store path: fail") {
		t.Fatalf("stdout = %q", out.String())
	}
	if !strings.Contains(out.String(), "local filesystem path") {
		t.Fatalf("stdout = %q", out.String())
	}
}

func TestDoctorDoesNotPrintToken(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "once.db")
	store, err := once.OpenSQLite(storePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	token := strings.Repeat("a", minAuthTokenLength)
	if err := os.WriteFile(storePath+".token", []byte(token+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := once.RestrictLocalFile(storePath + ".token"); err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	code := Run([]string{"--store", storePath, "doctor"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("doctor code = %d stdout = %s stderr = %s", code, out.String(), errOut.String())
	}
	combined := out.String() + errOut.String()
	if strings.Contains(combined, token) {
		t.Fatalf("doctor leaked token material:\n%s", combined)
	}
	if !strings.Contains(out.String(), "token file: ok") {
		t.Fatalf("stdout = %q", out.String())
	}
}

func TestDoctorRejectsBadSQLiteSidecar(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "once.db")
	store, err := once.OpenSQLite(storePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	for _, sidecar := range []string{storePath + "-wal", storePath + "-shm"} {
		if err := os.Remove(sidecar); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(storePath+"-wal", 0o700); err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	code := Run([]string{"--store", storePath, "doctor"}, &out, &errOut)
	if code == 0 {
		t.Fatalf("doctor should fail for non-regular sidecar:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "sqlite wal: fail") {
		t.Fatalf("stdout = %q", out.String())
	}
}

func TestDoctorReportsFailureAsJSON(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "once.db")
	store, err := once.OpenSQLite(storePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	for _, sidecar := range []string{storePath + "-wal", storePath + "-shm"} {
		if err := os.Remove(sidecar); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(storePath+"-wal", 0o700); err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	code := Run([]string{"--store", storePath, "doctor", "--json"}, &out, &errOut)
	if code != 1 {
		t.Fatalf("doctor code = %d stdout = %s stderr = %s", code, out.String(), errOut.String())
	}
	if errOut.Len() != 0 {
		t.Fatalf("stderr = %q", errOut.String())
	}

	doc := decodeDoctorJSON(t, out.String())
	if doc.OK {
		t.Fatalf("doctor json ok = true: %#v", doc)
	}
	requireDoctorJSONCheck(t, doc, "sqlite wal", "fail")
}

func TestDoctorRejectsOrphanSQLiteSidecar(t *testing.T) {
	dir := t.TempDir()
	storePath := filepath.Join(dir, "once.db")
	walPath := storePath + "-wal"
	if err := os.WriteFile(walPath, []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := once.RestrictLocalFile(walPath); err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	code := Run([]string{"--store", storePath, "doctor"}, &out, &errOut)
	if code == 0 {
		t.Fatalf("doctor should fail for orphan sidecar:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "sqlite wal: fail") {
		t.Fatalf("stdout = %q", out.String())
	}
	if !strings.Contains(out.String(), "store file is missing") {
		t.Fatalf("stdout = %q", out.String())
	}
}

func TestDoctorReportsMissingSchema(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "once.db")
	db, err := sql.Open("sqlite", storePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := once.RestrictLocalFile(storePath); err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	code := Run([]string{"--store", storePath, "doctor"}, &out, &errOut)
	if code == 0 {
		t.Fatalf("doctor should fail for missing schema:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "sqlite schema: fail") {
		t.Fatalf("stdout = %q", out.String())
	}
	if !strings.Contains(out.String(), "once_records table is missing") {
		t.Fatalf("stdout = %q", out.String())
	}
}

func TestDoctorReportsSchemaWithoutPrimaryKey(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "once.db")
	db := openDoctorTestDB(t, storePath)
	execDoctorTestSQL(t, db, `
CREATE TABLE once_records (
	key TEXT,
	attempt_hash TEXT NOT NULL DEFAULT '',
	state TEXT NOT NULL,
	exit_code INTEGER NOT NULL DEFAULT 0,
	stdout BLOB NOT NULL DEFAULT X'',
	stderr BLOB NOT NULL DEFAULT X'',
	error TEXT NOT NULL DEFAULT '',
	command TEXT NOT NULL DEFAULT '[]',
	started_at TEXT NOT NULL,
	finished_at TEXT,
	updated_at TEXT NOT NULL
);
CREATE INDEX once_records_state_idx ON once_records(state);
`)
	closeDoctorTestDB(t, db, storePath)

	var out, errOut bytes.Buffer
	code := Run([]string{"--store", storePath, "doctor"}, &out, &errOut)
	if code == 0 {
		t.Fatalf("doctor should fail for missing primary key:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "key column is not the primary key") {
		t.Fatalf("stdout = %q", out.String())
	}
}

func TestDoctorReportsMissingSchemaColumn(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "once.db")
	db := openDoctorTestDB(t, storePath)
	execDoctorTestSQL(t, db, `
CREATE TABLE once_records (
	key TEXT PRIMARY KEY,
	state TEXT NOT NULL,
	exit_code INTEGER NOT NULL DEFAULT 0,
	stdout BLOB NOT NULL DEFAULT X'',
	stderr BLOB NOT NULL DEFAULT X'',
	error TEXT NOT NULL DEFAULT '',
	command TEXT NOT NULL DEFAULT '[]',
	started_at TEXT NOT NULL,
	finished_at TEXT,
	updated_at TEXT NOT NULL
);
CREATE INDEX once_records_state_idx ON once_records(state);
`)
	closeDoctorTestDB(t, db, storePath)

	var out, errOut bytes.Buffer
	code := Run([]string{"--store", storePath, "doctor"}, &out, &errOut)
	if code == 0 {
		t.Fatalf("doctor should fail for missing column:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "once_records missing columns: attempt_hash") {
		t.Fatalf("stdout = %q", out.String())
	}
}

func TestDoctorReportsMissingSchemaDefault(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "once.db")
	db := openDoctorTestDB(t, storePath)
	execDoctorTestSQL(t, db, `
CREATE TABLE once_records (
	key TEXT PRIMARY KEY,
	attempt_hash TEXT NOT NULL DEFAULT '',
	state TEXT NOT NULL,
	exit_code INTEGER NOT NULL,
	stdout BLOB NOT NULL DEFAULT X'',
	stderr BLOB NOT NULL DEFAULT X'',
	error TEXT NOT NULL DEFAULT '',
	command TEXT NOT NULL DEFAULT '[]',
	started_at TEXT NOT NULL,
	finished_at TEXT,
	updated_at TEXT NOT NULL
);
CREATE INDEX once_records_state_idx ON once_records(state);
`)
	closeDoctorTestDB(t, db, storePath)

	var out, errOut bytes.Buffer
	code := Run([]string{"--store", storePath, "doctor"}, &out, &errOut)
	if code == 0 {
		t.Fatalf("doctor should fail for missing default:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "exit_code column default is <none>, want 0") {
		t.Fatalf("stdout = %q", out.String())
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

func TestListAndExportFilterOlderThan(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "once.db")
	store, err := once.OpenSQLite(storePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Reserve("old-running", []string{"send", "email"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Reserve("new-running", []string{"send", "sms"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	oldTime := time.Now().UTC().Add(-2 * time.Hour)
	newTime := time.Now().UTC()
	setRecordUpdatedAt(t, storePath, "old-running", oldTime)
	setRecordUpdatedAt(t, storePath, "new-running", newTime)

	var out, errOut bytes.Buffer
	code := Run([]string{"--store", storePath, "list", "--state", "running", "--older-than", "1h"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("list code = %d stderr = %s", code, errOut.String())
	}
	listed := out.String()
	if !strings.Contains(listed, "old-running") {
		t.Fatalf("list output missing old record:\n%s", listed)
	}
	if strings.Contains(listed, "new-running") {
		t.Fatalf("list output included new record:\n%s", listed)
	}

	out.Reset()
	errOut.Reset()
	code = Run([]string{"--store", storePath, "export", "--state", "running", "--older-than", "1h"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("export code = %d stderr = %s", code, errOut.String())
	}
	docs := decodeExport(t, out.String())
	if len(docs) != 1 || docs[0]["key"] != "old-running" {
		t.Fatalf("export docs = %#v", docs)
	}
}

func TestGetRedactsOutputByDefault(t *testing.T) {
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
	code = Run([]string{"--store", storePath, "get", "demo"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("get code = %d stderr = %s", code, errOut.String())
	}
	var doc map[string]any
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatalf("decode get json: %v\n%s", err, out.String())
	}
	if doc["key"] != "demo" || doc["state"] != "succeeded" {
		t.Fatalf("get doc = %#v", doc)
	}
	if _, ok := doc["stdout_b64"]; ok {
		t.Fatalf("stdout_b64 should be omitted by default: %#v", doc)
	}
	if _, ok := doc["stderr_b64"]; ok {
		t.Fatalf("stderr_b64 should be omitted by default: %#v", doc)
	}
}

func TestGetIncludeOutput(t *testing.T) {
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
	code = Run([]string{"--store", storePath, "get", "--include-output", "demo"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("get --include-output code = %d stderr = %s", code, errOut.String())
	}
	var doc map[string]any
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatalf("decode get json: %v\n%s", err, out.String())
	}
	if doc["stdout_b64"] != base64.StdEncoding.EncodeToString([]byte("xxxxxx")) {
		t.Fatalf("stdout_b64 = %#v", doc["stdout_b64"])
	}
	if value, ok := doc["stderr_b64"]; !ok || value != "" {
		t.Fatalf("stderr_b64 = %#v, present=%v", value, ok)
	}
}

func TestGetAcceptsDashPrefixedKey(t *testing.T) {
	t.Setenv("ONCE_TEST_HELPER", "1")

	storePath := filepath.Join(t.TempDir(), "once.db")
	cmd := helperCommand("stdout", "2")

	var out, errOut bytes.Buffer
	code := Run(append([]string{"--store", storePath, "run", "--key", "-demo", "--"}, cmd...), &out, &errOut)
	if code != 0 {
		t.Fatalf("run code = %d stderr = %s", code, errOut.String())
	}

	out.Reset()
	errOut.Reset()
	code = Run([]string{"--store", storePath, "get", "-demo"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("get code = %d stderr = %s", code, errOut.String())
	}
	var doc map[string]any
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatalf("decode get json: %v\n%s", err, out.String())
	}
	if doc["key"] != "-demo" {
		t.Fatalf("key = %#v", doc["key"])
	}
}

func TestGetAcceptsFlagNamedKey(t *testing.T) {
	t.Setenv("ONCE_TEST_HELPER", "1")

	storePath := filepath.Join(t.TempDir(), "once.db")
	cmd := helperCommand("stdout", "2")

	var out, errOut bytes.Buffer
	code := Run(append([]string{"--store", storePath, "run", "--key", "--include-output", "--"}, cmd...), &out, &errOut)
	if code != 0 {
		t.Fatalf("run code = %d stderr = %s", code, errOut.String())
	}

	out.Reset()
	errOut.Reset()
	code = Run([]string{"--store", storePath, "get", "--include-output"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("get code = %d stderr = %s", code, errOut.String())
	}
	var doc map[string]any
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatalf("decode get json: %v\n%s", err, out.String())
	}
	if doc["key"] != "--include-output" {
		t.Fatalf("key = %#v", doc["key"])
	}

	out.Reset()
	errOut.Reset()
	code = Run([]string{"--store", storePath, "get", "--include-output", "--", "--include-output"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("get --include-output code = %d stderr = %s", code, errOut.String())
	}
	doc = map[string]any{}
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatalf("decode get json: %v\n%s", err, out.String())
	}
	if doc["stdout_b64"] != base64.StdEncoding.EncodeToString([]byte("xx")) {
		t.Fatalf("stdout_b64 = %#v", doc["stdout_b64"])
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

	out.Reset()
	errOut.Reset()
	code = Run([]string{"--store", storePath, "list", "--older-than", "0d"}, &out, &errOut)
	if code != 2 {
		t.Fatalf("list older-than code = %d", code)
	}
	if !strings.Contains(errOut.String(), "--older-than") {
		t.Fatalf("list older-than stderr = %q", errOut.String())
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

func TestPruneDryRunThenForce(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "once.db")

	var out, errOut bytes.Buffer
	code := Run([]string{"--store", storePath, "run", "--key", "done", "--", shell(), shellFlag(), "echo ok"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("run code = %d stderr = %s", code, errOut.String())
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
	code = Run([]string{"--store", storePath, "prune", "--state", "succeeded", "--older-than", "1ns"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("dry-run prune code = %d stderr = %s", code, errOut.String())
	}
	dryRun := out.String()
	if !strings.Contains(dryRun, "would prune 1 record") || !strings.Contains(dryRun, "done") {
		t.Fatalf("dry-run output = %q", dryRun)
	}
	if strings.Contains(dryRun, "stuck") {
		t.Fatalf("dry-run output included running record: %q", dryRun)
	}

	store, err = once.OpenSQLite(storePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get("done"); err != nil {
		t.Fatalf("dry run removed done: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	errOut.Reset()
	code = Run([]string{"--store", storePath, "prune", "--state", "succeeded", "--older-than", "1ns", "--force"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("force prune code = %d stderr = %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "pruned 1 record") {
		t.Fatalf("force output = %q", out.String())
	}

	store, err = once.OpenSQLite(storePath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.Get("done"); err != once.ErrNotFound {
		t.Fatalf("done err = %v, want ErrNotFound", err)
	}
	if _, err := store.Get("stuck"); err != nil {
		t.Fatalf("stuck err = %v, want record to remain", err)
	}
}

func TestPruneRejectsInvalidFilters(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "once.db")

	var out, errOut bytes.Buffer
	code := Run([]string{"--store", storePath, "prune", "--state", "running", "--older-than", "1h"}, &out, &errOut)
	if code != 2 {
		t.Fatalf("running prune code = %d", code)
	}
	if !strings.Contains(errOut.String(), "--state") {
		t.Fatalf("running prune stderr = %q", errOut.String())
	}

	out.Reset()
	errOut.Reset()
	code = Run([]string{"--store", storePath, "prune", "--state", "succeeded"}, &out, &errOut)
	if code != 2 {
		t.Fatalf("missing age code = %d", code)
	}
	if !strings.Contains(errOut.String(), "--older-than") {
		t.Fatalf("missing age stderr = %q", errOut.String())
	}

	out.Reset()
	errOut.Reset()
	code = Run([]string{"--store", storePath, "prune", "--state", "failed", "--older-than", "0d"}, &out, &errOut)
	if code != 2 {
		t.Fatalf("zero age code = %d", code)
	}
	if !strings.Contains(errOut.String(), "--older-than") {
		t.Fatalf("zero age stderr = %q", errOut.String())
	}

	out.Reset()
	errOut.Reset()
	code = Run([]string{"--store", storePath, "prune", "--state", "failed", "--older-than", "-1571175806d"}, &out, &errOut)
	if code != 2 {
		t.Fatalf("negative wrapped age code = %d", code)
	}
	if !strings.Contains(errOut.String(), "--older-than") {
		t.Fatalf("negative wrapped age stderr = %q", errOut.String())
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
	storePath := filepath.Join(t.TempDir(), "once.db")

	var out, errOut bytes.Buffer
	code := Run([]string{"--store", storePath, "serve", "--token", "short"}, &out, &errOut)
	if code != 2 {
		t.Fatalf("code = %d", code)
	}
	if strings.Contains(out.String(), "short") {
		t.Fatalf("stdout leaked token: %q", out.String())
	}
	if !strings.Contains(errOut.String(), "at least 32 characters") {
		t.Fatalf("stderr = %q", errOut.String())
	}
	if _, err := os.Stat(storePath); !os.IsNotExist(err) {
		t.Fatalf("store stat err = %v, want not exist", err)
	}
}

func TestServeDoesNotCreateTokenFileWhenStorePathIsInvalid(t *testing.T) {
	dir := t.TempDir()
	storePath := filepath.Join(dir, "once.db?mode=memory")
	tokenFile := filepath.Join(dir, "once.token")

	var out, errOut bytes.Buffer
	code := Run([]string{"--store", storePath, "serve", "--token-file", tokenFile}, &out, &errOut)
	if code != 1 {
		t.Fatalf("code = %d stderr = %s", code, errOut.String())
	}
	if !strings.Contains(errOut.String(), "local filesystem path") {
		t.Fatalf("stderr = %q", errOut.String())
	}
	if _, err := os.Stat(tokenFile); !os.IsNotExist(err) {
		t.Fatalf("token file stat err = %v, want not exist", err)
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

func setRecordUpdatedAt(t *testing.T, storePath, key string, updatedAt time.Time) {
	t.Helper()

	db, err := sql.Open("sqlite", storePath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	res, err := db.Exec(`UPDATE once_records SET updated_at = ? WHERE key = ?`, updatedAt.UTC().Format(time.RFC3339Nano), key)
	if err != nil {
		t.Fatal(err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		t.Fatal(err)
	}
	if affected != 1 {
		t.Fatalf("updated %d records for key %q, want 1", affected, key)
	}
}

func openDoctorTestDB(t *testing.T, storePath string) *sql.DB {
	t.Helper()

	db, err := sql.Open("sqlite", storePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	return db
}

func execDoctorTestSQL(t *testing.T, db *sql.DB, query string) {
	t.Helper()

	if _, err := db.Exec(query); err != nil {
		t.Fatal(err)
	}
}

func closeDoctorTestDB(t *testing.T, db *sql.DB, storePath string) {
	t.Helper()

	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := once.RestrictLocalFile(storePath); err != nil {
		t.Fatal(err)
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
	case "stdout-stderr":
		if len(args) != 3 {
			os.Exit(2)
		}
		stdoutSize, err := strconv.Atoi(args[1])
		if err != nil {
			os.Exit(2)
		}
		stderrSize, err := strconv.Atoi(args[2])
		if err != nil {
			os.Exit(2)
		}
		if _, err := os.Stdout.WriteString(strings.Repeat("o", stdoutSize)); err != nil {
			os.Exit(3)
		}
		if _, err := os.Stderr.WriteString(strings.Repeat("e", stderrSize)); err != nil {
			os.Exit(3)
		}
		os.Exit(0)
	case "sleep":
		if len(args) != 2 {
			os.Exit(2)
		}
		duration, err := time.ParseDuration(args[1])
		if err != nil {
			os.Exit(2)
		}
		time.Sleep(duration)
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

type doctorJSONOutput struct {
	OK     bool                    `json:"ok"`
	Checks []doctorJSONOutputCheck `json:"checks"`
}

type doctorJSONOutputCheck struct {
	Name   string `json:"name"`
	Level  string `json:"level"`
	Detail string `json:"detail"`
}

func decodeDoctorJSON(t *testing.T, output string) doctorJSONOutput {
	t.Helper()

	var doc doctorJSONOutput
	if err := json.Unmarshal([]byte(output), &doc); err != nil {
		t.Fatalf("decode doctor json %q: %v", output, err)
	}
	if len(doc.Checks) == 0 {
		t.Fatalf("doctor json checks are empty: %q", output)
	}
	return doc
}

func requireDoctorJSONCheck(t *testing.T, doc doctorJSONOutput, name, level string) doctorJSONOutputCheck {
	t.Helper()

	for _, check := range doc.Checks {
		if check.Name != name {
			continue
		}
		if check.Level != level {
			t.Fatalf("doctor json check %q level = %q, want %q", name, check.Level, level)
		}
		return check
	}
	t.Fatalf("doctor json missing check %q: %#v", name, doc.Checks)
	return doctorJSONOutputCheck{}
}
