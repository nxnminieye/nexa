//go:build darwin || linux

package entexec

import (
	"errors"
	"syscall"
	"testing"
	"time"
)

func waitForProcessExit(t *testing.T, pid int) {
	t.Helper()
	if waitForProcessExitWithin(pid, 5*time.Second) {
		return
	}
	t.Fatalf("descendant process %d is still alive", pid)
}

func waitForProcessExitWithin(pid int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		err := syscall.Kill(pid, 0)
		if errors.Is(err, syscall.ESRCH) {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

func killTestProcessGroup(groupPID int) {
	_ = syscall.Kill(-groupPID, syscall.SIGKILL)
}
