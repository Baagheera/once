//go:build !windows

package cli

import (
	"os/exec"
	"syscall"
)

func prepareTimeoutCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func attachTimeoutCommand(cmd *exec.Cmd) (timeoutProcess, error) {
	return timeoutProcess{
		kill: func() error {
			if cmd.Process == nil {
				return nil
			}
			if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err == nil {
				return nil
			}
			return cmd.Process.Kill()
		},
		cleanup: func() {},
	}, nil
}
