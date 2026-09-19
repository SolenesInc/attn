package config

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	if os.Getenv("ATTN_TEST_DATADIR_BACKSTOP_HELPER") == "1" {
		os.Exit(m.Run())
	}

	dir, err := os.MkdirTemp("", "attn-test-data-*")
	if err != nil {
		panic("config: TestMain: MkdirTemp: " + err.Error())
	}
	ScopeTestEnvironment(dir)
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}
