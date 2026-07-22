//go:build darwin || linux

package entexec

import (
	"os/exec"
	"syscall"
)

func processTreeSupported() bool { return true }

func configureProcessTree(command *exec.Cmd) bool {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return true
}

func killProcessTree(command *exec.Cmd) error {
	if command == nil || command.Process == nil {
		return nil
	}
	return syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
}
