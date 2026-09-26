package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestAutoModeDenialsNameWhatTheLedgerLost(t *testing.T) {
	var out bytes.Buffer
	writeAutoModeDenials(&out, nil, "3 older denials were dropped when the local ledger rotated")
	rendered := out.String()
	if !strings.Contains(rendered, "no denials recorded") {
		t.Errorf("the feed itself went missing: %q", rendered)
	}
	if !strings.Contains(rendered, "note: 3 older denials were dropped") {
		t.Errorf("the note is missing: %q", rendered)
	}
}
