package cryptox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
)

const (
	NonceSize = 12
	Overhead  = NonceSize + 16
)

var ErrAuth = errors.New("wsit: decrypt failed")

func DeriveKey(passphrase string) ([32]byte, error) {
	var out [32]byte
	if passphrase == "" {
		return out, errors.New("wsit: empty passphrase")
	}
	key, err := hkdfSHA256([]byte(passphrase), []byte("wsit-v1-salt"), []byte("wsit-aead"), 32)
	if err != nil {
		return out, err
	}
	copy(out[:], key)
	return out, nil
}

func hkdfSHA256(secret, salt, info []byte, n int) ([]byte, error) {
	if n <= 0 || n > 255*32 {
		return nil, errors.New("wsit: hkdf length")
	}
	mac := hmac.New(sha256.New, salt)
	if _, err := mac.Write(secret); err != nil {
		return nil, err
	}
	prk := mac.Sum(nil)
	var (
		t   []byte
		okm []byte
		ctr byte
	)
	for len(okm) < n {
		ctr++
		mac = hmac.New(sha256.New, prk)
		if _, err := mac.Write(t); err != nil {
			return nil, err
		}
		if _, err := mac.Write(info); err != nil {
			return nil, err
		}
		if _, err := mac.Write([]byte{ctr}); err != nil {
			return nil, err
		}
		t = mac.Sum(nil)
		okm = append(okm, t...)
	}
	return okm[:n], nil
}

func Seal(key [32]byte, plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, NonceSize)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("wsit: nonce: %w", err)
	}
	ct := aead.Seal(nil, nonce, plaintext, []byte("WST1"))
	out := make([]byte, 0, NonceSize+len(ct))
	out = append(out, nonce...)
	out = append(out, ct...)
	return out, nil
}

func Open(key [32]byte, blob []byte) ([]byte, error) {
	if len(blob) < Overhead {
		return nil, ErrAuth
	}
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	pt, err := aead.Open(nil, blob[:NonceSize], blob[NonceSize:], []byte("WST1"))
	if err != nil {
		return nil, ErrAuth
	}
	return pt, nil
}
