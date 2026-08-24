package main

import (
	"os"
	"testing"
)

func TestParseHookStateArgsCarriesThePromptSubmitMarker(t *testing.T) {
	previous := os.Args
	t.Cleanup(func() { os.Args = previous })

	t.Run("explicit session", func(t *testing.T) {
		os.Args = []string{"attn", "_hook-state", "session-1", "working", "user_prompt_submit"}
		sessionID, state, event := parseHookStateArgs()
		if sessionID != "session-1" || state != "working" || event != "user_prompt_submit" {
			t.Fatalf("parsed (%q, %q, %q)", sessionID, state, event)
		}
	})

	t.Run("session from environment", func(t *testing.T) {
		t.Setenv("ATTN_SESSION_ID", "session-env")
		os.Args = []string{"attn", "_hook-state", "working", "user_prompt_submit"}
		sessionID, state, event := parseHookStateArgs()
		if sessionID != "session-env" || state != "working" || event != "user_prompt_submit" {
			t.Fatalf("parsed (%q, %q, %q)", sessionID, state, event)
		}
	})
}
