package main

import (
	"fmt"
	"github.com/victorarias/attn/internal/prompts"
	"io"
	"os"
)

func runSeedGuide(args []string) {
	if len(args) > 0 {
		fmt.Fprintf(os.Stderr, "seed guide: takes no arguments\n")
		os.Exit(2)
	}
	writeSeedGuide(os.Stdout)
}

func writeSeedGuide(w io.Writer) {
	fmt.Fprint(w, seedGuideText)
}

var seedGuideText = prompts.RenderText("authoring-agent", "seed-guide", nil)
