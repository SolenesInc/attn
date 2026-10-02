package daemon_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestSharedCodexHiddenApprovalAndLateStopStayWithTheirOwner(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	sharedCodexSetting(t, app, true)
	a := w.Spawn(app, fakeagent.Codex, w.Path("a"))
	agentA := w.Launched(a)
	awaitSharedView(app, a, a)
	b := w.Spawn(app, fakeagent.Codex, w.Path("b"))
	agentB := w.Launched(b)
	awaitSharedView(app, b, b)
	beforeB := queriedSession(t, cli, b)
	app.TypeLine(a, "work on A")
	agentA.Prompted()
	app.TypeLine(a, "/agents "+agentB.ConversationID)
	awaitSharedView(app, a, b)
	agentA.AskApproval()
	asking := testworld.AwaitSession(app, a, func(s protocol.Session) bool { return s.State == protocol.SessionStatePendingApproval })
	if !protocol.Deref(asking.TurnOwed) || protocol.Deref(asking.CodexMode) != "shared" {
		t.Fatalf("hidden approval has no queue identity: %+v", asking)
	}
	blocked := sharedAnnotationSubmit(app, a, "blocked", "feedback during approval")
	if blocked.Success || blocked.Status != "skipped_pending_approval" {
		t.Fatalf("approval accepted input: %+v", blocked)
	}
	attached, err := cli.SessionReopen(client.SessionReopenOptions{SessionID: a})
	if err != nil {
		t.Fatal(err)
	}
	second := testworld.Await(app, protocol.EventWorkspaceLayoutUpdated, func(e protocol.WorkspaceLayoutUpdatedMessage) bool {
		for _, pane := range e.WorkspaceLayout.Panes {
			if pane.PaneID == protocol.Deref(attached.PaneID) && protocol.Deref(pane.CodexResolution) == protocol.CodexViewResolutionResolved {
				return true
			}
		}
		return false
	})
	var runtime string
	for _, pane := range second.WorkspaceLayout.Panes {
		if pane.PaneID == protocol.Deref(attached.PaneID) {
			runtime = protocol.Deref(pane.RuntimeID)
		}
	}
	app.AwaitScreen(runtime, "Allow the command to run?")
	app.TypeLine(a, "/agents "+agentA.ConversationID)
	awaitSharedView(app, a, a)
	app.AwaitScreen(a, "Allow the command to run?")
	app.Send(protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: runtime, Data: "\r"})
	if got := agentA.Answered(); got != "accepted" {
		t.Fatal(got)
	}
	testworld.AwaitStateAfter(app, asking, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
	app.TypeLine(a, "/agents "+agentB.ConversationID)
	awaitSharedView(app, a, b)
	agentA.Reply("A needs feedback <!-- attn:state=waiting_input -->")
	testworld.AwaitSession(app, a, func(s protocol.Session) bool { return s.State == protocol.SessionStateWaitingInput })
	if got := queriedSession(t, cli, b); got.State != beforeB.State || protocol.Deref(got.TurnOwed) != protocol.Deref(beforeB.TurnOwed) {
		t.Fatalf("A approval/late Stop changed B: %+v", got)
	}
}

func sharedAnnotationSubmit(app *testworld.Peer, owner, request, text string) protocol.SessionAnnotationsSubmitResultMessage {
	app.T.Helper()
	return testworld.Request(app, protocol.SessionAnnotationsSubmitMessage{Cmd: protocol.CmdSessionAnnotationsSubmit, RequestID: request, SessionID: owner, Text: text}, protocol.EventSessionAnnotationsSubmitResult, func(e protocol.SessionAnnotationsSubmitResultMessage) bool { return e.RequestID == request })
}

func TestSharedCodexSystemErrorReplacesHiddenBusyAndApprovalClaims(t *testing.T) {
	for _, approval := range []bool{false, true} {
		t.Run(map[bool]string{false: "working", true: "approval"}[approval], func(t *testing.T) {
			w := newWorld(t, fakeagent.Codex)
			app := w.App()
			sharedCodexSetting(t, app, true)
			a := w.Spawn(app, fakeagent.Codex, w.Path("a"))
			agentA := w.Launched(a)
			awaitSharedView(app, a, a)
			b := w.Spawn(app, fakeagent.Codex, w.Path("b"))
			agentB := w.Launched(b)
			awaitSharedView(app, b, b)
			app.TypeLine(a, "work on A")
			agentA.Prompted()
			app.TypeLine(a, "/agents "+agentB.ConversationID)
			awaitSharedView(app, a, b)
			if approval {
				agentA.AskApproval()
				testworld.AwaitSession(app, a, func(s protocol.Session) bool { return s.State == protocol.SessionStatePendingApproval })
			}
			agentA.NativeSystemError()
			failed := testworld.AwaitSession(app, a, func(s protocol.Session) bool { return s.State == protocol.SessionStateWaitingInput })
			if !protocol.Deref(failed.TurnOwed) {
				t.Fatal("native error lost its owed attention turn")
			}
		})
	}
}

