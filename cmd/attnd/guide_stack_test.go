package main_test

import (
	"regexp"
	"strings"
	"testing"

	attn "github.com/victorarias/attn/cmd/attnd"
	"github.com/victorarias/attn/internal/agent"
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

	requireStdout(t, s.Attn("guide", "getting-started"), "# Getting started", "The home screen, with the Chief at the top of the sidebar")

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
		requireStdout(t, help, "guide [<topic>]", "## What attn can do", "attn skill --reference <name>")
	}
}

func TestHelpListsEveryCommandAndReferenceAndEachCommandExplainsItself(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t)
	help := s.Attn("--help").Stdout

	named := map[string]bool{}
	for _, match := range regexp.MustCompile("`attn ([a-z][a-z-]*)").FindAllStringSubmatch(help, -1) {
		named[match[1]] = true
	}
	for _, match := range regexp.MustCompile(`(?m)^  ([a-z][a-z-]*)`).FindAllStringSubmatch(help[strings.Index(help, "Other commands:"):], -1) {
		named[match[1]] = true
	}
	commands := attn.ListedCommands()
	if len(commands) < 30 {
		t.Fatalf("attn dispatches only %d listed commands: %v", len(commands), commands)
	}
	for _, command := range commands {
		if !named[command] {
			t.Errorf("attn dispatches %q, but attn --help does not name it:\n%s", command, help)
		}
		if command == "instance" || command == "version" {
			continue
		}
		out := s.Attn(command, "--help")
		text := strings.ToLower(out.Stdout + out.Stderr)
		if out.Code != 0 || !strings.Contains(text, "usage") || !regexp.MustCompile(`attn `+regexp.QuoteMeta(command)+`\b`).MatchString(text) {
			t.Errorf("attn --help names %q, but attn %s --help exited %d with:\n%s%s", command, command, out.Code, out.Stdout, out.Stderr)
		}
	}

	for _, reference := range agent.SkillReferenceNames() {
		if reference != "onboarding" && !strings.Contains(help, "`"+reference+"`") {
			t.Errorf("attn --help does not name the reference %q, so no agent can find it", reference)
		}
	}
}
