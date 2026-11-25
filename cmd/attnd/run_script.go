package main

import (
	"fmt"
	"os"

	"github.com/victorarias/attn/internal/script"
)

func runScript() {
	if err := script.Serve(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, script.Command+":", err)
		exit(1)
	}
}
