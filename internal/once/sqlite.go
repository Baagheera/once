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
	"sort"
	"strconv"
	"strings"
	"time"

	sqlite "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

type SQLiteStore struct {
	db *sql.DB
}

type sqliteMetadataState uint8

const (
	sqliteMetadataAbsent sqliteMetadataState = iota
	sqliteMetadataVersionMissing
	sqliteMetadataVersionPresent
)

type sqliteRecordSchemaState uint8

const (
	sqliteRecordSchemaAbsent sqliteRecordSchemaState = iota
	sqliteRecordSchemaLegacy
	sqliteRecordSchemaCurrent
)

// SQLiteSchemaQueryer is the subset of database/sql used to inspect a store's
// schema. Both *sql.DB and *sql.Conn implement it.
type SQLiteSchemaQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type sqliteColumn struct {
	typ          string
	notNull      bool
	pk           int
	defaultValue string
}

type sqliteColumnRequirement struct {
	name         string
	typ          string
	notNull      bool
	defaultValue string
}

var sqliteRecordColumnRequirements = []sqliteColumnRequirement{
	{name: "key", typ: "TEXT"},
	{name: "attempt_hash", typ: "TEXT", notNull: true, defaultValue: "''"},
	{name: "state", typ: "TEXT", notNull: true},
	{name: "exit_code", typ: "INTEGER", notNull: true, defaultValue: "0"},
	{name: "stdout", typ: "BLOB", notNull: true, defaultValue: "X''"},
	{name: "stderr", typ: "BLOB", notNull: true, defaultValue: "X''"},
	{name: "error", typ: "TEXT", notNull: true, defaultValue: "''"},
	{name: "command", typ: "TEXT", notNull: true, defaultValue: "'[]'"},
	{name: "started_at", typ: "TEXT", notNull: true},
	{name: "finished_at", typ: "TEXT"},
	{name: "updated_at", typ: "TEXT", notNull: true},
}

type sqliteSchemaVersionMissingError struct{}

func (sqliteSchemaVersionMissingError) Error() string {
	return "sqlite metadata table is missing schema_version"
}

func (sqliteSchemaVersionMissingError) sqliteSchemaVersionMissing() {}

var errSQLiteSchemaVersionMissing = sqliteSchemaVersionMissingError{}

const (
	sqliteSchemaVersion   = "1"
	sqliteDriverName      = "github.com/Baagheera/once/sqlite"
	sqliteBusyTimeoutMS   = 5000
	sqliteSynchronousFull = 2
)

type sqliteInitOptions struct {
	afterMissingMetadataRead     func()
	afterMigrationLock           func()
	afterWALTransitionContention func()
	migrationBusyTimeoutMS       int
}

func init() {
	driver := &sqlite.Driver{}
	driver.RegisterConnectionHook(func(conn sqlite.ExecQuerierContext, _ string) error {
		ctx := context.Background()
		for _, pragma := range []string{
			"PRAGMA busy_timeout = 5000",
			"PRAGMA synchronous = FULL",
			"PRAGMA foreign_keys = ON",
		} {
			if _, err := conn.ExecContext(ctx, pragma, nil); err != nil {
				return err
			}
		}
		return nil
	})
	sql.Register(sqliteDriverName, driver)
}

func OpenSQLite(path string) (*SQLiteStore, error) {
	return openSQLite(path, sqliteInitOptions{})
}

