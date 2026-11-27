package bridge

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/ssh"
)

// PublicHostKey is the public half of the bridge host key at path, as a peer that meets it pins it.
func PublicHostKey(path string) (string, error) {
	signer, err := hostKey(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey()))), nil
}

func hostKey(path string) (ssh.Signer, error) {
	data, err := os.ReadFile(path)
	if err == nil {
		return ssh.ParsePrivateKey(data)
	}
	if !os.IsNotExist(err) {
		return nil, err
	}
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	block, err := ssh.MarshalPrivateKey(key, "attn bridge")
	if err != nil {
		return nil, err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".bridge-host-key-")
	if err != nil {
		return nil, fmt.Errorf("create bridge host key: %w", err)
	}
	defer os.Remove(file.Name())
	_, writeErr := file.Write(pem.EncodeToMemory(block))
	closeErr := file.Close()
	if writeErr != nil {
		return nil, writeErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if err := os.Rename(file.Name(), path); err != nil {
		return nil, err
	}
	return ssh.NewSignerFromKey(key)
}
