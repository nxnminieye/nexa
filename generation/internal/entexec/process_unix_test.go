//go:build darwin || linux

package entexec

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime/debug"
	"syscall"
	"testing"
	"time"
)

func TestDirectPreStartCancellationDoesNotAccumulateFileDescriptors(t *testing.T) {
	previousGCPercent := debug.SetGCPercent(-1)
	defer debug.SetGCPercent(previousGCPercent)

	spec := validDirectProcessSpec(t)
	marker := filepath.Join(spec.RepositoryRoot, "main-ran")
	spec.Args = []string{"mark", marker}
	baseline := openFileDescriptorCount(t)
	for iteration := 0; iteration < 96; iteration++ {
		ctx, cancel := context.WithCancel(context.Background())
		spec.processHook = func(event processEvent) {
			if event.Name == "start" && len(event.Args) > 0 && event.Args[len(event.Args)-1] == marker {
				cancel()
			}
		}
		_, err := RunProcess(ctx, spec)
		assertDirectInvoked(t, err)
		if _, statErr := os.Stat(marker); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("iteration %d started main process: %v", iteration, statErr)
		}
	}
	// Runtime facilities may retain an unrelated descriptor, while a pipe leak
	// per canceled launch would exceed this fixed allowance.
	if after := openFileDescriptorCount(t); after > baseline+2 {
		t.Fatalf("open file descriptors grew from %d to %d without a GC", baseline, after)
	}
}

func openFileDescriptorCount(t *testing.T) int {
	t.Helper()
	var limit syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &limit); err != nil {
		t.Fatalf("get file descriptor limit: %v", err)
	}
	maximum := limit.Cur
	if maximum > 4096 {
		maximum = 4096
	}
	count := 0
	for descriptor := 0; uint64(descriptor) < maximum; descriptor++ {
		var info syscall.Stat_t
		if err := syscall.Fstat(descriptor, &info); err == nil {
			count++
		}
	}
	return count
}

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
