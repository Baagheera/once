package once

import (
	"sync"
	"sync/atomic"
	"testing"
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
