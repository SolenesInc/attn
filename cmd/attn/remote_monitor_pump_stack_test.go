package main_test

import (
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestRemoteMonitorCommandsLeaveTheAppPumpFree(t *testing.T) {
	home := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	outpost := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	pair.Start()
	awaitNotebookState(t, pair, protocol.OutpostNotebookStateInSync)
	app := pair.App
	settings := testworld.Request(app, protocol.SetSettingMessage{Cmd: protocol.CmdSetSetting, Key: "asks_enabled", Value: "true", RequestID: protocol.Ptr("asks-on")}, protocol.EventSettingsUpdated,
		func(m protocol.SettingsUpdatedMessage) bool { return protocol.Deref(m.RequestID) == "asks-on" })
	if !protocol.Deref(settings.Success) {
		t.Fatalf("enable asks: %s", protocol.Deref(settings.Error))
	}
	outpostApp := outpost.App()
	if outpostApp.Initial.Settings["asks_enabled"] != "true" {
		testworld.Await(outpostApp, protocol.EventSettingsUpdated, func(m protocol.SettingsUpdatedMessage) bool { return m.Settings["asks_enabled"] == "true" })
	}
	id := home.Spawn(app, fakeagent.Claude, outpost.Path("remote"), func(m *protocol.SpawnSessionMessage) {
		m.Host = &pair.Host
		m.InitialPrompt = protocol.Ptr("work remotely")
	})
	remote := outpost.LaunchedCarrying(app.Terminal(id))
	remote.Prompted()
	remote.Reply("Ready for review. <!-- attn:state=waiting_input -->")
	testworld.AwaitSession(app, id, func(s protocol.Session) bool { return protocol.Deref(s.TurnOwed) })
	localID := home.Spawn(app, fakeagent.Claude, home.Path("local"))
	local := home.Launched(localID)
	app.Watch(localID)
	session := protocol.SessionID(id)
	asked, err := home.Client().AskOpen(session, "", ":::question\nWhich color?\n- [ ] Blue\n- [ ] Red\n:::\n", "Pick a color", outpost.Path("remote"))
	if err != nil {
		t.Fatalf("open remote ask: %v", err)
	}
	planted, err := home.Client().SeedPlant(session, "Review remote work", "Review the change before merging.", "", "", "")
	if err != nil {
		t.Fatalf("plant remote work: %v", err)
	}
	until := time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)
	path := home.Path("review.md")
	uri := "attn://file/" + strings.ReplaceAll(url.QueryEscape(path), "+", "%20")
	saved := testworld.Request(app, protocol.MarkdownAnnotationsSaveMessage{
		Cmd: protocol.CmdMarkdownAnnotationsSave, RequestID: "draft", DocumentUri: uri, SourceKind: "file", Path: &path, Generation: 1,
		Annotations: []protocol.MarkdownAnnotation{{ID: "review", Type: "global", Text: protocol.Ptr("Use blue for the banner"), CreatedAt: 1}},
	}, protocol.EventMarkdownAnnotationsSaveResult, func(r protocol.MarkdownAnnotationsSaveResultMessage) bool { return r.RequestID == "draft" })
	if !saved.Success {
		t.Fatalf("save annotation: %+v", saved)
	}
	for _, step := range []struct {
		name    string
		command any
		check   func()
	}{
		{"settle", protocol.SettleTurnMessage{Cmd: protocol.CmdSettleTurn, SessionID: session}, func() {
			testworld.AwaitSession(app, id, func(s protocol.Session) bool { return !protocol.Deref(s.TurnOwed) })
		}},
		{"snooze", protocol.SnoozeTurnMessage{Cmd: protocol.CmdSnoozeTurn, SessionID: session, Until: until}, func() {
			testworld.AwaitSession(app, id, func(s protocol.Session) bool { return protocol.Deref(s.TurnSnoozedUntil) == until })
		}},
		{"wake", protocol.WakeTurnMessage{Cmd: protocol.CmdWakeTurn, SessionID: session}, func() {
			testworld.AwaitSession(app, id, func(s protocol.Session) bool {
				return protocol.Deref(s.TurnOwed) && protocol.Deref(s.TurnSnoozedUntil) == ""
			})
		}},
		{"cancel_countdown", protocol.CancelCountdownMessage{Cmd: protocol.CmdCancelCountdown, SessionID: session}, func() {}},
		{"assistant_window", protocol.SessionMessagesGetMessage{Cmd: protocol.CmdSessionMessagesGet, SessionID: session, RequestID: "window"}, func() {
			r := testworld.Await(app, protocol.EventSessionMessagesGetResult, func(r protocol.SessionMessagesGetResultMessage) bool { return r.RequestID == "window" })
			if !r.Success {
				t.Fatalf("assistant window: %+v", r)
			}
		}},
		{"session_annotations", protocol.SessionAnnotationsSubmitMessage{Cmd: protocol.CmdSessionAnnotationsSubmit, SessionID: session, RequestID: "session-review", Text: "Fix the contrast"}, func() {
			r := testworld.Await(app, protocol.EventSessionAnnotationsSubmitResult, func(r protocol.SessionAnnotationsSubmitResultMessage) bool { return r.RequestID == "session-review" })
			if !r.Success || r.Status != "delivered" {
				t.Fatalf("session annotations: %+v", r)
			}
			if got := remote.Prompted(); !strings.Contains(got, "Fix the contrast") {
				t.Fatalf("annotation prompt: %q", got)
			}
			remote.Reply("Reviewed. <!-- attn:state=waiting_input -->")
			testworld.AwaitSession(app, id, func(s protocol.Session) bool { return s.State == protocol.SessionStateWaitingInput })
		}},
		{"markdown_annotations", protocol.MarkdownAnnotationsSubmitMessage{Cmd: protocol.CmdMarkdownAnnotationsSubmit, RequestID: "markdown-review", DocumentUri: uri, SourceKind: "file", Path: &path, TargetSessionID: &id}, func() {
			r := testworld.Await(app, protocol.EventMarkdownAnnotationsSubmitResult, func(r protocol.MarkdownAnnotationsSubmitResultMessage) bool { return r.RequestID == "markdown-review" })
			if !r.Success || r.Status != "delivered" {
				t.Fatalf("markdown annotations: %+v", r)
			}
			if got := remote.Prompted(); !strings.Contains(got, "Use blue for the banner") {
				t.Fatalf("markdown annotation prompt: %q", got)
			}
			remote.Reply("Reviewed. <!-- attn:state=waiting_input -->")
			testworld.AwaitSession(app, id, func(s protocol.Session) bool { return s.State == protocol.SessionStateWaitingInput })
		}},
		{"rename", protocol.RenameSessionMessage{Cmd: protocol.CmdRenameSession, SessionID: session, Label: "Remote review"}, func() {
			r := testworld.Await(app, protocol.EventRenameResult, func(r protocol.RenameResultMessage) bool { return r.ID == session })
			if !r.Success {
				t.Fatalf("rename: %+v", r)
			}
		}},
		{"ask_answer", protocol.AskAnswerMessage{Cmd: protocol.CmdAskAnswer, RequestID: "answer", AskID: asked.ID, Answers: []protocol.AskAnswer{{Key: "q1", Picked: []string{"Blue"}}}}, func() {
			r := testworld.Await(app, protocol.EventAskAnswerResult, func(r protocol.AskAnswerResultMessage) bool { return r.RequestID == "answer" })
			if !r.Success {
				t.Fatalf("answer remote ask: %+v", r)
			}
			if got := remote.Prompted(); !strings.Contains(got, "Blue") {
				t.Fatalf("ask answer prompt: %q", got)
			}
		}},
		{"harvest_when_merged", protocol.SeedTransitionMessage{Cmd: protocol.CmdSeedTransition, RequestID: protocol.Ptr("arm-harvest"), SeedID: planted.Seed.ID, Verb: "harvest", SourceSessionID: &session,
			WhenMerged: &protocol.SeedHarvestWhenMerged{PullRequestURL: protocol.Ptr("https://github.com/example/project/pull/1")}}, func() {
			r := testworld.Await(app, protocol.EventSeedTransitionResult, func(r protocol.SeedTransitionResultMessage) bool { return r.RequestID == "arm-harvest" })
			if !r.Success {
				t.Fatalf("arm remote harvest: %s", protocol.Deref(r.Error))
			}
		}},
	} {
		t.Logf("holding outpost for %s", step.name)
		func() {
			resume := pair.SSH.Pause(pair.Destination)
			defer resume()
			app.Send(step.command)
			app.Send(protocol.SessionMessagesGetMessage{Cmd: protocol.CmdSessionMessagesGet, SessionID: session, RequestID: step.name})
			app.TypeLine(localID, step.name)
			r := testworld.Request(app, protocol.SetSettingMessage{Cmd: protocol.CmdSetSetting, Key: "headless_tasks.enabled", Value: "false", RequestID: &step.name}, protocol.EventSettingsUpdated,
				func(r protocol.SettingsUpdatedMessage) bool { return protocol.Deref(r.RequestID) == step.name })
			if !protocol.Deref(r.Success) {
				t.Fatalf("local setting while outpost stopped: %s", protocol.Deref(r.Error))
			}
			if got := local.Prompted(); got != step.name {
				t.Fatalf("local typing while outpost stopped: %q", got)
			}
			t.Logf("outpost stopped; settings_updated request_id=%s success=%t; local agent prompt=%q", protocol.Deref(r.RequestID), protocol.Deref(r.Success), step.name)
		}()
		barrier := testworld.Await(app, protocol.EventSessionMessagesGetResult, func(r protocol.SessionMessagesGetResultMessage) bool { return r.RequestID == step.name })
		if !barrier.Success {
			t.Fatalf("remote read after %s: %+v", step.name, barrier)
		}
		step.check()
		local.Reply("Local ready. <!-- attn:state=waiting_input -->")
	}
}

