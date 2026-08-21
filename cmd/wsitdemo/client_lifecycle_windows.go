//go:build windows

package main

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode/utf16"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const (
	clientExeName   = "WSIT-Client.exe"
	clientRegistry  = `Software\Microsoft\Windows\CurrentVersion\Uninstall\WSIT`
	clientRunKey    = `Software\Microsoft\Windows\CurrentVersion\Run`
	clientRunValue  = "WSIT"
	clientDisplay   = "White-Shield IMAP Transport"
	clientPublisher = "WSIT Project"
)

func clientPaths() (installDir, executable, shortcut string, err error) {
	local := os.Getenv("LOCALAPPDATA")
	roaming := os.Getenv("APPDATA")
	if local == "" || roaming == "" {
		return "", "", "", fmt.Errorf("LOCALAPPDATA or APPDATA is empty")
	}
	installDir = filepath.Join(local, "Programs", "WSIT")
	executable = filepath.Join(installDir, clientExeName)
	shortcut = filepath.Join(roaming, "Microsoft", "Windows", "Start Menu", "Programs", "WSIT.lnk")
	return installDir, executable, shortcut, nil
}

func ensureClientInstallation() error {
	installDir, target, shortcut, err := clientPaths()
	if err != nil {
		return err
	}
	source, err := os.Executable()
	if err != nil {
		return err
	}
	source, err = filepath.Abs(source)
	if err != nil {
		return err
	}
	installing := !strings.EqualFold(filepath.Clean(source), filepath.Clean(target))
	if installing {
		clientInstallStep(1, 6, "Проверка установочного пакета")
	}
	if err := os.MkdirAll(installDir, 0o755); err != nil {
		return err
	}
	if installing {
		clientInstallStep(2, 6, "Копирование клиента")
		if err := copyExecutable(source, target); err != nil {
			return err
		}
		clientInstallStep(3, 6, "Проверка установленного файла")
		match, err := sameExecutable(source, target)
		if err != nil {
			return err
		}
		if !match {
			return fmt.Errorf("installed executable verification failed")
		}
		clientInstallStep(4, 6, "Создание ярлыка в меню Пуск")
	}
	if err := createShortcut(shortcut, target); err != nil {
		return err
	}
	if installing {
		clientInstallStep(5, 6, "Регистрация приложения в Windows")
	}
	if err := registerInstalledClient(target, installDir); err != nil {
		return err
	}
	if installing {
		clientInstallStep(6, 6, "Установка завершена")
		time.Sleep(500 * time.Millisecond)
	}
	return nil
}

func clientInstallStep(index, total int, label string) {
	fmt.Printf("[%d/%d] %s\r\n", index, total, label)
	time.Sleep(250 * time.Millisecond)
}

func sameExecutable(left, right string) (bool, error) {
	leftHash, err := executableHash(left)
	if err != nil {
		return false, err
	}
	rightHash, err := executableHash(right)
	if err != nil {
		return false, err
	}
	return leftHash == rightHash, nil
}

func executableHash(path string) ([sha256.Size]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return [sha256.Size]byte{}, err
	}
	var result [sha256.Size]byte
	copy(result[:], hash.Sum(nil))
	return result, nil
}

func copyExecutable(source, target string) error {
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
	from, err := windows.UTF16PtrFromString(temporary)
	if err != nil {
		_ = os.Remove(temporary)
		return err
	}
	to, err := windows.UTF16PtrFromString(target)
	if err != nil {
		_ = os.Remove(temporary)
		return err
	}
	if err := windows.MoveFileEx(from, to, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return nil
}

func createShortcut(shortcut, target string) error {
	script := fmt.Sprintf(
		`$w=New-Object -ComObject WScript.Shell;$s=$w.CreateShortcut('%s');$s.TargetPath='%s';$s.WorkingDirectory='%s';$s.Description='%s';$s.Save()`,
		psQuote(shortcut), psQuote(target), psQuote(filepath.Dir(target)), psQuote(clientDisplay),
	)
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-EncodedCommand", encodedPowerShell(script))
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("create shortcut: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func registerInstalledClient(target, installDir string) error {
	key, _, err := registry.CreateKey(registry.CURRENT_USER, clientRegistry, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	values := map[string]string{
		"DisplayName":     clientDisplay,
		"DisplayVersion":  "0.1.0",
		"Publisher":       clientPublisher,
		"InstallLocation": installDir,
		"DisplayIcon":     target,
		"UninstallString": `"` + target + `" --uninstall`,
	}
	for name, value := range values {
		if err := key.SetStringValue(name, value); err != nil {
			return err
		}
	}
	return key.SetDWordValue("NoModify", 1)
}

func configureClientAutostart(enabled bool) error {
	_, target, _, err := clientPaths()
	if err != nil {
		return err
	}
	key, _, err := registry.CreateKey(registry.CURRENT_USER, clientRunKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	if !enabled {
		err := key.DeleteValue(clientRunValue)
		if err == registry.ErrNotExist {
			return nil
		}
		return err
	}
	return key.SetStringValue(clientRunValue, `"`+target+`"`)
}

func scheduleClientUninstall(pid int) error {
	installDir, _, shortcut, err := clientPaths()
	if err != nil {
		return err
	}
	statePath, stateErr := clientStatePath()
	if stateErr != nil {
		return stateErr
	}
	script := fmt.Sprintf(
		`$Host.UI.RawUI.WindowTitle='Удаление WSIT'; Write-Host '[1/6] Остановка клиента'; Wait-Process -Id %d -ErrorAction SilentlyContinue; Write-Host '[2/6] Удаление файлов приложения'; Remove-Item -LiteralPath '%s' -Recurse -Force -ErrorAction SilentlyContinue; Write-Host '[3/6] Удаление ярлыка'; Remove-Item -LiteralPath '%s' -Force -ErrorAction SilentlyContinue; Write-Host '[4/6] Удаление локальных настроек'; Remove-Item -LiteralPath '%s' -Recurse -Force -ErrorAction SilentlyContinue; Write-Host '[5/6] Очистка регистрации Windows'; Remove-Item -LiteralPath 'HKCU:\%s' -Recurse -Force -ErrorAction SilentlyContinue; Remove-ItemProperty -LiteralPath 'HKCU:\%s' -Name '%s' -Force -ErrorAction SilentlyContinue; Write-Host '[6/6] WSIT удалён'; Start-Sleep -Seconds 2`,
		pid, psQuote(installDir), psQuote(shortcut), psQuote(filepath.Dir(statePath)), clientRegistry, clientRunKey, clientRunValue,
	)
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-EncodedCommand", encodedPowerShell(script))
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_CONSOLE}
	return cmd.Start()
}

func encodedPowerShell(script string) string {
	encoded := utf16.Encode([]rune(script))
	raw := make([]byte, len(encoded)*2)
	for i, value := range encoded {
		binary.LittleEndian.PutUint16(raw[i*2:], value)
	}
	return base64.StdEncoding.EncodeToString(raw)
}

func psQuote(value string) string {
	return strings.ReplaceAll(value, "'", "''")
}