func TestSharedCodexControlDisconnectReconcilesHiddenBusyAndApprovalClaims(t *testing.T) {
	for _, approval := range []bool{false, true} {
		t.Run(map[bool]string{false: "working", true: "approval"}[approval], func(t *testing.T) {
			w := newWorld(t, fakeagent.Codex)
			app := w.App()
			sharedCodexSetting(t, app, true)
			a := w.Spawn(app, fakeagent.Codex, w.Path("a"))
			agentA := w.Launched(a)
			awaitSharedView(app, a, a)
			b := w.Spawn(app, fakeagent.Codex, w.Path("b"))
			agentB := w.Launched(b)
			awaitSharedView(app, b, b)
			app.TypeLine(a, "work on A")
			agentA.Prompted()
			app.TypeLine(a, "/agents "+agentB.ConversationID)
			awaitSharedView(app, a, b)
			if approval {
				agentA.AskApproval()
				testworld.AwaitSession(app, a, func(s protocol.Session) bool { return s.State == protocol.SessionStatePendingApproval })
			}
			agentA.SetNativeControlAvailable(false)
			failed := testworld.AwaitSession(app, a, func(s protocol.Session) bool { return s.State == protocol.SessionStateWaitingInput })
			if !protocol.Deref(failed.TurnOwed) {
				t.Fatal("control disconnect lost its owed attention turn")
			}
			agentA.SetNativeControlAvailable(true)
			app.TypeLine(a, "/agents "+agentA.ConversationID)
			awaitSharedView(app, a, a)
			if approval {
				recovered := testworld.AwaitStateAfter(app, failed, func(s protocol.Session) bool { return s.State == protocol.SessionStatePendingApproval })
				app.AwaitScreen(a, "Allow the command to run?")
				app.Send(protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: a, Data: "\r"})
				if got := agentA.Answered(); got != "accepted" {
					t.Fatal(got)
				}
				testworld.AwaitStateAfter(app, recovered, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
			} else {
				testworld.AwaitStateAfter(app, failed, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
			}
			result := sharedAnnotationSubmit(app, a, "reconnect-A", "reconnect and steer A")
			if !result.Success {
				t.Fatal(result)
			}
			if got := agentA.Prompted(); got != "reconnect and steer A" {
				t.Fatal(got)
			}
			testworld.AwaitSession(app, a, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
			agentA.Reply("A finished <!-- attn:state=idle -->")
			testworld.AwaitSession(app, a, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
		})
	}
}

func TestSharedCodexMailboxUsesOneHiddenOwnerAndSteeringPreservesBothDrafts(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	sharedCodexSetting(t, app, true)
	a := w.Spawn(app, fakeagent.Codex, w.Path("a"))
	agentA := w.Launched(a)
	awaitSharedView(app, a, a)
	b := w.Spawn(app, fakeagent.Codex, w.Path("b"))
	agentB := w.Launched(b)
	awaitSharedView(app, b, b)
	app.AwaitScreen(a, "Showing "+agentA.ConversationID)
	app.Send(protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: a, Source: protocol.Ptr("automation"), Data: "/agents " + agentB.ConversationID + "\r"})
	awaitSharedView(app, a, b)
	app.Send(protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: a, Data: "draft B"})
	app.AwaitScreen(a, "draft B")
	sent := sendAgentMessage(t, cli, b, a, "hidden A mail")
	if sent.Status != protocol.AgentMsgStatusNotified {
		t.Fatalf("hidden owner mail: %+v", sent)
	}
	if got := agentA.Prompted(); !strings.Contains(got, inboxDoorbell) {
		t.Fatal(got)
	}
	testworld.AwaitSession(app, a, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
	result := sharedAnnotationSubmit(app, a, "active-A", "steer A")
	if !result.Success || result.Status != "delivered" {
		t.Fatalf("active steer: %+v", result)
	}
	if got := agentA.Prompted(); got != "steer A" {
		t.Fatal(got)
	}
	var inbox protocol.AgentInboxBatchResult
	if err := json.Unmarshal([]byte(agentA.ToolShell(`"$ATTN_WRAPPER_PATH" agent inbox --json`)), &inbox); err != nil || len(inbox.Items) != 1 {
		t.Fatalf("owner tool inbox: %+v %v", inbox, err)
	}
	other, err := cli.AgentInboxBatch(b, 0)
	if err != nil || len(other.Items) != 0 {
		t.Fatalf("foreign inbox: %+v %v", other, err)
	}
	agentA.Reply("A done <!-- attn:state=idle -->")
	app.Send(protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: a, Data: " still here\r"})
	if got := agentB.Prompted(); got != "draft B still here" {
		t.Fatal(got)
	}
	agentB.Reply("B done <!-- attn:state=idle -->")
	testworld.AwaitSession(app, b, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	app.Send(protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: a, Data: "same root draft"})
	app.AwaitScreen(a, "same root draft")
	result = sharedAnnotationSubmit(app, b, "idle-B", "API input for B")
	if !result.Success {
		t.Fatalf("idle start: %+v", result)
	}
	if got := agentB.Prompted(); got != "API input for B" {
		t.Fatal(got)
	}
	agentB.Reply("API done <!-- attn:state=idle -->")
	app.Send(protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: a, Data: "\r"})
	if got := agentB.Prompted(); got != "same root draft" {
		t.Fatal(got)
	}
	agentB.Reply("draft done <!-- attn:state=idle -->")
}