func TestRemoteDesktopClosesLeaveUnrelatedSnapshotsAndTypingFree(t *testing.T) {
	for _, kind := range []string{"tile", "desktop"} {
		t.Run(kind, func(t *testing.T) {
			home := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
			outpost := testworld.NewStack(t)
			pair := testworld.PrepareOutpostPair(t, home, outpost)
			pair.Start()
			app := pair.App
			remoteID := home.Spawn(app, fakeagent.Harness("shell"), outpost.Path("remote"), func(m *protocol.SpawnSessionMessage) { m.Host = &pair.Host })
			if kind == "desktop" {
				home.Spawn(app, fakeagent.Harness("shell"), outpost.Path("other"), func(m *protocol.SpawnSessionMessage) { m.Host = &pair.Host })
			}
			var closing protocol.Desktop
			var tile string
			for _, desktop := range home.App().Initial.Desktops {
				for _, pane := range desktop.Panes {
					if string(pane.SessionID) == remoteID {
						closing, tile = desktop, pane.PaneID
					}
				}
			}
			if tile == "" {
				t.Fatal("remote session has no tile")
			}
			created := testworld.Request(app, protocol.DesktopCreateMessage{Cmd: protocol.CmdDesktopCreate, RequestID: "keep", ProfileID: app.SelectedProfile()}, protocol.EventProfileActionResult,
				func(r protocol.ProfileActionResultMessage) bool { return r.RequestID == "keep" })
			if !created.Success || len(created.Desktops) != 1 {
				t.Fatalf("create local desktop: %+v", created)
			}
			switched := testworld.Request(app, protocol.DesktopSetCurrentMessage{Cmd: protocol.CmdDesktopSetCurrent, RequestID: "switch", ProfileID: app.SelectedProfile(), DesktopID: created.Desktops[0].ID}, protocol.EventProfileActionResult,
				func(r protocol.ProfileActionResultMessage) bool { return r.RequestID == "switch" })
			if !switched.Success {
				t.Fatalf("switch to local desktop: %+v", switched)
			}
			localID := home.Spawn(app, fakeagent.Claude, home.Path("local"))
			local := home.Launched(localID)
			app.Watch(localID)
			otherApp := home.App()
			func() {
				resume := pair.SSH.Pause(pair.Destination)
				defer resume()
				if kind == "tile" {
					app.Send(protocol.DesktopCloseTileMessage{Cmd: protocol.CmdDesktopCloseTile, RequestID: "close", DesktopID: closing.ID, TileID: tile})
				} else {
					app.Send(protocol.DesktopCloseMessage{Cmd: protocol.CmdDesktopClose, RequestID: "close", DesktopID: closing.ID, ExpectedRevision: closing.Revision})
				}
				app.Send(protocol.SetSessionPriorityMessage{Cmd: protocol.CmdSetSessionPriority, SessionID: protocol.SessionID(localID), Priority: true})
				changed := testworld.AwaitSession(app, localID, func(s protocol.Session) bool { return protocol.Deref(s.Priority) })
				renamed := testworld.Request(otherApp, protocol.DesktopRenameMessage{Cmd: protocol.CmdDesktopRename, RequestID: "local-rename", DesktopID: created.Desktops[0].ID, Name: "Local review"}, protocol.EventProfileActionResult,
					func(r protocol.ProfileActionResultMessage) bool { return r.RequestID == "local-rename" })
				if !renamed.Success {
					t.Fatalf("rename local desktop: %+v", renamed)
				}
				testworld.Await(app, protocol.EventProfileArrangementChanged, func(m protocol.ProfileArrangementChangedMessage) bool {
					for _, desktop := range m.Desktops {
						if desktop.ID == created.Desktops[0].ID && desktop.Name == "Local review" {
							return true
						}
					}
					return false
				})
				app.TypeLine(localID, "keep typing")
				if got := local.Prompted(); got != "keep typing" {
					t.Fatalf("local prompt during remote close: %q", got)
				}
				t.Logf("outpost stopped; local session snapshot id=%s priority=%t; other app's desktop rename arrived; local agent received typing", changed.ID, protocol.Deref(changed.Priority))
			}()
			closed := testworld.Await(app, protocol.EventProfileActionResult, func(r protocol.ProfileActionResultMessage) bool { return r.RequestID == "close" })
			if !closed.Success {
				t.Fatalf("remote %s close: %s", kind, protocol.Deref(closed.Error))
			}
		})
	}
}

