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
	Reason   string        `json:"reason"`
	Selected bool          `json:"selected"`
	Source   string        `json:"source"`
	Children []promptTrace `json:"children"`
}

func (p promptTrace) sources() []string {
	var found []string
	if p.Source != "" {
		found = append(found, p.Source)
	}
	for _, child := range p.Children {
		found = append(found, child.sources()...)
	}
	return found
}

func TestPromptsExplainAndShowTheCatalogAndRefuseAMistypedScenario(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t)

	explained := s.Attn("prompts", "explain", "session", "launch", "--set", "notebook_root=/tmp/notebook", "--json")
	var explanation struct {
		Delivery string      `json:"delivery"`
		Text     string      `json:"text"`
		Trace    promptTrace `json:"trace"`
	}
	explained.JSON(t, &explanation)
	if explanation.Delivery != "launch_instructions" || !strings.Contains(explanation.Text, "/tmp/notebook") {
		t.Errorf("explain delivers %q with text:\n%s\nwant launch instructions naming the notebook", explanation.Delivery, explanation.Text)
	}
	choice := explanation.Trace.Children[0]
	if choice.Reason != "notebook_root is present" || !choice.Children[0].Selected || choice.Children[1].Selected {
		t.Errorf("explain chose %+v, want the chief branch selected because notebook_root is present", choice)
	}
	if sources := explanation.Trace.sources(); !slices.Contains(sources, "content/chief.md") || !slices.Contains(sources, "content/agent.md") {
		t.Errorf("explain traced %q, want the selected chief and the skipped agent sources", sources)
	}

	shown := s.Attn("prompts", "show", "session")
	for _, want := range []string{"session.chief", "session.agent", "otherwise", "crew_priming (text)", "internal/prompts/content/garden.md"} {
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

	chief := s.Spawn(app, fakeagent.Claude, s.Path("shop"), func(m *protocol.SpawnSessionMessage) { m.ChiefOfStaff = protocol.Ptr(true) })
	ordinary := s.Spawn(app, fakeagent.Claude, s.Path("shop"))
	guide, err := s.Client().NotebookGuide(chief)
	if err != nil || !guide.SessionIsChief || guide.Root == "" {
		t.Fatalf("notebook guide for the chief = %+v, %v", guide, err)
	}
	woken, err := s.Client().CrewWake("keel", string(fakeagent.Claude))
	if err != nil {
		t.Fatalf("crew wake keel: %v", err)
	}
	priming, err := s.Client().CrewPrime(woken.SessionID)
	if err != nil || priming.Guidance == nil {
		t.Fatalf("crew prime for keel = %+v, %v", priming, err)
	}

	for _, tc := range []struct {
		name    string
		session string
		set     []string
	}{
		{name: "chief", session: chief, set: []string{"--set", "notebook_root=" + guide.Root, "--set", "garden_available=true"}},
		{name: "crew member", session: woken.SessionID, set: []string{"--set", "crew_priming=  \n" + *priming.Guidance + "\n  ", "--set", "garden_available=true"}},
		{name: "ordinary", session: ordinary, set: []string{"--set", "garden_available=true"}},
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
