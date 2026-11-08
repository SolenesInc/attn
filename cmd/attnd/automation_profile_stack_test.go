package main_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAutomationCLIChoosesOneProfile(t *testing.T) {
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	s.Start()
	app := s.App()
	home := app.SelectedProfile()
	file := s.Path("check.yaml")
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	raw := fmt.Sprintf("api_version: attn.dev/automations/v1alpha1\nname: Check\ntrigger: {type: manual}\nprompt: Check locally.\nlaunch: {driver: claude}\nlocation: {type: directory, path: %q}\n", s.Dir)
	if err := os.WriteFile(file, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	var first protocol.AutomationDefinitionSummary
	s.Attn("automation", "apply", "--file", file).JSON(t, &first)
	created := testworld.Request(app, protocol.ProfileCreateMessage{Cmd: protocol.CmdProfileCreate, RequestID: "side", Name: "Side"}, protocol.EventProfileActionResult, func(r protocol.ProfileActionResultMessage) bool { return r.RequestID == "side" })
	if !created.Success {
		t.Fatalf("create Side: %+v", created)
	}
	denied := s.Attn("automation", "list")
	if denied.Code == 0 || !strings.Contains(denied.Stderr, "choose --profile") {
		t.Fatalf("unscoped list: %+v", denied)
	}
	var side protocol.AutomationDefinitionSummary
	s.Attn("automation", "apply", "--profile", "Side", "--file", file).JSON(t, &side)
	if side.ProfileID != created.Profile.ID {
		t.Fatalf("Side apply: %+v", side)
	}
	for _, args := range [][]string{
		{"automation", "list", "--profile", home},
		{"automation", "list", "--profile=Default"},
	} {
		var listed []protocol.AutomationDefinitionSummary
		s.Attn(args...).JSON(t, &listed)
		if len(listed) != 1 || listed[0].ID != first.ID {
			t.Fatalf("scoped list: %+v", listed)
		}
	}
	for _, command := range []string{"show", "run", "delete", "enable", "disable", "cleanup", "set"} {
		args := []string{"automation", command, strconv.Itoa(side.ID), "--profile", home}
		if command == "set" {
			args = append(args, "--launch-desktop", "own")
		}
		result := s.Attn(args...)
		if result.Code == 0 || (!strings.Contains(result.Stderr, "not found") && !strings.Contains(result.Stderr, "does not exist")) {
			t.Errorf("foreign %s: %+v", command, result)
		}
	}
	requireStdout(t, s.Attn("automation", "show", strconv.Itoa(side.ID), "--profile", "Side"), "# Profile: Side")
	requireStdout(t, s.Attn("automation", "set", strconv.Itoa(side.ID), "--profile", "Side", "--launch-desktop", "own"), "Check")
}
