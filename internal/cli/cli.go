package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	once "github.com/Baagheera/once/internal/once"
	"github.com/Baagheera/once/internal/server"
)

const defaultStorePath = "once.db"
const minAuthTokenLength = 32

func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return 2
	}

	storePath := defaultStorePath
	if args[0] == "--store" {
		if len(args) < 3 {
			fmt.Fprintln(stderr, "once: --store needs a path")
			return 2
		}
		storePath = args[1]
		args = args[2:]
	}
	if len(args) == 0 {
		usage(stderr)
		return 2
	}

	switch args[0] {
	case "run":
		return runCommand(args[1:], storePath, stdout, stderr)
	case "serve":
		return serveCommand(args[1:], storePath, stdout, stderr)
	case "status":
		return statusCommand(args[1:], storePath, stdout, stderr)
	case "get":
		return getCommand(args[1:], storePath, stdout, stderr)
	case "list":
		return listCommand(args[1:], storePath, stdout, stderr)
	case "export":
		return exportCommand(args[1:], storePath, stdout, stderr)
	case "forget":
		return forgetCommand(args[1:], storePath, stdout, stderr)
	case "help", "-h", "--help":
		usage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "once: unknown command %q\n", args[0])
		usage(stderr)
		return 2
	}
}

func runCommand(args []string, storePath string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	key := fs.String("key", "", "idempotency key")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	command := fs.Args()
	if *key == "" {
		fmt.Fprintln(stderr, "once: run needs --key")
		return 2
	}
	if len(command) == 0 {
		fmt.Fprintln(stderr, "once: run needs a command after --")
		return 2
	}

	store, err := once.OpenSQLite(storePath)
	if err != nil {
		fmt.Fprintf(stderr, "once: open store: %v\n", err)
		return 1
	}
	defer store.Close()

	rec, fresh, err := store.Reserve(*key, command)
	if errors.Is(err, once.ErrConflict) {
		fmt.Fprintf(stderr, "once: key already exists with a different command: %s\n", *key)
		return 1
	}
	if err != nil {
		fmt.Fprintf(stderr, "once: reserve: %v\n", err)
		return 1
	}

	if !fresh {
		return replayRecord(rec, stdout, stderr)
	}

	exitCode, out, errOut, runErr := execute(command)
	state := once.Succeeded
	if exitCode != 0 || runErr != "" {
		state = once.Failed
	}

	rec, err = store.Commit(*key, rec.Attempt, state, exitCode, out, errOut, runErr)
	if err != nil {
		fmt.Fprintf(stderr, "once: commit: %v\n", err)
		return 1
	}

	return replayRecord(rec, stdout, stderr)
}

func serveCommand(args []string, storePath string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	listen := fs.String("listen", "127.0.0.1:7410", "listen address")
	token := fs.String("token", "", "bearer token for HTTP API; must be at least 32 characters")
	tokenFile := fs.String("token-file", "", "file containing the HTTP bearer token; created if missing")
	unsafeNoAuth := fs.Bool("unsafe-no-auth", false, "disable HTTP auth")
	allowRemote := fs.Bool("allow-remote", false, "allow non-loopback listen addresses")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(stderr, "once: serve does not take positional arguments")
		return 2
	}
	if !*allowRemote && !isLoopbackListen(*listen) {
		fmt.Fprintln(stderr, "once: non-loopback listen address requires --allow-remote")
		return 2
	}
	if *unsafeNoAuth && !isLoopbackListen(*listen) {
		fmt.Fprintln(stderr, "once: --unsafe-no-auth is only allowed on loopback addresses")
		return 2
	}

	store, err := once.OpenSQLite(storePath)
	if err != nil {
		fmt.Fprintf(stderr, "once: open store: %v\n", err)
		return 1
	}
	defer store.Close()

	authToken, authTokenFile, err := resolveAuthToken(*token, *tokenFile, *unsafeNoAuth, storePath)
	if err != nil {
		fmt.Fprintf(stderr, "once: auth token: %v\n", err)
		return 2
	}

	fmt.Fprintf(stdout, "once: listening on %s\n", *listen)
	if authToken != "" {
		if authTokenFile != "" {
			fmt.Fprintf(stdout, "once: auth token file %s\n", authTokenFile)
		} else {
			fmt.Fprintln(stdout, "once: auth enabled")
		}
	} else {
		fmt.Fprintln(stdout, "once: warning: HTTP auth disabled")
	}

	srv := &http.Server{
		Addr:              *listen,
		Handler:           server.NewHandler(store, server.Options{AuthToken: authToken}),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	if err := srv.ListenAndServe(); err != nil {
		fmt.Fprintf(stderr, "once: serve: %v\n", err)
		return 1
	}
	return 0
}

