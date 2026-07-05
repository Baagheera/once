package once

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestReserveCommitAndReplay(t *testing.T) {
	store, err := OpenSQLite(t.TempDir() + "/once.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	rec, fresh, err := store.Reserve("k1", []string{"echo", "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if !fresh {
		t.Fatal("first reservation should be fresh")
	}
	if rec.State != Running {
		t.Fatalf("state = %s, want %s", rec.State, Running)
	}

	rec, err = store.Commit("k1", rec.Attempt, Succeeded, 0, []byte("hello\n"), nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if rec.State != Succeeded {
		t.Fatalf("state = %s, want %s", rec.State, Succeeded)
	}

	rec, fresh, err = store.Reserve("k1", []string{"echo", "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if fresh {
		t.Fatal("second reservation should replay existing record")
	}
	if string(rec.Stdout) != "hello\n" {
		t.Fatalf("stdout = %q", rec.Stdout)
	}
}

func TestForget(t *testing.T) {
	store, err := OpenSQLite(t.TempDir() + "/once.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	rec, _, err := store.Reserve("k1", []string{"true"})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := store.Commit("k1", rec.Attempt, Succeeded, 0, nil, nil, ""); err != nil {
		t.Fatal(err)
	}

	ok, err := store.Forget("k1", false, rec.Attempt)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("forget should delete record")
	}

	if _, err := store.Get("k1"); err != ErrNotFound {
		t.Fatalf("Get err = %v, want ErrNotFound", err)
	}
}

func TestReserveRejectsDifferentCommand(t *testing.T) {
	store, err := OpenSQLite(t.TempDir() + "/once.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	rec, _, err := store.Reserve("k1", []string{"echo", "one"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Commit("k1", rec.Attempt, Succeeded, 0, []byte("one\n"), nil, ""); err != nil {
		t.Fatal(err)
	}

	if _, _, err := store.Reserve("k1", []string{"echo", "two"}); err != ErrConflict {
		t.Fatalf("Reserve err = %v, want ErrConflict", err)
	}
}

func TestCommitIsIdempotentForSameResult(t *testing.T) {
	store, err := OpenSQLite(t.TempDir() + "/once.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	rec, _, err := store.Reserve("k1", []string{"echo", "one"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Commit("k1", rec.Attempt, Succeeded, 0, []byte("one\n"), nil, ""); err != nil {
		t.Fatal(err)
	}
	rec, err = store.Commit("k1", rec.Attempt, Succeeded, 0, []byte("one\n"), nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if rec.State != Succeeded {
		t.Fatalf("state = %s", rec.State)
	}
}

func TestCommitDoesNotReturnStoredAttemptHash(t *testing.T) {
	store, err := OpenSQLite(t.TempDir() + "/once.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	rec, _, err := store.Reserve("k1", []string{"echo", "one"})
	if err != nil {
		t.Fatal(err)
	}
	attempt := rec.Attempt
	if attempt == "" {
		t.Fatal("fresh reservation should return attempt token")
	}

	rec, err = store.Commit("k1", attempt, Succeeded, 0, []byte("one\n"), nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if rec.Attempt != "" {
		t.Fatalf("committed record attempt = %q, want empty", rec.Attempt)
	}

	rec, err = store.Commit("k1", attempt, Succeeded, 0, []byte("one\n"), nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if rec.Attempt != "" {
		t.Fatalf("idempotent commit attempt = %q, want empty", rec.Attempt)
	}
}

func TestCommitConflictsForDifferentResult(t *testing.T) {
	store, err := OpenSQLite(t.TempDir() + "/once.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	rec, _, err := store.Reserve("k1", []string{"echo", "one"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Commit("k1", rec.Attempt, Succeeded, 0, []byte("one\n"), nil, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Commit("k1", rec.Attempt, Succeeded, 0, []byte("two\n"), nil, ""); err != ErrConflict {
		t.Fatalf("Commit err = %v, want ErrConflict", err)
	}
}

func TestCommitRejectsWrongAttempt(t *testing.T) {
	store, err := OpenSQLite(t.TempDir() + "/once.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	rec, _, err := store.Reserve("k1", []string{"echo", "one"})
	if err != nil {
		t.Fatal(err)
	}
	wrongAttempt, err := NewAttemptToken()
	if err != nil {
		t.Fatal(err)
	}
	if wrongAttempt == rec.Attempt {
		t.Fatal("unexpected token collision")
	}
	if _, err := store.Commit("k1", wrongAttempt, Succeeded, 0, []byte("one\n"), nil, ""); err != ErrConflict {
		t.Fatalf("Commit err = %v, want ErrConflict", err)
	}
}

func TestGetRejectsInvalidKey(t *testing.T) {
	store, err := OpenSQLite(t.TempDir() + "/once.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	if _, err := store.Get("bad/key"); err == nil || err == ErrNotFound {
		t.Fatalf("Get err = %v, want validation error", err)
	}
}

func TestReserveRejectsEmptyVsNonEmptyCommand(t *testing.T) {
	store, err := OpenSQLite(t.TempDir() + "/once.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	if _, _, err := store.Reserve("k1", nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Reserve("k1", []string{"echo", "one"}); err != ErrConflict {
		t.Fatalf("Reserve err = %v, want ErrConflict", err)
	}
}

func TestForgetRunningNeedsForce(t *testing.T) {
	store, err := OpenSQLite(t.TempDir() + "/once.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	rec, _, err := store.Reserve("k1", []string{"true"})
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := store.Forget("k1", false, ""); err == nil || ok {
		t.Fatalf("Forget ok=%v err=%v, want invalid attempt", ok, err)
	}
	if ok, err := store.Forget("k1", false, rec.Attempt); err != ErrRunning || ok {
		t.Fatalf("Forget ok=%v err=%v, want ErrRunning", ok, err)
	}
	if ok, err := store.Forget("k1", true, rec.Attempt); err != nil || !ok {
		t.Fatalf("Forget force ok=%v err=%v", ok, err)
	}
}

func TestForgetRequiresAttempt(t *testing.T) {
	store, err := OpenSQLite(t.TempDir() + "/once.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	if _, _, err := store.Reserve("k1", []string{"true"}); err != nil {
		t.Fatal(err)
	}
	if ok, err := store.Forget("k1", true, ""); err == nil || ok {
		t.Fatalf("Forget ok=%v err=%v, want missing attempt error", ok, err)
	}
}

func TestAdminForgetDoesNotNeedAttempt(t *testing.T) {
	store, err := OpenSQLite(t.TempDir() + "/once.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	if _, _, err := store.Reserve("k1", []string{"true"}); err != nil {
		t.Fatal(err)
	}
	if ok, err := store.AdminForget("k1", true); err != nil || !ok {
		t.Fatalf("AdminForget ok=%v err=%v", ok, err)
	}
}

func TestForceForgetRejectsWrongAttempt(t *testing.T) {
	store, err := OpenSQLite(t.TempDir() + "/once.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	if _, _, err := store.Reserve("k1", []string{"true"}); err != nil {
		t.Fatal(err)
	}
	wrongAttempt, err := NewAttemptToken()
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := store.Forget("k1", true, wrongAttempt); err != nil || ok {
		t.Fatalf("Forget ok=%v err=%v, want no delete", ok, err)
	}
}

func TestConcurrentReserveOnlyOneFresh(t *testing.T) {
	store, err := OpenSQLite(t.TempDir() + "/once.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	var freshCount int64
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, fresh, err := store.Reserve("k1", []string{"true"})
			if err != nil {
				t.Errorf("Reserve err = %v", err)
				return
			}
			if fresh {
				atomic.AddInt64(&freshCount, 1)
			}
		}()
	}
	wg.Wait()

	if freshCount != 1 {
		t.Fatalf("freshCount = %d, want 1", freshCount)
	}
}

func TestConcurrentReserveAcrossStoresOnlyOneFresh(t *testing.T) {
	path := filepath.Join(t.TempDir(), "once.db")
	const storesCount = 8

	stores := make([]*SQLiteStore, storesCount)
	for i := range stores {
		store, err := OpenSQLite(path)
		if err != nil {
			t.Fatal(err)
		}
		stores[i] = store
		defer store.Close()
	}

	var freshCount int64
	var wg sync.WaitGroup
	for _, store := range stores {
		wg.Add(1)
		go func(store *SQLiteStore) {
			defer wg.Done()
			_, fresh, err := store.Reserve("k1", []string{"true"})
			if err != nil {
				t.Errorf("Reserve err = %v", err)
				return
			}
			if fresh {
				atomic.AddInt64(&freshCount, 1)
			}
		}(store)
	}
	wg.Wait()

	if freshCount != 1 {
		t.Fatalf("freshCount = %d, want 1", freshCount)
	}
}

func TestListFiltersAndLimitsRecords(t *testing.T) {
	store, err := OpenSQLite(t.TempDir() + "/once.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	rec, _, err := store.Reserve("done", []string{"echo", "ok"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Commit("done", rec.Attempt, Succeeded, 0, []byte("ok\n"), nil, ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Reserve("stuck", []string{"send", "email"}); err != nil {
		t.Fatal(err)
	}

	records, err := store.List(ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 {
		t.Fatalf("len(records) = %d, want 2", len(records))
	}
	for _, rec := range records {
		if len(rec.Stdout) != 0 || len(rec.Stderr) != 0 {
			t.Fatalf("default List returned output for %s", rec.Key)
		}
	}

	records, err = store.List(ListOptions{State: Running})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Key != "stuck" {
		t.Fatalf("running records = %#v, want stuck", records)
	}

	records, err = store.List(ListOptions{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("limited len = %d, want 1", len(records))
	}

	if _, err := store.List(ListOptions{State: State("other")}); err == nil {
		t.Fatal("expected invalid state error")
	}
	if _, err := store.List(ListOptions{Limit: -1}); err == nil {
		t.Fatal("expected invalid limit error")
	}

	records, err = store.List(ListOptions{IncludeOutput: true})
	if err != nil {
		t.Fatal(err)
	}
	var foundDone bool
	for _, rec := range records {
		if rec.Key == "done" {
			foundDone = true
			if string(rec.Stdout) != "ok\n" {
				t.Fatalf("stdout = %q, want ok", rec.Stdout)
			}
		}
	}
	if !foundDone {
		t.Fatal("missing done record")
	}
}

func TestListOrdersByParsedUpdatedAt(t *testing.T) {
	store, err := OpenSQLite(t.TempDir() + "/once.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	base := time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC)
	older := base.Add(100 * time.Millisecond)
	newer := base.Add(110 * time.Millisecond)
	newest := base.Add(120 * time.Millisecond)
	for _, row := range []struct {
		key string
		at  time.Time
	}{
		{key: "older", at: older},
		{key: "newer", at: newer},
		{key: "same-b", at: newest},
		{key: "same-a", at: newest},
	} {
		if _, err := store.db.Exec(`
INSERT INTO once_records (key, attempt_hash, state, command, started_at, updated_at)
VALUES (?, ?, 'running', '[]', ?, ?)
`, row.key, row.key+"-attempt", formatTime(row.at), formatTime(row.at)); err != nil {
			t.Fatal(err)
		}
	}

	records, err := store.List(ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 4 {
		t.Fatalf("len(records) = %d, want 4", len(records))
	}
	got := recordKeys(records)
	want := []string{"same-a", "same-b", "newer", "older"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("order = %v, want %v", got, want)
	}
}

func TestPruneDeletesOnlyOldTerminalRecords(t *testing.T) {
	store, err := OpenSQLite(t.TempDir() + "/once.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	now := time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC)
	old := now.Add(-48 * time.Hour)
	recent := now.Add(-2 * time.Hour)
	cutoff := now.Add(-24 * time.Hour)

	for _, row := range []struct {
		key   string
		state State
		at    time.Time
	}{
		{key: "old-success", state: Succeeded, at: old},
		{key: "recent-success", state: Succeeded, at: recent},
		{key: "old-failure", state: Failed, at: old},
		{key: "old-running", state: Running, at: old},
	} {
		finishedAt := any(nil)
		if row.state != Running {
			finishedAt = formatTime(row.at)
		}
		if _, err := store.db.Exec(`
INSERT INTO once_records (key, attempt_hash, state, exit_code, command, started_at, finished_at, updated_at)
VALUES (?, ?, ?, 0, '[]', ?, ?, ?)
`, row.key, row.key+"-attempt", string(row.state), formatTime(row.at), finishedAt, formatTime(row.at)); err != nil {
			t.Fatal(err)
		}
	}

	dryRun, err := store.Prune(PruneOptions{State: Succeeded, Cutoff: cutoff})
	if err != nil {
		t.Fatal(err)
	}
	if dryRun.Deleted != 0 {
		t.Fatalf("dry run deleted = %d, want 0", dryRun.Deleted)
	}
	if len(dryRun.Records) != 1 || dryRun.Records[0].Key != "old-success" {
		t.Fatalf("dry run records = %#v, want old-success", dryRun.Records)
	}
	if _, err := store.Get("old-success"); err != nil {
		t.Fatalf("dry run deleted old-success: %v", err)
	}

	result, err := store.Prune(PruneOptions{State: Succeeded, Cutoff: cutoff, Force: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.Deleted != 1 {
		t.Fatalf("deleted = %d, want 1", result.Deleted)
	}
	if _, err := store.Get("old-success"); err != ErrNotFound {
		t.Fatalf("old-success err = %v, want ErrNotFound", err)
	}
	for _, key := range []string{"recent-success", "old-failure", "old-running"} {
		if _, err := store.Get(key); err != nil {
			t.Fatalf("%s err = %v, want record to remain", key, err)
		}
	}
}

func TestPruneOrdersCandidatesByParsedUpdatedAt(t *testing.T) {
	store, err := OpenSQLite(t.TempDir() + "/once.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	base := time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC)
	cutoff := base.Add(200 * time.Millisecond)
	for _, row := range []struct {
		key string
		at  time.Time
	}{
		{key: "old-b", at: base.Add(100 * time.Millisecond)},
		{key: "old-a", at: base.Add(100 * time.Millisecond)},
		{key: "older", at: base.Add(90 * time.Millisecond)},
		{key: "recent", at: base.Add(210 * time.Millisecond)},
	} {
		if _, err := store.db.Exec(`
INSERT INTO once_records (key, attempt_hash, state, exit_code, command, started_at, finished_at, updated_at)
VALUES (?, ?, 'succeeded', 0, '[]', ?, ?, ?)
`, row.key, row.key+"-attempt", formatTime(row.at), formatTime(row.at), formatTime(row.at)); err != nil {
			t.Fatal(err)
		}
	}

	result, err := store.Prune(PruneOptions{State: Succeeded, Cutoff: cutoff})
	if err != nil {
		t.Fatal(err)
	}
	got := recordKeys(result.Records)
	want := []string{"older", "old-a", "old-b"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("prune order = %v, want %v", got, want)
	}
}

func TestPruneRejectsRunningState(t *testing.T) {
	store, err := OpenSQLite(t.TempDir() + "/once.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	if _, err := store.Prune(PruneOptions{State: Running, Cutoff: time.Now()}); err == nil {
		t.Fatal("expected running prune to be rejected")
	}
	if _, err := store.Prune(PruneOptions{State: Succeeded}); err == nil {
		t.Fatal("expected missing cutoff error")
	}
}

func TestReserveFailsWhenDatabaseIsLocked(t *testing.T) {
	path := filepath.Join(t.TempDir(), "once.db")
	store, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.db.Exec("PRAGMA busy_timeout = 50"); err != nil {
		t.Fatal(err)
	}

	locker, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer locker.Close()
	locker.SetMaxOpenConns(1)

	ctx := context.Background()
	conn, err := locker.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	defer conn.ExecContext(ctx, "ROLLBACK")

	if _, _, err := store.Reserve("locked", []string{"true"}); err == nil {
		t.Fatal("Reserve succeeded while database was locked")
	}
	if _, err := store.Get("locked"); err != ErrNotFound {
		t.Fatalf("Get err = %v, want ErrNotFound", err)
	}
}

func TestOpenSQLiteRejectsURIStylePaths(t *testing.T) {
	tests := []string{
		":memory:",
		filepath.Join("x", "..", ":memory:"),
		"file:" + filepath.Join(t.TempDir(), "once.db") + "?mode=rwc",
		filepath.Join("x", "..", "file:"+filepath.Join(t.TempDir(), "once.db")),
		filepath.Join(t.TempDir(), "once.db?mode=memory"),
	}
	for _, path := range tests {
		t.Run(path, func(t *testing.T) {
			store, err := OpenSQLite(path)
			if err == nil {
				_ = store.Close()
				t.Fatalf("OpenSQLite(%q) succeeded, want error", path)
			}
		})
	}
}

func TestOpenSQLiteRejectsSymlinkAncestor(t *testing.T) {
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

	store, err := OpenSQLite(filepath.Join(link, "sub", "once.db"))
	if err == nil {
		_ = store.Close()
		t.Fatal("expected symlink ancestor to be rejected")
	}
}

func TestOpenSQLiteRejectsSymlinkSidecarsBeforeOpen(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires privileges on some Windows installs")
	}

	for _, suffix := range []string{"-wal", "-shm"} {
		t.Run(suffix, func(t *testing.T) {
			dir := t.TempDir()
			db := filepath.Join(dir, "once.db")
			target := filepath.Join(dir, "target")
			if err := os.WriteFile(target, []byte("target"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, db+suffix); err != nil {
				t.Fatal(err)
			}

			store, err := OpenSQLite(db)
			if err == nil {
				_ = store.Close()
				t.Fatalf("expected symlink %s sidecar to be rejected", suffix)
			}
			if _, err := os.Stat(db); !os.IsNotExist(err) {
				t.Fatalf("db exists after rejected sidecar: %v", err)
			}
		})
	}
}

func recordKeys(records []Record) []string {
	keys := make([]string, len(records))
	for i, rec := range records {
		keys[i] = rec.Key
	}
	return keys
}
