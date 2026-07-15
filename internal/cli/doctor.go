package cli

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	once "github.com/Baagheera/once/internal/once"
)

type doctorLevel string

const (
	doctorOK   doctorLevel = "ok"
	doctorWarn doctorLevel = "warn"
	doctorFail doctorLevel = "fail"
	doctorSkip doctorLevel = "skip"
)

type doctorCheck struct {
	name   string
	level  doctorLevel
	detail string
}

type doctorJSON struct {
	OK     bool              `json:"ok"`
	Checks []doctorJSONCheck `json:"checks"`
}

type doctorJSONCheck struct {
	Name   string      `json:"name"`
	Level  doctorLevel `json:"level"`
	Detail string      `json:"detail,omitempty"`
}

func doctorCommand(args []string, storePath string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(stderr)
	jsonOutput := fs.Bool("json", false, "write checks as JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(stderr, "once: doctor does not take positional arguments")
		return 2
	}

	checks := runDoctor(storePath)
	failed := false
	for _, check := range checks {
		if check.level == doctorFail {
			failed = true
			break
		}
	}

	if *jsonOutput {
		if err := json.NewEncoder(stdout).Encode(doctorJSONDoc(checks, !failed)); err != nil {
			fmt.Fprintf(stderr, "once: doctor: %v\n", err)
			return 1
		}
		if failed {
			return 1
		}
		return 0
	}

	for _, check := range checks {
		fmt.Fprintf(stdout, "%s: %s", check.name, check.level)
		if check.detail != "" {
			fmt.Fprintf(stdout, " - %s", check.detail)
		}
		fmt.Fprintln(stdout)
	}
	if failed {
		fmt.Fprintln(stdout, "doctor: failed")
		return 1
	}
	fmt.Fprintln(stdout, "doctor: ok")
	return 0
}

func doctorJSONDoc(checks []doctorCheck, ok bool) doctorJSON {
	doc := doctorJSON{
		OK:     ok,
		Checks: make([]doctorJSONCheck, 0, len(checks)),
	}
	for _, check := range checks {
		doc.Checks = append(doc.Checks, doctorJSONCheck{
			Name:   check.name,
			Level:  check.level,
			Detail: check.detail,
		})
	}
	return doc
}

func runDoctor(storePath string) []doctorCheck {
	path, err := cleanDoctorStorePath(storePath)
	if err != nil {
		return []doctorCheck{{
			name:   "store path",
			level:  doctorFail,
			detail: err.Error(),
		}}
	}

	checks := []doctorCheck{{
		name:   "store path",
		level:  doctorOK,
		detail: path,
	}}
	checks = append(checks, doctorCheckParent(path))

	storeCheck, storeExists := doctorCheckRegularFile(
		"store file",
		path,
		"missing; once will create it when the store is opened",
		doctorWarn,
	)
	checks = append(checks, storeCheck)
	checks = append(checks,
		doctorCheckSidecar("sqlite wal", path+"-wal", storeExists),
		doctorCheckSidecar("sqlite shm", path+"-shm", storeExists),
		doctorCheckTokenFile(path),
	)
	if !storeExists {
		checks = append(checks,
			doctorCheck{name: "sqlite open", level: doctorSkip, detail: "store file is missing"},
			doctorCheck{name: "sqlite schema", level: doctorSkip, detail: "store file is missing"},
		)
		return checks
	}

	return append(checks, doctorCheckSQLite(path)...)
}

func cleanDoctorStorePath(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("empty sqlite path")
	}
	if err := once.ValidateSQLitePath(path); err != nil {
		return "", err
	}
	path = filepath.Clean(path)
	if err := once.ValidateSQLitePath(path); err != nil {
		return "", err
	}
	return path, nil
}

func doctorCheckParent(path string) doctorCheck {
	dir := filepath.Dir(path)
	if dir == "" {
		dir = "."
	}
	if err := once.RejectSymlinkPath(dir); err != nil {
		return doctorCheck{name: "store parent", level: doctorFail, detail: err.Error()}
	}
	if err := once.RejectSharedWritableParent(path); err != nil {
		return doctorCheck{name: "store parent", level: doctorFail, detail: err.Error()}
	}

	info, err := os.Stat(dir)
	if err == nil {
		if !info.IsDir() {
			return doctorCheck{name: "store parent", level: doctorFail, detail: fmt.Sprintf("%s is not a directory", dir)}
		}
		if detail := doctorDirectoryPermissionProblem(dir, info); detail != "" {
			return doctorCheck{name: "store parent", level: doctorWarn, detail: detail}
		}
		if dir == "." {
			return doctorCheck{name: "store parent", level: doctorOK, detail: "current directory"}
		}
		return doctorCheck{name: "store parent", level: doctorOK, detail: dir}
	}
	if !os.IsNotExist(err) {
		return doctorCheck{name: "store parent", level: doctorFail, detail: err.Error()}
	}

	parent, err := nearestExistingParent(dir)
	if err != nil {
		return doctorCheck{name: "store parent", level: doctorFail, detail: err.Error()}
	}
	if err := once.RejectSymlinkPath(parent); err != nil {
		return doctorCheck{name: "store parent", level: doctorFail, detail: err.Error()}
	}
	return doctorCheck{
		name:   "store parent",
		level:  doctorWarn,
		detail: fmt.Sprintf("missing %s; nearest existing parent is %s", dir, parent),
	}
}

