//go:build windows

package imapc

import (
	"math/bits"
	"syscall"

	"golang.org/x/sys/windows"
)

const ipUnicastInterface = 31

func interfaceControl(index uint32) func(string, string, syscall.RawConn) error {
	if index == 0 {
		return nil
	}
	// Winsock expects IF_INDEX in network byte order for IP_UNICAST_IF.
	networkOrderIndex := int(bits.ReverseBytes32(index))
	return func(_, _ string, raw syscall.RawConn) error {
		var socketErr error
		if err := raw.Control(func(fd uintptr) {
			socketErr = windows.SetsockoptInt(
				windows.Handle(fd),
				windows.IPPROTO_IP,
				ipUnicastInterface,
				networkOrderIndex,
			)
		}); err != nil {
			return err
		}
		return socketErr
	}
}
