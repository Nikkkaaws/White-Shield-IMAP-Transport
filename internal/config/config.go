package config

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type IMAPAccount struct {
	Host            string `yaml:"host,omitempty" json:"host,omitempty"`
	Port            int    `yaml:"port,omitempty" json:"port,omitempty"`
	PinIP           string `yaml:"pin_ip,omitempty" json:"pin_ip,omitempty"`
	DirectInterface string `yaml:"direct_interface,omitempty" json:"direct_interface,omitempty"`
	Username        string `yaml:"username" json:"username"`
	Password        string `yaml:"password" json:"password"`
}

type IMAP struct {
	Host            string        `yaml:"host"`
	Port            int           `yaml:"port"`
	PinIP           string        `yaml:"pin_ip"`
	DirectInterface string        `yaml:"direct_interface"`
	Username        string        `yaml:"username"`
	Password        string        `yaml:"password"`
	Accounts        []IMAPAccount `yaml:"accounts"`
	FolderSend      string        `yaml:"folder_send"`
	FolderRecv      string        `yaml:"folder_recv"`
}

type Config struct {
	Mode               string `yaml:"mode"`
	Listen             string `yaml:"listen"`
	Target             string `yaml:"target"`
	DNSResolver        string `yaml:"dns_resolver"`
	Passphrase         string `yaml:"passphrase"`
	ClientID           uint8  `yaml:"client_id"`
	IMAP               IMAP   `yaml:"imap"`
	BatchDelayMS       int    `yaml:"batch_delay_ms"`
	BatchMinKB         int    `yaml:"batch_min_kb"`
	BatchMaxKB         int    `yaml:"batch_max_kb"`
	StripeData         bool   `yaml:"stripe_data"`
	StreamReadKB       int    `yaml:"stream_read_kb"`
	StreamWindowKB     int    `yaml:"stream_window_kb"`
	AckEveryFrames     int    `yaml:"ack_every_frames"`
	SendQueueFrames    int    `yaml:"send_queue_frames"`
	ReorderMaxKB       int    `yaml:"reorder_max_kb"`
	IMAPIdleRefreshSec int    `yaml:"imap_idle_refresh_sec"`
	IMAPAppendWorkers  int    `yaml:"imap_append_workers"`
	StatsIntervalSec   int    `yaml:"stats_interval_sec"`
	OptimisticOpenMS   int    `yaml:"optimistic_open_ms"`
	PingIntervalMS     int    `yaml:"ping_interval_ms"`
	PurgeAfterSec      int    `yaml:"purge_after_sec"`
	PurgeEverySec      int    `yaml:"purge_every_sec"`
	PurgeOwner         string `yaml:"purge_owner"`
	LogLevel           string `yaml:"log_level"`
}

func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	cfg := Default()
	if err := yaml.Unmarshal(raw, cfg); err != nil {
		return nil, err
	}
	return cfg, cfg.Validate()
}

func Default() *Config {
	return &Config{
		Mode:               "client",
		Listen:             "127.0.0.1:1080",
		Target:             "127.0.0.1:1080",
		DNSResolver:        "1.1.1.1:53",
		ClientID:           1,
		BatchDelayMS:       5,
		BatchMinKB:         192,
		BatchMaxKB:         384,
		StripeData:         true,
		StreamReadKB:       64,
		StreamWindowKB:     8 * 1024,
		AckEveryFrames:     32,
		SendQueueFrames:    256,
		ReorderMaxKB:       16 * 1024,
		IMAPIdleRefreshSec: 45,
		IMAPAppendWorkers:  1,
		StatsIntervalSec:   15,
		OptimisticOpenMS:   20,
		PingIntervalMS:     10000,
		PurgeAfterSec:      90,
		PurgeEverySec:      30,
		PurgeOwner:         "server",
		LogLevel:           "info",
		IMAP: IMAP{
			Host:            "imap.rambler.ru",
			Port:            993,
			DirectInterface: "auto",
			FolderSend:      "Notes",
			FolderRecv:      "Journal",
		},
	}
}

