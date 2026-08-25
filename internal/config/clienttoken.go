package config

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const ClientTokenFile = "client-token"

func ClientTokenPath() string {
	return filepath.Join(attnDir(), ClientTokenFile)
}

func ClientToken() string {
	if token := strings.TrimSpace(os.Getenv("ATTN_CLIENT_TOKEN")); token != "" {
		return token
	}
	data, err := os.ReadFile(ClientTokenPath())
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func EnsureClientToken(dir string) (string, error) {
	if token := strings.TrimSpace(os.Getenv("ATTN_CLIENT_TOKEN")); token != "" {
		return token, nil
	}
	path := filepath.Join(dir, ClientTokenFile)
	if data, err := os.ReadFile(path); err == nil {
		if token := strings.TrimSpace(string(data)); token != "" {
			return token, nil
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create data directory %s: %w", dir, err)
	}
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return "", fmt.Errorf("generate client token: %w", err)
	}
	token := hex.EncodeToString(random)
	if err := os.WriteFile(path, []byte(token), 0o600); err != nil {
		return "", fmt.Errorf("write client token %s: %w", path, err)
	}
	// WriteFile honours the mode only when it creates the file; an existing empty one keeps whatever mode it had.
	if err := os.Chmod(path, 0o600); err != nil {
		return "", fmt.Errorf("secure client token %s: %w", path, err)
	}
	return token, nil
}