func statusCommand(args []string, storePath string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "once: status needs exactly one key")
		return 2
	}

	rec, code := loadRecord(storePath, args[0], stderr)
	if code != 0 {
		return code
	}
	fmt.Fprintln(stdout, rec.State)
	return 0
}

func getCommand(args []string, storePath string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "once: get needs exactly one key")
		return 2
	}

	rec, code := loadRecord(storePath, args[0], stderr)
	if code != 0 {
		return code
	}

	data, err := json.MarshalIndent(recordDoc(rec, false), "", "  ")
	if err != nil {
		fmt.Fprintf(stderr, "once: encode: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, string(data))
	return 0
}

func listCommand(args []string, storePath string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	stateFlag := fs.String("state", "", "filter by state: running, succeeded, or failed")
	limitFlag := fs.Int("limit", 0, "maximum records to print; 0 means all")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(stderr, "once: list does not take positional arguments")
		return 2
	}

	opts, ok := listOptions(*stateFlag, *limitFlag, stderr)
	if !ok {
		return 2
	}
	records, code := listRecords(storePath, opts, stderr)
	if code != 0 {
		return code
	}

	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "KEY\tSTATE\tEXIT\tUPDATED\tCOMMAND")
	for _, rec := range records {
		fmt.Fprintf(tw, "%s\t%s\t%d\t%s\t%s\n",
			rec.Key,
			rec.State,
			rec.ExitCode,
			rec.UpdatedAt.Format(time.RFC3339),
			formatCommand(rec.Command),
		)
	}
	if err := tw.Flush(); err != nil {
		fmt.Fprintf(stderr, "once: list: %v\n", err)
		return 1
	}
	return 0
}

func exportCommand(args []string, storePath string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("export", flag.ContinueOnError)
	fs.SetOutput(stderr)
	stateFlag := fs.String("state", "", "filter by state: running, succeeded, or failed")
	limitFlag := fs.Int("limit", 0, "maximum records to export; 0 means all")
	includeOutput := fs.Bool("include-output", false, "include stdout_b64 and stderr_b64")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(stderr, "once: export does not take positional arguments")
		return 2
	}

	opts, ok := listOptions(*stateFlag, *limitFlag, stderr)
	if !ok {
		return 2
	}
	opts.IncludeOutput = *includeOutput
	records, code := listRecords(storePath, opts, stderr)
	if code != 0 {
		return code
	}

	enc := json.NewEncoder(stdout)
	for _, rec := range records {
		if err := enc.Encode(recordDoc(rec, *includeOutput)); err != nil {
			fmt.Fprintf(stderr, "once: export: %v\n", err)
			return 1
		}
	}
	return 0
}

func forgetCommand(args []string, storePath string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("forget", flag.ContinueOnError)
	fs.SetOutput(stderr)
	force := fs.Bool("force", false, "delete even if the key is still running")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "once: forget needs exactly one key")
		return 2
	}
	key := fs.Arg(0)

	store, err := once.OpenSQLite(storePath)
	if err != nil {
		fmt.Fprintf(stderr, "once: open store: %v\n", err)
		return 1
	}
	defer store.Close()

	ok, err := store.AdminForget(key, *force)
	if errors.Is(err, once.ErrRunning) {
		fmt.Fprintf(stderr, "once: key is still running: %s\n", key)
		return 1
	}
	if err != nil {
		fmt.Fprintf(stderr, "once: forget: %v\n", err)
		return 1
	}
	if !ok {
		fmt.Fprintf(stderr, "once: key not found: %s\n", key)
		return 1
	}
	fmt.Fprintln(stdout, "forgot")
	return 0
}

