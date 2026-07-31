//go:build windows

package ui

// testShell is the local shell used by plugin tests.
func testShell() string { return "cmd" }

// testShellArgs wraps a one-line script into the shell's argument form.
func testShellArgs(script string) []string { return []string{"/c", script} }

// testSleepScript runs long enough for the test to kill it.
const testSleepScript = "ping -n 30 127.0.0.1 >nul"
