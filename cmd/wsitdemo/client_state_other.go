//go:build !windows

package main

func loadClientStateBytes() ([]byte, error) { return nil, nil }
func saveClientStateBytes(raw []byte) error { return nil }