func loadRecord(storePath string, key string, stderr io.Writer) (once.Record, int) {
	store, err := once.OpenSQLite(storePath)
	if err != nil {
		fmt.Fprintf(stderr, "once: open store: %v\n", err)
		return once.Record{}, 1
	}
	defer store.Close()

	rec, err := store.Get(key)
	if errors.Is(err, once.ErrNotFound) {
		fmt.Fprintf(stderr, "once: key not found: %s\n", key)
		return once.Record{}, 1
	}
	if err != nil {
		fmt.Fprintf(stderr, "once: get: %v\n", err)
		return once.Record{}, 1
	}
	return rec, 0
}

func listRecords(storePath string, opts once.ListOptions, stderr io.Writer) ([]once.Record, int) {
	store, err := once.OpenSQLite(storePath)
	if err != nil {
		fmt.Fprintf(stderr, "once: open store: %v\n", err)
		return nil, 1
	}
	defer store.Close()

	records, err := store.List(opts)
	if err != nil {
		fmt.Fprintf(stderr, "once: list: %v\n", err)
		return nil, 1
	}
	return records, 0
}

func listOptions(state string, limit int, stderr io.Writer) (once.ListOptions, bool) {
	if limit < 0 {
		fmt.Fprintln(stderr, "once: --limit must be non-negative")
		return once.ListOptions{}, false
	}
	opts := once.ListOptions{Limit: limit}
	switch strings.TrimSpace(state) {
	case "":
	case string(once.Running):
		opts.State = once.Running
	case string(once.Succeeded):
		opts.State = once.Succeeded
	case string(once.Failed):
		opts.State = once.Failed
	default:
		fmt.Fprintln(stderr, "once: --state must be running, succeeded, or failed")
		return once.ListOptions{}, false
	}
	return opts, true
}

type recordJSON struct {
	Key        string   `json:"key"`
	State      string   `json:"state"`
	ExitCode   int      `json:"exit_code"`
	Error      string   `json:"error,omitempty"`
	Command    []string `json:"command"`
	StartedAt  string   `json:"started_at"`
	FinishedAt string   `json:"finished_at,omitempty"`
	UpdatedAt  string   `json:"updated_at"`
	StdoutB64  *[]byte  `json:"stdout_b64,omitempty"`
	StderrB64  *[]byte  `json:"stderr_b64,omitempty"`
}

func recordDoc(rec once.Record, includeOutput bool) recordJSON {
	doc := recordJSON{
		Key:       rec.Key,
		State:     string(rec.State),
		ExitCode:  rec.ExitCode,
		Error:     rec.Error,
		Command:   rec.Command,
		StartedAt: rec.StartedAt.Format(time.RFC3339Nano),
		UpdatedAt: rec.UpdatedAt.Format(time.RFC3339Nano),
	}
	if rec.FinishedAt != nil {
		doc.FinishedAt = rec.FinishedAt.Format(time.RFC3339Nano)
	}
	if includeOutput {
		stdout := nonNilBytes(rec.Stdout)
		stderr := nonNilBytes(rec.Stderr)
		doc.StdoutB64 = &stdout
		doc.StderrB64 = &stderr
	}
	return doc
}

func nonNilBytes(data []byte) []byte {
	if data == nil {
		return []byte{}
	}
	return data
}

func formatCommand(command []string) string {
	if len(command) == 0 {
		return "-"
	}
	quoted := make([]string, len(command))
	for i, part := range command {
		quoted[i] = strconv.Quote(part)
	}
	return strings.Join(quoted, " ")
}

