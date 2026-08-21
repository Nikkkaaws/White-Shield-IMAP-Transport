//go:build windows

package main

import (
	"errors"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

func clientStatePath() (string, error) {
	roaming := os.Getenv("APPDATA")
	if roaming == "" {
		return "", errors.New("APPDATA is empty")
	}
	return filepath.Join(roaming, "WSIT", "client.dat"), nil
}

func loadClientStateBytes() ([]byte, error) {
	path, err := clientStatePath()
	if err != nil {
		return nil, err
	}
	protected, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return unprotectClientData(protected)
}

func saveClientStateBytes(raw []byte) error {
	path, err := clientStatePath()
	if err != nil {
		return err
	}
	protected, err := protectClientData(raw)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temporary := path + ".new"
	if err := os.WriteFile(temporary, protected, 0o600); err != nil {
		return err
	}
	from, err := windows.UTF16PtrFromString(temporary)
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(from, to, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}

func protectClientData(raw []byte) ([]byte, error) {
	input := dataBlob(raw)
	var output windows.DataBlob
	description, _ := windows.UTF16PtrFromString("WSIT client configuration")
	if err := windows.CryptProtectData(&input, description, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &output); err != nil {
		return nil, err
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(output.Data)))
	return append([]byte(nil), unsafe.Slice(output.Data, output.Size)...), nil
}

func unprotectClientData(raw []byte) ([]byte, error) {
	input := dataBlob(raw)
	var output windows.DataBlob
	if err := windows.CryptUnprotectData(&input, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &output); err != nil {
		return nil, err
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(output.Data)))
	return append([]byte(nil), unsafe.Slice(output.Data, output.Size)...), nil
}

func dataBlob(raw []byte) windows.DataBlob {
	if len(raw) == 0 {
		return windows.DataBlob{}
	}
	return windows.DataBlob{Size: uint32(len(raw)), Data: &raw[0]}
}
