//go:build linux

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func useTemporaryLinuxHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	return home
}

func TestLinuxStateRoundTripAndPermissions(t *testing.T) {
	useTemporaryLinuxHome(t)
	want := []byte(`{"password":"linux-secret"}`)
	if err := saveClientStateBytes(want); err != nil {
		t.Fatal(err)
	}
	got, err := loadClientStateBytes()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("state = %q", got)
	}
	path, err := clientStatePath()
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("state mode = %o", info.Mode().Perm())
	}
}

func TestLinuxInstallAutostartAndUninstall(t *testing.T) {
	useTemporaryLinuxHome(t)
	previousDelay := linuxClientStepDelay
	linuxClientStepDelay = time.Millisecond
	t.Cleanup(func() { linuxClientStepDelay = previousDelay })

	if err := ensureClientInstallation(); err != nil {
		t.Fatal(err)
	}
	paths, err := resolveLinuxClientPaths()
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{paths.executable, paths.command, paths.desktop} {
		if _, err := os.Lstat(path); err != nil {
			t.Fatalf("installed path %s: %v", path, err)
		}
	}
	linkTarget, err := os.Readlink(paths.command)
	if err != nil {
		t.Fatal(err)
	}
	if linkTarget != paths.executable {
		t.Fatalf("command target = %q", linkTarget)
	}
	desktop, err := os.ReadFile(paths.desktop)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(desktop), "Terminal=true") || !strings.Contains(string(desktop), quoteDesktopExec(paths.executable)) {
		t.Fatalf("invalid desktop entry: %q", desktop)
	}
	if err := configureClientAutostart(true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(paths.autostart); err != nil {
		t.Fatal(err)
	}
	if err := saveClientStateBytes([]byte(`{"client_id":7}`)); err != nil {
		t.Fatal(err)
	}
	if err := scheduleClientUninstall(os.Getpid()); err != nil {
		t.Fatal(err)
	}
	statePath, err := clientStatePath()
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{paths.executable, paths.command, paths.desktop, paths.autostart, statePath} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("path survived uninstall: %s (%v)", path, err)
		}
	}
}
