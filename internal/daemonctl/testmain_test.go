package daemonctl

import (
	"os"
	"testing"

	"github.com/victorarias/attn/internal/config"
)

// Scopes every test in this package to a temp data dir so none can resolve
// config.DataDir() to the real ~/.attn.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "attn-test-data-*")
	if err != nil {
		panic("daemonctl: TestMain: MkdirTemp: " + err.Error())
	}
	config.ScopeTestEnvironment(dir)
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}
