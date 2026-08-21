//go:build windows

package main

import (
	"bytes"
	"testing"
)

func TestClientStateDPAPIRoundTrip(t *testing.T) {
	plain := []byte(`{"password":"never-store-plain"}`)
	protected, err := protectClientData(plain)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(protected, []byte("never-store-plain")) {
		t.Fatal("DPAPI output contains plaintext")
	}
	decoded, err := unprotectClientData(protected)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decoded, plain) {
		t.Fatalf("decoded = %q", decoded)
	}
}
