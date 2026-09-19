package client

import (
	"os"
	"testing"

	"github.com/victorarias/attn/internal/config"
)

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "attn-test-data-*")
	if err != nil {
		panic("client: TestMain: MkdirTemp: " + err.Error())
	}
	config.ScopeTestEnvironment(dir)
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}
