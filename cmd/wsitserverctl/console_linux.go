//go:build linux

package main

import "golang.org/x/sys/unix"

type terminalState struct{ termios unix.Termios }

func prepareConsole()        {}
func resizeConsole(int, int) {}
func restoreTerminal(fd int, state *terminalState) error {
	if state == nil {
		return nil
	}
	return unix.IoctlSetTermios(fd, unix.TCSETS, &state.termios)
}
func makeTerminalRaw(fd int) (*terminalState, error) {
	current, err := unix.IoctlGetTermios(fd, unix.TCGETS)
	if err != nil {
		return nil, err
	}
	raw := *current
	raw.Iflag &^= unix.BRKINT | unix.ICRNL | unix.INPCK | unix.ISTRIP | unix.IXON
	raw.Oflag &^= unix.OPOST
	raw.Cflag |= unix.CS8
	raw.Lflag &^= unix.ECHO | unix.ICANON | unix.IEXTEN | unix.ISIG
	raw.Cc[unix.VMIN], raw.Cc[unix.VTIME] = 1, 0
	if err := unix.IoctlSetTermios(fd, unix.TCSETS, &raw); err != nil {
		return nil, err
	}
	return &terminalState{termios: *current}, nil
}
