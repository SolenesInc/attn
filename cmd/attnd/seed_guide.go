package main

import (
	"fmt"
	"io"
	"os"

	"github.com/victorarias/attn/internal/prompts"
)

func runSeedGuide(args []string) {
	f := newSeedFlags("guide")
	if len(f.parse("guide", args)) > 0 {
		seedUsageError("guide", fmt.Errorf("takes no arguments"))
	}
	writeSeedGuide(os.Stdout)
}

func writeSeedGuide(w io.Writer) {
	fmt.Fprint(w, seedGuideText())
}

func seedGuideText() string { return prompts.RenderText("authoring-agent", "seed-guide", nil) }
