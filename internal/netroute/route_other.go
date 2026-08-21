//go:build !windows

package netroute

func resolve(string) (Selection, error) {
	// Linux VPS traffic is already outside the local Windows TUN. Keeping this
	// unbound also avoids requiring SO_BINDTODEVICE privileges.
	return Selection{}, nil
}
