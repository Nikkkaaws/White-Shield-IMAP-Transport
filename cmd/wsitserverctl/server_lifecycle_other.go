//go:build !linux

package main

import "fmt"

func prepareServerInstallation(configPath string, elevated bool) (string, bool, bool, error) {
	return configPath, false, false, nil
}

func serverServiceAction(args ...string) error {
	return fmt.Errorf("systemd service control is only available on Linux")
}

func serverServiceStatus() transportStatus {
	return transportStatus{Phase: "stopped", Stage: "Остановлен"}
}

func serverServiceLogs(limit int) []string { return nil }

func serverDefaultConfigPath() string { return "config.yaml" }

func uninstallServerInstallation() error {
	return fmt.Errorf("self-uninstall is only available in the Linux client")
}

func requestServerUninstall(elevated bool) error {
	return fmt.Errorf("self-uninstall is only available in the Linux client")
}

func writeServerStatus(status transportStatus) error { return nil }
func removeServerStatus()                            {}
