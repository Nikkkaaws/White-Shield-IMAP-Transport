//go:build windows

package main

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	kernel32Console                = windows.NewLazySystemDLL("kernel32.dll")
	procSetConsoleScreenBufferSize = kernel32Console.NewProc("SetConsoleScreenBufferSize")
	procSetConsoleWindowInfo       = kernel32Console.NewProc("SetConsoleWindowInfo")
	procFlushConsoleInputBuffer    = kernel32Console.NewProc("FlushConsoleInputBuffer")
)

type terminalState struct {
	mode uint32
}

func prepareConsole() {
	_ = windows.SetConsoleCP(65001)
	_ = windows.SetConsoleOutputCP(65001)
	handle, err := windows.GetStdHandle(windows.STD_OUTPUT_HANDLE)
	if err != nil {
		return
	}
	var mode uint32
	if err := windows.GetConsoleMode(handle, &mode); err != nil {
		return
	}
	_ = windows.SetConsoleMode(handle, mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING)
}

func isTerminal(fd int) bool {
	var mode uint32
	return windows.GetConsoleMode(windows.Handle(fd), &mode) == nil
}

func makeTerminalRaw(fd int) (*terminalState, error) {
	handle := windows.Handle(fd)
	var mode uint32
	if err := windows.GetConsoleMode(handle, &mode); err != nil {
		return nil, err
	}
	raw := mode
	raw &^= windows.ENABLE_ECHO_INPUT | windows.ENABLE_LINE_INPUT | windows.ENABLE_PROCESSED_INPUT
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

func terminalSize(fd int) (int, int, error) {
	var info windows.ConsoleScreenBufferInfo
	if err := windows.GetConsoleScreenBufferInfo(windows.Handle(fd), &info); err != nil {
		return 0, 0, err
	}
	width := int(info.Window.Right-info.Window.Left) + 1
	height := int(info.Window.Bottom-info.Window.Top) + 1
	if width <= 0 || height <= 0 {
		return 0, 0, fmt.Errorf("invalid console size %dx%d", width, height)
	}
	return width, height, nil
}

func resizeConsole(width, height int) {
	if width < 40 || height < 12 || width > 300 || height > 120 {
		return
	}
	handle, err := windows.GetStdHandle(windows.STD_OUTPUT_HANDLE)
	if err != nil {
		return
	}
	var info windows.ConsoleScreenBufferInfo
	if err := windows.GetConsoleScreenBufferInfo(handle, &info); err != nil {
		return
	}
	bufferWidth := max(width, int(info.Size.X))
	bufferHeight := max(height, int(info.Size.Y))
	if int(info.Size.X) < width || int(info.Size.Y) < height {
		if !setConsoleBufferSize(handle, bufferWidth, bufferHeight) {
			return
		}
	}
	rect := windows.SmallRect{Left: 0, Top: 0, Right: int16(width - 1), Bottom: int16(height - 1)}
	if !setConsoleWindowRect(handle, &rect) {
		return
	}
	_ = setConsoleBufferSize(handle, width, height)
}

func setConsoleBufferSize(handle windows.Handle, width, height int) bool {
	packed := uintptr(uint32(uint16(width)) | uint32(uint16(height))<<16)
	result, _, _ := procSetConsoleScreenBufferSize.Call(uintptr(handle), packed)
	return result != 0
}

func setConsoleWindowRect(handle windows.Handle, rect *windows.SmallRect) bool {
	result, _, _ := procSetConsoleWindowInfo.Call(uintptr(handle), 1, uintptr(unsafe.Pointer(rect)))
	return result != 0
}

func flushTerminalInput(fd int) {
	_, _, _ = procFlushConsoleInputBuffer.Call(uintptr(windows.Handle(fd)))
}
