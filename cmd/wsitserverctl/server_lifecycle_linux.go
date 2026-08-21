//go:build linux

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/Nikkkaaws/wsit/internal/config"
)

const (
	serverInstallDir  = "/usr/local/lib/wsit"
	serverExecutable  = "/usr/local/lib/wsit/wsit"
	serverCommand     = "/usr/local/bin/wsit"
	serverRootCommand = "/wsit"
	serverConfigDir   = "/etc/wsit"
	serverConfigPath  = "/etc/wsit/config.yaml"
	serverUnitPath    = "/etc/systemd/system/wsit.service"
	serverStatusPath  = "/run/wsit/status.json"
)

func prepareServerInstallation(configPath string, elevated bool) (string, bool, bool, error) {
	if os.Geteuid() != 0 {
		executable, err := os.Executable()
		if err != nil {
			return "", false, false, err
		}
		args := append([]string{"--elevated"}, os.Args[1:]...)
		cmd := exec.Command("sudo", append([]string{executable}, args...)...)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			return "", false, false, err
		}
		return "", false, true, nil
	}
	_ = elevated
	installedNow, err := installServerExecutable(configPath)
	if err != nil {
		return "", false, false, err
	}
	if installedNow {
		if _, err := config.Load(serverConfigPath); err == nil {
			serverInstallStep(6, 6, "Запуск и проверка systemd-сервиса")
			if err := serverServiceAction("enable", "--now"); err != nil {
				return "", false, false, err
			}
		} else {
			serverInstallStep(6, 6, "Установка завершена; конфигурация требует настройки")
		}
		time.Sleep(500 * time.Millisecond)
	}
	return serverConfigPath, true, false, nil
}

func installServerExecutable(configPath string) (bool, error) {
	current, err := os.Executable()
	if err != nil {
		return false, err
	}
	current, err = filepath.Abs(current)
	if err != nil {
		return false, err
	}
	installedNow := filepath.Clean(current) != serverExecutable
	if installedNow {
		serverInstallStep(1, 6, "Проверка каталогов и прав")
	}
	if err := os.MkdirAll(serverInstallDir, 0o755); err != nil {
		return false, err
	}
	if err := os.MkdirAll(serverConfigDir, 0o700); err != nil {
		return false, err
	}
	if installedNow {
		serverInstallStep(2, 6, "Копирование серверного клиента")
		if err := copyServerExecutable(current, serverExecutable); err != nil {
			return false, err
		}
	}
	_, configErr := os.Stat(serverConfigPath)
	if installedNow {
		if errors.Is(configErr, os.ErrNotExist) {
			serverInstallStep(3, 6, "Установка конфигурации")
		} else {
			serverInstallStep(3, 6, "Сохранение существующей конфигурации")
		}
	}
	if errors.Is(configErr, os.ErrNotExist) {
		source, resolveErr := filepath.Abs(configPath)
		if resolveErr == nil {
			if info, statErr := os.Stat(source); statErr == nil && !info.IsDir() {
				if err := copyFileMode(source, serverConfigPath, 0o600); err != nil {
					return false, err
				}
			}
		}
	}
	if installedNow {
		serverInstallStep(4, 6, "Регистрация systemd-сервиса")
	}
	if err := writeServerUnit(); err != nil {
		return false, err
	}
	if err := replaceSymlink(serverCommand, serverExecutable); err != nil {
		return false, err
	}
	if err := replaceSymlink(serverRootCommand, serverCommand); err != nil {
		return false, err
	}
	if installedNow {
		serverInstallStep(5, 6, "Создание команд wsit и /wsit")
	}
	if err := runSystemctl("daemon-reload"); err != nil {
		return false, err
	}
	return installedNow, nil
}

func serverInstallStep(index, total int, label string) {
	fmt.Printf("[%d/%d] %s\r\n", index, total, label)
	time.Sleep(300 * time.Millisecond)
}

func copyServerExecutable(source, target string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	temporary := target + ".new"
	out, err := os.OpenFile(temporary, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		_ = os.Remove(temporary)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(temporary)
		return closeErr
	}
	if err := os.Chmod(temporary, 0o755); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return os.Rename(temporary, target)
}

func copyFileMode(source, target string, mode os.FileMode) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		_ = os.Remove(target)
		return copyErr
	}
	return closeErr
}

func replaceSymlink(path, target string) error {
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink == 0 {
			return fmt.Errorf("%s already exists and is not a symlink", path)
		}
		if err := os.Remove(path); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.Symlink(target, path)
}

