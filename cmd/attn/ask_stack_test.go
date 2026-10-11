package main_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAnAgentAsksAndTheAskSurvivesADaemonRestartUntilWithdrawn(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	s.Start()
	app := s.App()
	enabled := testworld.Request(app, protocol.SetSettingMessage{Cmd: protocol.CmdSetSetting, Key: "asks_enabled", Value: "true", RequestID: protocol.Ptr("asks")},
		protocol.EventSettingsUpdated, func(m protocol.SettingsUpdatedMessage) bool { return protocol.Deref(m.RequestID) == "asks" })
	if !protocol.Deref(enabled.Success) {
		t.Fatalf("turn asks on: %s", protocol.Deref(enabled.Error))
	}
	session := s.Spawn(app, fakeagent.Claude, s.Path("shop"))
	s.Launched(session)
	inSession := func(stdin string, args ...string) testworld.Result {
		t.Helper()
		return s.Run(testworld.Invocation{Args: args, Session: session, Stdin: stdin})
	}

	asked := inSession(":::question\nWhich store keeps the cache?\n- [ ] Postgres\n- [ ] Redis\n:::\n", "ask")
	line := regexp.MustCompile(`^Asked (a-[0-9a-z]{6}): "Which store keeps the cache\?"\. The answer arrives in your attn inbox\.\n$`).FindStringSubmatch(asked.Stdout)
	if asked.Code != 0 || line == nil {
		t.Fatalf("attn ask exited %d with stdout %q and stderr %q", asked.Code, asked.Stdout, asked.Stderr)
	}
	id := line[1]
	if listed := inSession("", "ask", "list"); listed.Code != 0 || listed.Stdout != id+"  Which store keeps the cache?\n" {
		t.Errorf("attn ask list exited %d and printed %q", listed.Code, listed.Stdout)
	}
	s.Stop()
	s.Start()
	if listed := inSession("", "ask", "list"); listed.Code != 0 || listed.Stdout != id+"  Which store keeps the cache?\n" {
		t.Errorf("after a daemon restart, attn ask list exited %d and printed %q, stderr %q", listed.Code, listed.Stdout, listed.Stderr)
	}
	refused(t, inSession(":::question\nWhich?\n", "ask"), 1, "ask: line 1: question block has no closing :::")
	refused(t, inSession("", "ask"), 1, "ask: pipe the ask's markdown on stdin, or pass --file")

	if withdrawn := inSession("", "ask", "close", id, "--session", session); withdrawn.Code != 0 || withdrawn.Stdout != "Withdrew "+id+".\n" {
		t.Errorf("attn ask close exited %d and printed %q, stderr %q", withdrawn.Code, withdrawn.Stdout, withdrawn.Stderr)
	}
	refused(t, inSession("", "ask", "close", id), 1, "ask close: "+id+" is not an open ask of this session")
	if listed := inSession("", "ask", "list"); listed.Stdout != "No open asks.\n" {
		t.Errorf("after withdrawing, attn ask list printed %q", listed.Stdout)
	}
	if help := s.Attn("ask", "--help"); help.Code != 0 || !strings.Contains(help.Stdout, ":::question-multi") {
		t.Errorf("attn ask --help exited %d and printed %q", help.Code, help.Stdout)
	}
}