func openSQLite(path string, options sqliteInitOptions) (*SQLiteStore, error) {
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

	db, err := sql.Open(sqliteDriverName, path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)

	s := &SQLiteStore{db: db}
	if err := s.init(options); err != nil {
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

func (s *SQLiteStore) init(options sqliteInitOptions) error {
	ctx := context.Background()
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()

	metadataState, version, err := readSQLiteSchemaVersion(ctx, conn)
	if err != nil {
		return err
	}
	switch metadataState {
	case sqliteMetadataVersionMissing:
		return errSQLiteSchemaVersionMissing
	case sqliteMetadataVersionPresent:
		if err := CheckSQLiteSchemaVersion(version); err != nil {
			return err
		}
		if err := CheckSQLiteRecordSchema(ctx, conn); err != nil {
			return err
		}
		return configureSQLiteConnection(ctx, conn, options.afterWALTransitionContention)
	}

	if _, err := inspectSQLiteRecordSchema(ctx, conn, true, false); err != nil {
		return err
	}
	if options.afterMissingMetadataRead != nil {
		options.afterMissingMetadataRead()
	}
	if options.migrationBusyTimeoutMS > 0 {
		if _, err := conn.ExecContext(ctx, fmt.Sprintf("PRAGMA busy_timeout = %d", options.migrationBusyTimeoutMS)); err != nil {
			return err
		}
	}
	if err := configureSQLiteDurability(ctx, conn, options.afterWALTransitionContention); err != nil {
		return err
	}
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(ctx, "ROLLBACK")
		}
	}()
	if options.afterMigrationLock != nil {
		options.afterMigrationLock()
	}

	metadataState, version, err = readSQLiteSchemaVersion(ctx, conn)
	if err != nil {
		return err
	}
	switch metadataState {
	case sqliteMetadataAbsent:
		if err := initializeSQLiteSchema(ctx, conn); err != nil {
			return err
		}
		metadataState, version, err = readSQLiteSchemaVersion(ctx, conn)
		if err != nil {
			return err
		}
		if metadataState == sqliteMetadataVersionMissing {
			return errSQLiteSchemaVersionMissing
		}
		if metadataState != sqliteMetadataVersionPresent {
			return fmt.Errorf("sqlite schema version is missing after initialization")
		}
	case sqliteMetadataVersionMissing:
		return errSQLiteSchemaVersionMissing
	}
	if err := CheckSQLiteSchemaVersion(version); err != nil {
		return err
	}
	if err := CheckSQLiteRecordSchema(ctx, conn); err != nil {
		return err
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return err
	}
	committed = true

	if options.migrationBusyTimeoutMS > 0 {
		if _, err := conn.ExecContext(ctx, fmt.Sprintf("PRAGMA busy_timeout = %d", sqliteBusyTimeoutMS)); err != nil {
			return err
		}
	}
	return configureSQLiteConnection(ctx, conn, options.afterWALTransitionContention)
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
	var filters []string
	if opts.State != "" {
		filters = append(filters, "state = ?")
		args = append(args, string(opts.State))
	}
	if !opts.UpdatedBefore.IsZero() {
		filters = append(filters, updatedAtExpr+" < ?")
		args = append(args, formatSortableTime(opts.UpdatedBefore))
	}
	if len(filters) > 0 {
		query += "WHERE " + strings.Join(filters, " AND ") + "\n"
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
	for i, name := range sqliteFilePaths(path) {
		allowMissing := i > 0
		if err := restrictSQLiteFile(name, allowMissing); err != nil {
			return fmt.Errorf("restrict sqlite file %s: %w", name, err)
		}
	}
	return nil
}

func restrictSQLiteFile(name string, allowMissing bool) error {
	const (
		initialDelay = 5 * time.Millisecond
		maximumDelay = 100 * time.Millisecond
	)
	err := restrictSQLiteFileOnce(name, allowMissing)
	if err == nil || !allowMissing || !errors.Is(err, os.ErrPermission) {
		return err
	}

	// SQLite can temporarily deny metadata access to a live sidecar on Windows.
	deadline := time.Now().Add(time.Duration(sqliteBusyTimeoutMS) * time.Millisecond)
	delay := initialDelay
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return err
		}
		if delay > remaining {
			delay = remaining
		}
		time.Sleep(delay)
		err = restrictSQLiteFileOnce(name, allowMissing)
		if err == nil || !errors.Is(err, os.ErrPermission) {
			return err
		}
		if delay < maximumDelay {
			delay *= 2
			if delay > maximumDelay {
				delay = maximumDelay
			}
		}
	}
}

