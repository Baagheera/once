package once

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	sqlite "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
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

func TestOpenSQLiteMigratesMissingAttemptHash(t *testing.T) {
	path := filepath.Join(t.TempDir(), "once.db")
	createLegacySQLiteStore(t, path)

	store, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	assertSQLiteMigrationStamped(t, store.db)

	rec, err := store.Get("legacy")
	if err != nil {
		t.Fatal(err)
	}
	if rec.Attempt != "" {
		t.Fatalf("legacy attempt = %q, want empty", rec.Attempt)
	}
	if rec.State != Running || len(rec.Command) != 1 || rec.Command[0] != "old" {
		t.Fatalf("legacy record = %#v", rec)
	}
}

func TestOpenSQLiteEnablesWALBeforeMigrationLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "once.db")
	createLegacySQLiteStore(t, path)

	var observedMode string
	var observeErr error
	store, err := openSQLite(path, sqliteInitOptions{
		afterMigrationLock: func() {
			observer, err := sql.Open("sqlite", path)
			if err != nil {
				observeErr = err
				return
			}
			defer observer.Close()
			observeErr = observer.QueryRow("PRAGMA journal_mode").Scan(&observedMode)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if observeErr != nil {
		t.Fatal(observeErr)
	}
	if observedMode != "wal" {
		t.Fatalf("journal_mode during migration lock = %q, want wal", observedMode)
	}
}

func TestOpenSQLiteConcurrentEmptyFirstOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "once.db")
	missingMetadata := make(chan struct{}, 2)
	release := make(chan struct{})
	released := false
	defer func() {
		if !released {
			close(release)
		}
	}()
	results := make(chan sqliteOpenResult, 2)

	for i := 0; i < 2; i++ {
		go func() {
			store, err := openSQLite(path, sqliteInitOptions{
				afterMissingMetadataRead: func() {
					missingMetadata <- struct{}{}
					<-release
				},
			})
			results <- sqliteOpenResult{store: store, err: err}
		}()
	}
	waitSQLiteTestSignal(t, missingMetadata, "first opener to observe missing metadata")
	waitSQLiteTestSignal(t, missingMetadata, "second opener to observe missing metadata")
	close(release)
	released = true

	stores := make([]*SQLiteStore, 0, 2)
	for i := 0; i < 2; i++ {
		result := waitSQLiteOpenResult(t, results)
		if result.err != nil {
			t.Errorf("opener %d: %v", i+1, result.err)
		}
		if result.store != nil {
			defer result.store.Close()
			stores = append(stores, result.store)
		}
	}
	if len(stores) != 2 {
		t.FailNow()
	}

	assertSQLiteMigrationStamped(t, stores[0].db)
	var versionRows int
	if err := stores[0].db.QueryRow(`SELECT count(*) FROM once_meta WHERE key = 'schema_version'`).Scan(&versionRows); err != nil {
		t.Fatal(err)
	}
	if versionRows != 1 {
		t.Fatalf("schema_version row count = %d, want 1", versionRows)
	}
	for i, key := range []string{"first-empty-opener", "second-empty-opener"} {
		assertSQLiteConnectionPragmas(t, stores[i].db, true)
		if _, fresh, err := stores[i].Reserve(key, []string{"true"}); err != nil {
			t.Fatalf("opener %d Reserve: %v", i+1, err)
		} else if !fresh {
			t.Fatalf("opener %d Reserve was not fresh", i+1)
		}
		if _, err := stores[i].Get(key); err != nil {
			t.Fatalf("opener %d Get: %v", i+1, err)
		}
	}
}

