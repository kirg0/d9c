//go:build windows

package ui

import "os/exec"

// setProcessGroup is a no-op on Windows: process groups there do not give a
// portable way to kill a whole tree, and stop() closes the output pipes anyway.
func setProcessGroup(*exec.Cmd) {}

// killProcessTree kills the child process.
func killProcessTree(c *exec.Cmd) {
	if c.Process == nil {
		return
	}
	_ = c.Process.Kill()
}
