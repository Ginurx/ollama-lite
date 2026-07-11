//go:build !windows

package tui

// enableVTOutput is a no-op on non-Windows platforms, where terminals already
// interpret ANSI escape sequences.
func enableVTOutput() {}
