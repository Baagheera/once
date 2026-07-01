//go:build windows

package cli

import (
	"fmt"
	"os/exec"

	"golang.org/x/sys/windows"
)

func prepareTimeoutCommand(cmd *exec.Cmd) {
}

func attachTimeoutCommand(cmd *exec.Cmd) (timeoutProcess, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return timeoutProcess{}, err
	}

	if cmd.Process == nil {
		_ = windows.CloseHandle(job)
		return timeoutProcess{}, fmt.Errorf("missing process handle")
	}
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if err != nil {
		_ = windows.CloseHandle(job)
		return timeoutProcess{}, err
	}
	defer windows.CloseHandle(process)
	if err := windows.AssignProcessToJobObject(job, process); err != nil {
		_ = windows.CloseHandle(job)
		return timeoutProcess{}, err
	}

	return timeoutProcess{
		kill: func() error {
			return windows.TerminateJobObject(job, 124)
		},
		cleanup: func() {
			_ = windows.CloseHandle(job)
		},
	}, nil
}
