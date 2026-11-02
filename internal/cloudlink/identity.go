package cloudlink

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/flynn/noise"
	"golang.org/x/crypto/curve25519"
)

const (
	signKeyFile  = "cloud_sign_key"
	noiseKeyFile = "cloud_noise_key"
	homeIDFile   = "cloud_home_id"
)

// Identity is the home's standing with Attn Cloud. Its keys are created once and never replaced: a new key would orphan every pairing.
type Identity struct {
	dir      string
	SignKey  ed25519.PrivateKey
	NoiseKey noise.DHKey
}

func LoadIdentity(dir string) (Identity, error) {
	sign, err := loadOrCreateKey(filepath.Join(dir, signKeyFile), func() ([]byte, error) {
		_, private, err := ed25519.GenerateKey(rand.Reader)
		return private.Seed(), err
	})
	if err != nil {
		return Identity{}, err
	}
	noiseSecret, err := loadOrCreateKey(filepath.Join(dir, noiseKeyFile), func() ([]byte, error) {
		pair, err := noise.DH25519.GenerateKeypair(rand.Reader)
		return pair.Private, err
	})
	if err != nil {
		return Identity{}, err
	}
	noisePair, err := noiseKeyFromPrivate(noiseSecret)
	if err != nil {
		return Identity{}, err
	}
	return Identity{dir: dir, SignKey: ed25519.NewKeyFromSeed(sign), NoiseKey: noisePair}, nil
}

func loadOrCreateKey(path string, generate func() ([]byte, error)) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err == nil {
		if len(data) != 32 {
			return nil, fmt.Errorf("%s holds %d bytes, want a 32-byte key; it was not written by attn", path, len(data))
		}
		return data, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	key, err := generate()
	if err != nil {
		return nil, err
	}
	return key, writeFileAtomic(path, key)
}

func noiseKeyFromPrivate(private []byte) (noise.DHKey, error) {
	public, err := curve25519.X25519(private, curve25519.Basepoint)
	return noise.DHKey{Private: private, Public: public}, err
}

func writeFileAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (id Identity) SignPub() ed25519.PublicKey { return id.SignKey.Public().(ed25519.PublicKey) }

func (id Identity) HomeID() string {
	data, err := os.ReadFile(filepath.Join(id.dir, homeIDFile))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func (id Identity) saveHomeID(homeID string) error {
	return writeFileAtomic(filepath.Join(id.dir, homeIDFile), []byte(homeID+"\n"))
}

func KeyID(pub []byte) string {
	sum := sha256.Sum256(pub)
	return strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(sum[:16]))
}

func b64(data []byte) string { return base64.RawURLEncoding.EncodeToString(data) }