func (c *Config) Validate() error {
	c.Mode = strings.ToLower(strings.TrimSpace(c.Mode))
	switch c.Mode {
	case "client", "server", "probe":
	default:
		return fmt.Errorf("wsit: mode must be client, server or probe")
	}
	if c.ClientID == 0 {
		c.ClientID = 1
	}
	if c.IMAP.Port == 0 {
		c.IMAP.Port = 993
	}
	if c.IMAP.Host == "" {
		return fmt.Errorf("wsit: imap host required")
	}
	if len(c.AccountList()) == 0 {
		return fmt.Errorf("wsit: imap username/password or accounts required")
	}
	if c.Passphrase == "" || c.Passphrase == "change-me-long-secret" {
		return fmt.Errorf("wsit: set a real passphrase")
	}
	if c.Mode != "probe" {
		if err := mustLoopback(c.Listen, "listen"); err != nil {
			return err
		}
	}
	if c.Mode == "server" {
		if strings.TrimSpace(c.Target) == "" {
			c.Target = "direct"
		}
		if c.Target != "direct" {
			if err := mustLoopback(c.Target, "target"); err != nil {
				return err
			}
		}
	}
	if strings.TrimSpace(c.DNSResolver) == "" {
		c.DNSResolver = "1.1.1.1:53"
	}
	host, port, err := net.SplitHostPort(c.DNSResolver)
	portNumber, portErr := strconv.ParseUint(port, 10, 16)
	if err != nil || strings.TrimSpace(host) == "" || portErr != nil || portNumber == 0 {
		return fmt.Errorf("wsit: dns_resolver must be host:port")
	}
	if c.IMAP.PinIP != "" {
		if ip := net.ParseIP(c.IMAP.PinIP); ip == nil {
			return fmt.Errorf("wsit: pin_ip is not an IP")
		}
	}
	for _, account := range c.AccountList() {
		if strings.TrimSpace(account.Host) == "" {
			return fmt.Errorf("wsit: IMAP host required for %s", account.Username)
		}
		if account.Port < 1 || account.Port > 65535 {
			return fmt.Errorf("wsit: IMAP port must be within 1..65535 for %s", account.Username)
		}
		if account.PinIP != "" && net.ParseIP(account.PinIP) == nil {
			return fmt.Errorf("wsit: pin_ip is not an IP for %s", account.Username)
		}
	}
	c.IMAP.DirectInterface = strings.TrimSpace(c.IMAP.DirectInterface)
	if c.IMAP.DirectInterface == "" {
		c.IMAP.DirectInterface = "auto"
	}
	if c.BatchMaxKB <= 0 {
		c.BatchMaxKB = 384
	}
	if c.BatchMaxKB > 1024 {
		c.BatchMaxKB = 1024
	}
	if c.BatchMinKB <= 0 {
		c.BatchMinKB = min(192, c.BatchMaxKB)
	}
	if c.BatchMinKB > c.BatchMaxKB {
		c.BatchMinKB = c.BatchMaxKB
	}
	if c.BatchDelayMS < 0 {
		c.BatchDelayMS = 0
	}
	if c.BatchDelayMS > 1000 {
		c.BatchDelayMS = 1000
	}
	if c.StreamReadKB < 4 {
		c.StreamReadKB = 64
	}
	if c.StreamReadKB > 256 {
		c.StreamReadKB = 256
	}
	if c.StreamWindowKB < 256 {
		c.StreamWindowKB = 8 * 1024
	}
	if c.StreamWindowKB > 64*1024 {
		c.StreamWindowKB = 64 * 1024
	}
	if c.SendQueueFrames < 64 {
		c.SendQueueFrames = 256
	}
	if c.SendQueueFrames > 4096 {
		c.SendQueueFrames = 4096
	}
	if c.ReorderMaxKB < 1024 {
		c.ReorderMaxKB = 16 * 1024
	}
	if c.ReorderMaxKB > 128*1024 {
		c.ReorderMaxKB = 128 * 1024
	}
	if c.StreamWindowKB >= c.ReorderMaxKB {
		c.StreamWindowKB = c.ReorderMaxKB / 2
	}
	if c.AckEveryFrames < 1 {
		c.AckEveryFrames = 32
	}
	if c.AckEveryFrames > 256 {
		c.AckEveryFrames = 256
	}
	windowFrames := max(4, c.StreamWindowKB/c.StreamReadKB)
	if c.AckEveryFrames >= windowFrames {
		c.AckEveryFrames = max(1, windowFrames/2)
	}
	if c.IMAPIdleRefreshSec < 15 {
		c.IMAPIdleRefreshSec = 45
	}
	if c.IMAPIdleRefreshSec > 300 {
		c.IMAPIdleRefreshSec = 300
	}
	if c.IMAPAppendWorkers < 1 {
		c.IMAPAppendWorkers = 1
	}
	if c.IMAPAppendWorkers > 4 {
		c.IMAPAppendWorkers = 4
	}
	if c.StatsIntervalSec < 0 {
		c.StatsIntervalSec = 0
	}
	if c.OptimisticOpenMS < 0 {
		c.OptimisticOpenMS = 0
	}
	if c.OptimisticOpenMS > 250 {
		c.OptimisticOpenMS = 250
	}
	if c.PurgeAfterSec < 0 {
		c.PurgeAfterSec = 0
	}
	// in-flight IMAP is ~2–4s; wiping faster than this eats live frames
	if c.PurgeAfterSec > 0 && c.PurgeAfterSec < 15 {
		c.PurgeAfterSec = 15
	}
	if c.PurgeEverySec <= 0 {
		c.PurgeEverySec = 30
	}
	if c.PurgeEverySec < 10 {
		c.PurgeEverySec = 10
	}
	c.PurgeOwner = strings.ToLower(strings.TrimSpace(c.PurgeOwner))
	if c.PurgeOwner == "" {
		c.PurgeOwner = "server"
	}
	if c.PurgeOwner != "server" && c.PurgeOwner != "all" {
		return fmt.Errorf("wsit: purge_owner must be server or all")
	}
	return nil
}