func TestOpenSQLiteConcurrentEmptyFirstOpenAcrossProcesses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "once.db")
	locker, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer locker.Close()
	locker.SetMaxOpenConns(1)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	conn, err := locker.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	locked := true
	defer func() {
		if locked {
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}
	}()

	type childProcess struct {
		cmd    *exec.Cmd
		stdout bytes.Buffer
		stderr bytes.Buffer
	}
	const openerCount = 8
	barrierDir := t.TempDir()
	releasePath := filepath.Join(barrierDir, "release")
	children := make([]childProcess, openerCount)
	markers := make([]string, openerCount)
	contentionMarkers := make([]string, openerCount)
	contentionReleasePaths := make([]string, openerCount)
	startedChildren := 0
	childrenFinished := false
	stopChildren := func() {
		if childrenFinished {
			return
		}
		cancel()
		for i := 0; i < startedChildren; i++ {
			_ = children[i].cmd.Wait()
		}
		childrenFinished = true
	}
	defer stopChildren()

	for i := range children {
		markers[i] = filepath.Join(barrierDir, "ready-"+strconv.Itoa(i))
		contentionMarkers[i] = filepath.Join(barrierDir, "contention-"+strconv.Itoa(i))
		contentionReleasePaths[i] = filepath.Join(barrierDir, "contention-release-"+strconv.Itoa(i))
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestOpenSQLiteConcurrentEmptyFirstOpenProcessHelper$", "-test.count=1")
		cmd.Env = append(os.Environ(),
			"ONCE_SQLITE_OPEN_HELPER=1",
			"ONCE_SQLITE_OPEN_PATH="+path,
			"ONCE_SQLITE_OPEN_MARKER="+markers[i],
			"ONCE_SQLITE_OPEN_RELEASE="+releasePath,
			"ONCE_SQLITE_OPEN_CONTENTION_MARKER="+contentionMarkers[i],
			"ONCE_SQLITE_OPEN_CONTENTION_RELEASE="+contentionReleasePaths[i],
		)
		children[i].cmd = cmd
		cmd.Stdout = &children[i].stdout
		cmd.Stderr = &children[i].stderr
		if err := cmd.Start(); err != nil {
			t.Fatalf("start opener %d: %v", i+1, err)
		}
		startedChildren++
	}

	childOutput := func() string {
		var output strings.Builder
		for i := range children {
			output.WriteString("opener ")
			output.WriteString(strconv.Itoa(i + 1))
			output.WriteString(" stdout:\n")
			output.WriteString(children[i].stdout.String())
			output.WriteString("\nstderr:\n")
			output.WriteString(children[i].stderr.String())
			output.WriteByte('\n')
		}
		return output.String()
	}
	failBarrier := func(message string) {
		stopChildren()
		t.Fatalf("%s\n%s", message, childOutput())
	}
	waitForMarkers := func(paths []string, description string) {
		deadline := time.Now().Add(10 * time.Second)
		for i, marker := range paths {
			for {
				if _, err := os.Stat(marker); err == nil {
					break
				} else if !errors.Is(err, os.ErrNotExist) {
					failBarrier("stat opener " + strconv.Itoa(i+1) + " " + description + " marker: " + err.Error())
				}
				if time.Now().After(deadline) {
					failBarrier("timed out waiting for opener " + strconv.Itoa(i+1) + " to " + description)
				}
				select {
				case <-ctx.Done():
					failBarrier("context ended waiting for opener " + strconv.Itoa(i+1) + " to " + description + ": " + ctx.Err().Error())
				case <-time.After(5 * time.Millisecond):
				}
			}
		}
	}

	waitForMarkers(markers, "read missing metadata")
	if err := os.WriteFile(releasePath, []byte("release"), 0o600); err != nil {
		failBarrier("release subprocesses after metadata barrier: " + err.Error())
	}
	waitForMarkers(contentionMarkers, "observe WAL transition contention")
	if _, err := conn.ExecContext(ctx, "ROLLBACK"); err != nil {
		failBarrier("release SQLite lock: " + err.Error())
	}
	locked = false

	// Complete one opener at a time so the 1 ms timeout isolates the WAL
	// transition instead of creating unrelated migration-lock failures.
	for i := range children {
		if err := os.WriteFile(contentionReleasePaths[i], []byte("release"), 0o600); err != nil {
			failBarrier("release opener " + strconv.Itoa(i+1) + " after contention barrier: " + err.Error())
		}
		if err := children[i].cmd.Wait(); err != nil {
			t.Errorf("opener %d: %v\nstdout:\n%s\nstderr:\n%s", i+1, err, children[i].stdout.String(), children[i].stderr.String())
		}
	}
	childrenFinished = true
	if t.Failed() {
		return
	}

	store, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	assertSQLiteMigrationStamped(t, store.db)
}

