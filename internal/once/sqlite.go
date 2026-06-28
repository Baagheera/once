package once

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	if err := rejectSQLiteDSNPath(path); err != nil {
		return nil, err
	}
	path = filepath.Clean(path)
	if err := rejectSQLiteDSNPath(path); err != nil {
		return nil, err
	}
	if err := RejectSymlinkPath(path); err != nil {
		return nil, err
	}
	if dir := filepath.Dir(path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, err
		}
	}
	if err := RejectSymlinkPath(path); err != nil {
		return nil, err
	}
	if err := rejectSQLiteFileSymlinks(path); err != nil {
		return nil, err
	}
	if file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600); err != nil {
		return nil, err
	} else if err := file.Close(); err != nil {
		return nil, err
	}
	if err := restrictSQLiteFiles(path); err != nil {
		return nil, err
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
	if err := restrictSQLiteFiles(path); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func rejectSQLiteDSNPath(path string) error {
	lower := strings.ToLower(path)
	if lower == ":memory:" || strings.HasPrefix(lower, "file:") || strings.Contains(path, "?") {
		return fmt.Errorf("sqlite path must be a local filesystem path")
	}
	return nil
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
	attempt_hash TEXT NOT NULL DEFAULT '',
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
	if err != nil {
		return err
	}
	return s.ensureColumn("once_records", "attempt_hash", "ALTER TABLE once_records ADD COLUMN attempt_hash TEXT NOT NULL DEFAULT ''")
}

func (s *SQLiteStore) Reserve(key string, command []string) (Record, bool, error) {
	if err := ValidateKey(key); err != nil {
		return Record{}, false, err
	}

	now := time.Now().UTC()
	commandJSON, err := json.Marshal(command)
	if err != nil {
		return Record{}, false, err
	}
	attempt, err := NewAttemptToken()
	if err != nil {
		return Record{}, false, err
	}
	attemptHash := HashAttemptToken(attempt)

	res, err := s.db.Exec(`
INSERT OR IGNORE INTO once_records
	(key, attempt_hash, state, command, started_at, updated_at)
VALUES
	(?, ?, 'running', ?, ?, ?)
`, key, attemptHash, string(commandJSON), formatTime(now), formatTime(now))
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
	if affected == 0 && !sameCommand(rec.Command, command) {
		return rec, false, ErrConflict
	}
	if affected == 1 {
		rec.Attempt = attempt
	}
	return rec, affected == 1, nil
}

func (s *SQLiteStore) Commit(key, attempt string, state State, exitCode int, stdout, stderr []byte, runErr string) (Record, error) {
	if err := ValidateKey(key); err != nil {
		return Record{}, err
	}
	if err := ValidateAttemptToken(attempt); err != nil {
		return Record{}, err
	}
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
	attemptHash := HashAttemptToken(attempt)
	res, err := s.db.Exec(`
UPDATE once_records
SET state = ?,
	exit_code = ?,
	stdout = ?,
	stderr = ?,
	error = ?,
	finished_at = ?,
	updated_at = ?
WHERE key = ? AND state = 'running' AND attempt_hash = ?
`, string(state), exitCode, stdout, stderr, runErr, formatTime(now), formatTime(now), key, attemptHash)
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
		if rec.Attempt == attemptHash && sameResult(rec, state, exitCode, stdout, stderr, runErr) {
			rec.Attempt = attempt
			return rec, nil
		}
		return Record{}, ErrConflict
	}

	return s.Get(key)
}

func (s *SQLiteStore) Get(key string) (Record, error) {
	row := s.db.QueryRow(`
SELECT key, attempt_hash, state, exit_code, stdout, stderr, error, command, started_at, finished_at, updated_at
FROM once_records
WHERE key = ?
`, key)

	var rec Record
	var state string
	var attemptHash string
	var commandJSON string
	var startedAt string
	var finishedAt sql.NullString
	var updatedAt string

	err := row.Scan(
		&rec.Key,
		&attemptHash,
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
	rec.Attempt = attemptHash
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

func (s *SQLiteStore) Forget(key string, force bool, attempt string) (bool, error) {
	if err := ValidateKey(key); err != nil {
		return false, err
	}
	if err := ValidateAttemptToken(attempt); err != nil {
		return false, err
	}
	attemptHash := HashAttemptToken(attempt)

	if !force {
		res, err := s.db.Exec(`DELETE FROM once_records WHERE key = ? AND state <> 'running' AND attempt_hash = ?`, key, attemptHash)
		if err != nil {
			return false, err
		}
		affected, err := res.RowsAffected()
		if err != nil {
			return false, err
		}
		if affected != 0 {
			return true, nil
		}
		rec, err := s.Get(key)
		if errors.Is(err, ErrNotFound) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if rec.State == Running && rec.Attempt == attemptHash {
			return false, ErrRunning
		}
		return false, nil
	}

	res, err := s.db.Exec(`DELETE FROM once_records WHERE key = ? AND attempt_hash = ?`, key, attemptHash)
	if err != nil {
		return false, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected != 0, nil
}

func (s *SQLiteStore) AdminForget(key string, force bool) (bool, error) {
	if err := ValidateKey(key); err != nil {
		return false, err
	}

	if !force {
		res, err := s.db.Exec(`DELETE FROM once_records WHERE key = ? AND state <> 'running'`, key)
		if err != nil {
			return false, err
		}
		affected, err := res.RowsAffected()
		if err != nil {
			return false, err
		}
		if affected != 0 {
			return true, nil
		}
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
		return false, nil
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

func restrictSQLiteFiles(path string) error {
	for _, name := range sqliteFilePaths(path) {
		if err := RejectSymlinkPath(name); err != nil {
			return err
		}
		if info, err := os.Stat(name); err == nil {
			if !info.Mode().IsRegular() {
				return fmt.Errorf("sqlite path must be a regular file: %s", name)
			}
			if err := RestrictLocalFile(name); err != nil {
				return err
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func rejectSQLiteFileSymlinks(path string) error {
	for _, name := range sqliteFilePaths(path) {
		if err := RejectSymlinkPath(name); err != nil {
			return err
		}
	}
	return nil
}

func sqliteFilePaths(path string) []string {
	return []string{path, path + "-wal", path + "-shm"}
}

func (s *SQLiteStore) ensureColumn(table, column, ddl string) error {
	rows, err := s.db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var cid int
		var name string
		var typ string
		var notNull int
		var defaultValue any
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			return err
		}
		if name == column {
			return rows.Err()
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	_, err = s.db.Exec(ddl)
	return err
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