func nearestExistingParent(path string) (string, error) {
	dir := filepath.Clean(path)
	for {
		info, err := os.Stat(dir)
		if err == nil {
			if !info.IsDir() {
				return "", fmt.Errorf("%s is not a directory", dir)
			}
			return dir, nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		next := filepath.Dir(dir)
		if next == dir {
			return "", fmt.Errorf("no existing parent for %s", path)
		}
		dir = next
	}
}

func doctorCheckSidecar(name, path string, storeExists bool) doctorCheck {
	check, exists := doctorCheckRegularFile(name, path, "missing", doctorOK)
	if check.level == doctorFail || !exists || storeExists {
		return check
	}
	return doctorCheck{name: name, level: doctorFail, detail: fmt.Sprintf("%s is present while the store file is missing", path)}
}

func doctorCheckRegularFile(name, path, missingDetail string, missingLevel doctorLevel) (doctorCheck, bool) {
	if err := once.RejectSymlinkPath(path); err != nil {
		return doctorCheck{name: name, level: doctorFail, detail: err.Error()}, false
	}

	info, err := os.Stat(path)
	if err == nil {
		if !info.Mode().IsRegular() {
			return doctorCheck{name: name, level: doctorFail, detail: fmt.Sprintf("%s is not a regular file", path)}, false
		}
		if detail := doctorFilePermissionProblem(path, info); detail != "" {
			return doctorCheck{name: name, level: doctorFail, detail: detail}, true
		}
		return doctorCheck{name: name, level: doctorOK, detail: fmt.Sprintf("%s is a regular file", path)}, true
	}
	if os.IsNotExist(err) {
		return doctorCheck{name: name, level: missingLevel, detail: missingDetail}, false
	}
	return doctorCheck{name: name, level: doctorFail, detail: err.Error()}, false
}

func doctorCheckTokenFile(storePath string) doctorCheck {
	path := storePath + ".token"
	check, exists := doctorCheckRegularFile(
		"token file",
		path,
		fmt.Sprintf("missing %s; serve will create it when needed", path),
		doctorOK,
	)
	if !exists || check.level == doctorFail {
		return check
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return doctorCheck{name: "token file", level: doctorFail, detail: err.Error()}
	}
	if err := validateAuthToken(strings.TrimSpace(string(data))); err != nil {
		return doctorCheck{name: "token file", level: doctorFail, detail: fmt.Sprintf("%s: %v", path, err)}
	}
	return doctorCheck{name: "token file", level: doctorOK, detail: fmt.Sprintf("%s exists; token contents not printed", path)}
}

func doctorCheckSQLite(path string) []doctorCheck {
	dsn, err := sqliteReadOnlyDSN(path)
	if err != nil {
		return sqliteOpenFailed(err)
	}

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return sqliteOpenFailed(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	if err := db.Ping(); err != nil {
		return sqliteOpenFailed(err)
	}

	return []doctorCheck{
		{name: "sqlite open", level: doctorOK, detail: "read-only open succeeded"},
		doctorCheckSQLiteSchema(db),
	}
}

func sqliteOpenFailed(err error) []doctorCheck {
	return []doctorCheck{
		{name: "sqlite open", level: doctorFail, detail: err.Error()},
		{name: "sqlite schema", level: doctorSkip, detail: "sqlite open failed"},
	}
}

func sqliteReadOnlyDSN(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	slashPath := filepath.ToSlash(abs)
	if filepath.VolumeName(abs) != "" && !strings.HasPrefix(slashPath, "/") {
		slashPath = "/" + slashPath
	}
	u := url.URL{Scheme: "file", Path: slashPath}
	q := u.Query()
	q.Set("mode", "ro")
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func doctorCheckSQLiteSchema(db *sql.DB) doctorCheck {
	if err := doctorCheckSQLiteSchemaVersion(db); err != nil {
		return doctorCheck{name: "sqlite schema", level: doctorFail, detail: err.Error()}
	}
	if err := once.CheckSQLiteRecordSchema(context.Background(), db); err != nil {
		return doctorCheck{name: "sqlite schema", level: doctorFail, detail: err.Error()}
	}
	return doctorCheck{name: "sqlite schema", level: doctorOK, detail: "once_records table is readable"}
}

func doctorCheckSQLiteSchemaVersion(db *sql.DB) error {
	var tableCount int
	if err := db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'once_meta'").Scan(&tableCount); err != nil {
		return err
	}
	if tableCount == 0 {
		return nil
	}

	var version string
	err := db.QueryRow(`SELECT value FROM once_meta WHERE key = 'schema_version'`).Scan(&version)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("sqlite metadata table is missing schema_version")
	}
	if err != nil {
		return err
	}
	return once.CheckSQLiteSchemaVersion(version)
}
