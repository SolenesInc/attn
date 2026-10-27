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
		fmt.Fprintln(os.Stderr, "usage: attn guide [<topic>]")
		os.Exit(2)
	}
	if hasHelpFlag(args) {
		fmt.Println("usage: attn guide [<topic>]\n\nRead the user guide. attn --help lists commands.")
		return
	}
	page, err := guide.Topic(args[0])
	if err != nil {
		fmt.Fprintln(os.Stderr, "guide: "+err.Error())
		os.Exit(1)
	}
	_, _ = os.Stdout.Write(page)
}
