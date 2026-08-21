//go:build !windows

package imapc

import "syscall"

func interfaceControl(uint32) func(string, string, syscall.RawConn) error {
	return nil
}
