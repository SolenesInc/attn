package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/headless"
)

// The executable exists and would run: only the switch can produce ErrRefused here.
func writeRunnableAgentStub(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "agent-stub")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write agent stub: %v", err)
	}
	return path
}

func TestHeadlessSwitchOffRefusesBeforeSpawning(t *testing.T) {
	t.Setenv(headless.EnvVar, "off")
	executable := writeRunnableAgentStub(t)

	for name, provider := range map[string]HeadlessTaskProvider{
		"claude": &Claude{},
		"codex":  &Codex{},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := provider.RunHeadlessTask(context.Background(), HeadlessTaskRequest{
				Executable:   executable,
				Model:        "test-model",
				Prompt:       "hello",
				WorkDir:      t.TempDir(),
				DisableTools: true,
			})
			if !errors.Is(err, headless.ErrRefused) {
				t.Fatalf("RunHeadlessTask() error = %v, want ErrRefused", err)
			}
		})
	}
}

func TestHeadlessSwitchOffRefusesTheDriverClassifier(t *testing.T) {
	t.Setenv(headless.EnvVar, "off")

	for name, driver := range map[string]Driver{
		"claude":  &Claude{},
		"codex":   &Codex{},
		"copilot": &Copilot{},
	} {
		t.Run(name, func(t *testing.T) {
			state, err, ok := ClassifyWithDriver(driver, "did that work?", "", "", time.Second)
			if !ok {
				t.Fatal("ClassifyWithDriver() reported no classifier")
			}
			if !errors.Is(err, headless.ErrRefused) {
				t.Fatalf("ClassifyWithDriver() error = %v, want ErrRefused", err)
			}
			if state != "unknown" {
				t.Fatalf("state = %q, want unknown", state)
			}
		})
	}
}
