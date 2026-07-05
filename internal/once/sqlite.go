package once

import (
	"bytes"
	"context"
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
	if err := ValidateSQLitePath(path); err != nil {
		return nil, err
	}
	path = filepath.Clean(path)
	if err := ValidateSQLitePath(path); err != nil {
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
	if err := RejectSharedWritableParent(path); err != nil {
		return nil, err
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

// ValidateSQLitePath rejects SQLite DSN forms. once stores must be local
// filesystem paths so SQLite options cannot bypass local file checks.
func ValidateSQLitePath(path string) error {
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
		rec, err := s.getWithAttemptHash(key)
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
			rec.Attempt = ""
			return rec, nil
		}
		return Record{}, ErrConflict
	}

	return s.Get(key)
}

func (s *SQLiteStore) Get(key string) (Record, error) {
	if err := ValidateKey(key); err != nil {
		return Record{}, err
	}

	rec, err := s.getWithAttemptHash(key)
	if err != nil {
		return Record{}, err
	}
	rec.Attempt = ""
	return rec, nil
}

func (s *SQLiteStore) getWithAttemptHash(key string) (Record, error) {
	row := s.db.QueryRow(`
SELECT key, attempt_hash, state, exit_code, stdout, stderr, error, command, started_at, finished_at, updated_at
FROM once_records
WHERE key = ?
`, key)

	rec, err := scanRecord(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Record{}, ErrNotFound
	}
	if err != nil {
		return Record{}, err
	}

	return rec, nil
}

func (s *SQLiteStore) List(opts ListOptions) ([]Record, error) {
	if opts.Limit < 0 {
		return nil, fmt.Errorf("limit must be non-negative")
	}
	if opts.State != "" && opts.State != Running && opts.State != Succeeded && opts.State != Failed {
		return nil, fmt.Errorf("invalid state: %s", opts.State)
	}

	outputColumns := "X'' AS stdout, X'' AS stderr"
	if opts.IncludeOutput {
		outputColumns = "stdout, stderr"
	}
	updatedAtExpr := sqliteSortableTimeExpr("updated_at")
	query := `
SELECT key, attempt_hash, state, exit_code, ` + outputColumns + `, error, command, started_at, finished_at, updated_at
FROM once_records
`
	var args []any
	if opts.State != "" {
		query += "WHERE state = ?\n"
		args = append(args, string(opts.State))
	}
	query += "ORDER BY " + updatedAtExpr + " DESC, key ASC\n"
	if opts.Limit > 0 {
		query += "LIMIT ?\n"
		args = append(args, opts.Limit)
	}

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var records []Record
	for rows.Next() {
		rec, err := scanRecord(rows)
		if err != nil {
			return nil, err
		}
		rec.Attempt = ""
		records = append(records, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return records, nil
}

func (s *SQLiteStore) Prune(opts PruneOptions) (PruneResult, error) {
	if opts.State != Succeeded && opts.State != Failed {
		return PruneResult{}, fmt.Errorf("prune state must be succeeded or failed")
	}
	if opts.Cutoff.IsZero() {
		return PruneResult{}, fmt.Errorf("prune cutoff is required")
	}

	ctx := context.Background()
	if opts.Force {
		return s.pruneForced(ctx, opts)
	}

	records, err := pruneCandidates(ctx, s.db, opts)
	if err != nil {
		return PruneResult{}, err
	}
	return PruneResult{Records: records}, nil
}

type pruneRunner interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func pruneCandidates(ctx context.Context, runner pruneRunner, opts PruneOptions) ([]Record, error) {
	updatedAtExpr := sqliteSortableTimeExpr("updated_at")
	rows, err := runner.QueryContext(ctx, `
SELECT key, attempt_hash, state, exit_code, X'' AS stdout, X'' AS stderr, error, command, started_at, finished_at, updated_at
FROM once_records
WHERE state = ? AND `+updatedAtExpr+` < ?
ORDER BY `+updatedAtExpr+` ASC, key ASC
`, string(opts.State), formatSortableTime(opts.Cutoff))
	if err != nil {
		return nil, err
	}

	var records []Record
	for rows.Next() {
		rec, err := scanRecord(rows)
		if err != nil {
			_ = rows.Close()
			return nil, err
		}
		rec.Attempt = ""
		records = append(records, rec)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return records, nil
}

func deletePruneCandidates(ctx context.Context, runner pruneRunner, records []Record) (int, error) {
	var deleted int
	for _, rec := range records {
		res, err := runner.ExecContext(ctx, `
DELETE FROM once_records
WHERE key = ? AND state = ? AND updated_at = ?
`, rec.Key, string(rec.State), formatTime(rec.UpdatedAt))
		if err != nil {
			return 0, err
		}
		affected, err := res.RowsAffected()
		if err != nil {
			return 0, err
		}
		deleted += int(affected)
	}
	return deleted, nil
}

func (s *SQLiteStore) pruneForced(ctx context.Context, opts PruneOptions) (PruneResult, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return PruneResult{}, err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return PruneResult{}, err
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(ctx, "ROLLBACK")
		}
	}()

	records, err := pruneCandidates(ctx, conn, opts)
	if err != nil {
		return PruneResult{}, err
	}
	deleted, err := deletePruneCandidates(ctx, conn, records)
	if err != nil {
		return PruneResult{}, err
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return PruneResult{}, err
	}
	committed = true
	return PruneResult{Records: records, Deleted: deleted}, nil
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
		rec, err := s.getWithAttemptHash(key)
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

func formatSortableTime(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05.000000000")
}

// Stored timestamps use RFC3339Nano, so fractional seconds are not fixed width.
// Normalize before comparing in SQLite; raw lexical ordering puts ".1Z" after ".11Z".
func sqliteSortableTimeExpr(column string) string {
	return "substr(" + column + ", 1, 19) || '.' || " +
		"CASE WHEN substr(" + column + ", 20, 1) = '.' " +
		"THEN substr(substr(" + column + ", 21, instr(" + column + ", 'Z') - 21) || '000000000', 1, 9) " +
		"ELSE '000000000' END"
}

func parseTime(s string) (time.Time, error) {
	return time.Parse(time.RFC3339Nano, s)
}

type recordScanner interface {
	Scan(dest ...any) error
}

func scanRecord(scanner recordScanner) (Record, error) {
	var rec Record
	var state string
	var attemptHash string
	var commandJSON string
	var startedAt string
	var finishedAt sql.NullString
	var updatedAt string

	err := scanner.Scan(
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
