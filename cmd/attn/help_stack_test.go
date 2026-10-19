package main_test

import (
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/testworld"
)

func TestTheHelpCommandReadsTheEmbeddedGuide(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t)
	requireStdout(t, s.Attn("help"), "getting-started", "Before you install", "Why did my desktop disappear?", "delegation", "What happens to a delegate's seed when the delegate closes?")
	requireStdout(t, s.Attn("help", "getting-started"), "# Getting started", "The home screen, with the sidebar")
	queue := s.Attn("help", "queue")
	requireStdout(t, queue, "```text", "# Queue flow")
	if strings.Contains(queue.Stdout, "![") {
		t.Fatalf("guide retained image markdown: %s", queue.Stdout)
	}
	missing := s.Attn("help", "nope")
	if missing.Code != 1 {
		t.Fatalf("unknown topic exited %d", missing.Code)
	}
	requireLines(t, "unknown topic", missing.Stderr, `no topic "nope"`, "getting-started", "attn <command> --help")
	help := s.Attn("--help")
	requireStdout(t, help, "Are you an AI agent?", "attn skill", "attn help")
	if !strings.HasSuffix(help.Stdout, "  attn help     the user guide, to answer questions about Attn\n") {
		t.Fatalf("agent footer is not last: %s", help.Stdout)
	}
}