func mustLoopback(addr, name string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("wsit: %s: %w", name, err)
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("wsit: %s must be 127.0.0.1 (refusing %s — will not touch PC routing)", name, addr)
	}
	return nil
}

func (c *Config) AccountList() []IMAPAccount {
	seen := map[string]struct{}{}
	var out []IMAPAccount
	add := func(a IMAPAccount) {
		a.Username = strings.TrimSpace(a.Username)
		if a.Username == "" || a.Password == "" {
			return
		}
		if strings.TrimSpace(a.Host) == "" {
			a.Host = c.IMAP.Host
		}
		if a.Port == 0 {
			a.Port = c.IMAP.Port
		}
		if strings.TrimSpace(a.PinIP) == "" {
			a.PinIP = c.IMAP.PinIP
		}
		if strings.TrimSpace(a.DirectInterface) == "" {
			a.DirectInterface = c.IMAP.DirectInterface
		}
		k := strings.ToLower(a.Host + "\x00" + a.Username)
		if _, ok := seen[k]; ok {
			return
		}
		seen[k] = struct{}{}
		out = append(out, a)
	}
	add(IMAPAccount{
		Host: c.IMAP.Host, Port: c.IMAP.Port, PinIP: c.IMAP.PinIP,
		DirectInterface: c.IMAP.DirectInterface,
		Username:        c.IMAP.Username, Password: c.IMAP.Password,
	})
	for _, a := range c.IMAP.Accounts {
		add(a)
	}
	return out
}

func (a IMAPAccount) DialHost() string {
	if a.PinIP != "" {
		return a.PinIP
	}
	return a.Host
}

func (c *Config) DialHost() string {
	if c.IMAP.PinIP != "" {
		return c.IMAP.PinIP
	}
	return c.IMAP.Host
}

func (c *Config) SendFolder() string {
	if c.Mode == "server" {
		return c.IMAP.FolderRecv
	}
	return c.IMAP.FolderSend
}

func (c *Config) RecvFolder() string {
	if c.Mode == "server" {
		return c.IMAP.FolderSend
	}
	return c.IMAP.FolderRecv
}
