package ptyhost

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestBinaryNameForProfile(t *testing.T) {
	if got := BinaryNameForProfile(""); got != "attn-pty-host" {
		t.Fatalf("BinaryNameForProfile(empty) = %q", got)
	}
	if got := BinaryNameForProfile("dev"); got != "attn-pty-host-dev" {
		t.Fatalf("BinaryNameForProfile(dev) = %q", got)
	}
}

func TestImportArtifactPinsTheBinaryHashedAtStartup(t *testing.T) {
	source := filepath.Join(t.TempDir(), "attn-pty-host")
	if err := os.WriteFile(source, []byte("one"), 0o700); err != nil {
		t.Fatal(err)
	}
	id, err := HashArtifact(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("two"), 0o700); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if _, err := ImportArtifact(dir, source, id); !errors.Is(err, ErrArtifactChanged) {
		t.Fatalf("import of a replaced binary = %v, want ErrArtifactChanged", err)
	}
	if _, stored := StoredArtifact(dir, id); stored {
		t.Fatal("a replaced binary was stored under the startup hash")
	}
	if err := os.WriteFile(source, []byte("one"), 0o700); err != nil {
		t.Fatal(err)
	}
	artifact, err := ImportArtifact(dir, source, id)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("two"), 0o700); err != nil {
		t.Fatal(err)
	}
	if got, err := HashArtifact(artifact.Path); err != nil || got != id {
		t.Fatalf("stored artifact hash = %q, %v; want %q", got, err, id)
	}
}

func TestValidateSocketPathAllowsOldGenerationsInsideHostRoot(t *testing.T) {
	root, err := os.MkdirTemp("/tmp", "attn-host-path-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	socket, err := SocketPath(root, "daemon", "oldgeneration")
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateSocketPath(root, "daemon", socket); err != nil {
		t.Fatal(err)
	}
	if err := ValidateSocketPath(root, "daemon", filepath.Join(root, "outside.sock")); err == nil {
		t.Fatal("outside socket was accepted")
	}
}
