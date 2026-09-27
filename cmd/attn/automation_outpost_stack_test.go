package main_test

import (
	"fmt"
	"os"
	"testing"

	"github.com/victorarias/attn/internal/enrollment"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAnOutpostRefusesAnAutomationRunBeforeLaunchingAnything(t *testing.T) {
	t.Parallel()
	const home = "d-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	if enrolled := s.Attn("enrollment", "enroll", "--home", home); enrolled.Code != 0 {
		t.Fatalf("enroll exited %d: %s", enrolled.Code, enrolled.Stderr)
	}
	s.Start()
	check := s.Path("check")
	if err := os.MkdirAll(check, 0o755); err != nil {
		t.Fatal(err)
	}
	spec := s.Path("nightly.yaml")
	if err := os.WriteFile(spec, []byte(fmt.Sprintf(`api_version: attn.dev/automations/v1alpha1
id: nightly
name: Nightly
trigger: {type: manual}
prompt: Check locally.
launch: {driver: claude}
location: {type: directory, path: %q}
`, check)), 0o644); err != nil {
		t.Fatal(err)
	}
	if applied := s.Attn("automation", "apply", "--file", spec); applied.Code != 0 {
		t.Fatalf("automation apply exited %d: %s", applied.Code, applied.Stderr)
	}

	refused := s.Attn("automation", "run", "nightly")
	if refused.Code == 0 {
		t.Fatalf("attn automation run answered on an outpost: %s", refused.Stdout)
	}
	requireLines(t, "attn automation run on an outpost", refused.Stderr, garden.Surface, home, "attn enrollment leave", enrollment.PlanPath)
	if initial := s.App().Initial; len(initial.Sessions) != 0 || len(initial.Workspaces) != 0 || len(initial.Seeds) != 0 {
		t.Errorf("the refused run left sessions %+v, workspaces %+v and seeds %+v, want none", initial.Sessions, initial.Workspaces, initial.Seeds)
	}
}
