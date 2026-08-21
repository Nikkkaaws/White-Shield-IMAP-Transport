//go:build windows

package main

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	kernel32  = windows.NewLazySystemDLL("kernel32.dll")
	setBuffer = kernel32.NewProc("SetConsoleScreenBufferSize")
	setWindow = kernel32.NewProc("SetConsoleWindowInfo")
)

type terminalState struct{ mode uint32 }

func prepareConsole() {
	_ = windows.SetConsoleCP(65001)
	_ = windows.SetConsoleOutputCP(65001)
	handle, _ := windows.GetStdHandle(windows.STD_OUTPUT_HANDLE)
	var mode uint32
	if windows.GetConsoleMode(handle, &mode) == nil {
		_ = windows.SetConsoleMode(handle, mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING)
	}
}

func makeTerminalRaw(fd int) (*terminalState, error) {
	handle := windows.Handle(fd)
	var mode uint32
	if err := windows.GetConsoleMode(handle, &mode); err != nil {
		return nil, err
	}
	raw := mode &^ (windows.ENABLE_ECHO_INPUT | windows.ENABLE_LINE_INPUT | windows.ENABLE_PROCESSED_INPUT)
	raw |= windows.ENABLE_VIRTUAL_TERMINAL_INPUT
	if err := windows.SetConsoleMode(handle, raw); err != nil {
		return nil, err
	}
	return &terminalState{mode: mode}, nil
}

func restoreTerminal(fd int, state *terminalState) error {
	if state == nil {
		return nil
	}
	return windows.SetConsoleMode(windows.Handle(fd), state.mode)
}

func resizeConsole(width, height int) {
	handle, err := windows.GetStdHandle(windows.STD_OUTPUT_HANDLE)
	if err != nil {
		return
	}
	packed := uintptr(uint32(uint16(width)) | uint32(uint16(height))<<16)
	_, _, _ = setBuffer.Call(uintptr(handle), packed)
	rect := windows.SmallRect{Left: 0, Top: 0, Right: int16(width - 1), Bottom: int16(height - 1)}
	_, _, _ = setWindow.Call(uintptr(handle), 1, uintptr(unsafe.Pointer(&rect)))
}
