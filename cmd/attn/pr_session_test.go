package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestPRHelpNamesTheSessionCommands(t *testing.T) {
	var stdout bytes.Buffer
	if code := executePRCommand([]string{"--help"}, &stdout, &stdout); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	for _, want := range []string{"record <url>", "ls [--session", "forget <url>", "wait-ready"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("help does not mention %q:\n%s", want, stdout.String())
		}
	}
}

func TestSessionPRArgsTakeTheUrlBeforeOrAfterTheFlags(t *testing.T) {
	t.Setenv("ATTN_SESSION_ID", "")
	url := "https://github.com/victorarias/attn/pull/71"

	for _, args := range [][]string{
		{url, "--session", "s1"},
		{"--session", "s1", url},
		{"--session=s1", url},
	} {
		parsed, err := parseSessionPRArgs("record", args)
		if err != nil {
			t.Fatalf("parse %v: %v", args, err)
		}
		if parsed.sessionID != "s1" || parsed.url != url {
			t.Errorf("parse %v = %+v, want session s1 and the url", args, parsed)
		}
	}
}

func TestSessionPRArgsFallBackToTheSessionEnvironment(t *testing.T) {
	t.Setenv("ATTN_SESSION_ID", "from-env")
	parsed, err := parseSessionPRArgs("ls", nil)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if parsed.sessionID != "from-env" {
		t.Errorf("session = %q, want the environment's", parsed.sessionID)
	}
}

func TestSessionPRCommandsSayWhatTheyNeed(t *testing.T) {
	t.Setenv("ATTN_SESSION_ID", "")

	tests := []struct {
		name string
		args []string
		want string
	}{
		{"record outside a session", []string{"record", "https://github.com/a/b/pull/1"}, "no session"},
		{"ls outside a session", []string{"ls"}, "no session"},
		{"record without a url", []string{"record", "--session", "s1"}, "exactly one pull request url"},
		{"forget with two urls", []string{"forget", "--session", "s1", "a", "b"}, "exactly one pull request url"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := executePRCommand(tc.args, &stdout, &stderr)
			if code != prWaitExitUsage {
				t.Fatalf("exit code = %d, want %d", code, prWaitExitUsage)
			}
			if !strings.Contains(stderr.String(), tc.want) {
				t.Errorf("stderr = %q, want it to name %q", stderr.String(), tc.want)
			}
		})
	}
}
