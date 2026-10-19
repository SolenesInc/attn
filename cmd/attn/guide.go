package main

import (
	"fmt"
	"os"

	"github.com/victorarias/attn/docs/guide"
)

func runGuide() {
	args := os.Args[2:]
	if len(args) == 0 {
		_, _ = os.Stdout.Write(guide.Index())
		return
	}
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: attn help [<topic>]")
		os.Exit(2)
	}
	if hasHelpFlag(args) {
		fmt.Println("usage: attn help [<topic>]\n\nRead the user guide. attn --help lists commands.")
		return
	}
	page, err := guide.Topic(args[0])
	if err != nil {
		fmt.Fprintln(os.Stderr, "help: "+err.Error())
		os.Exit(1)
	}
	_, _ = os.Stdout.Write(page)
}
