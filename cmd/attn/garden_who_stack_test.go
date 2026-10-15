package main_test

import (
	"fmt"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
	"os"
	"strings"
	"testing"
)

func TestAPlainShellAssignsSeedClaimsToMembers(t *testing.T) {
	s := testworld.NewStack(t)
	writeCharter(t, s, "keel")
	s.Start()
	var planted protocol.Seed
	s.Attn("seed", "plant", "Assign this seed", "--json").JSON(t, &planted)
	refused := s.Attn("seed", "tend", planted.ID)
	if refused.Code == 0 {
		t.Fatal("a user claimed the seed")
	}
	requireLines(t, "stderr", refused.Stderr, "--for")
	requireStdout(t, s.Attn("seed", "tend", planted.ID, "--for", "Keel"), "Keel")
	requireStdout(t, s.Attn("seed", "show", planted.ID), "tender", "Keel")
}

func TestCancellingAnAutomationAfterStartupFailureReleasesItsOwnClaim(t *testing.T) {
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Codex))
	s.Start()
	cli := s.Client()
	directory := s.Path("check")
	if err := os.MkdirAll(directory, 0755); err != nil {
		t.Fatal(err)
	}
	definition, err := cli.AutomationApply(fmt.Sprintf(`api_version: attn.dev/automations/v1alpha1
name: Startup check
trigger: {type: manual}
prompt: Check the code.
launch: {driver: codex}
location: {type: directory, path: %q}
`, directory))
	if err != nil {
		t.Fatal(err)
	}
	s.ScreenAtNextBoot("Do you trust the contents of this directory?")
	_, err = cli.AutomationRun(definition.Definition.ID, "gate", "")
	if err == nil || !strings.Contains(err.Error(), "trust prompt did not clear") {
		t.Fatalf("startup refusal: %v", err)
	}
	runs, err := cli.AutomationRuns(definition.Definition.ID)
	if err != nil || len(runs.Runs) != 1 {
		t.Fatalf("pending runs: %+v %v", runs, err)
	}
	seedID := protocol.Deref(runs.Runs[0].SeedID)
	before, err := cli.SeedShow("", seedID)
	if err != nil || !before.Seed.Claimed {
		t.Fatalf("live startup claim: %+v %v", before, err)
	}
	if _, err := cli.AutomationSetEnabled(definition.Definition.ID, false); err != nil {
		t.Fatal(err)
	}
	after, err := cli.SeedShow("", seedID)
	if err != nil || after.Seed.Status != "withered" || after.Seed.Claimed {
		t.Fatalf("cancelled claim: %+v %v", after, err)
	}
}
