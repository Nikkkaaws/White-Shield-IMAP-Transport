//go:build !linux && !windows

package main

import "fmt"

type terminalState struct{}

func prepareConsole()                             {}
func resizeConsole(int, int)                      {}
func makeTerminalRaw(int) (*terminalState, error) { return nil, fmt.Errorf("unsupported terminal") }
func restoreTerminal(int, *terminalState) error   { return nil }
