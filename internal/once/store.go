package once

import "errors"

var (
	ErrConflict = errors.New("record conflicts with existing key")
	ErrNotFound = errors.New("record not found")
	ErrRunning  = errors.New("record is still running")
)

type Store interface {
	Close() error
	Reserve(key string, command []string) (Record, bool, error)
	Commit(key, attempt string, state State, exitCode int, stdout, stderr []byte, runErr string) (Record, error)
	Get(key string) (Record, error)
	Forget(key string, force bool, attempt string) (bool, error)
}
