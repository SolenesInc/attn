package main_test

import (
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/testworld"
)

func TestTheGuideCommandReadsTheEmbeddedGuide(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t)
	requireStdout(t, s.Attn("guide"), "getting-started", "Before you install", "Why did my desktop disappear?", "delegation", "What happens to a delegate's seed when the delegate closes?")
	requireStdout(t, s.Attn("guide", "getting-started"), "# Getting started", "The home screen, with the sidebar")
	queue := s.Attn("guide", "queue")
	requireStdout(t, queue, "```text", "# Queue flow")
	if strings.Contains(queue.Stdout, "![") {
		t.Fatalf("guide retained image markdown: %s", queue.Stdout)
	}
	missing := s.Attn("guide", "nope")
	if missing.Code != 1 {
		t.Fatalf("unknown topic exited %d", missing.Code)
	}
	requireLines(t, "unknown topic", missing.Stderr, `no topic "nope"`, "getting-started", "attn <command> --help")
	for _, command := range []string{"help", "-h", "--help"} {
		help := s.Attn(command)
		requireStdout(t, help, "Are you an AI agent?", "attn skill", "attn guide")
		if !strings.HasSuffix(help.Stdout, "  attn guide    the user guide, to answer questions about Attn\n") {
			t.Fatalf("agent footer is not last: %s", help.Stdout)
		}
	}
}

func TestCrewRenameAndMoveHomeParseErrorsPrintFullHelp(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t)
	for _, command := range []string{"rename", "move-home"} {
		for _, args := range [][]string{nil, {"--help"}, {"--unknown"}, {"--profile"}} {
			result := s.Attn(append([]string{"crew", command}, args...)...)
			if result.Code != 2 || result.Stdout != "" {
				t.Fatalf("crew %s %q: %+v", command, args, result)
			}
			requireLines(t, "crew parse error", result.Stderr, "usage: attn crew <command>", "  create", "  move-home", "  set")
		}
	}
}
