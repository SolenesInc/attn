package main_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

type promptTrace struct {
	Selected bool          `json:"selected"`
	Source   string        `json:"source"`
	Children []promptTrace `json:"children"`
}

func (p promptTrace) selectedBySource() map[string]bool {
	found := map[string]bool{}
	if p.Source != "" {
		found[p.Source] = p.Selected
	}
	for _, child := range p.Children {
		for source, selected := range child.selectedBySource() {
			found[source] = selected
		}
	}
	return found
}

func TestPromptsExplainAndShowTheCatalogAndRefuseAMistypedScenario(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t)

	explained := s.Attn("prompts", "explain", "session", "launch", "--set", "vent_enabled=true", "--json")
	var explanation struct {
		Delivery string      `json:"delivery"`
		Text     string      `json:"text"`
		Trace    promptTrace `json:"trace"`
	}
	explained.JSON(t, &explanation)
	if explanation.Delivery != "launch_instructions" || explanation.Text == "" {
		t.Errorf("explain delivers %q with text:\n%s\nwant launch instructions", explanation.Delivery, explanation.Text)
	}
	selected := explanation.Trace.selectedBySource()
	for source, want := range map[string]bool{"content/agent.md": true, "content/session/vent-guidance.md": true, "content/session/vent-attn-only.md": true, "content/session/vent-tools.md": false, "content/garden.md": false} {
		if got, traced := selected[source]; !traced || got != want {
			t.Errorf("explain traced %s selected=%v (traced %v), want selected=%v: %v", source, got, traced, want, selected)
		}
	}

	shown := s.Attn("prompts", "show", "session")
	for _, want := range []string{"session.queue", "session.agent", "otherwise", "crew_priming (text)", "internal/prompts/content/garden.md"} {
		if shown.Code != 0 || !strings.Contains(shown.Stdout, want) {
			t.Errorf("prompts show session exited %d without %q:\n%s", shown.Code, want, shown.Stdout)
		}
	}

	primed := s.Attn("prompts", "render", "session", "launch", "--set", "crew_priming=  Crew {{literal}}.  ")
	if primed.Code != 0 || !strings.Contains(primed.Stdout, "\nCrew {{literal}}.") || strings.Contains(primed.Stdout, "Crew {{literal}}.  ") {
		t.Errorf("render with crew priming exited %d, want the priming trimmed with its braces verbatim:\n%s", primed.Code, primed.Stdout)
	}

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
			got := s.Attn(append([]string{"prompts"}, args...)...)
			if got.Code != 2 || got.Stdout != "" || got.Stderr == "" {
				t.Errorf("exited %d, stdout %q, stderr %q; want 2 with only an error", got.Code, got.Stdout, got.Stderr)
			}
		})
	}
}

func appendedSystemPrompt(t *testing.T, run *fakeagent.Run) string {
	t.Helper()
	i := slices.Index(run.Argv, "--append-system-prompt")
	if i < 0 || i+1 >= len(run.Argv) {
		t.Fatalf("claude for session %s launched without instructions: %q", run.SessionID, run.Argv)
	}
	return run.Argv[i+1]
}

func TestPromptsRenderShowsExactlyWhatAChiefACrewMemberAndAnOrdinarySessionReceiveAtLaunch(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	keelHome := filepath.Join(s.Dir, "crew", "keel")
	if err := os.MkdirAll(keelHome, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(keelHome, "CHARTER.md"), []byte("# Keel\n\nKeep {{literal}} braces as written.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s.Start()
	app := s.App()

	ordinary := s.Spawn(app, fakeagent.Claude, s.Path("shop"))
	primed := func(member string) (protocol.SessionID, string) {
		t.Helper()
		woken, err := s.Client().CrewWake(member, string(fakeagent.Claude), "")
		if err != nil {
			t.Fatalf("crew start %s: %v", member, err)
		}
		priming, err := s.Client().CrewPrime(woken.SessionID)
		if err != nil || priming.Guidance == nil {
			t.Fatalf("crew prime for %s = %+v, %v", member, priming, err)
		}
		return woken.SessionID, *priming.Guidance
	}
	if _, err := s.Client().CrewSet("chief", nil, protocol.Ptr(string(fakeagent.Claude)), nil, nil, nil); err != nil {
		t.Fatalf("crew set chief: %v", err)
	}
	chief, chiefPriming := primed("chief")
	keel, keelPriming := primed("keel")

	for _, tc := range []struct {
		name    string
		session string
		set     []string
	}{
		{name: "chief", session: string(chief), set: []string{"--set", "crew_priming=" + chiefPriming, "--set", "garden_available=true", "--set", "asks_enabled=true"}},
		{name: "crew member", session: string(keel), set: []string{"--set", "crew_priming=  \n" + keelPriming + "\n  ", "--set", "garden_available=true", "--set", "asks_enabled=true"}},
		{name: "ordinary", session: ordinary, set: []string{"--set", "garden_available=true", "--set", "asks_enabled=true"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rendered := s.Attn(append([]string{"prompts", "render", "session", "launch"}, tc.set...)...)
			if rendered.Code != 0 {
				t.Fatalf("prompts render exited %d: %s", rendered.Code, rendered.Stderr)
			}
			if received := appendedSystemPrompt(t, s.Launched(tc.session)); received != rendered.Stdout {
				t.Errorf("the %s session received:\n%s\n\nprompts render shows:\n%s", tc.name, received, rendered.Stdout)
			}
		})
	}
}
