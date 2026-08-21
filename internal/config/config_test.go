package config

import "testing"

func validConfig() *Config {
	return &Config{
		Mode:       "client",
		Listen:     "127.0.0.1:1080",
		Passphrase: "test-passphrase",
		ClientID:   1,
		IMAP: IMAP{
			Host:       "imap.example.test",
			Port:       993,
			Username:   "one@example.test",
			Password:   "secret",
			FolderSend: "Notes",
			FolderRecv: "Journal",
		},
		StreamReadKB:   64,
		StreamWindowKB: 256,
		ReorderMaxKB:   1024,
		AckEveryFrames: 256,
		PurgeOwner:     "server",
	}
}

func TestValidateKeepsAckBelowFlowWindow(t *testing.T) {
	cfg := validConfig()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	windowFrames := cfg.StreamWindowKB / cfg.StreamReadKB
	if cfg.AckEveryFrames >= windowFrames {
		t.Fatalf("ack_every_frames=%d can deadlock window=%d", cfg.AckEveryFrames, windowFrames)
	}
}

func TestAccountListDeduplicatesAndPreservesOrder(t *testing.T) {
	cfg := validConfig()
	cfg.IMAP.Accounts = []IMAPAccount{
		{Username: " ONE@example.test ", Password: "duplicate"},
		{Username: "two@example.test", Password: "second"},
		{Username: "", Password: "missing"},
	}
	accounts := cfg.AccountList()
	if len(accounts) != 2 {
		t.Fatalf("accounts=%d, want 2", len(accounts))
	}
	if accounts[0].Username != "one@example.test" || accounts[1].Username != "two@example.test" {
		t.Fatalf("unexpected account order: %+v", accounts)
	}
}

func TestAccountListSupportsDifferentIMAPServers(t *testing.T) {
	cfg := validConfig()
	cfg.IMAP.Accounts = []IMAPAccount{
		{Host: "imap.other.test", Port: 1993, Username: "one@example.test", Password: "other"},
	}
	accounts := cfg.AccountList()
	if len(accounts) != 2 {
		t.Fatalf("accounts=%d, want 2", len(accounts))
	}
	if accounts[0].Host != "imap.example.test" || accounts[0].Port != 993 {
		t.Fatalf("default endpoint not applied: %+v", accounts[0])
	}
	if accounts[1].DialHost() != "imap.other.test" || accounts[1].Port != 1993 {
		t.Fatalf("per-account endpoint lost: %+v", accounts[1])
	}
}

func TestValidateRejectsUnknownPurgeOwner(t *testing.T) {
	cfg := validConfig()
	cfg.PurgeOwner = "clients"
	if err := cfg.Validate(); err == nil {
		t.Fatal("unknown purge owner was accepted")
	}
}

func TestValidateDNSResolver(t *testing.T) {
	for _, bad := range []string{"missing-port", "1.1.1.1:0", "1.1.1.1:not-a-port", ":53"} {
		cfg := validConfig()
		cfg.DNSResolver = bad
		if err := cfg.Validate(); err == nil {
			t.Fatalf("invalid DNS resolver %q was accepted", bad)
		}
	}
	cfg := validConfig()
	cfg.DNSResolver = "[2606:4700:4700::1111]:53"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("valid IPv6 DNS resolver rejected: %v", err)
	}
}
