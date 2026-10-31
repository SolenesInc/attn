package main

import (
	"fmt"
	"os"

	"github.com/victorarias/attn/docs/guide"
)

const guideUsage = "usage: attn guide [<topic>]"

func runGuide() {
	args := os.Args[2:]
	if hasHelpFlag(args) {
		fmt.Println(guideUsage + "\n\nRead the user guide. attn --help lists commands.")
		return
	}
	switch len(args) {
	case 0:
		_, _ = os.Stdout.Write(guide.Index())
	case 1:
		page, err := guide.Topic(args[0])
		if err != nil {
			recordUsageError("guide", usageErrorUnknownCommand)
			fmt.Fprintln(os.Stderr, "guide: "+err.Error())
			os.Exit(1)
		}
		_, _ = os.Stdout.Write(page)
	default:
		fmt.Fprintln(os.Stderr, guideUsage)
		os.Exit(2)
	}
}
