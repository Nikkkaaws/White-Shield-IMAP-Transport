package mobile

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Nikkkaaws/wsit/internal/pairing"
)

func validMobileConfig() string {
	return `{
  "mode":"client",
  "listen":"127.0.0.1:1080",
  "target":"direct",
  "dns_resolver":"1.1.1.1:53",
  "passphrase":"a-real-test-passphrase",
  "client_id":2,
  "folder_send":"Notes",
  "folder_recv":"Journal",
  "accounts":[
    {"enabled":true,"host":"imap.one.test","port":993,"username":"one@example.test","password":"secret"},
    {"enabled":true,"host":"imap.two.test","port":1993,"username":"two@example.test","password":"secret2"}
  ]
}`
}

func TestDecodePairingCode(t *testing.T) {
	code, err := pairing.Encode(pairing.Profile{Passphrase: "mobile-test-secret", FolderSend: "Notes", FolderRecv: "Journal"})
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(DecodePairingCode(code)), &result); err != nil {
		t.Fatal(err)
	}
	if result["ok"] != true || result["passphrase"] != "mobile-test-secret" {
		t.Fatalf("result = %#v", result)
	}
}

func TestParseConfigPreservesPerAccountServers(t *testing.T) {
	cfg, err := parseConfig(validMobileConfig())
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}
	accounts := cfg.AccountList()
	if len(accounts) != 2 || accounts[0].Host != "imap.one.test" || accounts[1].Port != 1993 {
		t.Fatalf("unexpected accounts: %+v", accounts)
	}
}

func TestValidateConfigRejectsBadClientIDAndMissingSecrets(t *testing.T) {
	badID := strings.Replace(validMobileConfig(), `"client_id":2`, `"client_id":256`, 1)
	if detail := ValidateConfig(badID); !strings.Contains(detail, "1 до 255") {
		t.Fatalf("bad client ID detail=%q", detail)
	}
	missingPass := strings.Replace(validMobileConfig(), `"a-real-test-passphrase"`, `""`, 1)
	if detail := ValidateConfig(missingPass); detail == "" {
		t.Fatal("missing passphrase accepted")
	}
}

func TestControllerStatusDoesNotExposePasswords(t *testing.T) {
	controller, err := NewController(validMobileConfig())
	if err != nil {
		t.Fatalf("new controller: %v", err)
	}
	status := controller.Status()
	if strings.Contains(status, "secret") || strings.Contains(status, "passphrase") {
		t.Fatalf("secret leaked in status: %s", status)
	}
	var decoded statusDocument
	if err := json.Unmarshal([]byte(status), &decoded); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if decoded.ClientID != 2 || decoded.Phase != "stopped" {
		t.Fatalf("unexpected status: %+v", decoded)
	}
}

func TestDefaultConfigIsValidJSON(t *testing.T) {
	var doc configDocument
	if err := json.Unmarshal([]byte(DefaultConfig("client")), &doc); err != nil {
		t.Fatalf("default config JSON: %v", err)
	}
	if doc.ClientID != 1 || doc.Listen != "127.0.0.1:1080" {
		t.Fatalf("unexpected defaults: %+v", doc)
	}
}

func TestTransitionLogMovesControllerToRunning(t *testing.T) {
	controller, err := NewController(validMobileConfig())
	if err != nil {
		t.Fatalf("new controller: %v", err)
	}
	controller.phase = "starting"
	controller.onLog("lane up", "lane up")
	controller.onLog("wsit up", "wsit up")
	controller.onLog("socks", "socks")
	if controller.phase != "running" || controller.stage != "Работает" {
		t.Fatalf("unexpected state: %s/%s", controller.phase, controller.stage)
	}
}
