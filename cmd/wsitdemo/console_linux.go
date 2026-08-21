//go:build linux || android

package main

import "golang.org/x/sys/unix"

type terminalState struct {
	termios unix.Termios
}

func prepareConsole() {}

func resizeConsole(width, height int) {}

func flushTerminalInput(fd int) {}

func isTerminal(fd int) bool {
	_, err := unix.IoctlGetTermios(fd, unix.TCGETS)
	return err == nil
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
	raw.Cc[unix.VMIN] = 1
	raw.Cc[unix.VTIME] = 0
	if err := unix.IoctlSetTermios(fd, unix.TCSETS, &raw); err != nil {
		return nil, err
	}
	return &terminalState{termios: *current}, nil
}

func restoreTerminal(fd int, state *terminalState) error {
	if state == nil {
		return nil
	}
	return unix.IoctlSetTermios(fd, unix.TCSETS, &state.termios)
}

func terminalSize(fd int) (int, int, error) {
	ws, err := unix.IoctlGetWinsize(fd, unix.TIOCGWINSZ)
	if err != nil {
		return 0, 0, err
	}
	return int(ws.Col), int(ws.Row), nil
}
