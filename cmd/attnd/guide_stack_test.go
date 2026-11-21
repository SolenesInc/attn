package main_test

import (
	"regexp"
	"slices"
	"strings"
	"testing"

	attn "github.com/victorarias/attn/cmd/attnd"
	"github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/catalog"
	"github.com/victorarias/attn/internal/prompts"
	"github.com/victorarias/attn/internal/testworld"
)

func TestTheGuideListsItsPagesAndPrintsEachForTheTerminal(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t)

	index := s.Attn("guide")
	requireStdout(t, index, "attn guide <topic>", "Tutorials", "Reference",
		"getting-started", "building-with-roles",
		"queue", "sessions", "desktops", "delegation", "crew", "automations", "annotations",
		"telemetry", "using-outposts", "using-shared-codex", "using-sigma",
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
	requireLines(t, "unknown topic", missing.Stderr, `no topic "nope"`, "getting-started", "using-sigma", "attn <command> --help")

	for _, command := range []string{"help", "-h", "--help"} {
		help := s.Attn(command)
		requireStdout(t, help, "guide [<topic>]", "## What attn can do", "attn skill --reference <name>")
	}
}

func TestHelpListsEveryCommandAndReferenceAndEachCommandExplainsItself(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t)
	help := s.Attn("--help").Stdout

	c, err := catalog.Builtin()
	if err != nil {
		t.Fatal(err)
	}
	var listed, inCatalog []string
	for _, top := range c.TopLevel() {
		inCatalog = append(inCatalog, top.Name)
		if !top.Hidden {
			listed = append(listed, top.Name)
		}
	}
	commandList := help[strings.Index(help, "\nCommands:\n"):]
	var helped []string
	for _, match := range regexp.MustCompile(`(?m)^  ([a-z][a-z-]*)`).FindAllStringSubmatch(commandList, -1) {
		helped = append(helped, match[1])
	}
	if !slices.Equal(slices.Sorted(slices.Values(helped)), listed) {
		t.Errorf("attn --help lists %v, the catalog lists %v", helped, listed)
	}
	for _, command := range attn.TopLevelCommandNames() {
		if !strings.HasPrefix(command, "-") && !slices.Contains(inCatalog, command) {
			t.Errorf("attnd dispatches %q, which the catalog does not hold, so attn refuses it", command)
		}
	}
	unknown := s.Attn("not-a-command")
	if unknown.Code != 1 || !strings.Contains(unknown.Stderr, `unknown command "not-a-command"`) {
		t.Errorf("a name the catalog does not hold: %+v", unknown)
	}

	for _, command := range listed {
		if command == "instance" || command == "version" {
			continue
		}
		out := s.Attn(command, "--help")
		text := strings.ToLower(out.Stdout + out.Stderr)
		if out.Code != 0 || !strings.Contains(text, "usage") || !regexp.MustCompile(`attn `+regexp.QuoteMeta(command)+`\b`).MatchString(text) {
			t.Errorf("attn --help names %q, but attn %s --help exited %d with:\n%s%s", command, command, out.Code, out.Stdout, out.Stderr)
		}
	}

	launchedWithEveryGatedRow := prompts.Launch{CodeReview: true}.Instructions()
	for _, reference := range agent.SkillReferenceNames() {
		if reference == "onboarding" || strings.Contains(help, "`"+reference+"`") || strings.Contains(launchedWithEveryGatedRow, "| `"+reference+"` |") {
			continue
		}
		t.Errorf("neither attn --help nor the attn map of an agent launched with every setting-gated row names the reference %q, so no agent can find it", reference)
	}
}
