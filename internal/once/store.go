package once

import "errors"

var ErrNotFound = errors.New("record not found")

type Store interface {
	Close() error
	Reserve(key string, command []string) (Record, bool, error)
	Commit(key string, state State, exitCode int, stdout, stderr []byte, runErr string) (Record, error)
	Get(key string) (Record, error)
	Forget(key string) (bool, error)
}
