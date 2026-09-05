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

func TestSeedGuideDefinesCompletionFromTheBody(t *testing.T) {
	for _, want := range []string{
		"Harvest when the outcome and required verification in the body are complete",
		"behavior exists and required verification passes",
	} {
		if !strings.Contains(seedGuideText, want) {
			t.Fatalf("seed guide dropped %q:\n%s", want, seedGuideText)
		}
	}
}
