//go:build !darwin && !linux

package entexec

import "os/exec"

func processTreeSupported() bool          { return false }
func configureProcessTree(*exec.Cmd) bool { return false }
func killProcessTree(*exec.Cmd) error     { return nil }
