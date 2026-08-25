package agent

import (
	"os"
	"testing"

	"github.com/victorarias/attn/internal/toolhome"
)

// Scopes every test in this package to a throwaway ATTN_TOOL_HOME so none can
// resolve toolhome.Dir() to the real home directory.
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
