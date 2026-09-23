package ptyhost

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const BinaryName = "attn-pty-host"

func BinaryNameForInstance(instance string) string {
	instance = strings.TrimSpace(instance)
	if instance == "" {
		return BinaryName
	}
	return BinaryName + "-" + instance
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
	ArtifactID       string `json:"generation"`
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

func HostRegistryDir(dataRoot, daemonInstanceID string) string {
	return filepath.Join(Root(dataRoot, daemonInstanceID), "hosts")
}

func HostRegistryPath(dataRoot, daemonInstanceID, incarnation string) string {
	return filepath.Join(HostRegistryDir(dataRoot, daemonInstanceID), incarnation+".json")
}

func LogPath(dataRoot, daemonInstanceID string) string {
	return filepath.Join(Root(dataRoot, daemonInstanceID), "log", "host.log")
}

func SocketPath(dataRoot, daemonInstanceID, incarnation string) (string, error) {
	root := filepath.Join(Root(dataRoot, daemonInstanceID), "sock")
	incarnation = strings.TrimSpace(incarnation)
	if incarnation == "" || strings.ContainsAny(incarnation, `/\\`) {
		return "", errors.New("invalid PTY host incarnation")
	}
	limit := 104
	if runtime.GOOS == "linux" {
		limit = 108
	}
	available := limit - 1 - len(root) - 1 - len(".sock")
	if available < 5 {
		return "", fmt.Errorf("unix socket directory path too long: %s", root)
	}
	if available > len(incarnation) {
		available = len(incarnation)
	}
	return filepath.Join(root, incarnation[:available]+".sock"), nil
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
