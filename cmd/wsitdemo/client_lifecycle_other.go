//go:build !windows && !linux

package main

import "fmt"

func ensureClientInstallation() error { return nil }

func configureClientAutostart(enabled bool) error { return nil }

func scheduleClientUninstall(pid int) error {
	return fmt.Errorf("self-uninstall is only available in the Windows client")
}
