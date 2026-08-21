package main

import (
	"strings"
	"testing"

	"github.com/Nikkkaaws/wsit/internal/pairing"
)

func TestSnapshotContainsServerFunctionsWithoutSecrets(t *testing.T) {
	rendered := stripANSI(newApplication("missing-config.yaml").render())
	for _, want := range []string{"WSIT SERVER CONTROL", "Запустить сервер", "Обзор", "Аккаунты", "Проверка", "Код подключения", "Настройки", "Журнал", "Удалить WSIT"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("missing %q", want)
		}
	}
	if strings.Contains(strings.ToLower(rendered), "password") {
		t.Fatal("password label leaked")
	}
}

func TestChunkRunes(t *testing.T) {
	got := chunkRunes("абвгд", 2)
	want := []string{"аб", "вг", "д"}
	if len(got) != len(want) {
		t.Fatalf("chunks = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("chunks = %v", got)
		}
	}
}

func TestPairingCodeContainsOnlyClientTransportSettings(t *testing.T) {
	code, err := pairingCode("testdata/server.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(code, "test@example.test") || strings.Contains(code, "test-mail-password") {
		t.Fatal("pairing code contains IMAP credentials")
	}
	profile, err := pairing.Decode(code)
	if err != nil {
		t.Fatal(err)
	}
	if profile.Passphrase != "test-connection-secret" || profile.FolderSend != "Notes" || profile.FolderRecv != "Journal" {
		t.Fatalf("profile = %+v", profile)
	}
}

func TestRedactEmail(t *testing.T) {
	if got := redactEmail("relay@example.com"); got != "r***@example.com" {
		t.Fatalf("redact=%q", got)
	}
}

func TestServerStatusColumnFits(t *testing.T) {
	app := newApplication("missing-config.yaml")
	rendered := stripANSI(app.render())
	for _, line := range strings.Split(rendered, "\n") {
		if len([]rune(strings.TrimSuffix(line, "\r"))) > 96 {
			t.Fatalf("line wider than 96: %q", line)
		}
	}
}
