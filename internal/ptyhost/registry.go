package ptyhost

import (
	"crypto/sha256"
	"encoding/base32"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const BinaryName = "attn-pty-host"

// Profiles share ~/.local/bin on remote hosts, so each daemon resolves a
// sidecar that another profile cannot replace underneath it.
func BinaryNameForProfile(profile string) string {
	profile = strings.TrimSpace(profile)
	if profile == "" {
		return BinaryName
	}
	return BinaryName + "-" + profile
}

type HostRegistry struct {
	Version          int    `json:"version"`
	DaemonInstanceID string `json:"daemon_instance_id"`
	HostPID          int    `json:"host_pid"`
	SocketPath       string `json:"socket_path"`
	ControlToken     string `json:"control_token"`
	Executable       string `json:"executable"`
	StartedAt        string `json:"started_at"`
	SnapshotFormat   string `json:"snapshot_format"`
	Generation       string `json:"generation"`
}

func Root(dataRoot, daemonInstanceID string) string {
	return filepath.Join(dataRoot, "pty-hosts", daemonInstanceID)
}

func RegistryDir(dataRoot, daemonInstanceID string) string {
	return filepath.Join(Root(dataRoot, daemonInstanceID), "registry")
}

func SessionRegistryPath(dataRoot, daemonInstanceID, sessionID string) string {
	return filepath.Join(RegistryDir(dataRoot, daemonInstanceID), sessionID+".json")
}

func HostRegistryPath(dataRoot, daemonInstanceID, generation string) string {
	return filepath.Join(Root(dataRoot, daemonInstanceID), "hosts", generation+".json")
}

func LogPath(dataRoot, daemonInstanceID string) string {
	return filepath.Join(Root(dataRoot, daemonInstanceID), "log", "host.log")
}

func SocketPath(dataRoot, daemonInstanceID, generation string) (string, error) {
	root := filepath.Join(Root(dataRoot, daemonInstanceID), "sock")
	generation = strings.TrimSpace(generation)
	if generation == "" || strings.ContainsAny(generation, `/\\`) {
		return "", errors.New("invalid PTY host generation")
	}
	limit := 104
	if runtime.GOOS == "linux" {
		limit = 108
	}
	available := limit - 1 - len(root) - 1 - len(".sock")
	if available < 5 {
		return "", fmt.Errorf("unix socket directory path too long: %s", root)
	}
	if available > len(generation) {
		available = len(generation)
	}
	return filepath.Join(root, generation[:available]+".sock"), nil
}

func Generation(binaryPath, snapshotFormat string) (string, error) {
	binary, err := os.ReadFile(binaryPath)
	if err != nil {
		return "", fmt.Errorf("read PTY host binary: %w", err)
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte(snapshotFormat))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write(binary)
	return strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(hash.Sum(nil))[:16]), nil
}

func ValidateSocketPath(dataRoot, daemonInstanceID, socketPath string) error {
	root := filepath.Clean(filepath.Join(Root(dataRoot, daemonInstanceID), "sock"))
	clean := filepath.Clean(socketPath)
	rel, err := filepath.Rel(root, clean)
	if err != nil || rel == "." || rel == ".." || strings.Contains(rel, string(filepath.Separator)) || strings.HasPrefix(rel, "..") {
		return errors.New("PTY host socket is outside its socket directory")
	}
	if filepath.Ext(rel) != ".sock" {
		return errors.New("PTY host socket has an invalid filename")
	}
	return nil
}

func ReadHostRegistry(path string) (HostRegistry, error) {
	var entry HostRegistry
	data, err := os.ReadFile(path)
	if err != nil {
		return entry, err
	}
	if err := json.Unmarshal(data, &entry); err != nil {
		return entry, fmt.Errorf("unmarshal PTY host registry: %w", err)
	}
	return entry, nil
}

func WriteHostRegistryAtomic(path string, entry HostRegistry) error {
	if entry.StartedAt == "" {
		entry.StartedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create PTY host registry directory: %w", err)
	}
	payload, err := json.MarshalIndent(entry, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal PTY host registry: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(payload, '\n'), 0o600); err != nil {
		return fmt.Errorf("write PTY host registry: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("publish PTY host registry: %w", err)
	}
	return nil
}