func restrictSQLiteFileOnce(name string, allowMissing bool) error {
	if err := RejectSymlinkPath(name); err != nil {
		return err
	}
	info, err := os.Stat(name)
	if err != nil {
		if allowMissing && errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("sqlite path must be a regular file: %s", name)
	}
	if err := RestrictLocalFile(name); err != nil {
		if allowMissing && errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
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
	return []string{path, path + "-wal", path + "-shm", path + "-journal"}
}

func readSQLiteSchemaVersion(ctx context.Context, conn *sql.Conn) (sqliteMetadataState, string, error) {
	var exists int
	if err := conn.QueryRowContext(ctx, `SELECT EXISTS (
	SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = 'once_meta'
)`).Scan(&exists); err != nil {
		return 0, "", err
	}
	if exists == 0 {
		return sqliteMetadataAbsent, "", nil
	}

	var version string
	err := conn.QueryRowContext(ctx, `SELECT value FROM once_meta WHERE key = 'schema_version'`).Scan(&version)
	if errors.Is(err, sql.ErrNoRows) {
		return sqliteMetadataVersionMissing, "", nil
	}
	if err != nil {
		return 0, "", err
	}
	return sqliteMetadataVersionPresent, version, nil
}

func initializeSQLiteSchema(ctx context.Context, conn *sql.Conn) error {
	state, err := inspectSQLiteRecordSchema(ctx, conn, true, false)
	if err != nil {
		return err
	}

	switch state {
	case sqliteRecordSchemaAbsent:
		if _, err := conn.ExecContext(ctx, `
CREATE TABLE once_records (
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
`); err != nil {
			return err
		}
	case sqliteRecordSchemaLegacy:
		if _, err := conn.ExecContext(ctx, "ALTER TABLE once_records ADD COLUMN attempt_hash TEXT NOT NULL DEFAULT ''"); err != nil {
			return err
		}
	}

	if _, err := inspectSQLiteRecordSchema(ctx, conn, false, false); err != nil {
		return err
	}
	if _, err := conn.ExecContext(ctx, "CREATE INDEX IF NOT EXISTS once_records_state_idx ON once_records(state)"); err != nil {
		return err
	}
	if err := checkSQLiteStateIndex(ctx, conn); err != nil {
		return err
	}
	if _, err := conn.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS once_meta (
	key TEXT PRIMARY KEY,
	value TEXT NOT NULL
);
`); err != nil {
		return err
	}
	_, err = conn.ExecContext(ctx, `INSERT OR IGNORE INTO once_meta (key, value) VALUES ('schema_version', ?)`, sqliteSchemaVersion)
	return err
}

// CheckSQLiteRecordSchema validates the current once_records table and its
// supporting index without modifying the database.
func CheckSQLiteRecordSchema(ctx context.Context, db SQLiteSchemaQueryer) error {
	state, err := inspectSQLiteRecordSchema(ctx, db, false, true)
	if err != nil {
		return err
	}
	if state == sqliteRecordSchemaAbsent {
		return fmt.Errorf("once_records table is missing")
	}
	return nil
}

func inspectSQLiteRecordSchema(ctx context.Context, db SQLiteSchemaQueryer, allowLegacy, requireStateIndex bool) (sqliteRecordSchemaState, error) {
	columns, err := sqliteOnceRecordColumns(ctx, db)
	if err != nil {
		return 0, err
	}
	if len(columns) == 0 {
		return sqliteRecordSchemaAbsent, nil
	}

	state := sqliteRecordSchemaCurrent
	requireAttemptHash := true
	if _, ok := columns["attempt_hash"]; !ok && allowLegacy {
		state = sqliteRecordSchemaLegacy
		requireAttemptHash = false
	}
	if detail := sqliteRecordSchemaProblem(columns, requireAttemptHash); detail != "" {
		return 0, errors.New(detail)
	}
	hasStateIndex, err := inspectSQLiteStateIndex(ctx, db)
	if err != nil {
		return 0, err
	}
	if requireStateIndex && !hasStateIndex {
		return 0, fmt.Errorf("once_records_state_idx index is missing")
	}
	return state, nil
}

func sqliteOnceRecordColumns(ctx context.Context, db SQLiteSchemaQueryer) (map[string]sqliteColumn, error) {
	rows, err := db.QueryContext(ctx, "PRAGMA table_info(once_records)")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	columns := map[string]sqliteColumn{}
	for rows.Next() {
		var cid int
		var name string
		var typ string
		var notNull int
		var defaultValue sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			return nil, err
		}
		var defaultText string
		if defaultValue.Valid {
			defaultText = defaultValue.String
		}
		columns[name] = sqliteColumn{
			typ:          strings.ToUpper(typ),
			notNull:      notNull != 0,
			pk:           pk,
			defaultValue: defaultText,
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return columns, nil
}

func sqliteRecordSchemaProblem(columns map[string]sqliteColumn, requireAttemptHash bool) string {
	required := make(map[string]sqliteColumnRequirement, len(sqliteRecordColumnRequirements))
	var missing []string
	for _, want := range sqliteRecordColumnRequirements {
		if want.name == "attempt_hash" && !requireAttemptHash {
			continue
		}
		required[want.name] = want
		if _, ok := columns[want.name]; !ok {
			missing = append(missing, want.name)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return "once_records missing columns: " + strings.Join(missing, ", ")
	}

	var unsupported []string
	for name := range columns {
		if _, ok := required[name]; !ok {
			unsupported = append(unsupported, name)
		}
	}
	if len(unsupported) > 0 {
		sort.Strings(unsupported)
		return "once_records has unsupported columns: " + strings.Join(unsupported, ", ")
	}

	key := columns["key"]
	if key.pk != 1 {
		return "once_records key column is not the primary key"
	}
	for name, column := range columns {
		if name != "key" && column.pk != 0 {
			return "once_records key column is not the sole primary key"
		}
	}

	for _, want := range sqliteRecordColumnRequirements {
		if want.name == "attempt_hash" && !requireAttemptHash {
			continue
		}
		got := columns[want.name]
		if got.typ != want.typ {
			return fmt.Sprintf("once_records %s column has type %s, want %s", want.name, got.typ, want.typ)
		}
		if want.notNull && !got.notNull {
			return fmt.Sprintf("once_records %s column is nullable", want.name)
		}
		if want.defaultValue != "" && normalizeSQLiteDefault(got.defaultValue) != want.defaultValue {
			return fmt.Sprintf("once_records %s column default is %s, want %s", want.name, printableSQLiteDefault(got.defaultValue), want.defaultValue)
		}
	}
	return ""
}

func normalizeSQLiteDefault(value string) string {
	return strings.ToUpper(strings.TrimSpace(value))
}

func printableSQLiteDefault(value string) string {
	if strings.TrimSpace(value) == "" {
		return "<none>"
	}
	return strings.TrimSpace(value)
}

func checkSQLiteStateIndex(ctx context.Context, db SQLiteSchemaQueryer) error {
	present, err := inspectSQLiteStateIndex(ctx, db)
	if err != nil {
		return err
	}
	if !present {
		return fmt.Errorf("once_records_state_idx index is missing")
	}
	return nil
}

func inspectSQLiteStateIndex(ctx context.Context, db SQLiteSchemaQueryer) (bool, error) {
	var unique, partial, columnCount, stateColumnCount int
	err := db.QueryRowContext(ctx, `SELECT
	COALESCE((SELECT "unique" FROM pragma_index_list('once_records') WHERE name = 'once_records_state_idx'), -1),
	COALESCE((SELECT partial FROM pragma_index_list('once_records') WHERE name = 'once_records_state_idx'), -1),
	(SELECT count(*) FROM pragma_index_info('once_records_state_idx')),
	(SELECT count(*) FROM pragma_index_info('once_records_state_idx') WHERE seqno = 0 AND name = 'state')
`).Scan(&unique, &partial, &columnCount, &stateColumnCount)
	if err != nil {
		return false, err
	}
	if unique == -1 {
		return false, nil
	}
	if unique != 0 || partial != 0 || columnCount != 1 || stateColumnCount != 1 {
		return false, fmt.Errorf("once_records_state_idx index must index only state")
	}
	return true, nil
}

func configureSQLiteDurability(ctx context.Context, conn *sql.Conn, afterWALTransitionContention func()) error {
	var journalMode string
	if err := conn.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journalMode); err != nil {
		return err
	}
	if !strings.EqualFold(journalMode, "wal") {
		if err := transitionSQLiteJournalModeToWAL(ctx, conn, afterWALTransitionContention); err != nil {
			return err
		}
	}
	if _, err := conn.ExecContext(ctx, "PRAGMA synchronous = FULL"); err != nil {
		return err
	}
	var synchronous int
	if err := conn.QueryRowContext(ctx, "PRAGMA synchronous").Scan(&synchronous); err != nil {
		return err
	}
	if synchronous != sqliteSynchronousFull {
		return fmt.Errorf("sqlite synchronous = %d, want %d", synchronous, sqliteSynchronousFull)
	}
	return nil
}

func transitionSQLiteJournalModeToWAL(ctx context.Context, conn *sql.Conn, afterContention func()) error {
	retryCtx, cancel := context.WithTimeout(ctx, time.Duration(sqliteBusyTimeoutMS)*time.Millisecond)
	defer cancel()

	const (
		initialDelay = 5 * time.Millisecond
		maximumDelay = 100 * time.Millisecond
	)
	delay := initialDelay
	var lastErr error
	contentionReported := false
	for {
		var journalMode string
		err := conn.QueryRowContext(retryCtx, "PRAGMA journal_mode = WAL").Scan(&journalMode)
		switch {
		case err == nil && strings.EqualFold(journalMode, "wal"):
			return nil
		case err == nil:
			lastErr = fmt.Errorf("sqlite journal mode = %q, want wal", journalMode)
		case !isSQLiteLockContention(err):
			return err
		default:
			lastErr = err
		}
		if !contentionReported && afterContention != nil {
			contentionReported = true
			afterContention()
		}

		timer := time.NewTimer(delay)
		select {
		case <-retryCtx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return fmt.Errorf("sqlite journal mode did not become wal (%v): %w", lastErr, retryCtx.Err())
		case <-timer.C:
		}
		if delay < maximumDelay {
			delay *= 2
			if delay > maximumDelay {
				delay = maximumDelay
			}
		}
	}
}

func isSQLiteLockContention(err error) bool {
	var sqliteErr *sqlite.Error
	if !errors.As(err, &sqliteErr) {
		return false
	}
	baseCode := sqliteErr.Code() & 0xff
	return baseCode == sqlite3.SQLITE_BUSY || baseCode == sqlite3.SQLITE_LOCKED
}

func configureSQLiteConnection(ctx context.Context, conn *sql.Conn, afterWALTransitionContention func()) error {
	if err := configureSQLiteDurability(ctx, conn, afterWALTransitionContention); err != nil {
		return err
	}

	for _, check := range []struct {
		name  string
		query string
		want  int
	}{
		{name: "busy timeout", query: "PRAGMA busy_timeout", want: sqliteBusyTimeoutMS},
		{name: "foreign keys", query: "PRAGMA foreign_keys", want: 1},
	} {
		var got int
		if err := conn.QueryRowContext(ctx, check.query).Scan(&got); err != nil {
			return err
		}
		if got != check.want {
			return fmt.Errorf("sqlite %s = %d, want %d", check.name, got, check.want)
		}
	}
	return nil
}

// CheckSQLiteSchemaVersion rejects stores that need a different once binary.
func CheckSQLiteSchemaVersion(version string) error {
	found, current, err := sqliteSchemaVersions(version)
	if err != nil {
		return err
	}
	if found > current {
		return newerSQLiteSchemaError(version)
	}
	if found < current {
		return olderSQLiteSchemaError(version)
	}
	return nil
}

func sqliteSchemaVersions(version string) (int, int, error) {
	found, err := strconv.Atoi(version)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid sqlite schema version %q", version)
	}
	current, err := strconv.Atoi(sqliteSchemaVersion)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid current sqlite schema version %q", sqliteSchemaVersion)
	}
	return found, current, nil
}

func newerSQLiteSchemaError(version string) error {
	return fmt.Errorf("newer sqlite schema version %s is not supported by this once binary", version)
}

func olderSQLiteSchemaError(version string) error {
	return fmt.Errorf("older sqlite schema version %s requires a migration not available in this once binary", version)
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