func TestOpenSQLiteConcurrentEmptyFirstOpenProcessHelper(t *testing.T) {
	if os.Getenv("ONCE_SQLITE_OPEN_HELPER") != "1" {
		return
	}

	marker := os.Getenv("ONCE_SQLITE_OPEN_MARKER")
	release := os.Getenv("ONCE_SQLITE_OPEN_RELEASE")
	contentionMarker := os.Getenv("ONCE_SQLITE_OPEN_CONTENTION_MARKER")
	contentionRelease := os.Getenv("ONCE_SQLITE_OPEN_CONTENTION_RELEASE")
	waitForRelease := func(path, description string) {
		deadline := time.Now().Add(10 * time.Second)
		for {
			if _, err := os.Stat(path); err == nil {
				return
			} else if !errors.Is(err, os.ErrNotExist) {
				t.Fatal(err)
			}
			if time.Now().After(deadline) {
				t.Fatal("timed out waiting for " + description)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	store, err := openSQLite(os.Getenv("ONCE_SQLITE_OPEN_PATH"), sqliteInitOptions{
		afterMissingMetadataRead: func() {
			if err := os.WriteFile(marker, []byte("ready"), 0o600); err != nil {
				t.Fatal(err)
			}
			waitForRelease(release, "subprocess metadata release")
		},
		afterWALTransitionContention: func() {
			if err := os.WriteFile(contentionMarker, []byte("contention"), 0o600); err != nil {
				t.Fatal(err)
			}
			waitForRelease(contentionRelease, "subprocess contention release")
		},
		migrationBusyTimeoutMS: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestOpenSQLiteSerializesConcurrentLegacyMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "once.db")
	createLegacySQLiteStore(t, path)

	firstLocked := make(chan struct{})
	releaseFirst := make(chan struct{})
	firstReleased := false
	defer func() {
		if !firstReleased {
			close(releaseFirst)
		}
	}()
	secondSawMissing := make(chan struct{})
	secondLocked := make(chan struct{})

	firstResult := make(chan sqliteOpenResult, 1)
	secondResult := make(chan sqliteOpenResult, 1)

	go func() {
		store, err := openSQLite(path, sqliteInitOptions{
			afterMigrationLock: func() {
				close(firstLocked)
				<-releaseFirst
			},
		})
		firstResult <- sqliteOpenResult{store: store, err: err}
	}()
	waitSQLiteTestSignal(t, firstLocked, "first opener to acquire migration lock")

	go func() {
		store, err := openSQLite(path, sqliteInitOptions{
			afterMissingMetadataRead: func() {
				close(secondSawMissing)
			},
			afterMigrationLock: func() {
				close(secondLocked)
			},
		})
		secondResult <- sqliteOpenResult{store: store, err: err}
	}()
	waitSQLiteTestSignal(t, secondSawMissing, "second opener to observe missing metadata")
	close(releaseFirst)
	firstReleased = true
	waitSQLiteTestSignal(t, secondLocked, "second opener to acquire migration lock")

	var stores []*SQLiteStore
	for i, resultCh := range []<-chan sqliteOpenResult{firstResult, secondResult} {
		result := waitSQLiteOpenResult(t, resultCh)
		if result.err != nil {
			t.Fatalf("opener %d: %v", i+1, result.err)
		}
		defer result.store.Close()
		stores = append(stores, result.store)
	}
	assertSQLiteMigrationStamped(t, stores[0].db)

	legacy, err := stores[0].Get("legacy")
	if err != nil {
		t.Fatal(err)
	}
	if legacy.State != Running || len(legacy.Command) != 1 || legacy.Command[0] != "old" {
		t.Fatalf("legacy record = %#v", legacy)
	}
	for i, key := range []string{"first-opener", "second-opener"} {
		if _, fresh, err := stores[i].Reserve(key, []string{"true"}); err != nil {
			t.Fatalf("opener %d Reserve: %v", i+1, err)
		} else if !fresh {
			t.Fatalf("opener %d Reserve was not fresh", i+1)
		}
		if _, err := stores[i].Get(key); err != nil {
			t.Fatalf("opener %d Get: %v", i+1, err)
		}
	}
	var versionRows int
	if err := stores[0].db.QueryRow(`SELECT count(*) FROM once_meta WHERE key = 'schema_version'`).Scan(&versionRows); err != nil {
		t.Fatal(err)
	}
	if versionRows != 1 {
		t.Fatalf("schema_version row count = %d, want 1", versionRows)
	}
}

func TestOpenSQLiteLockedLegacyMigrationIsAtomic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "once.db")
	createLegacySQLiteStore(t, path)

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
	var journalMode string
	if err := conn.QueryRowContext(ctx, "PRAGMA journal_mode = WAL").Scan(&journalMode); err != nil {
		t.Fatal(err)
	}
	if journalMode != "wal" {
		t.Fatalf("journal_mode = %q, want wal", journalMode)
	}
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	locked := true
	defer func() {
		if locked {
			_, _ = conn.ExecContext(ctx, "ROLLBACK")
		}
	}()

	store, err := openSQLite(path, sqliteInitOptions{migrationBusyTimeoutMS: 1})
	if err == nil {
		_ = store.Close()
		t.Fatal("openSQLite succeeded while legacy migration was locked")
	}
	var sqliteErr *sqlite.Error
	if !errors.As(err, &sqliteErr) {
		t.Fatalf("openSQLite err = %T %v, want *sqlite.Error", err, err)
	}
	baseCode := sqliteErr.Code() & 0xff
	if baseCode != sqlite3.SQLITE_BUSY && baseCode != sqlite3.SQLITE_LOCKED {
		t.Fatalf("sqlite base code = %d, want SQLITE_BUSY or SQLITE_LOCKED", baseCode)
	}

	var metaCount int
	if err := conn.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'once_meta'`).Scan(&metaCount); err != nil {
		t.Fatal(err)
	}
	if metaCount != 0 {
		t.Fatalf("once_meta table count = %d, want 0", metaCount)
	}
	var columnCount int
	if err := conn.QueryRowContext(ctx, `SELECT count(*) FROM pragma_table_info('once_records') WHERE name = 'attempt_hash'`).Scan(&columnCount); err != nil {
		t.Fatal(err)
	}
	if columnCount != 0 {
		t.Fatalf("attempt_hash column count = %d, want 0", columnCount)
	}

	if _, err := conn.ExecContext(ctx, "ROLLBACK"); err != nil {
		t.Fatal(err)
	}
	locked = false

	store, err = OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	assertSQLiteMigrationStamped(t, store.db)
}

func TestOpenSQLiteRejectsMetadataWithoutSchemaVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "once.db")
	createLegacySQLiteStore(t, path)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
CREATE TABLE once_meta (
	key TEXT PRIMARY KEY,
	value TEXT NOT NULL
);
`); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	store, err := OpenSQLite(path)
	if err == nil {
		_ = store.Close()
		t.Fatal("OpenSQLite succeeded with metadata missing schema_version")
	}
	var missingVersion interface {
		error
		sqliteSchemaVersionMissing()
	}
	if !errors.As(err, &missingVersion) {
		t.Fatalf("OpenSQLite err = %T %v, want missing-version sentinel", err, err)
	}
	if !errors.Is(err, errSQLiteSchemaVersionMissing) {
		t.Fatalf("OpenSQLite err = %v, want errSQLiteSchemaVersionMissing", err)
	}

	db, err = sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	var journalMode string
	if err := db.QueryRow("PRAGMA journal_mode").Scan(&journalMode); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if journalMode != "delete" {
		_ = db.Close()
		t.Fatalf("journal_mode = %q, want delete", journalMode)
	}
	var columnCount int
	if err := db.QueryRow(`SELECT count(*) FROM pragma_table_info('once_records') WHERE name = 'attempt_hash'`).Scan(&columnCount); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if columnCount != 0 {
		_ = db.Close()
		t.Fatalf("attempt_hash column count = %d, want 0", columnCount)
	}
	var versionRows int
	if err := db.QueryRow(`SELECT count(*) FROM once_meta WHERE key = 'schema_version'`).Scan(&versionRows); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if versionRows != 0 {
		_ = db.Close()
		t.Fatalf("schema_version row count = %d, want 0", versionRows)
	}
	var state string
	var command string
	if err := db.QueryRow(`SELECT state, command FROM once_records WHERE key = 'legacy'`).Scan(&state, &command); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if state != "running" || command != `["old"]` {
		_ = db.Close()
		t.Fatalf("legacy state=%q command=%q, want running and [\"old\"]", state, command)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	for _, sidecar := range []string{path + "-wal", path + "-shm", path + "-journal"} {
		if _, err := os.Stat(sidecar); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("sidecar %s exists or stat failed: %v", sidecar, err)
		}
	}
}

func TestOpenSQLiteRejectsMalformedRecordSchemaWithoutMutation(t *testing.T) {
	tests := []struct {
		name           string
		options        malformedSQLiteSchemaOptions
		wantErr        string
		wantMetaTables int
	}{
		{
			name: "unversioned current schema without primary key",
			options: malformedSQLiteSchemaOptions{
				attemptHash:     true,
				exitCodeDefault: true,
				stateIndex:      true,
			},
			wantErr: "key column is not the primary key",
		},
		{
			name: "unversioned legacy schema without primary key",
			options: malformedSQLiteSchemaOptions{
				exitCodeDefault: true,
				stateIndex:      true,
			},
			wantErr: "key column is not the primary key",
		},
		{
			name: "current version without primary key",
			options: malformedSQLiteSchemaOptions{
				attemptHash:     true,
				exitCodeDefault: true,
				stateIndex:      true,
				metadata:        true,
			},
			wantErr:        "key column is not the primary key",
			wantMetaTables: 1,
		},
		{
			name: "current version with legacy record schema",
			options: malformedSQLiteSchemaOptions{
				primaryKey:      true,
				exitCodeDefault: true,
				stateIndex:      true,
				metadata:        true,
			},
			wantErr:        "once_records missing columns: attempt_hash",
			wantMetaTables: 1,
		},
		{
			name: "unversioned current schema without required default",
			options: malformedSQLiteSchemaOptions{
				primaryKey:  true,
				attemptHash: true,
				stateIndex:  true,
			},
			wantErr: "exit_code column default is <none>, want 0",
		},
		{
			name: "current version without required default",
			options: malformedSQLiteSchemaOptions{
				primaryKey:  true,
				attemptHash: true,
				stateIndex:  true,
				metadata:    true,
			},
			wantErr:        "exit_code column default is <none>, want 0",
			wantMetaTables: 1,
		},
		{
			name: "current version without state index",
			options: malformedSQLiteSchemaOptions{
				primaryKey:      true,
				attemptHash:     true,
				exitCodeDefault: true,
				metadata:        true,
			},
			wantErr:        "once_records_state_idx index is missing",
			wantMetaTables: 1,
		},
		{
			name: "current version with composite primary key",
			options: malformedSQLiteSchemaOptions{
				compositePrimaryKey: true,
				attemptHash:         true,
				exitCodeDefault:     true,
				stateIndex:          true,
				metadata:            true,
			},
			wantErr:        "key column is not the sole primary key",
			wantMetaTables: 1,
		},
		{
			name: "current version with unsupported column",
			options: malformedSQLiteSchemaOptions{
				primaryKey:      true,
				attemptHash:     true,
				exitCodeDefault: true,
				stateIndex:      true,
				metadata:        true,
				extraColumn:     true,
			},
			wantErr:        "unsupported columns: extra",
			wantMetaTables: 1,
		},
		{
			name: "current version with wrong state index",
			options: malformedSQLiteSchemaOptions{
				primaryKey:      true,
				attemptHash:     true,
				exitCodeDefault: true,
				stateIndex:      true,
				wrongStateIndex: true,
				metadata:        true,
			},
			wantErr:        "once_records_state_idx index must index only state",
			wantMetaTables: 1,
		},
		{
			name: "unversioned schema with wrong state index",
			options: malformedSQLiteSchemaOptions{
				primaryKey:      true,
				attemptHash:     true,
				exitCodeDefault: true,
				stateIndex:      true,
				wrongStateIndex: true,
			},
			wantErr: "once_records_state_idx index must index only state",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "once.db")
			createMalformedSQLiteStore(t, path, tt.options)

			store, err := OpenSQLite(path)
			if err == nil {
				_ = store.Close()
				t.Fatal("OpenSQLite succeeded with malformed once_records schema")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("OpenSQLite err = %v, want %q", err, tt.wantErr)
			}

			db, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()

			var journalMode string
			if err := db.QueryRow("PRAGMA journal_mode").Scan(&journalMode); err != nil {
				t.Fatal(err)
			}
			if journalMode != "delete" {
				t.Fatalf("journal_mode = %q, want delete", journalMode)
			}

			var metaTables int
			if err := db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'once_meta'").Scan(&metaTables); err != nil {
				t.Fatal(err)
			}
			if metaTables != tt.wantMetaTables {
				t.Fatalf("once_meta table count = %d, want %d", metaTables, tt.wantMetaTables)
			}

			var attemptColumns int
			if err := db.QueryRow("SELECT count(*) FROM pragma_table_info('once_records') WHERE name = 'attempt_hash'").Scan(&attemptColumns); err != nil {
				t.Fatal(err)
			}
			wantAttemptColumns := 0
			if tt.options.attemptHash {
				wantAttemptColumns = 1
			}
			if attemptColumns != wantAttemptColumns {
				t.Fatalf("attempt_hash column count = %d, want %d", attemptColumns, wantAttemptColumns)
			}
		})
	}
}

