package main

import (
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

func TestParseHandoffArgs_TakesTheLetterAndFallsBackToTheSessionEnv(t *testing.T) {
	parsed, err := parseHandoffArgs([]string{"-m", "Dear next trellis,"}, "session-1")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if parsed.note != "Dear next trellis," {
		t.Errorf("note = %q", parsed.note)
	}
	if parsed.session != "session-1" {
		t.Errorf("session = %q, want the ATTN_SESSION_ID fallback", parsed.session)
	}
}

func TestParseHandoffArgs_AnExplicitSessionWins(t *testing.T) {
	parsed, err := parseHandoffArgs([]string{"-m", "x", "--session", "session-2"}, "session-1")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if parsed.session != "session-2" {
		t.Errorf("session = %q, want the explicit one", parsed.session)
	}
}

func TestParseHandoffArgs_ThereIsNoHandoffWithoutALetter(t *testing.T) {
	for _, args := range [][]string{{}, {"-m", "  "}} {
		_, err := parseHandoffArgs(args, "session-1")
		if err == nil {
			t.Fatalf("parse(%v) was accepted", args)
		}
		if !strings.Contains(err.Error(), "-m -") {
			t.Errorf("the refusal %q does not say how to pipe a letter in", err)
		}
	}
}

func TestParseHandoffArgs_ARetryNeedsNoLetter(t *testing.T) {
	parsed, err := parseHandoffArgs([]string{"--retry"}, "session-1")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !parsed.retry || parsed.note != "" {
		t.Fatalf("parsed retry=%t note=%q, want a retry carrying no letter", parsed.retry, parsed.note)
	}
	if parsed.session != "session-1" {
		t.Errorf("session = %q, want the env fallback", parsed.session)
	}
}

func TestParseHandoffArgs_ARetryWithALetterIsRefused(t *testing.T) {
	_, err := parseHandoffArgs([]string{"--retry", "-m", "another one"}, "session-1")
	if err == nil {
		t.Fatal("a retry carrying a letter was accepted")
	}
	if !strings.Contains(err.Error(), "--retry") {
		t.Errorf("the refusal %q does not name the flag that conflicts", err)
	}
}

func TestParseHandoffArgs_APositionalIsRefusedByName(t *testing.T) {
	_, err := parseHandoffArgs([]string{"-m", "x", "trellis"}, "session-1")
	if err == nil {
		t.Fatal("a positional argument was accepted")
	}
	if !strings.Contains(err.Error(), "trellis") {
		t.Errorf("the refusal %q does not name what it did not understand", err)
	}
}

func TestParseHandoffArgs_SleepAndNapDecideWhatFilingDoesToTheDay(t *testing.T) {
	for _, tc := range []struct {
		flag string
		want protocol.CrewDayClose
	}{
		{"--sleep", protocol.CrewDayCloseSleep},
		{"--nap", protocol.CrewDayCloseNap},
	} {
		parsed, err := parseHandoffArgs([]string{"-m", "x", tc.flag}, "session-1")
		if err != nil {
			t.Fatalf("parse(%s): %v", tc.flag, err)
		}
		if parsed.close != tc.want {
			t.Errorf("parse(%s) close = %q, want %q", tc.flag, parsed.close, tc.want)
		}
	}
	parsed, err := parseHandoffArgs([]string{"-m", "x"}, "session-1")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if parsed.close != "" {
		t.Errorf("close = %q with neither flag, want attn's own call", parsed.close)
	}
}

func TestParseHandoffArgs_SleepAndNapTogetherAreRefused(t *testing.T) {
	_, err := parseHandoffArgs([]string{"-m", "x", "--sleep", "--nap"}, "session-1")
	if err == nil {
		t.Fatal("--sleep and --nap were accepted together")
	}
	if !strings.Contains(err.Error(), "--sleep") || !strings.Contains(err.Error(), "--nap") {
		t.Errorf("the refusal %q does not name both flags", err)
	}
}
