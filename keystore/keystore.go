package keystore

import (
	"errors"

	"github.com/99designs/keyring"
)

const (
	serviceName = "growud"
	tokenKey    = "growatt-token"
)

func open() (keyring.Keyring, error) {
	return keyring.Open(keyring.Config{
		ServiceName:              serviceName,
		KeychainTrustApplication: true,
	})
}

// GetToken retrieves the Growatt API token from the OS keyring.
// Returns ("", nil) if no token is stored.
func GetToken() (string, error) {
	ring, err := open()
	if err != nil {
		return "", err
	}
	item, err := ring.Get(tokenKey)
	if errors.Is(err, keyring.ErrKeyNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return string(item.Data), nil
}

// SetToken saves the Growatt API token to the OS keyring.
func SetToken(token string) error {
	ring, err := open()
	if err != nil {
		return err
	}
	return ring.Set(keyring.Item{
		Key:         tokenKey,
		Label:       "Growatt API Token",
		Description: "Growatt API Token",
		Data:        []byte(token),
	})
}

// DeleteToken removes the Growatt API token from the OS keyring.
func DeleteToken() error {
	ring, err := open()
	if err != nil {
		return err
	}
	err = ring.Remove(tokenKey)
	if errors.Is(err, keyring.ErrKeyNotFound) {
		return nil
	}
	return err
}
