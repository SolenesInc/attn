package main

import (
	"strings"
	"testing"
)

// `attn seed --help` is how an agent finds the guide at all.
func TestSeedHelpNamesTheGuide(t *testing.T) {
	var b strings.Builder
	writeSeedHelp(&b)
	if !strings.Contains(b.String(), "guide") {
		t.Fatalf("seed help does not name the guide:\n%s", b.String())
	}
}

func TestSeedGuideDefinesDoneFromTheBody(t *testing.T) {
	for _, want := range []string{
		"outcome and required verification written in the seed body",
		"Review, acceptance, or merge keeps a seed open only when the body",
		"requires it. Otherwise record the evidence and harvest",
		"investigation that asks for a sourced answer",
		"A completed child is harvested by itself",
		"behavior exists and its required verification is green",
	} {
		if !strings.Contains(seedGuideText, want) {
			t.Fatalf("seed guide dropped %q:\n%s", want, seedGuideText)
		}
	}
}