func TestSharedCodexSwitchedTypingDefersOnlyTheDisplayedOwnersDoorbell(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	sharedCodexSetting(t, app, true)
	a := w.Spawn(app, fakeagent.Codex, w.Path("a"))
	agentA := w.Launched(a)
	awaitSharedView(app, a, a)
	b := w.Spawn(app, fakeagent.Codex, w.Path("b"))
	agentB := w.Launched(b)
	awaitSharedView(app, b, b)
	app.AwaitScreen(a, "Showing "+agentA.ConversationID)
	app.Send(protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: a, Source: protocol.Ptr("automation"), Data: "/agents " + agentB.ConversationID + "\r"})
	awaitSharedView(app, a, b)
	app.Send(protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: a, Data: "B draft"})
	app.AwaitScreen(a, "B draft")
	if sent := sendAgentMessage(t, cli, a, b, "mail for B"); sent.Status != protocol.AgentMsgStatusQueued {
		t.Fatalf("B composer ignored: %+v", sent)
	}
	if sent := sendAgentMessage(t, cli, b, a, "mail for A"); sent.Status != protocol.AgentMsgStatusNotified {
		t.Fatalf("typing B was credited to A: %+v", sent)
	}
	agentA.Prompted()
	agentA.Reply("A finished <!-- attn:state=idle -->")
}

func TestSharedCodexMaintenanceDoesNotSettleAttentionButOwnerTypingAndSteeringDo(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	sharedCodexSetting(t, app, true)
	setSetting(t, app, "auto_settle_enabled", "true")
	setSetting(t, app, "auto_settle_arm_seconds", "5")
	setSetting(t, app, "auto_settle_countdown_seconds", "3")
	a := w.Spawn(app, fakeagent.Codex, w.Path("a"))
	agentA := w.Launched(a)
	awaitSharedView(app, a, a)
	app.TypeLine(a, "A asks")
	agentA.Prompted()
	agentA.Reply("A question <!-- attn:state=waiting_input -->")
	testworld.AwaitSession(app, a, func(s protocol.Session) bool { return s.State == protocol.SessionStateWaitingInput })
	b := w.Spawn(app, fakeagent.Codex, w.Path("b"))
	agentB := w.Launched(b)
	awaitSharedView(app, b, b)
	app.TypeLine(b, "B asks")
	agentB.Prompted()
	agentB.Reply("B question <!-- attn:state=waiting_input -->")
	testworld.AwaitSession(app, b, func(s protocol.Session) bool { return s.State == protocol.SessionStateWaitingInput })
	app.Send(protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: a, Source: protocol.Ptr("automation"), Data: "/agents " + agentB.ConversationID + "\r"})
	awaitSharedView(app, a, b)
	if sent := sendAgentMessage(t, cli, b, a, "maintenance for A"); sent.Status != protocol.AgentMsgStatusNotified {
		t.Fatal(sent)
	}
	agentA.Prompted()
	testworld.AwaitSession(app, a, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
	app.TypeLine(a, "B user answer")
	if got := agentB.Prompted(); got != "B user answer" {
		t.Fatal(got)
	}
	testworld.AwaitSession(app, b, func(s protocol.Session) bool { return s.AutoSettleFiresAt != nil })
	if got := queriedSession(t, cli, a); got.AutoSettleFiresAt != nil || !protocol.Deref(got.TurnOwed) {
		t.Fatalf("maintenance got user credit: %+v", got)
	}
	if result := sharedAnnotationSubmit(app, a, "user-A", "A user answer"); !result.Success {
		t.Fatal(result)
	}
	if got := agentA.Prompted(); got != "A user answer" {
		t.Fatal(got)
	}
	testworld.AwaitSession(app, a, func(s protocol.Session) bool { return s.AutoSettleFiresAt != nil })
	agentA.Reply("A finished <!-- attn:state=idle -->")
	agentB.Reply("B finished <!-- attn:state=idle -->")
}

