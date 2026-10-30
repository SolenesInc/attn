package main

import (
	"os"

	"github.com/victorarias/attn/internal/fakessh"
)

func main() {
	if os.Getenv("ATTN_TESTWORLD_SSH") == "" {
		_ = os.Setenv("ATTN_TESTWORLD_SSH", "client")
	}
	fakessh.Main()
}
