//go:build !windows && !linux && !android

package main

import "fmt"

type terminalState struct{}

func prepareConsole() {}

func resizeConsole(width, height int) {}

func flushTerminalInput(fd int) {}

func isTerminal(fd int) bool { return false }

func makeTerminalRaw(fd int) (*terminalState, error) {
	return nil, fmt.Errorf("interactive terminal is not supported on this platform")
}

func restoreTerminal(fd int, state *terminalState) error { return nil }

func terminalSize(fd int) (int, int, error) { return 0, 0, fmt.Errorf("terminal size unavailable") }
