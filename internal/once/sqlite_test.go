package once

import (
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

	rec, err = store.Commit("k1", Succeeded, 0, []byte("hello\n"), nil, "")
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

	if _, _, err := store.Reserve("k1", []string{"true"}); err != nil {
		t.Fatal(err)
	}

	ok, err := store.Forget("k1")
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
