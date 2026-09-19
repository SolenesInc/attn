package agent

import (
	"os"
	"testing"

	"github.com/victorarias/attn/internal/toolhome"
)

func TestMain(m *testing.M) {
	toolHomeDir, err := os.MkdirTemp("", "attn-agent-test-toolhome-*")
	if err != nil {
		panic("agent: TestMain: MkdirTemp: " + err.Error())
	}
	_ = os.Setenv(toolhome.EnvVar, toolHomeDir)

	code := m.Run()
	os.RemoveAll(toolHomeDir)
	os.Exit(code)
}