func TestShowingAHiddenRemoteSessionLeavesTheAppPumpAndArrangementsFree(t *testing.T) {
	home := testworld.NewStack(t)
	outpost := testworld.NewStack(t, testworld.WithAgents(fakeagent.Codex))
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	pair.Start()
	awaitNotebookState(t, pair, protocol.OutpostNotebookStateInSync)
	shareOutpostCodex(t, pair)
	app := pair.App
	outpost.SetCodexFolderTrust(outpost.Path("agent"), "trusted")
	id := home.Spawn(app, fakeagent.Codex, outpost.Path("agent"), func(m *protocol.SpawnSessionMessage) { m.Host = &pair.Host })
	agent := outpost.LaunchedCarrying(app.Terminal(id))
	app.TypeLine(id, "before hiding")
	agent.Prompted()
	agent.Reply("Ready. <!-- attn:state=waiting_input -->")
	testworld.AwaitSession(app, id, func(s protocol.Session) bool { return protocol.Deref(s.TurnOwed) })
	app.TypeLine(id, "/new")
	agent.Prompted()
	testworld.AwaitSession(app, id, func(s protocol.Session) bool { return protocol.Deref(s.Hidden) })
	otherApp := home.App()
	desktop := otherApp.Initial.Desktops[0]
	func() {
		resume := pair.SSH.Pause(pair.Destination)
		defer resume()
		app.Send(protocol.DesktopShowSessionMessage{Cmd: protocol.CmdDesktopShowSession, RequestID: "show-hidden", SessionID: protocol.SessionID(id)})
		setting := testworld.Request(app, protocol.SetSettingMessage{Cmd: protocol.CmdSetSetting, Key: "sidebar_harness_logos_enabled", Value: "true", RequestID: protocol.Ptr("local-setting")}, protocol.EventSettingsUpdated,
			func(m protocol.SettingsUpdatedMessage) bool { return protocol.Deref(m.RequestID) == "local-setting" })
		if !protocol.Deref(setting.Success) {
			t.Fatalf("local setting during remote show: %s", protocol.Deref(setting.Error))
		}
		renamed := testworld.Request(otherApp, protocol.DesktopRenameMessage{Cmd: protocol.CmdDesktopRename, RequestID: "local-rename", DesktopID: desktop.ID, Name: "Local review"}, protocol.EventProfileActionResult,
			func(r protocol.ProfileActionResultMessage) bool { return r.RequestID == "local-rename" })
		if !renamed.Success {
			t.Fatalf("rename local desktop: %+v", renamed)
		}
		testworld.Await(app, protocol.EventProfileArrangementChanged, func(m protocol.ProfileArrangementChangedMessage) bool {
			for _, updated := range m.Desktops {
				if updated.ID == desktop.ID && updated.Name == "Local review" {
					return true
				}
			}
			return false
		})
		t.Log("outpost stopped; local setting answered and another app's desktop rename arrived while showing the hidden session waited")
	}()
	shown := testworld.Await(app, protocol.EventProfileActionResult, func(m protocol.ProfileActionResultMessage) bool { return m.RequestID == "show-hidden" })
	if !shown.Success {
		t.Fatalf("show hidden remote session: %s", protocol.Deref(shown.Error))
	}
	outpost.LaunchedCarrying(app.Terminal(id))
}