func writeServerUnit() error {
	const unit = `# Managed by WSIT
[Unit]
Description=White-Shield IMAP Transport server
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=/usr/local/lib/wsit/wsit --daemon --config /etc/wsit/config.yaml
Restart=on-failure
RestartSec=3s
TimeoutStopSec=30s
KillSignal=SIGTERM
NoNewPrivileges=true
PrivateTmp=true
ProtectHome=true
ProtectSystem=strict
ProtectKernelTunables=true
ProtectKernelModules=true
ProtectControlGroups=true
RestrictSUIDSGID=true
LockPersonality=true
RuntimeDirectory=wsit
RuntimeDirectoryMode=0755

[Install]
WantedBy=multi-user.target
`
	if raw, err := os.ReadFile(serverUnitPath); err == nil && !strings.Contains(string(raw), "# Managed by WSIT") {
		return fmt.Errorf("%s belongs to another installation", serverUnitPath)
	}
	return os.WriteFile(serverUnitPath, []byte(unit), 0o644)
}

func runSystemctl(args ...string) error {
	cmd := exec.Command("systemctl", args...)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("systemctl %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return nil
}

func serverServiceAction(args ...string) error {
	return runSystemctl(append(args, "wsit.service")...)
}

func serverServiceStatus() transportStatus {
	cmd := exec.Command("systemctl", "is-active", "wsit.service")
	output, _ := cmd.Output()
	switch strings.TrimSpace(string(output)) {
	case "active", "activating":
		if raw, err := os.ReadFile(serverStatusPath); err == nil {
			var status transportStatus
			if json.Unmarshal(raw, &status) == nil && status.Phase != "" {
				return status
			}
		}
		return transportStatus{Phase: "running", Stage: "Работает"}
	case "failed":
		return transportStatus{Phase: "error", Stage: "Ошибка сервиса"}
	default:
		return transportStatus{Phase: "stopped", Stage: "Остановлен"}
	}
}

func serverDefaultConfigPath() string {
	if _, err := os.Stat(serverConfigPath); err == nil {
		return serverConfigPath
	}
	return "config.yaml"
}

func serverServiceLogs(limit int) []string {
	cmd := exec.Command("journalctl", "-u", "wsit.service", "-n", fmt.Sprintf("%d", limit), "--no-pager", "--output=cat")
	output, err := cmd.Output()
	if err != nil {
		return []string{"Не удалось прочитать journalctl: " + err.Error()}
	}
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return nil
	}
	return lines
}

func uninstallServerInstallation() error {
	fmt.Print("\x1b[2J\x1b[H")
	serverUninstallStep(1, 6, "Остановка WSIT")
	_ = runSystemctl("disable", "--now", "wsit.service")
	serverUninstallStep(2, 6, "Удаление команд wsit и /wsit")
	for path, target := range map[string]string{serverCommand: serverExecutable, serverRootCommand: serverCommand} {
		if destination, err := os.Readlink(path); err == nil && destination == target {
			if err := os.Remove(path); err != nil {
				return err
			}
		}
	}
	serverUninstallStep(3, 6, "Удаление systemd-сервиса")
	_ = os.Remove(serverUnitPath)
	serverUninstallStep(4, 6, "Очистка рабочего состояния")
	_ = os.Remove(serverStatusPath)
	serverUninstallStep(5, 6, "Удаление серверного клиента")
	_ = os.Remove(serverExecutable)
	_ = os.Remove(serverInstallDir)
	if err := runSystemctl("daemon-reload"); err != nil {
		return err
	}
	serverUninstallStep(6, 6, "WSIT удалён; /etc/wsit/config.yaml сохранён")
	time.Sleep(750 * time.Millisecond)
	return nil
}

func serverUninstallStep(index, total int, label string) {
	fmt.Printf("[%d/%d] %s\r\n", index, total, label)
	time.Sleep(300 * time.Millisecond)
}

func requestServerUninstall(elevated bool) error {
	if os.Geteuid() == 0 {
		return uninstallServerInstallation()
	}
	_ = elevated
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command("sudo", executable, "--uninstall", "--elevated")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}

func writeServerStatus(status transportStatus) error {
	if err := os.MkdirAll(filepath.Dir(serverStatusPath), 0o755); err != nil {
		return err
	}
	raw, err := json.Marshal(status)
	if err != nil {
		return err
	}
	temporary := serverStatusPath + ".new"
	if err := os.WriteFile(temporary, raw, 0o644); err != nil {
		return err
	}
	return os.Rename(temporary, serverStatusPath)
}

func removeServerStatus() { _ = os.Remove(serverStatusPath) }

func replaceCurrentProcess(path string, args []string) error {
	return syscall.Exec(path, append([]string{path}, args...), os.Environ())
}
