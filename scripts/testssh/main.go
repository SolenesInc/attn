package main

import (
	"github.com/victorarias/attn/internal/testworld"
	"os"
)

func main() {
	if os.Getenv("ATTN_TESTWORLD_SSH") == "" {
		_ = os.Setenv("ATTN_TESTWORLD_SSH", "client")
	}
	testworld.RunSSH()
}
