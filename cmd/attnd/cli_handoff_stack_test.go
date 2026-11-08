package main_test

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/testworld"
)

func TestTheCLINamesThePathItSearchedWhenAttndIsMissing(t *testing.T) {
	t.Parallel()
	contents, err := os.ReadFile(testworld.AttnBinary(t))
	if err != nil {
		t.Fatal(err)
	}
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	alone := filepath.Join(dir, "attn")
	if err := os.WriteFile(alone, contents, 0o755); err != nil {
		t.Fatal(err)
	}

	var stderr bytes.Buffer
	command := exec.Command(alone, "list")
	command.Stderr = &stderr
	var exit *exec.ExitError
	if err := command.Run(); !errors.As(err, &exit) || exit.ExitCode() != 127 {
		t.Fatalf("attn without attnd: err=%v stderr=%s", err, stderr.String())
	}
	if want := filepath.Join(dir, "attnd"); !strings.Contains(stderr.String(), want) {
		t.Fatalf("stderr does not name %s:\n%s", want, stderr.String())
	}
}
