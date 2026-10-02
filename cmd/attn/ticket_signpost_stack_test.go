package main_test

import (
	"github.com/victorarias/attn/internal/testworld"
	"strings"
	"testing"
)

func TestRetiredTicketCommandsOnlyNameGardenCommands(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t)
	for _, verb := range []string{"list", "show", "inbox", "new", "comment", "attach", "attach-plan", "take", "subscribe", "unsubscribe", "status", "unknown"} {
		result := s.Attn("ticket", verb)
		if result.Code != 2 || result.Stdout != "" || strings.TrimSpace(result.Stderr) == "" {
			t.Fatalf("%s: %+v", verb, result)
		}
		for _, line := range strings.Split(strings.TrimSpace(result.Stderr), "\n") {
			if !strings.HasPrefix(line, "attn seed ") {
				t.Fatalf("%s output=%q", verb, result.Stderr)
			}
		}
	}
}
