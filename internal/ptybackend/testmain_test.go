package ptybackend

import (
	"os"
	"testing"

	"github.com/victorarias/attn/internal/config"
)

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "attn-ptybackend-test-*")
	if err != nil {
		panic("ptybackend: TestMain: MkdirTemp: " + err.Error())
	}
	config.ScopeTestEnvironment(dir)
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}
