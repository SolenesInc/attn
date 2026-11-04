// Package claudemod holds the Claude Code mod attn ships, and where it lives on this machine.
package claudemod

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

//go:embed all:mod
var embedded embed.FS

const socketName = "claude-mod.sock"

func Dir(dataDir string) string { return filepath.Join(dataDir, "claude-mod") }

func SocketDir(dataDir string) string {
	sum := sha256.Sum256([]byte(dataDir))
	return filepath.Join("/tmp", "attn-cm-"+hex.EncodeToString(sum[:8]))
}

func SocketPath(dataDir string) string { return filepath.Join(SocketDir(dataDir), socketName) }

func Install(dir string) error {
	root, err := fs.Sub(embedded, "mod")
	if err != nil {
		return err
	}
	return fs.WalkDir(root, ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		want, err := fs.ReadFile(root, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dir, filepath.FromSlash(path))
		if have, err := os.ReadFile(target); err == nil && bytes.Equal(have, want) {
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return writeWhole(target, want)
	})
}

func writeWhole(target string, content []byte) error {
	staging, err := os.CreateTemp(filepath.Dir(target), ".install-*")
	if err != nil {
		return err
	}
	defer os.Remove(staging.Name())
	if _, err := staging.Write(content); err != nil {
		staging.Close()
		return fmt.Errorf("write %s: %w", target, err)
	}
	if err := staging.Close(); err != nil {
		return err
	}
	if err := os.Chmod(staging.Name(), 0o644); err != nil {
		return err
	}
	return os.Rename(staging.Name(), target)
}
