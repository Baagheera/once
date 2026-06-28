package once

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

type SQLiteStore struct {
	db *sql.DB
}

func OpenSQLite(path string) (*SQLiteStore, error) {
	if path == "" {
		return nil, fmt.Errorf("empty sqlite path")
	}
	if dir := filepath.Dir(path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)

	s := &SQLiteStore{db: db}
	if err := s.init(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

func (s *SQLiteStore) init() error {
	pragmas := []string{
		"PRAGMA journal_mode = WAL",
		"PRAGMA busy_timeout = 5000",
		"PRAGMA foreign_keys = ON",
	}
	for _, pragma := range pragmas {
		if _, err := s.db.Exec(pragma); err != nil {
			return err
		}
	}

	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS once_records (
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

CREATE INDEX IF NOT EXISTS once_records_state_idx ON once_records(state);
`)
	return err
}

func (s *SQLiteStore) Reserve(key string, command []string) (Record, bool, error) {
	if key == "" {
		return Record{}, false, fmt.Errorf("empty key")
	}

	now := time.Now().UTC()
	commandJSON, err := json.Marshal(command)
	if err != nil {
		return Record{}, false, err
	}

	res, err := s.db.Exec(`
INSERT OR IGNORE INTO once_records
	(key, state, command, started_at, updated_at)
VALUES
	(?, 'running', ?, ?, ?)
`, key, string(commandJSON), formatTime(now), formatTime(now))
	if err != nil {
		return Record{}, false, err
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return Record{}, false, err
	}

	rec, err := s.Get(key)
	if err != nil {
		return Record{}, false, err
	}
	if affected == 0 && len(command) != 0 && len(rec.Command) != 0 && !sameCommand(rec.Command, command) {
		return rec, false, ErrConflict
	}
	return rec, affected == 1, nil
}

func (s *SQLiteStore) Commit(key string, state State, exitCode int, stdout, stderr []byte, runErr string) (Record, error) {
	if state != Succeeded && state != Failed {
		return Record{}, fmt.Errorf("invalid terminal state: %s", state)
	}
	if stdout == nil {
		stdout = []byte{}
	}
	if stderr == nil {
		stderr = []byte{}
	}

	now := time.Now().UTC()
	res, err := s.db.Exec(`
UPDATE once_records
SET state = ?,
	exit_code = ?,
	stdout = ?,
	stderr = ?,
	error = ?,
	finished_at = ?,
	updated_at = ?
WHERE key = ? AND state = 'running'
`, string(state), exitCode, stdout, stderr, runErr, formatTime(now), formatTime(now), key)
	if err != nil {
		return Record{}, err
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return Record{}, err
	}
	if affected == 0 {
		rec, err := s.Get(key)
		if errors.Is(err, ErrNotFound) {
			return Record{}, ErrNotFound
		}
		if err != nil {
			return Record{}, err
		}
		if rec.State == Running {
			return Record{}, ErrConflict
		}
		if sameResult(rec, state, exitCode, stdout, stderr, runErr) {
			return rec, nil
		}
		return Record{}, ErrConflict
	}

	return s.Get(key)
}

func (s *SQLiteStore) Get(key string) (Record, error) {
	row := s.db.QueryRow(`
SELECT key, state, exit_code, stdout, stderr, error, command, started_at, finished_at, updated_at
FROM once_records
WHERE key = ?
`, key)

	var rec Record
	var state string
	var commandJSON string
	var startedAt string
	var finishedAt sql.NullString
	var updatedAt string

	err := row.Scan(
		&rec.Key,
		&state,
		&rec.ExitCode,
		&rec.Stdout,
		&rec.Stderr,
		&rec.Error,
		&commandJSON,
		&startedAt,
		&finishedAt,
		&updatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Record{}, ErrNotFound
	}
	if err != nil {
		return Record{}, err
	}

	rec.State = State(state)
	if err := json.Unmarshal([]byte(commandJSON), &rec.Command); err != nil {
		return Record{}, err
	}
	if rec.StartedAt, err = parseTime(startedAt); err != nil {
		return Record{}, err
	}
	if finishedAt.Valid {
		t, err := parseTime(finishedAt.String)
		if err != nil {
			return Record{}, err
		}
		rec.FinishedAt = &t
	}
	if rec.UpdatedAt, err = parseTime(updatedAt); err != nil {
		return Record{}, err
	}

	return rec, nil
}

func (s *SQLiteStore) Forget(key string, force bool) (bool, error) {
	if !force {
		rec, err := s.Get(key)
		if errors.Is(err, ErrNotFound) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if rec.State == Running {
			return false, ErrRunning
		}
	}

	res, err := s.db.Exec(`DELETE FROM once_records WHERE key = ?`, key)
	if err != nil {
		return false, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected != 0, nil
}

func formatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

func parseTime(s string) (time.Time, error) {
	return time.Parse(time.RFC3339Nano, s)
}

func sameCommand(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func sameResult(rec Record, state State, exitCode int, stdout, stderr []byte, runErr string) bool {
	return rec.State == state &&
		rec.ExitCode == exitCode &&
		bytes.Equal(rec.Stdout, stdout) &&
		bytes.Equal(rec.Stderr, stderr) &&
		rec.Error == runErr
}