func TestOpenSQLiteRepairsSupportedUnversionedSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "once.db")
	createMalformedSQLiteStore(t, path, malformedSQLiteSchemaOptions{
		primaryKey:      true,
		attemptHash:     true,
		exitCodeDefault: true,
	})

	store, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	assertSQLiteMigrationStamped(t, store.db)
	var indexCount int
	if err := store.db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type = 'index' AND name = 'once_records_state_idx'").Scan(&indexCount); err != nil {
		t.Fatal(err)
	}
	if indexCount != 1 {
		t.Fatalf("state index count = %d, want 1", indexCount)
	}
}

func TestOpenSQLiteCurrentSchemaDoesNotTakeMigrationLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "once.db")
	store, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
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

	tookMigrationLock := false
	store, err = openSQLite(path, sqliteInitOptions{
		migrationBusyTimeoutMS: 1,
		afterMigrationLock: func() {
			tookMigrationLock = true
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if tookMigrationLock {
		t.Fatal("current schema took migration lock")
	}
}

func TestOpenSQLiteConcurrentCurrentSchemaDeleteMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "once.db")
	store, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	const (
		rounds      = 8
		openerCount = 16
	)
	for round := 0; round < rounds; round++ {
		db, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatal(err)
		}
		var journalMode string
		if err := db.QueryRow("PRAGMA journal_mode = DELETE").Scan(&journalMode); err != nil {
			_ = db.Close()
			t.Fatal(err)
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
		if journalMode != "delete" {
			t.Fatalf("round %d journal_mode = %q, want delete", round+1, journalMode)
		}

		start := make(chan struct{})
		results := make(chan sqliteOpenResult, openerCount)
		for i := 0; i < openerCount; i++ {
			go func() {
				<-start
				store, err := OpenSQLite(path)
				results <- sqliteOpenResult{store: store, err: err}
			}()
		}
		close(start)

		stores := make([]*SQLiteStore, 0, openerCount)
		for i := 0; i < openerCount; i++ {
			result := waitSQLiteOpenResult(t, results)
			if result.err != nil {
				t.Errorf("round %d opener %d: %v", round+1, i+1, result.err)
			}
			if result.store != nil {
				stores = append(stores, result.store)
			}
		}
		for _, store := range stores {
			if err := store.Close(); err != nil {
				t.Error(err)
			}
		}
		if t.Failed() {
			return
		}
	}
}