func TestSharedCodexViewResumeProjectsSnapshotWhenControlResumeFails(t *testing.T) {
	for _, approval := range []bool{false, true} {
		t.Run(map[bool]string{false: "working", true: "approval"}[approval], func(t *testing.T) {
			w := newWorld(t, fakeagent.Codex)
			app := w.App()
			sharedCodexSetting(t, app, true)
			a := w.Spawn(app, fakeagent.Codex, w.Path("a"))
			agentA := w.Launched(a)
			awaitSharedView(app, a, a)
			b := w.Spawn(app, fakeagent.Codex, w.Path("b"))
			agentB := w.Launched(b)
			awaitSharedView(app, b, b)
			agentA.NativeSnapshotsOnly()
			app.TypeLine(a, "work on A")
			agentA.Prompted()
			app.TypeLine(a, "/agents "+agentB.ConversationID)
			awaitSharedView(app, a, b)
			if approval {
				agentA.AskApproval()
			}
			app.TypeLine(a, "/agents "+agentA.ConversationID)
			awaitSharedView(app, a, a)
			want := protocol.SessionStateWorking
			if approval {
				want = protocol.SessionStatePendingApproval
			}
			testworld.AwaitSession(app, a, func(s protocol.Session) bool { return s.State == want })
			if got := queriedSession(t, w.Client(), a); got.State != want || !protocol.Deref(got.TurnOwed) {
				t.Fatalf("resumed owner lost native snapshot: %+v", got)
			}
		})
	}
}

func TestSharedCodexSurvivingViewTrafficReconcilesControlLoss(t *testing.T) {
	for _, approval := range []bool{false, true} {
		t.Run(map[bool]string{false: "prompt", true: "approval_answer"}[approval], func(t *testing.T) {
			w := newWorld(t, fakeagent.Codex)
			app := w.App()
			sharedCodexSetting(t, app, true)
			a := w.Spawn(app, fakeagent.Codex, w.Path("a"))
			agentA := w.Launched(a)
			awaitSharedView(app, a, a)
			if approval {
				app.TypeLine(a, "work on A")
				agentA.Prompted()
				agentA.AskApproval()
				app.AwaitScreen(a, "Allow the command to run?")
				testworld.AwaitSession(app, a, func(s protocol.Session) bool { return s.State == protocol.SessionStatePendingApproval })
			}
			agentA.SetNativeControlAvailable(false)
			failed := testworld.AwaitSession(app, a, func(s protocol.Session) bool { return s.State == protocol.SessionStateWaitingInput })
			agentA.SetNativeControlAvailable(true)
			if approval {
				app.Send(protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: a, Data: "\r"})
				if got := agentA.Answered(); got != "accepted" {
					t.Fatal(got)
				}
			} else {
				app.TypeLine(a, "prompt through surviving view")
				if got := agentA.Prompted(); got != "prompt through surviving view" {
					t.Fatal(got)
				}
			}
			testworld.AwaitStateAfter(app, failed, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
			if result := sharedAnnotationSubmit(app, a, "active-A", "steer after reconnect"); !result.Success {
				t.Fatal(result)
			}
			if got := agentA.Prompted(); got != "steer after reconnect" {
				t.Fatal(got)
			}
			agentA.Reply("A finished <!-- attn:state=idle -->")
			testworld.AwaitSession(app, a, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
		})
	}
}
