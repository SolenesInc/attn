package main_test

import (
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAgentCLIChoosesAProfileFromAPlainShell(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	requireStdout(t, s.Attn("agent", "peek", "--help"), "--profile <name|id>")
	s.Start()
	app := s.App()
	home := app.SelectedProfile()
	sender := s.Spawn(app, fakeagent.Claude, s.Path("sender"))
	target := s.Spawn(app, fakeagent.Claude, s.Path("target"))
	s.Launched(sender)
	s.Launched(target)
	requireStdout(t, s.Attn("agent", "list"), "--profile <name|id>")
	var peek protocol.AgentPeekResult
	s.Attn("agent", "peek", target, "--json").JSON(t, &peek)
	if string(peek.SessionID) != target {
		t.Fatalf("single-profile peek = %+v", peek)
	}

	created := testworld.Request(app, protocol.ProfileCreateMessage{Cmd: protocol.CmdProfileCreate, RequestID: "side", Name: "Side"}, protocol.EventProfileActionResult, func(r protocol.ProfileActionResultMessage) bool { return r.RequestID == "side" })
	if !created.Success {
		t.Fatalf("create Side: %+v", created)
	}
	side := protocol.Deref(created.Profile).ID
	sideApp := s.AppOn(side)
	foreign := s.Spawn(sideApp, fakeagent.Claude, s.Path("foreign"))
	s.Launched(foreign)

	requireFailure(t, s.Attn("agent", "peek", target, "--json"), "agent peek: ", "choose --profile")
	for _, profile := range []string{"Default", home, "Side", side} {
		wanted := target
		if profile == "Side" || profile == side {
			wanted = foreign
		}
		s.Attn("agent", "peek", wanted[:8], "--profile", profile, "--json").JSON(t, &peek)
		if string(peek.SessionID) != wanted {
			t.Errorf("peek --profile %s = %+v", profile, peek)
		}
	}

	for _, address := range []string{foreign, foreign[:8], "session:" + foreign} {
		requireFailure(t, s.Attn("agent", "peek", address, "--profile", home), "agent peek: ", "no session, crew member or seed matches")
		requireFailure(t, s.Attn("agent", "msg", address, "hello", "--source-session", sender), "agent msg: ", "no session, crew member or seed matches")
		requireFailure(t, s.Attn("agent", "close", address, "-m", "done", "--source-session", sender), "agent close: ", "no session, crew member or seed matches")
	}
	requireFailure(t, s.Run(testworld.Invocation{Args: []string{"agent", "peek", foreign, "--profile", side}, Session: sender}), "agent peek: ", "belongs to profile")

	var sent protocol.AgentMsgResult
	s.Attn("agent", "msg", target, "the source session selects the profile", "--source-session", sender, "--json").JSON(t, &sent)
	if sent.MessageID == "" || sent.Status == protocol.AgentMsgStatusRefused {
		t.Fatalf("msg --source-session = %+v", sent)
	}
	var message protocol.AgentPeerMessage
	s.Attn("agent", "msg-status", sent.MessageID, "--session", sender, "--json").JSON(t, &message)
	if message.Sender.Ref != protocol.PartyRef("session:"+sender) || message.To != protocol.AddressRef("session:"+target) {
		t.Errorf("selected-profile message = %+v", message)
	}
	requireFailure(t, s.Attn("agent", "close", target, "-m", "done", "--source-session", sender), "agent close: ", "may close")
	var closed protocol.AgentCloseResult
	s.Attn("agent", "close", sender, "-m", "finished", "--source-session", sender, "--json").JSON(t, &closed)
	if string(closed.TargetSessionID) != sender || closed.Rule != protocol.AgentCloseRuleSelf {
		t.Errorf("close --source-session = %+v", closed)
	}
	s.Attn("agent", "peek", foreign, "--profile", side, "--json").JSON(t, &peek)
	if string(peek.SessionID) != foreign {
		t.Errorf("foreign session after refused operations = %+v", peek)
	}
}
