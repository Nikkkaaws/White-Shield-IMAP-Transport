//go:build linux

package main

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const linuxClientName = "wsit-client"

var linuxClientStepDelay = 250 * time.Millisecond

type linuxClientPaths struct {
	installDir string
	executable string
	command    string
	desktop    string
	autostart  string
}

func resolveLinuxClientPaths() (linuxClientPaths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return linuxClientPaths{}, err
	}
	if !filepath.IsAbs(home) {
		return linuxClientPaths{}, fmt.Errorf("home directory must be absolute")
	}
	dataHome := os.Getenv("XDG_DATA_HOME")
	if dataHome == "" {
		dataHome = filepath.Join(home, ".local", "share")
	} else if !filepath.IsAbs(dataHome) {
		return linuxClientPaths{}, fmt.Errorf("XDG_DATA_HOME must be absolute")
	}
	configHome, err := linuxConfigHome()
	if err != nil {
		return linuxClientPaths{}, err
	}
	installDir := filepath.Join(home, ".local", "lib", "wsit")
	return linuxClientPaths{
		installDir: installDir,
		executable: filepath.Join(installDir, linuxClientName),
		command:    filepath.Join(home, ".local", "bin", linuxClientName),
		desktop:    filepath.Join(dataHome, "applications", "wsit-client.desktop"),
		autostart:  filepath.Join(configHome, "autostart", "wsit-client.desktop"),
	}, nil
}

func ensureClientInstallation() error {
	paths, err := resolveLinuxClientPaths()
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
	installing := filepath.Clean(source) != filepath.Clean(paths.executable)
	if installing {
		linuxInstallStep(1, 6, "Проверка установочного пакета")
	}
	if err := os.MkdirAll(paths.installDir, 0o755); err != nil {
		return err
	}
	if installing {
		linuxInstallStep(2, 6, "Копирование клиента")
		if err := copyLinuxExecutable(source, paths.executable); err != nil {
			return err
		}
		linuxInstallStep(3, 6, "Проверка установленного файла")
		match, err := sameLinuxExecutable(source, paths.executable)
		if err != nil {
			return err
		}
		if !match {
			return fmt.Errorf("installed executable verification failed")
		}
		linuxInstallStep(4, 6, "Создание команды wsit-client")
	}
	if err := replaceLinuxSymlink(paths.command, paths.executable); err != nil {
		return err
	}
	if installing {
		linuxInstallStep(5, 6, "Создание пункта меню приложений")
	}
	if err := writeLinuxDesktopEntry(paths.desktop, paths.executable, false); err != nil {
		return err
	}
	if installing {
		linuxInstallStep(6, 6, "Установка завершена")
		time.Sleep(500 * time.Millisecond)
	}
	return nil
}

func linuxInstallStep(index, total int, label string) {
	fmt.Printf("[%d/%d] %s\r\n", index, total, label)
	time.Sleep(linuxClientStepDelay)
}

func copyLinuxExecutable(source, target string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(target), ".wsit-client-*.new")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	committed := false
	defer func() {
		_ = temporary.Close()
		if !committed {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o755); err != nil {
		return err
	}
	if _, err := io.Copy(temporary, in); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, target); err != nil {
		return err
	}
	committed = true
	return nil
}

func sameLinuxExecutable(left, right string) (bool, error) {
	leftHash, err := linuxExecutableHash(left)
	if err != nil {
		return false, err
	}
	rightHash, err := linuxExecutableHash(right)
	if err != nil {
		return false, err
	}
	return leftHash == rightHash, nil
}

func linuxExecutableHash(path string) ([sha256.Size]byte, error) {
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

func replaceLinuxSymlink(link, target string) error {
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		return err
	}
	info, err := os.Lstat(link)
	if err == nil {
		if info.Mode()&os.ModeSymlink == 0 {
			return fmt.Errorf("command path already exists and is not a symlink: %s", link)
		}
		if err := os.Remove(link); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	return os.Symlink(target, link)
}

func writeLinuxDesktopEntry(path, executable string, autostart bool) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	entry := strings.Join([]string{
		"[Desktop Entry]",
		"Type=Application",
		"Version=1.0",
		"Name=WSIT Control",
		"Comment=White-Shield IMAP Transport client",
		"Exec=" + quoteDesktopExec(executable),
		"Terminal=true",
		"Categories=Network;Utility;",
		"StartupNotify=false",
	}, "\n") + "\n"
	if autostart {
		entry += "X-GNOME-Autostart-enabled=true\n"
	}
	return writeLinuxFileAtomic(path, []byte(entry), 0o644)
}

func quoteDesktopExec(path string) string {
	escaped := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "`", "\\`").Replace(path)
	return `"` + escaped + `"`
}

func writeLinuxFileAtomic(path string, raw []byte, mode os.FileMode) error {
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, ".wsit-*.new")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	committed := false
	defer func() {
		_ = temporary.Close()
		if !committed {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(mode); err != nil {
		return err
	}
	if _, err := temporary.Write(raw); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return err
	}
	committed = true
	return os.Chmod(path, mode)
}

func configureClientAutostart(enabled bool) error {
	paths, err := resolveLinuxClientPaths()
	if err != nil {
		return err
	}
	if !enabled {
		if err := os.Remove(paths.autostart); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	return writeLinuxDesktopEntry(paths.autostart, paths.executable, true)
}

func scheduleClientUninstall(_ int) error {
	paths, err := resolveLinuxClientPaths()
	if err != nil {
		return err
	}
	statePath, err := clientStatePath()
	if err != nil {
		return err
	}
	linuxInstallStep(1, 6, "Остановка клиента")
	linuxInstallStep(2, 6, "Удаление файлов приложения")
	if err := removeLinuxFile(paths.executable); err != nil {
		return err
	}
	_ = os.Remove(paths.installDir)
	linuxInstallStep(3, 6, "Удаление команды и пункта меню")
	if err := removeLinuxFile(paths.command); err != nil {
		return err
	}
	if err := removeLinuxFile(paths.desktop); err != nil {
		return err
	}
	linuxInstallStep(4, 6, "Удаление локальных настроек")
	if err := removeLinuxFile(statePath); err != nil {
		return err
	}
	_ = os.Remove(filepath.Dir(statePath))
	linuxInstallStep(5, 6, "Отключение автозапуска")
	if err := removeLinuxFile(paths.autostart); err != nil {
		return err
	}
	linuxInstallStep(6, 6, "WSIT удалён")
	time.Sleep(500 * time.Millisecond)
	return nil
}

func removeLinuxFile(path string) error {
	err := os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
