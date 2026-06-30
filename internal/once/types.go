package once

import "time"

type State string

const (
	Running   State = "running"
	Succeeded State = "succeeded"
	Failed    State = "failed"
)

type Record struct {
	Key        string
	Attempt    string
	State      State
	ExitCode   int
	Stdout     []byte
	Stderr     []byte
	Error      string
	Command    []string
	StartedAt  time.Time
	FinishedAt *time.Time
	UpdatedAt  time.Time
}

type ListOptions struct {
	State         State
	Limit         int
	IncludeOutput bool
}
