//go:build windows
// +build windows

package ui

import (
	tea "github.com/charmbracelet/bubbletea"
	"golang.org/x/sys/windows"
)

// enableQuickEditCmd re-enables the Windows console "QuickEdit Mode" that
// bubbletea's input reader turns off on startup.
//
// On Windows, bubbletea's console input reader sets the input mode to
// ENABLE_MOUSE_INPUT | ENABLE_WINDOW_INPUT | ENABLE_EXTENDED_FLAGS. Because
// ENABLE_EXTENDED_FLAGS is set WITHOUT ENABLE_QUICK_EDIT_MODE, QuickEdit
// (native left-drag text selection + right-click copy) is disabled, and
// ENABLE_MOUSE_INPUT routes mouse events to the program instead of the
// terminal. We restore the native behaviour by turning QuickEdit back on and
// dropping mouse input.
//
// This must run after the input reader has been initialised, so it is issued
// as a tea.Cmd from Model.Init (commands run once the event loop has started,
// which is after the reader's console-mode setup).
func enableQuickEditCmd() tea.Cmd {
	return func() tea.Msg {
		restoreQuickEdit()
		return nil
	}
}

func restoreQuickEdit() {
	namep, err := windows.UTF16PtrFromString("CONIN$")
	if err != nil {
		return
	}
	// All handles to CONIN$ share the same console input buffer, so the mode
	// we set here applies to the buffer bubbletea reads from.
	h, err := windows.CreateFile(
		namep,
		windows.GENERIC_READ|windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
		nil,
		windows.OPEN_EXISTING,
		0,
		0,
	)
	if err != nil {
		return
	}
	defer func() { _ = windows.CloseHandle(h) }()

	var mode uint32
	if err := windows.GetConsoleMode(h, &mode); err != nil {
		return
	}

	mode &^= windows.ENABLE_MOUSE_INPUT // let the terminal handle the mouse
	mode |= windows.ENABLE_EXTENDED_FLAGS | windows.ENABLE_QUICK_EDIT_MODE

	_ = windows.SetConsoleMode(h, mode)
}
