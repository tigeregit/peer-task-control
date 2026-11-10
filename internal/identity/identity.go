package identity

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/libp2p/go-libp2p/core/crypto"
)

func LoadOrCreate(path string) (crypto.PrivKey, error) {
	if path == "" {
		return nil, fmt.Errorf("identity path is empty")
	}
	if b, err := os.ReadFile(path); err == nil {
		key, err := crypto.UnmarshalPrivateKey(b)
		if err != nil {
			return nil, fmt.Errorf("identity %s: %w", path, err)
		}
		return key, nil
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	priv, _, err := crypto.GenerateKeyPair(crypto.Ed25519, -1)
	if err != nil {
		return nil, err
	}
	raw, err := crypto.MarshalPrivateKey(priv)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return nil, err
	}
	return priv, nil
}
