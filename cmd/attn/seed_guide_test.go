package main

import (
	"strings"
	"testing"
)

// Copy receipt for the text between BEGIN guide and END guide in
// /Users/victor/.attn/crew/trellis/working/2026-08-21-eval-r1/trellis-sections.md.
// normalizedCopyHash converts CRLF to LF and removes only surrounding LF bytes.
func TestSeedGuideMatchesFinalMarkedBlock(t *testing.T) {
	var b strings.Builder
	writeSeedGuide(&b)
	const want = "ceac0918d5b55f1a96e7d4f5d64c707b3088ad70c9ad20369735b238c76b9ce9"
	if got := normalizedCopyHash(b.String()); got != want {
		t.Fatalf("normalized BEGIN guide copy SHA-256 = %s, want %s", got, want)
	}
}

// `attn seed --help` is how an agent finds the guide at all.
func TestSeedHelpNamesTheGuide(t *testing.T) {
	var b strings.Builder
	writeSeedHelp(&b)
	if !strings.Contains(b.String(), "guide") {
		t.Fatalf("seed help does not name the guide:\n%s", b.String())
	}
}
