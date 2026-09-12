package jobs

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestDiagnosticOutputBoundsTheContractAtSixteenKiB(t *testing.T) {
	diagnostic := "result: useful classification\n" +
		strings.Repeat("noisy provider output ", diagnosticOutputLimit) +
		"\nstderr: final failure"

	got := DiagnosticOutput(WithDiagnostic(errors.New("safe cause"), diagnostic))
	if len(got) > diagnosticOutputLimit {
		t.Fatalf("diagnostic length = %d, want <= %d", len(got), diagnosticOutputLimit)
	}
	if !strings.HasPrefix(got, "result: useful classification") {
		t.Fatalf("diagnostic lost useful prefix: %q", got[:80])
	}
	if !strings.Contains(got, diagnosticTruncatedMarker) {
		t.Fatal("diagnostic does not disclose truncation")
	}
	if !strings.HasSuffix(got, "stderr: final failure") {
		t.Fatalf("diagnostic lost failure tail: %q", got[len(got)-80:])
	}
}

func TestDiagnosticOutputKeepsUTF8ValidAtTheBoundary(t *testing.T) {
	diagnostic := "result: " + strings.Repeat("é", diagnosticOutputLimit) + " :stderr"
	got := DiagnosticOutput(WithDiagnostic(errors.New("safe cause"), diagnostic))
	if !utf8.ValidString(got) {
		t.Fatal("bounded diagnostic split a UTF-8 rune")
	}
}
