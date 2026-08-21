package main

import (
	"encoding/json"
	"fmt"
)

type clientStateDocument struct {
	ClientID       int       `json:"client_id"`
	Proxy          string    `json:"proxy"`
	Passphrase     string    `json:"passphrase"`
	FolderSend     string    `json:"folder_send"`
	FolderRecv     string    `json:"folder_recv"`
	DNSResolver    string    `json:"dns_resolver"`
	Autostart      bool      `json:"autostart"`
	SpeedMode      string    `json:"speed_mode"`
	SpeedDirection string    `json:"speed_direction"`
	SpeedStreams   int       `json:"speed_streams"`
	SpeedTimeout   int       `json:"speed_timeout"`
	Accounts       []account `json:"accounts"`
}

func (a *application) loadPersistedState() error {
	raw, err := loadClientStateBytes()
	if err != nil || len(raw) == 0 {
		return err
	}
	var doc clientStateDocument
	if err := json.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("decode client settings: %w", err)
	}
	if doc.ClientID >= 1 && doc.ClientID <= 255 {
		a.clientID = doc.ClientID
	}
	if doc.Proxy != "" {
		a.proxy = doc.Proxy
	}
	a.passphrase = doc.Passphrase
	if doc.FolderSend != "" {
		a.folderSend = doc.FolderSend
	}
	if doc.FolderRecv != "" {
		a.folderRecv = doc.FolderRecv
	}
	if doc.DNSResolver != "" {
		a.dnsResolver = doc.DNSResolver
	}
	a.autostart = doc.Autostart
	if doc.SpeedMode != "" {
		a.speedMode = doc.SpeedMode
	}
	if doc.SpeedDirection != "" {
		a.speedDirection = doc.SpeedDirection
	}
	if doc.SpeedStreams >= 0 && doc.SpeedStreams <= 8 {
		a.speedStreams = doc.SpeedStreams
	}
	if doc.SpeedTimeout >= 15 && doc.SpeedTimeout <= 300 {
		a.speedTimeout = doc.SpeedTimeout
	}
	a.accounts = doc.Accounts
	a.syncLanes()
	return nil
}

func (a *application) persist() {
	doc := clientStateDocument{
		ClientID: a.clientID, Proxy: a.proxy, Passphrase: a.passphrase,
		FolderSend: a.folderSend, FolderRecv: a.folderRecv, DNSResolver: a.dnsResolver,
		Autostart: a.autostart, SpeedMode: a.speedMode, SpeedDirection: a.speedDirection,
		SpeedStreams: a.speedStreams, SpeedTimeout: a.speedTimeout, Accounts: a.accounts,
	}
	raw, err := json.Marshal(doc)
	if err == nil {
		err = saveClientStateBytes(raw)
	}
	if err != nil {
		a.log("Не удалось сохранить настройки: " + err.Error())
	}
}