func TestConfigureSQLiteDurabilityStopsAtContextDeadline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "once.db")
	locker, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer locker.Close()
	locker.SetMaxOpenConns(1)

	ctx := context.Background()
	lockerConn, err := locker.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer lockerConn.Close()
	if _, err := lockerConn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	defer lockerConn.ExecContext(ctx, "ROLLBACK")

	contender, err := sql.Open(sqliteDriverName, path)
	if err != nil {
		t.Fatal(err)
	}
	defer contender.Close()
	contender.SetMaxOpenConns(1)
	contenderConn, err := contender.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer contenderConn.Close()

	retryCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	started := time.Now()
	err = configureSQLiteDurability(retryCtx, contenderConn, nil)
	elapsed := time.Since(started)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("configureSQLiteDurability err = %T %v, want context deadline exceeded", err, err)
	}
	if elapsed > time.Second {
		t.Fatalf("configureSQLiteDurability elapsed = %v, want at most 1s", elapsed)
	}
}

func TestOpenSQLiteConfiguresOperationalConnection(t *testing.T) {
	store, err := OpenSQLite(filepath.Join(t.TempDir(), "once.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	assertSQLiteConnectionPragmas(t, store.db, true)
}

func TestOpenSQLiteInitializesSchemaVersion(t *testing.T) {
	store, err := OpenSQLite(t.TempDir() + "/once.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	var version string
	if err := store.db.QueryRow(`SELECT value FROM once_meta WHERE key = 'schema_version'`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != sqliteSchemaVersion {
		t.Fatalf("schema version = %q, want %q", version, sqliteSchemaVersion)
	}
}

func TestOpenSQLiteReappliesConnectionPragmasAfterReplacement(t *testing.T) {
	store, err := OpenSQLite(filepath.Join(t.TempDir(), "once.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ctx := context.Background()
	conn, err := store.db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, pragma := range []string{
		"PRAGMA busy_timeout = 1",
		"PRAGMA synchronous = NORMAL",
		"PRAGMA foreign_keys = OFF",
	} {
		if _, err := conn.ExecContext(ctx, pragma); err != nil {
			_ = conn.Close()
			t.Fatal(err)
		}
	}
	if err := conn.Raw(func(any) error { return driver.ErrBadConn }); !errors.Is(err, driver.ErrBadConn) {
		_ = conn.Close()
		t.Fatalf("discard connection err = %v, want %v", err, driver.ErrBadConn)
	}
	_ = conn.Close()

	assertSQLiteConnectionPragmas(t, store.db, false)
}

func TestOpenSQLiteRejectsIncompatibleSchemaWithoutMutation(t *testing.T) {
	tests := []struct {
		name    string
		version string
		wantErr string
	}{
		{name: "newer", version: "999", wantErr: "newer sqlite schema"},
		{name: "older", version: "0", wantErr: "older sqlite schema"},
		{name: "invalid", version: "invalid", wantErr: "invalid sqlite schema version"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "once.db")
			db, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`
CREATE TABLE once_meta (
	key TEXT PRIMARY KEY,
	value TEXT NOT NULL
);
INSERT INTO once_meta (key, value) VALUES ('schema_version', ?);
`, tt.version); err != nil {
				_ = db.Close()
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			if err := RestrictLocalFile(path); err != nil {
				t.Fatal(err)
			}

			store, err := OpenSQLite(path)
			if err == nil {
				_ = store.Close()
				t.Fatalf("OpenSQLite succeeded, want %s schema error", tt.name)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v, want %q", err, tt.wantErr)
			}

			db, err = sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			var recordTableCount int
			if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'once_records'`).Scan(&recordTableCount); err != nil {
				t.Fatal(err)
			}
			if recordTableCount != 0 {
				t.Fatalf("once_records table count = %d, want 0", recordTableCount)
			}
			var journalMode string
			if err := db.QueryRow("PRAGMA journal_mode").Scan(&journalMode); err != nil {
				t.Fatal(err)
			}
			if journalMode != "delete" {
				t.Fatalf("journal_mode = %q, want delete", journalMode)
			}
			for _, sidecar := range []string{path + "-wal", path + "-shm"} {
				if _, err := os.Stat(sidecar); !os.IsNotExist(err) {
					t.Fatalf("sidecar %s exists or stat failed: %v", sidecar, err)
				}
			}
		})
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

func TestForEachRecordPreservesListOrderAndStopsOnError(t *testing.T) {
	store, err := OpenSQLite(t.TempDir() + "/once.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	base := time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC)
	for _, row := range []struct {
		key string
		at  time.Time
	}{
		{key: "old", at: base},
		{key: "new-b", at: base.Add(time.Second)},
		{key: "new-a", at: base.Add(time.Second)},
	} {
		if _, err := store.db.Exec(`
INSERT INTO once_records (key, attempt_hash, state, command, started_at, updated_at)
VALUES (?, ?, 'running', '[]', ?, ?)
`, row.key, row.key+"-attempt", formatTime(row.at), formatTime(row.at)); err != nil {
			t.Fatal(err)
		}
	}

	stop := errors.New("stop visiting")
	var keys []string
	err = store.ForEachRecord(ListOptions{State: Running}, func(rec Record) error {
		keys = append(keys, rec.Key)
		if len(keys) == 2 {
			return stop
		}
		return nil
	})
	if !errors.Is(err, stop) {
		t.Fatalf("ForEachRecord error = %v, want %v", err, stop)
	}
	want := []string{"new-a", "new-b"}
	if strings.Join(keys, ",") != strings.Join(want, ",") {
		t.Fatalf("visited keys = %v, want %v", keys, want)
	}
	if err := store.ForEachRecord(ListOptions{}, nil); err == nil {
		t.Fatal("expected nil visitor error")
	}
}

func TestListFiltersByUpdatedBefore(t *testing.T) {
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
		key string
		at  time.Time
	}{
		{key: "old-running", at: old},
		{key: "recent-running", at: recent},
	} {
		if _, err := store.db.Exec(`
INSERT INTO once_records (key, attempt_hash, state, command, started_at, updated_at)
VALUES (?, ?, 'running', '[]', ?, ?)
`, row.key, row.key+"-attempt", formatTime(row.at), formatTime(row.at)); err != nil {
			t.Fatal(err)
		}
	}

	records, err := store.List(ListOptions{State: Running, UpdatedBefore: cutoff})
	if err != nil {
		t.Fatal(err)
	}
	got := recordKeys(records)
	want := []string{"old-running"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("records = %v, want %v", got, want)
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

func TestPruneForceCommitsCompletedBatches(t *testing.T) {
	const completedBatches = 2
	const completedRecords = pruneBatchSize * completedBatches

	store, err := OpenSQLite(t.TempDir() + "/once.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	base := time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC)
	tx, err := store.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	stmt, err := tx.Prepare(`
INSERT INTO once_records (key, attempt_hash, state, exit_code, command, started_at, finished_at, updated_at)
VALUES (?, ?, 'succeeded', 0, '[]', ?, ?, ?)
`)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < completedRecords+2; i++ {
		key := fmt.Sprintf("row-%04d", i)
		at := base.Add(time.Duration(i) * time.Nanosecond)
		if _, err := stmt.Exec(key, key+"-attempt", formatTime(at), formatTime(at), formatTime(at)); err != nil {
			t.Fatal(err)
		}
	}
	if err := stmt.Close(); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	blockedKey := fmt.Sprintf("row-%04d", completedRecords)
	if _, err := store.db.Exec(fmt.Sprintf(`
CREATE TRIGGER stop_prune BEFORE DELETE ON once_records
WHEN OLD.key = '%s'
BEGIN
	SELECT RAISE(ABORT, 'stop prune');
END;
`, blockedKey)); err != nil {
		t.Fatal(err)
	}

	result, err := store.Prune(PruneOptions{
		State:  Succeeded,
		Cutoff: base.Add(time.Hour),
		Force:  true,
	})
	if err == nil {
		t.Fatal("expected second prune batch to fail")
	}
	if result.Deleted != completedRecords {
		t.Fatalf("deleted before failure = %d, want %d", result.Deleted, completedRecords)
	}
	if _, err := store.Get("row-0000"); err != ErrNotFound {
		t.Fatalf("first batch was not committed: %v", err)
	}
	if _, err := store.Get(blockedKey); err != nil {
		t.Fatalf("failed batch removed blocked record: %v", err)
	}
	lastKey := fmt.Sprintf("row-%04d", completedRecords+1)
	if _, err := store.Get(lastKey); err != nil {
		t.Fatalf("failed batch removed later record: %v", err)
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

	stop := errors.New("stop visiting")
	var visited []string
	err = store.ForEachPruneCandidate(PruneOptions{State: Succeeded, Cutoff: cutoff}, func(rec Record) error {
		visited = append(visited, rec.Key)
		if len(visited) == 2 {
			return stop
		}
		return nil
	})
	if !errors.Is(err, stop) {
		t.Fatalf("ForEachPruneCandidate error = %v, want %v", err, stop)
	}
	wantVisited := []string{"older", "old-a"}
	if strings.Join(visited, ",") != strings.Join(wantVisited, ",") {
		t.Fatalf("visited prune candidates = %v, want %v", visited, wantVisited)
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

	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
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

func TestRestrictSQLiteFilesRejectsMissingMainDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.db")
	if err := restrictSQLiteFiles(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("restrictSQLiteFiles err = %v, want %v", err, os.ErrNotExist)
	}
}

func TestSQLiteFilePathsIncludesRollbackJournal(t *testing.T) {
	path := filepath.Join("store", "once.db")
	want := []string{path, path + "-wal", path + "-shm", path + "-journal"}
	got := sqliteFilePaths(path)
	if len(got) != len(want) {
		t.Fatalf("sqliteFilePaths() = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sqliteFilePaths()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func recordKeys(records []Record) []string {
	keys := make([]string, len(records))
	for i, rec := range records {
		keys[i] = rec.Key
	}
	return keys
}

type sqlitePragmaQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type sqliteOpenResult struct {
	store *SQLiteStore
	err   error
}

type malformedSQLiteSchemaOptions struct {
	primaryKey          bool
	compositePrimaryKey bool
	attemptHash         bool
	exitCodeDefault     bool
	stateIndex          bool
	wrongStateIndex     bool
	metadata            bool
	extraColumn         bool
}

func createMalformedSQLiteStore(t *testing.T, path string, options malformedSQLiteSchemaOptions) {
	t.Helper()

	keyDefinition := "key TEXT"
	if options.primaryKey {
		keyDefinition = "key TEXT PRIMARY KEY"
	}
	primaryKeyConstraint := ""
	if options.compositePrimaryKey {
		primaryKeyConstraint = ", PRIMARY KEY (key, state)"
	}
	attemptDefinition := ""
	if options.attemptHash {
		attemptDefinition = "attempt_hash TEXT NOT NULL DEFAULT '',"
	}
	exitCodeDefinition := "exit_code INTEGER NOT NULL"
	if options.exitCodeDefault {
		exitCodeDefinition += " DEFAULT 0"
	}
	extraDefinition := ""
	if options.extraColumn {
		extraDefinition = "extra TEXT,"
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE once_records (` +
		keyDefinition + `,
` + attemptDefinition + `
	state TEXT NOT NULL CHECK (state IN ('running', 'succeeded', 'failed')),
	` + exitCodeDefinition + `,
	stdout BLOB NOT NULL DEFAULT X'',
	stderr BLOB NOT NULL DEFAULT X'',
	error TEXT NOT NULL DEFAULT '',
	command TEXT NOT NULL DEFAULT '[]',
	` + extraDefinition + `
	started_at TEXT NOT NULL,
	finished_at TEXT,
	updated_at TEXT NOT NULL` + primaryKeyConstraint + `
);`); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if options.stateIndex {
		indexedColumn := "state"
		if options.wrongStateIndex {
			indexedColumn = "key"
		}
		if _, err := db.Exec("CREATE INDEX once_records_state_idx ON once_records(" + indexedColumn + ")"); err != nil {
			_ = db.Close()
			t.Fatal(err)
		}
	}
	if options.metadata {
		if _, err := db.Exec(`
CREATE TABLE once_meta (
	key TEXT PRIMARY KEY,
	value TEXT NOT NULL
);
INSERT INTO once_meta (key, value) VALUES ('schema_version', '1');
`); err != nil {
			_ = db.Close()
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := RestrictLocalFile(path); err != nil {
		t.Fatal(err)
	}
}

func createLegacySQLiteStore(t *testing.T, path string) {
	t.Helper()

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err := db.Exec(`
CREATE TABLE once_records (
	key TEXT PRIMARY KEY,
	state TEXT NOT NULL CHECK (state IN ('running', 'succeeded', 'failed')),
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
INSERT INTO once_records (key, state, command, started_at, updated_at)
VALUES ('legacy', 'running', '["old"]', ?, ?);
`, formatTime(now), formatTime(now)); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := RestrictLocalFile(path); err != nil {
		t.Fatal(err)
	}
}

func waitSQLiteTestSignal(t *testing.T, signal <-chan struct{}, description string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", description)
	}
}

func waitSQLiteOpenResult(t *testing.T, result <-chan sqliteOpenResult) sqliteOpenResult {
	t.Helper()
	select {
	case got := <-result:
		return got
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for SQLite opener")
		return sqliteOpenResult{}
	}
}

func assertSQLiteMigrationStamped(t *testing.T, db sqlitePragmaQueryer) {
	t.Helper()

	ctx := context.Background()
	var columnCount int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM pragma_table_info('once_records') WHERE name = 'attempt_hash'`).Scan(&columnCount); err != nil {
		t.Fatal(err)
	}
	if columnCount != 1 {
		t.Fatalf("attempt_hash column count = %d, want 1", columnCount)
	}
	var version string
	if err := db.QueryRowContext(ctx, `SELECT value FROM once_meta WHERE key = 'schema_version'`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != sqliteSchemaVersion {
		t.Fatalf("schema version = %q, want %q", version, sqliteSchemaVersion)
	}
}

func assertSQLiteConnectionPragmas(t *testing.T, db sqlitePragmaQueryer, checkJournal bool) {
	t.Helper()

	ctx := context.Background()
	if checkJournal {
		var journalMode string
		if err := db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journalMode); err != nil {
			t.Fatal(err)
		}
		if journalMode != "wal" {
			t.Errorf("journal_mode = %q, want wal", journalMode)
		}
	}

	for _, check := range []struct {
		name  string
		query string
		want  int
	}{
		{name: "busy_timeout", query: "PRAGMA busy_timeout", want: 5000},
		{name: "synchronous", query: "PRAGMA synchronous", want: 2},
		{name: "foreign_keys", query: "PRAGMA foreign_keys", want: 1},
	} {
		var got int
		if err := db.QueryRowContext(ctx, check.query).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != check.want {
			t.Errorf("%s = %d, want %d", check.name, got, check.want)
		}
	}
}
