//go:build !windows

package ui

import (
	"os/exec"
	"syscall"
)

// setProcessGroup makes the child the leader of its own process group, so that
// killProcessTree can take down everything it spawned (a plugin usually runs
// through a shell wrapper, and killing only the shell leaves the grandchildren
// holding the output pipes open).
func setProcessGroup(c *exec.Cmd) {
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killProcessTree kills the child together with its whole process group,
// falling back to the single process if the group kill fails.
func killProcessTree(c *exec.Cmd) {
	if c.Process == nil {
		return
	}
	if err := syscall.Kill(-c.Process.Pid, syscall.SIGKILL); err != nil {
		_ = c.Process.Kill()
	}
}
