package main_test

import (
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/testworld"
)

func TestTheGuideListsItsPagesAndPrintsEachForTheTerminal(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t)

	index := s.Attn("guide")
	requireStdout(t, index, "attn guide <topic>", "Tutorials", "Reference",
		"getting-started", "building-with-roles",
		"queue", "sessions", "desktops", "delegation", "crew", "automations", "annotations",
		"telemetry", "using-outposts", "using-shared-codex", "using-nisse",
		"Before you install", "Why did my desktop disappear?")

	requireStdout(t, s.Attn("guide", "getting-started"), "# Getting started", "The home screen, with the sidebar")

	queue := s.Attn("guide", "queue")
	requireStdout(t, queue, "# Queue flow", "```text", "anyone else waiting?")
	if strings.Contains(queue.Stdout, "![") {
		t.Fatalf("guide kept image markdown:\n%s", queue.Stdout)
	}

	missing := s.Attn("guide", "nope")
	if missing.Code != 1 || missing.Stdout != "" {
		t.Fatalf("unknown topic: %+v", missing)
	}
	requireLines(t, "unknown topic", missing.Stderr, `no topic "nope"`, "getting-started", "using-nisse", "attn <command> --help")

	for _, command := range []string{"help", "-h", "--help"} {
		help := s.Attn(command)
		requireStdout(t, help, "guide [<topic>]", "For AI agents:", "attn skill")
		if !strings.HasSuffix(help.Stdout, "  attn guide    the user guide, to answer questions about Attn\n") {
			t.Fatalf("%s does not end with the agent footer:\n%s", command, help.Stdout)
		}
	}
}
