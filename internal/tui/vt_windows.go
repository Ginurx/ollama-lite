//go:build windows

package tui

import (
	"os"

	"golang.org/x/sys/windows"
)

// enableVTOutput turns on ENABLE_VIRTUAL_TERMINAL_PROCESSING for stdout so the
// ANSI escape sequences the picker emits (cursor movement, colors) render on
// Windows conhost, which does not enable VT processing by default. Best-effort:
// failures leave the console mode unchanged.
func enableVTOutput() {
	h := windows.Handle(os.Stdout.Fd())
	var mode uint32
	if err := windows.GetConsoleMode(h, &mode); err != nil {
		return
	}
	_ = windows.SetConsoleMode(h, mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING)
}
