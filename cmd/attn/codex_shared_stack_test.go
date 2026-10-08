package main_test

import (
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestASharedCodexKeepsItsAppServerTerminalAndConversationAcrossADaemonRestart(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Codex))
	s.Start()
	app := s.App()
	shareCodex(t, app)
	session := s.Spawn(app, fakeagent.Codex, s.Path("shop"))
	terminal := app.Terminal(session)
	codex := s.Launched(session)
	conversation := codex.ConversationID
	servers := s.CodexServers()
	if len(servers) != 1 {
		t.Fatalf("app-servers launched = %v, want one", servers)
	}

	s.Stop()
	s.Start()
	app = s.App()
	if got := app.Terminal(session); got != terminal {
		t.Fatalf("after the restart the session shows terminal %s, want %s", got, terminal)
	}
	app.TypeLine(session, "add a discount field")
	if got := codex.Prompted(); got != "add a discount field" {
		t.Fatalf("codex received %q after the restart", got)
	}
	if codex.ConversationID != conversation {
		t.Fatalf("the terminal shows conversation %s after the restart, want %s", codex.ConversationID, conversation)
	}
	testworld.AwaitSession(app, session, func(x protocol.Session) bool { return x.State == protocol.SessionStateWorking })
	codex.Reply("Added. Should it round? <!-- attn:state=waiting_input -->")
	testworld.AwaitSession(app, session, func(x protocol.Session) bool { return x.State == protocol.SessionStateWaitingInput })
	if got := s.CodexServers(); !slices.Equal(got, servers) {
		t.Errorf("app-servers launched = %v, want the first one, %v, still serving", got, servers)
	}
}

func TestAnApprovalPendingInASharedCodexIsAnsweredAfterADaemonRestart(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Codex))
	s.Start()
	app := s.App()
	shareCodex(t, app)
	session := s.Spawn(app, fakeagent.Codex, s.Path("shop"))
	codex := s.Launched(session)
	app.TypeLine(session, "run the migration")
	codex.Prompted()
	codex.AskApproval()
	testworld.AwaitSession(app, session, func(x protocol.Session) bool { return x.State == protocol.SessionStatePendingApproval })

	s.Stop()
	s.Start()
	app = s.App()
	app.AwaitScreen(session, "Allow the command to run?")
	app.TypeLine(session, "1")
	if got := codex.Answered(); got == "" {
		t.Fatal("the approval took no answer")
	}
	codex.Reply("Migrated. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, session, func(x protocol.Session) bool { return x.State == protocol.SessionStateIdle })
}

func shareCodex(t *testing.T, app *testworld.Peer) {
	t.Helper()
	requestID := uuid.NewString()
	set := testworld.Request(app, protocol.SetSettingMessage{Cmd: protocol.CmdSetSetting, Key: "codex_shared_enabled", Value: "true", RequestID: protocol.Ptr(requestID)},
		protocol.EventSettingsUpdated, func(m protocol.SettingsUpdatedMessage) bool { return protocol.Deref(m.RequestID) == requestID })
	if !protocol.Deref(set.Success) {
		t.Fatalf("share Codex: %s", protocol.Deref(set.Error))
	}
}

