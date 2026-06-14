//go:build !windows
// +build !windows

package ui

import tea "github.com/charmbracelet/bubbletea"

// enableQuickEditCmd is a no-op on non-Windows platforms, where terminals
// handle native text selection without the program touching the input mode.
func enableQuickEditCmd() tea.Cmd { return nil }