func execute(command []string) (int, []byte, []byte, string) {
	cmd := exec.Command(command[0], command[1:]...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err == nil {
		return 0, stdout.Bytes(), stderr.Bytes(), ""
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), stdout.Bytes(), stderr.Bytes(), ""
	}

	return 127, stdout.Bytes(), stderr.Bytes(), err.Error()
}

func replayRecord(rec once.Record, stdout, stderr io.Writer) int {
	if rec.State == once.Running {
		fmt.Fprintf(stderr, "once: key is already running: %s\n", rec.Key)
		return 75
	}

	_, _ = stdout.Write(rec.Stdout)
	_, _ = stderr.Write(rec.Stderr)
	if rec.Error != "" {
		fmt.Fprintf(stderr, "once: %s\n", rec.Error)
	}
	return rec.ExitCode
}

func resolveAuthToken(flagToken, tokenFile string, unsafeNoAuth bool, storePath string) (string, string, error) {
	flagToken = strings.TrimSpace(flagToken)
	tokenFile = strings.TrimSpace(tokenFile)
	if unsafeNoAuth {
		if flagToken != "" || tokenFile != "" {
			return "", "", fmt.Errorf("--token and --token-file cannot be used with --unsafe-no-auth")
		}
		return "", "", nil
	}
	if flagToken != "" && tokenFile != "" {
		return "", "", fmt.Errorf("--token and --token-file are mutually exclusive")
	}
	if flagToken != "" {
		if err := validateAuthToken(flagToken); err != nil {
			return "", "", err
		}
		return flagToken, "", nil
	}
	if tokenFile != "" {
		token, err := loadOrCreateTokenFile(tokenFile)
		return token, tokenFile, err
	}
	if envToken := strings.TrimSpace(os.Getenv("ONCE_TOKEN")); envToken != "" {
		if err := validateAuthToken(envToken); err != nil {
			return "", "", fmt.Errorf("ONCE_TOKEN: %w", err)
		}
		return envToken, "", nil
	}

	defaultFile := storePath + ".token"
	token, err := loadOrCreateTokenFile(defaultFile)
	return token, defaultFile, err
}

func validateAuthToken(token string) error {
	if len(token) < minAuthTokenLength {
		return fmt.Errorf("token must be at least %d characters", minAuthTokenLength)
	}
	if strings.ContainsAny(token, " \t\r\n") {
		return fmt.Errorf("token must not contain whitespace")
	}
	return nil
}

func loadOrCreateTokenFile(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("empty token file path")
	}
	path = filepath.Clean(path)
	if err := once.RejectSymlinkPath(path); err != nil {
		return "", err
	}

	data, err := os.ReadFile(path)
	if err == nil {
		token := strings.TrimSpace(string(data))
		if err := validateAuthToken(token); err != nil {
			return "", err
		}
		if err := once.RestrictLocalFile(path); err != nil {
			return "", err
		}
		return token, nil
	}
	if !os.IsNotExist(err) {
		return "", err
	}

	if dir := filepath.Dir(path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return "", err
		}
	}
	if err := once.RejectSymlinkPath(path); err != nil {
		return "", err
	}
	token, err := once.NewAttemptToken()
	if err != nil {
		return "", err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", err
	}
	if _, err := file.WriteString(token + "\n"); err != nil {
		_ = file.Close()
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	if err := once.RestrictLocalFile(path); err != nil {
		return "", err
	}
	return token, nil
}

func usage(w io.Writer) {
	lines := []string{
		"usage:",
		"  once [--store PATH] run --key KEY -- COMMAND [ARG...]",
		"  once [--store PATH] serve [--listen ADDR] [--token TOKEN | --token-file PATH]",
		"  once [--store PATH] status KEY",
		"  once [--store PATH] get KEY",
		"  once [--store PATH] list [--state STATE] [--limit N]",
		"  once [--store PATH] export [--state STATE] [--limit N] [--include-output]",
		"  once [--store PATH] forget [--force] KEY",
	}
	fmt.Fprintln(w, strings.Join(lines, "\n"))
}

func isLoopbackListen(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
