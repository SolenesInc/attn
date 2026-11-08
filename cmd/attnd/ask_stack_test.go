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
	enableAsks(t, app)
	session := s.Spawn(app, fakeagent.Claude, s.Path("shop"))
	s.Launched(session)
	inSession := func(stdin string, args ...string) testworld.Result {
		t.Helper()
		return s.Run(testworld.Invocation{Args: args, Session: session, Stdin: stdin})
	}

	asked := inSession(":::question\nWhich store keeps the cache?\n- [ ] Postgres\n- [ ] Redis\n:::\n", "ask")
	line := regexp.MustCompile(`^Asked (a-[0-9a-z]{6}): "Which store keeps the cache\?"\. attn types the answer into this conversation as the user's message\.\n$`).FindStringSubmatch(asked.Stdout)
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

func TestAnAgentAsksAboutASeedItTends(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	s.Start()
	app := s.App()
	enableAsks(t, app)
	session := s.Spawn(app, fakeagent.Claude, s.Path("shop"))
	s.Launched(session)
	inSession := func(stdin string, args ...string) testworld.Result {
		t.Helper()
		return s.Run(testworld.Invocation{Args: args, Session: session, Stdin: stdin})
	}
	var tended, untended protocol.Seed
	inSession("", "seed", "plant", "Pick the cache store", "--json").JSON(t, &tended)
	inSession("", "seed", "plant", "Tune the queue", "--json").JSON(t, &untended)
	if tend := inSession("", "seed", "tend", tended.ID); tend.Code != 0 {
		t.Fatalf("attn seed tend exited %d: %s", tend.Code, tend.Stderr)
	}

	question := ":::question\nWhich store keeps the cache?\n- [ ] Postgres\n- [ ] Redis\n:::\n"
	asked := inSession(question, "ask", "--seed", tended.StepSlug)
	line := regexp.MustCompile(`^Asked (a-[0-9a-z]{6}) about pick-cache-store: "Which store keeps the cache\?"\. The answer goes to whoever tends the seed by then, and the seed keeps it as a note: attn seed notes ` + tended.ID + `\n$`).FindStringSubmatch(asked.Stdout)
	if asked.Code != 0 || line == nil {
		t.Fatalf("attn ask --seed exited %d with stdout %q and stderr %q", asked.Code, asked.Stdout, asked.Stderr)
	}
	if listed := inSession("", "ask", "list"); listed.Code != 0 || listed.Stdout != line[1]+"  pick-cache-store  Which store keeps the cache?\n" {
		t.Errorf("attn ask list exited %d and printed %q", listed.Code, listed.Stdout)
	}
	refused(t, inSession(question, "ask", "--seed", untended.ID), 1, "ask: nobody is tending "+untended.ID)
}

func enableAsks(t *testing.T, app *testworld.Peer) {
	t.Helper()
	enabled := testworld.Request(app, protocol.SetSettingMessage{Cmd: protocol.CmdSetSetting, Key: "asks_enabled", Value: "true", RequestID: protocol.Ptr("asks")},
		protocol.EventSettingsUpdated, func(m protocol.SettingsUpdatedMessage) bool { return protocol.Deref(m.RequestID) == "asks" })
	if !protocol.Deref(enabled.Success) {
		t.Fatalf("turn asks on: %s", protocol.Deref(enabled.Error))
	}
}
