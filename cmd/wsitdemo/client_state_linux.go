//go:build linux

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

func linuxConfigHome() (string, error) {
	if configured := os.Getenv("XDG_CONFIG_HOME"); configured != "" {
		if !filepath.IsAbs(configured) {
			return "", fmt.Errorf("XDG_CONFIG_HOME must be absolute")
		}
		return configured, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config"), nil
}

func clientStatePath() (string, error) {
	configHome, err := linuxConfigHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(configHome, "wsit", "client.dat"), nil
}

func loadClientStateBytes() ([]byte, error) {
	path, err := clientStatePath()
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return raw, err
}

func saveClientStateBytes(raw []byte) error {
	path, err := clientStatePath()
	if err != nil {
		return err
	}
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(directory, ".client-*.new")
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
	if err := temporary.Chmod(0o600); err != nil {
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
	return os.Chmod(path, 0o600)
}