func TestAHiddenSharedCodexSessionSurvivesADaemonRestartAndTakesInput(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Codex))
	s.Start()
	app := s.App()
	shareCodex(t, app)
	session := s.Spawn(app, fakeagent.Codex, s.Path("shop"))
	codex := s.Launched(session)
	terminal := app.Terminal(session)
	app.TypeLine(session, "find the flaky checkout test")
	codex.Prompted()
	codex.Reply("It races the tax lookup. Lock it? <!-- attn:state=waiting_input -->")
	testworld.AwaitSession(app, session, func(x protocol.Session) bool { return x.State == protocol.SessionStateWaitingInput })
	conversation := codex.ConversationID
	for _, line := range []string{"/new", "add a discount field"} {
		typeLineInto(app, terminal, line)
		codex.Prompted()
	}
	testworld.AwaitSession(app, session, func(x protocol.Session) bool { return protocol.Deref(x.Hidden) })

	s.Stop()
	s.Start()
	app = s.App()
	i := slices.IndexFunc(app.Initial.Sessions, func(x protocol.Session) bool { return string(x.ID) == session })
	if i < 0 {
		t.Fatalf("session %s is gone after the restart", session)
	}
	if hidden := app.Initial.Sessions[i]; !protocol.Deref(hidden.Hidden) || hidden.State != protocol.SessionStateWaitingInput {
		t.Fatalf("after the restart the session is %s (hidden=%v), want it hidden and still waiting", hidden.State, protocol.Deref(hidden.Hidden))
	}
	requestID := uuid.NewString()
	feedback := "Lock the tax table before the lookup."
	delivered := testworld.Request(app, protocol.SessionAnnotationsSubmitMessage{
		Cmd: protocol.CmdSessionAnnotationsSubmit, RequestID: requestID, SessionID: protocol.SessionID(session), Text: feedback,
	}, protocol.EventSessionAnnotationsSubmitResult, func(r protocol.SessionAnnotationsSubmitResultMessage) bool { return r.RequestID == requestID })
	if !delivered.Success {
		t.Fatalf("feedback to the hidden session: %s", protocol.Deref(delivered.Error))
	}
	server := s.CodexServer()
	if got := server.Prompted(conversation); got != feedback {
		t.Fatalf("the hidden conversation took %q, want the feedback", got)
	}
	server.Reply(conversation, "Locked. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, session, func(x protocol.Session) bool { return x.State == protocol.SessionStateIdle })
}

func typeLineInto(app *testworld.Peer, terminal, text string) {
	app.T.Helper()
	probe := uuid.NewString()
	testworld.Request(app, protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: protocol.TerminalID(terminal), Data: text + "\r", ProbeID: protocol.Ptr(probe)},
		protocol.EventPtyInputProbeResult, func(r protocol.PtyInputProbeResultMessage) bool { return r.ProbeID == probe })
}

func TestAHiddenSharedCodexSessionCutOffByAMachineRestartComesBackIdle(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Codex))
	s.Start()
	app := s.App()
	shareCodex(t, app)
	session := s.Spawn(app, fakeagent.Codex, s.Path("shop"))
	codex := s.Launched(session)
	terminal := app.Terminal(session)
	app.TypeLine(session, "find the flaky checkout test")
	codex.Prompted()
	codex.Reply("It races the tax lookup. Lock it? <!-- attn:state=waiting_input -->")
	testworld.AwaitSession(app, session, func(x protocol.Session) bool { return x.State == protocol.SessionStateWaitingInput })
	conversation := codex.ConversationID
	for _, line := range []string{"/new", "add a discount field"} {
		typeLineInto(app, terminal, line)
		codex.Prompted()
	}
	testworld.AwaitSession(app, session, func(x protocol.Session) bool { return protocol.Deref(x.Hidden) })
	submitFeedback(t, app, session, "Lock the tax table before the lookup.")
	s.CodexServer().Prompted(conversation)
	testworld.AwaitSession(app, session, func(x protocol.Session) bool { return x.State == protocol.SessionStateWorking })

	s.Reboot()
	s.Start()
	app = s.App()
	i := slices.IndexFunc(app.Initial.Sessions, func(x protocol.Session) bool { return string(x.ID) == session })
	if i < 0 {
		t.Fatalf("session %s is gone after the restart", session)
	}
	if back := app.Initial.Sessions[i]; !protocol.Deref(back.Hidden) || back.State != protocol.SessionStateIdle {
		t.Fatalf("after the restart the session is %s (hidden=%v), want it hidden and idle", back.State, protocol.Deref(back.Hidden))
	}
}

func submitFeedback(t *testing.T, app *testworld.Peer, session, text string) {
	t.Helper()
	requestID := uuid.NewString()
	delivered := testworld.Request(app, protocol.SessionAnnotationsSubmitMessage{
		Cmd: protocol.CmdSessionAnnotationsSubmit, RequestID: requestID, SessionID: protocol.SessionID(session), Text: text,
	}, protocol.EventSessionAnnotationsSubmitResult, func(r protocol.SessionAnnotationsSubmitResultMessage) bool { return r.RequestID == requestID })
	if !delivered.Success {
		t.Fatalf("feedback to session %s: %s", session, protocol.Deref(delivered.Error))
	}
}
