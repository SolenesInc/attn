package ptyhost

import (
	"crypto/sha256"
	"encoding/base32"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"strings"
)

var ErrArtifactChanged = errors.New("PTY host binary changed after the daemon started")

type Artifact struct {
	ID   string
	Path string
}

type ArtifactEnvironment struct {
	OS             string `json:"os"`
	Arch           string `json:"arch"`
	CoreProtocol   int    `json:"core_protocol"`
	SnapshotFormat string `json:"snapshot_format"`
	ProbeContract  int    `json:"probe_contract"`
}

type ArtifactReceipt struct {
	Environment ArtifactEnvironment `json:"environment"`
	Passed      bool                `json:"passed"`
	Reason      string              `json:"reason,omitempty"`
}

type lastKnownGood struct {
	Artifact string `json:"artifact"`
}

func ArtifactsDir(dataRoot, daemonInstanceID string) string {
	return filepath.Join(Root(dataRoot, daemonInstanceID), "artifacts")
}

func artifactDir(dir, id string) string {
	return filepath.Join(dir, id)
}

func ArtifactPath(dir, id string) string {
	return filepath.Join(artifactDir(dir, id), BinaryName)
}

func HashArtifact(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("read PTY host binary: %w", err)
	}
	defer file.Close()
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return "", fmt.Errorf("read PTY host binary: %w", err)
	}
	return artifactID(digest), nil
}

func artifactID(digest hash.Hash) string {
	return strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(digest.Sum(nil))[:16])
}

func ImportArtifact(dir, source, id string) (Artifact, error) {
	artifact := Artifact{ID: id, Path: ArtifactPath(dir, id)}
	if info, err := os.Stat(artifact.Path); err == nil && info.Mode().IsRegular() {
		return artifact, nil
	}
	if err := os.MkdirAll(artifactDir(dir, id), 0o700); err != nil {
		return Artifact{}, fmt.Errorf("create PTY host artifact directory: %w", err)
	}
	src, err := os.Open(source)
	if err != nil {
		return Artifact{}, fmt.Errorf("read PTY host binary: %w", err)
	}
	defer src.Close()
	tmp, err := os.CreateTemp(artifactDir(dir, id), BinaryName+".*.tmp")
	if err != nil {
		return Artifact{}, fmt.Errorf("stage PTY host artifact: %w", err)
	}
	published := false
	defer func() {
		if !published {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
		}
	}()
	digest := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tmp, digest), src); err != nil {
		return Artifact{}, fmt.Errorf("copy PTY host artifact: %w", err)
	}
	if artifactID(digest) != id {
		return Artifact{}, ErrArtifactChanged
	}
	if err := tmp.Chmod(0o500); err != nil {
		return Artifact{}, fmt.Errorf("protect PTY host artifact: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		return Artifact{}, fmt.Errorf("flush PTY host artifact: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return Artifact{}, fmt.Errorf("flush PTY host artifact: %w", err)
	}
	if err := os.Rename(tmp.Name(), artifact.Path); err != nil {
		return Artifact{}, fmt.Errorf("publish PTY host artifact: %w", err)
	}
	published = true
	return artifact, nil
}

func StoredArtifact(dir, id string) (Artifact, bool) {
	artifact := Artifact{ID: id, Path: ArtifactPath(dir, id)}
	info, err := os.Stat(artifact.Path)
	return artifact, err == nil && info.Mode().IsRegular()
}

func StoredArtifactIDs(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			ids = append(ids, entry.Name())
		}
	}
	return ids
}

func RemoveArtifact(dir, id string) error {
	return os.RemoveAll(artifactDir(dir, id))
}

func ReadArtifactReceipt(dir, id string) (ArtifactReceipt, error) {
	var receipt ArtifactReceipt
	err := readJSON(filepath.Join(artifactDir(dir, id), "receipt.json"), &receipt)
	return receipt, err
}

func WriteArtifactReceipt(dir, id string, receipt ArtifactReceipt) error {
	return writeJSONAtomic(filepath.Join(artifactDir(dir, id), "receipt.json"), receipt)
}

func LastKnownGood(dir string) string {
	var state lastKnownGood
	if err := readJSON(filepath.Join(dir, "last-known-good.json"), &state); err != nil {
		return ""
	}
	return state.Artifact
}

func SetLastKnownGood(dir, id string) error {
	path := filepath.Join(dir, "last-known-good.json")
	if id == "" {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	return writeJSONAtomic(path, lastKnownGood{Artifact: id})
}

func readJSON(path string, value any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, value)
}

func writeJSONAtomic(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create %s directory: %w", filepath.Base(path), err)
	}
	payload, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal %s: %w", filepath.Base(path), err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(payload, '\n'), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("publish %s: %w", filepath.Base(path), err)
	}
	return nil
}
