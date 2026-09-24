package main

import (
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

func delegationTableForEdits() protocol.DelegationPreferences {
	return protocol.DelegationPreferences{Enabled: true, Revision: 4, Roles: []protocol.DelegationRole{{
		ID: "build", Name: "Build", Enabled: true, DefaultChoiceID: "default",
		Choices: []protocol.DelegationChoice{
			{ID: "default", Name: "Default", Selection: protocol.DelegationSelection{Harness: "claude", Model: "opus", Effort: "high"}},
			{ID: "hard", Name: "Hard", When: "Concurrency", Selection: protocol.DelegationSelection{Harness: "codex", Model: "gpt-5.6-sol", Effort: "xhigh"}},
		},
	}}}
}

func applyDelegationRolesEdit(t *testing.T, command string, args ...string) (protocol.DelegationPreferences, string, error) {
	t.Helper()
	files := map[string]string{"guide.md": "Read the guide.\n"}
	read := func(path string) ([]byte, error) {
		if content, ok := files[path]; ok {
			return []byte(content), nil
		}
		return nil, os.ErrNotExist
	}
	cfg := delegationTableForEdits()
	edit, message, err := parseDelegationRolesEdit(command, args, read)
	if err == nil {
		err = edit(&cfg)
	}
	return cfg, message, err
}

func TestDelegationRolesEditModelFlagsFollowLaunchOverrideRules(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want protocol.DelegationSelection
	}{
		{[]string{"build", "--model", "sonnet"}, protocol.DelegationSelection{Harness: "claude", Model: "sonnet"}},
		{[]string{"build", "--model", "sonnet", "--effort", "low"}, protocol.DelegationSelection{Harness: "claude", Model: "sonnet", Effort: "low"}},
		{[]string{"build", "--model", "opus"}, protocol.DelegationSelection{Harness: "claude", Model: "opus", Effort: "high"}},
		{[]string{"build", "--effort", "default"}, protocol.DelegationSelection{Harness: "claude", Model: "opus"}},
		{[]string{"--agent", "codex", "build"}, protocol.DelegationSelection{Harness: "codex"}},
		{[]string{"build", "--model", "default"}, protocol.DelegationSelection{Harness: "claude"}},
	} {
		cfg, _, err := applyDelegationRolesEdit(t, "set", tc.args...)
		if got := cfg.Roles[0].Choices[0].Selection; err != nil || got != tc.want {
			t.Errorf("set %v: got %+v, %v; want %+v", tc.args, got, err, tc.want)
		}
	}
}

func TestDelegationRolesEditsChangeOnlyTheirTarget(t *testing.T) {
	cfg, message, err := applyDelegationRolesEdit(t, "add", "review", "--name", "Review", "--instructions", "@guide.md", "--agent", "codex", "--model", "gpt-5.6-sol", "-m", "user asked")
	if err != nil || message != "user asked" {
		t.Fatalf("add: %v %q", err, message)
	}
	added := cfg.Roles[1]
	if added.Name != "Review" || added.Instructions != "Read the guide." || !added.Enabled || added.Choices[0].Selection != (protocol.DelegationSelection{Harness: "codex", Model: "gpt-5.6-sol"}) {
		t.Fatalf("added role: %+v", added)
	}
	if !reflect.DeepEqual(cfg.Roles[0], delegationTableForEdits().Roles[0]) {
		t.Fatal("adding a role changed another")
	}

	cfg, _, err = applyDelegationRolesEdit(t, "add", "build/fast", "--when", "Small fixes", "--model", "sonnet")
	if err != nil {
		t.Fatal(err)
	}
	fast := cfg.Roles[0].Choices[2]
	if fast.Name != "fast" || fast.When != "Small fixes" || fast.Selection != (protocol.DelegationSelection{Harness: "claude", Model: "sonnet"}) {
		t.Fatalf("alternative starts from the default model: %+v", fast)
	}

	cfg, _, err = applyDelegationRolesEdit(t, "set", "build/hard", "--default")
	if err != nil || cfg.Roles[0].DefaultChoiceID != "hard" {
		t.Fatalf("make default: %+v %v", cfg.Roles[0], err)
	}
	cfg, _, err = applyDelegationRolesEdit(t, "set", "--fallback", "--agent", "claude", "--model", "sonnet")
	if err != nil || cfg.Fallback.Selection != (protocol.DelegationSelection{Harness: "claude", Model: "sonnet"}) {
		t.Fatalf("fallback: %+v %v", cfg.Fallback, err)
	}
	cfg, _, err = applyDelegationRolesEdit(t, "disable")
	if err != nil || cfg.Enabled || !cfg.Roles[0].Enabled {
		t.Fatalf("disable the table: %+v %v", cfg, err)
	}
}

func TestDelegationRolesEditRefusalsSayWhatToDoInstead(t *testing.T) {
	for _, tc := range []struct {
		command string
		args    []string
		want    string
		usage   bool
	}{
		{"set", []string{"build"}, "nothing to change", true},
		{"rm", []string{"build/"}, "is not a role or role/alternative", true},
		{"set", []string{"/hard", "--model", "x"}, "is not a role or role/alternative", true},
		{"add", []string{"build/fast"}, "needs --when", true},
		{"add", []string{"review"}, "needs --name", true},
		{"add", []string{"build", "--name", "Again"}, "already exists", false},
		{"rm", []string{"build/default"}, "--default", false},
		{"set", []string{"missing", "--model", "x"}, "attn delegate roles show", false},
		{"add", []string{"pathfinder", "--builtin", "pathfinder"}, "Add Attn roles", false},
	} {
		_, _, err := applyDelegationRolesEdit(t, tc.command, tc.args...)
		var usage usageError
		if err == nil || !strings.Contains(err.Error(), tc.want) || errors.As(err, &usage) != tc.usage {
			t.Errorf("%s %v: %v (usage=%v); want %q usage=%v", tc.command, tc.args, err, errors.As(err, &usage), tc.want, tc.usage)
		}
	}
}
