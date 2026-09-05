package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/hooks"
	"github.com/victorarias/attn/internal/prompts"
)

func TestPromptsRenderMatchesLaunchAdapter(t *testing.T) {
	var stdout, stderr bytes.Buffer
	args := []string{"render", "session", "launch", "--set", "notebook_root= /tmp/notebook ", "--set", "garden_available=true", "--set", "crew_priming= Crew {{literal}}. "}
	if code := writePrompts(&stdout, &stderr, args); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	want := (hooks.Launch{NotebookRoot: " /tmp/notebook ", Garden: true, Crew: " Crew {{literal}}. "}).Instructions()
	if stdout.String() != want {
		t.Fatal("CLI and runtime launch composition differ")
	}
}

func TestPromptsExplainIncludesSkippedSources(t *testing.T) {
	var stdout, stderr bytes.Buffer
	args := []string{"explain", "session", "launch", "--set", "notebook_root=/tmp/notebook", "--json"}
	if code := writePrompts(&stdout, &stderr, args); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	var result prompts.Result
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	choice := result.Trace.Children[0]
	if !choice.Children[0].Selected || choice.Children[1].Selected || choice.Reason != "notebook_root is present" {
		t.Fatalf("choice: %+v", choice)
	}
	if !strings.Contains(stdout.String(), "content/agent.md") || !strings.Contains(stdout.String(), "content/chief.md") {
		t.Fatal("explanation dropped the unselected branch")
	}
	if result.Delivery != "launch_instructions" || !strings.Contains(result.Text, "/tmp/notebook") {
		t.Fatalf("result: %+v", result)
	}
}

func TestPromptsShowDoesNotNeedScenarioInputs(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := writePrompts(&stdout, &stderr, []string{"show", "session"}); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	for _, want := range []string{"session.chief", "session.agent", "otherwise", "crew_priming (text)", "internal/prompts/content/garden.md"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("show missing %q:\n%s", want, stdout.String())
		}
	}
}

func TestPromptsRejectsMistypedScenario(t *testing.T) {
	for _, args := range [][]string{
		{"render", "session", "launch", "--set", "garden_available=yes"},
		{"render", "session", "launch", "--set", "typo=true"},
		{"render", "session", "launch", "--set", "garden_available=true", "--set", "garden_available=false"},
		{"render", "session", "missing"},
		{"render", "missing", "launch"},
		{"show", "session", "--set", "garden_available=true"},
		{"render", "session", "launch", "--set"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := writePrompts(&stdout, &stderr, args); code != 2 {
				t.Fatalf("exit %d, want 2", code)
			}
			if stdout.Len() != 0 || stderr.Len() == 0 {
				t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
			}
		})
	}
}
