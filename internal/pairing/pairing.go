package pairing

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

const prefix = "WSIT1."

type Profile struct {
	Version     int    `json:"v"`
	Passphrase  string `json:"passphrase"`
	FolderSend  string `json:"folder_send"`
	FolderRecv  string `json:"folder_recv"`
	DNSResolver string `json:"dns_resolver"`
}

func Encode(profile Profile) (string, error) {
	profile.Version = 1
	if strings.TrimSpace(profile.Passphrase) == "" {
		return "", fmt.Errorf("wsit: connection key is empty")
	}
	raw, err := json.Marshal(profile)
	if err != nil {
		return "", err
	}
	return prefix + base64.RawURLEncoding.EncodeToString(raw), nil
}

func Decode(code string) (Profile, error) {
	code = strings.TrimSpace(code)
	if !strings.HasPrefix(code, prefix) {
		return Profile{}, fmt.Errorf("wsit: invalid connection code")
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(code, prefix))
	if err != nil {
		return Profile{}, fmt.Errorf("wsit: invalid connection code: %w", err)
	}
	var profile Profile
	if err := json.Unmarshal(raw, &profile); err != nil {
		return Profile{}, fmt.Errorf("wsit: invalid connection code: %w", err)
	}
	if profile.Version != 1 || strings.TrimSpace(profile.Passphrase) == "" {
		return Profile{}, fmt.Errorf("wsit: unsupported connection code")
	}
	return profile, nil
}
