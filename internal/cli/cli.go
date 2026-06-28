package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"time"

	once "github.com/Baagheera/once/internal/once"
	"github.com/Baagheera/once/internal/server"
)

const defaultStorePath = "once.db"

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

	rec, err = store.Commit(*key, state, exitCode, out, errOut, runErr)
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
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(stderr, "once: serve does not take positional arguments")
		return 2
	}

	store, err := once.OpenSQLite(storePath)
	if err != nil {
		fmt.Fprintf(stderr, "once: open store: %v\n", err)
		return 1
	}
	defer store.Close()

	fmt.Fprintf(stdout, "once: listening on %s\n", *listen)
	if err := http.ListenAndServe(*listen, server.NewHandler(store)); err != nil {
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

	doc := struct {
		Key        string   `json:"key"`
		State      string   `json:"state"`
		ExitCode   int      `json:"exit_code"`
		Error      string   `json:"error,omitempty"`
		Command    []string `json:"command"`
		StartedAt  string   `json:"started_at"`
		FinishedAt string   `json:"finished_at,omitempty"`
		UpdatedAt  string   `json:"updated_at"`
	}{
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

	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		fmt.Fprintf(stderr, "once: encode: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, string(data))
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

	ok, err := store.Forget(key, *force)
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

func usage(w io.Writer) {
	lines := []string{
		"usage:",
		"  once [--store PATH] run --key KEY -- COMMAND [ARG...]",
		"  once [--store PATH] serve [--listen ADDR]",
		"  once [--store PATH] status KEY",
		"  once [--store PATH] get KEY",
		"  once [--store PATH] forget [--force] KEY",
	}
	fmt.Fprintln(w, strings.Join(lines, "\n"))
}
