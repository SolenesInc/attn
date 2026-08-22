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
	const want = "d84e1f508cdf671d2eef70e1641697857bec9e0fd7a0356223379d3c39e8fae8"
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
