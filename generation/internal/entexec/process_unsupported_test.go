//go:build !darwin && !linux

package entexec

import (
	"testing"
	"time"
)

func waitForProcessExit(t *testing.T, _ int) {
	t.Helper()
	t.Skip("process trees are not supported on this platform")
}

func killTestProcessGroup(int)                         {}
func waitForProcessExitWithin(int, time.Duration) bool { return true }
