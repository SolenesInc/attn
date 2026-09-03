package ptyhost

import (
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

func TestGenerationChangesWithBinaryContent(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "attn-pty-host")
	if err := os.WriteFile(binary, []byte("one"), 0o700); err != nil {
		t.Fatal(err)
	}
	one, err := Generation(binary, "format")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binary, []byte("two"), 0o700); err != nil {
		t.Fatal(err)
	}
	two, err := Generation(binary, "format")
	if err != nil {
		t.Fatal(err)
	}
	if one == two {
		t.Fatalf("generation stayed %q after binary changed", one)
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
