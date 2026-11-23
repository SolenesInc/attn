package daemon_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptyworker"
	"github.com/victorarias/attn/internal/testworld"
)

func sessionByID(t *testing.T, w *world, id string) protocol.Session {
	t.Helper()
	result, err := w.Client().List("")
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	for _, s := range result.Sessions {
		if string(s.ID) == id {
			return s
		}
	}
	t.Fatalf("session %s not found in open sessions", id)
	return protocol.Session{}
}

func awaitAdoptedSession(app *testworld.Peer, label string) protocol.Session {
	app.T.Helper()
	for _, s := range app.Initial.Sessions {
		if s.Label == label {
			return s
		}
	}
	return *testworld.Await(app, protocol.EventSessionRegistered, func(e protocol.WebSocketEvent) bool {
		return e.Session != nil && e.Session.Label == label
	}).Session
}

func awaitVisibleWorker(w *world, app *testworld.Peer, label string) (protocol.Session, *fakeagent.Run) {
	app.T.Helper()
	adopted := awaitAdoptedSession(app, label)
	id := string(adopted.ID)
	testworld.AwaitSessionFromConnect(app, id, func(s protocol.Session) bool {
		return !protocol.Deref(s.Hidden)
	})
	return adopted, w.Launched(id)
}

func openSessionsWithLabel(t *testing.T, w *world, label string) []protocol.Session {
	t.Helper()
	result, err := w.Client().List("")
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	var matching []protocol.Session
	for _, s := range result.Sessions {
		if s.Label == label {
			matching = append(matching, s)
		}
	}
	return matching
}

func TestAppServerWorkerAdoptsOnceAcrossAllTriggersAndFollowsItsOwnTree(t *testing.T) {
	eachAppServer(t, func(t *testing.T, h fakeagent.Harness) {
		w := newWorld(t, h)
		app := w.App()
		if h == fakeagent.Sigma {
			setSetting(t, app, "agent_tools_enabled", "true")
		}
		parent, view, server := appServerSession(t, w, app, h)
		if h == fakeagent.Sigma {
			server.AwaitMCPRegistration("attn", true)
		}
		parentConv := view.ConversationID
		promptUntilWorking(app, parent, view, server, "start workers")
		server.Reply(parentConv, "Started. <!-- attn:state=idle -->")
		testworld.AwaitSession(app, parent, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })

		seed := plantSeed(t, w, "worker note target").ID
		server.Worker(parentConv, "worker-one", "refactor")

		var wg sync.WaitGroup
		var cliOut string
		var mcpRes fakeagent.ServerToolResult
		wg.Add(2)
		go func() {
			defer wg.Done()
			server.Work("worker-one")
		}()
		go func() {
			defer wg.Done()
			cliOut = strings.TrimSpace(server.RunTool("worker-one", `"$ATTN_WRAPPER_PATH" presence --session-id`))
		}()
		if h == fakeagent.Sigma {
			wg.Add(1)
			go func() {
				defer wg.Done()
				mcpRes = server.CallTool("worker-one", "seed_note", map[string]any{"seed": seed, "text": "From worker MCP."})
			}()
		}
		wg.Wait()

		adopted := awaitAdoptedSession(app, "refactor")
		workerID := string(adopted.ID)
		if workerID == parent {
			t.Fatalf("worker adopted as parent session %s, want its own session", parent)
		}
		if cliOut != workerID {
			t.Errorf("CLI presence --session-id = %q, want worker session %q", cliOut, workerID)
		}
		if h == fakeagent.Sigma {
			if mcpRes.Refused != "" || mcpRes.IsError {
				t.Fatalf("MCP seed_note from worker: refused %q, %s", mcpRes.Refused, mcpRes.Text())
			}
			if author, _ := newestNoteAuthor(t, w, seed); author != workerID {
				t.Errorf("MCP note author = %q, want worker session %q", author, workerID)
			}
		}
		if got := openSessionsWithLabel(t, w, "refactor"); len(got) != 1 {
			t.Fatalf("open sessions titled refactor = %d, want 1", len(got))
		}
		if adopted.Directory != w.Path("shop") || adopted.Agent != protocol.SessionAgent(h) {
			t.Errorf("adopted worker metadata = (%s, %s), want (%s, %s)", adopted.Directory, adopted.Agent, w.Path("shop"), h)
		}

		testworld.AwaitSession(app, workerID, func(s protocol.Session) bool {
			return !protocol.Deref(s.Hidden)
		})
		workerTile := w.Launched(workerID)
		if at := slices.Index(workerTile.Argv, "--conversation"); at < 0 || workerTile.Argv[at+1] != "worker-one" {
			t.Errorf("worker tile ran %q, want --conversation worker-one", workerTile.Argv)
		}

		testworld.AwaitSession(app, workerID, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
		server.AskApproval("worker-one")
		testworld.AwaitSession(app, workerID, func(s protocol.Session) bool { return s.State == protocol.SessionStatePendingApproval })
		if got := sessionByID(t, w, parent); got.State != protocol.SessionStateIdle {
			t.Errorf("parent state while worker waits on approval = %s, want idle", got.State)
		}

		server.Reply("worker-one", "Refactored. <!-- attn:state=idle -->")
		testworld.AwaitSession(app, workerID, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	})
}

func TestAppServerWorkerCallerAuthAdoptsBeforeAnyStatusEventAndExternalWorkersStayOutside(t *testing.T) {
	eachAppServer(t, func(t *testing.T, h fakeagent.Harness) {
		w := newWorld(t, h)
		app := w.App()
		if h == fakeagent.Sigma {
			setSetting(t, app, "agent_tools_enabled", "true")
		}
		parent, view, server := appServerSession(t, w, app, h)
		if h == fakeagent.Sigma {
			server.AwaitMCPRegistration("attn", true)
		}
		parentConv := view.ConversationID
		seed := plantSeed(t, w, "early caller target").ID

		server.StartedElsewhere("outside-root", w.Path("shop"), "external work")
		server.Worker("outside-root", "outside-worker", "external task")
		server.Work("outside-worker")

		server.Worker(parentConv, "early-worker", "early audit")
		cliSession := strings.TrimSpace(server.RunTool("early-worker", `"$ATTN_WRAPPER_PATH" presence --session-id`))
		if cliSession == "" || cliSession == parent {
			t.Fatalf("early CLI call resolved to %q, want newly adopted worker session (parent=%s)", cliSession, parent)
		}
		adopted, _ := awaitVisibleWorker(w, app, "early audit")
		if string(adopted.ID) != cliSession {
			t.Errorf("CLI resolved %s, want adopted session %s", cliSession, adopted.ID)
		}

		if h == fakeagent.Sigma {
			server.Worker(parentConv, "mcp-worker", "early mcp")
			res := server.CallTool("mcp-worker", "seed_note", map[string]any{"seed": seed, "text": "Before any status item."})
			if res.Refused != "" || res.IsError {
				t.Fatalf("early MCP call from worker failed: refused %q, %s", res.Refused, res.Text())
			}
			mcpAdopted, _ := awaitVisibleWorker(w, app, "early mcp")
			if author, _ := newestNoteAuthor(t, w, seed); author != string(mcpAdopted.ID) {
				t.Errorf("MCP note author = %q, want adopted session %q", author, mcpAdopted.ID)
			}

			refused := server.CallTool("outside-worker", "seed_note", map[string]any{"seed": seed, "text": "Should be refused."})
			if refused.Refused == "" {
				t.Errorf("external worker MCP call succeeded, want refusal")
			}
		}

		server.Worker(parentConv, "unheld-lead", "chain lead")
		server.Worker("unheld-lead", "unheld-helper", "chain helper")
		server.Work("unheld-helper")
		lead, _ := awaitVisibleWorker(w, app, "chain lead")
		helper, _ := awaitVisibleWorker(w, app, "chain helper")
		if string(lead.ID) == parent || string(helper.ID) == parent || lead.ID == helper.ID {
			t.Errorf("chained workers got overlapping session ids lead=%s helper=%s parent=%s", lead.ID, helper.ID, parent)
		}

		if outside := openSessionsWithLabel(t, w, "external task"); len(outside) != 0 {
			t.Errorf("external worker was adopted into %+v, want none", outside)
		}
	})
}

func TestAppServerWorkerAdoptedAfterDaemonRestartCountsSpendFromZero(t *testing.T) {
	eachAppServer(t, func(t *testing.T, h fakeagent.Harness) {
		t.Setenv("ATTN_PTY_BACKEND", "worker")
		t.Setenv("ATTN_PTY_WORKER_BINARY", testworld.AttnBinary(t))
		w := newWorld(t, h)
		t.Cleanup(func() { ptyworker.ReapDataDir(w.Dir) })
		app := w.App()
		parent, view, server := appServerSession(t, w, app, h)
		parentConv := view.ConversationID
		lateDoc := markdownFile(t, w, "late-worker.md")
		lateSubDoc := markdownFile(t, w, "late-worker-sub.md")
		promptUntilWorking(app, parent, view, server, "stay open")
		server.Reply(parentConv, "Ready. <!-- attn:state=idle -->")
		testworld.AwaitSession(app, parent, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })

		w.stop()
		server.Worker(parentConv, "late-worker", "late audit")
		server.ToolCall("late-worker", writeMarkdownCall(lateDoc))
		server.ToolCall("late-worker", fakeagent.ServerToolCall{Name: "bash", Input: map[string]any{"command": "gh pr create --fill"}, Output: shopPull(14) + "\n"})
		server.SetUsage("late-worker", "anthropic/claude-haiku-4-5", serverUsage(750))
		server.Subagent("late-worker", "late-sub", "late research")
		server.ToolCall("late-sub", writeMarkdownCall(lateSubDoc))
		server.SetUsage("late-sub", "anthropic/claude-haiku-4-5", serverUsage(250))
		server.Reply("late-worker", "Audited. <!-- attn:state=idle -->")

		w.start()
		app = w.App()
		rejoinAppServer(t, w, h)

		adopted := awaitAdoptedSession(app, "late audit")
		workerID := string(adopted.ID)
		testworld.AwaitSessionFromConnect(app, workerID, func(s protocol.Session) bool {
			return s.State == protocol.SessionStateIdle &&
				s.Usage != nil && s.Usage.TotalTokens == 1000 &&
				slices.Contains(pullNumbers(s), 14) &&
				!protocol.Deref(s.Hidden)
		})
		lateTile := w.Launched(workerID)
		if at := slices.Index(lateTile.Argv, "--conversation"); at < 0 || lateTile.Argv[at+1] != "late-worker" {
			t.Errorf("late worker tile ran %q, want --conversation late-worker", lateTile.Argv)
		}
		if got := recentFileCountForSession(app, lateDoc, workerID); got != 1 {
			t.Errorf("late-worker.md counts %d on worker session, want 1", got)
		}
		if got := recentFileCountForSession(app, lateSubDoc, workerID); got != 1 {
			t.Errorf("late-worker-sub.md counts %d on worker session, want 1", got)
		}
		if got := recentFileCountForSession(app, lateDoc, parent); got != 0 {
			t.Errorf("late-worker.md counts %d on parent session, want 0", got)
		}
	})
}

func TestClosedAppServerWorkerStaysClosedWhileIdleAndReopensWhenItRunsAgain(t *testing.T) {
	eachAppServer(t, func(t *testing.T, h fakeagent.Harness) {
		w := newWorld(t, h)
		app := w.App()
		parent, view, server := appServerSession(t, w, app, h)
		parentConv := view.ConversationID
		promptUntilWorking(app, parent, view, server, "start worker")
		server.Reply(parentConv, "Done. <!-- attn:state=idle -->")
		testworld.AwaitSession(app, parent, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })

		server.Worker(parentConv, "reopen-worker", "migrate")
		server.SetUsage("reopen-worker", "anthropic/claude-haiku-4-5", serverUsage(200))
		server.Work("reopen-worker")
		adopted := awaitAdoptedSession(app, "migrate")
		workerID := string(adopted.ID)
		testworld.AwaitSession(app, workerID, func(s protocol.Session) bool {
			return s.State == protocol.SessionStateWorking && !protocol.Deref(s.Hidden)
		})
		awaitUsageTokens(app, workerID, 200)
		workerTile := w.Launched(workerID)
		if h != fakeagent.Sigma {
			server.Reply("reopen-worker", "Done. <!-- attn:state=idle -->")
			testworld.AwaitSession(app, workerID, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
		}

		released := w.PauseAt(pausepoint.SessionCloseReleased)
		tornDown := w.PauseAt(pausepoint.SessionTeardownFinished)
		if closed := closeFromApp(app, workerID); !closed.Accepted {
			t.Fatalf("close worker: %s", protocol.Deref(closed.Error))
		}
		if h == fakeagent.Sigma {
			server.AwaitInterrupted("reopen-worker")
		}
		workerTile.AwaitGone()
		released.Await()
		released.Release()
		tornDown.Await()
		tornDown.Release()

		server.Worker(parentConv, "scan-receipt", "scan receipt")
		triggerDoc := markdownFile(t, w, "parent-after-close.md")
		server.ToolCall(parentConv, writeMarkdownCall(triggerDoc))
		awaitVisibleWorker(w, app, "scan receipt")
		if open := openSessionsWithLabel(t, w, "migrate"); len(open) != 0 {
			t.Fatalf("closed worker reopened while idle: %+v", open)
		}

		mark := len(app.Log())
		server.Work("reopen-worker")
		reopened := *testworld.AwaitSince(app, mark, protocol.EventSessionRegistered, func(e protocol.WebSocketEvent) bool {
			return e.Session != nil && e.Session.Label == "migrate"
		}).Session
		if string(reopened.ID) != workerID {
			t.Errorf("reopened worker session = %s, want original %s", reopened.ID, workerID)
		}
		testworld.AwaitSessionSince(app, mark, workerID, func(s protocol.Session) bool {
			return s.State == protocol.SessionStateWorking && !protocol.Deref(s.Hidden)
		})
		server.SetUsage("reopen-worker", "anthropic/claude-haiku-4-5", serverUsage(500))
		awaitUsageTokens(app, workerID, 500)
		reopenedTile := w.Launched(workerID)
		if at := slices.Index(reopenedTile.Argv, "--conversation"); at < 0 || reopenedTile.Argv[at+1] != "reopen-worker" {
			t.Errorf("reopened worker tile ran %q, want --conversation reopen-worker", reopenedTile.Argv)
		}
	})
}

func TestForkedWorkerNeverMovesParentTileAndHiddenWorkerCanBeShownAgain(t *testing.T) {
	w := newWorld(t, fakeagent.Sigma)
	app := w.App()
	parent, view, server := appServerSession(t, w, app, fakeagent.Sigma)
	parentConv := view.ConversationID
	parentTerminal := app.Terminal(parent)
	settleTwoSigmaPrompts(app, parent, view, server)

	server.ForkedWorker(parentConv, "forked-worker", "review")
	server.Work("forked-worker")
	adopted := awaitAdoptedSession(app, "review")
	workerID := string(adopted.ID)
	testworld.AwaitSession(app, workerID, func(s protocol.Session) bool {
		return s.State == protocol.SessionStateWorking && !protocol.Deref(s.Hidden)
	})
	workerTile := w.Launched(workerID)
	if shown := sessionShownIn(t, w, app, parentTerminal); shown != parent {
		t.Fatalf("parent tile moved to %s after forked worker started, want %s", shown, parent)
	}

	server.Reply("forked-worker", "Reviewed. <!-- attn:state=idle -->")
	settleTwoSigmaPrompts(app, workerID, workerTile, server)

	workerTerminal := app.Terminal(workerID)
	mark := len(app.Log())
	typeInto(app, workerTerminal, "/fork\r")
	workerTile.Prompted()
	awaitTerminalSessionChange(app, mark, workerTerminal, workerID)
	testworld.AwaitSessionSince(app, mark, workerID, func(s protocol.Session) bool { return protocol.Deref(s.Hidden) })

	showMark := len(app.Log())
	shown := requestShowSession(app, workerID)
	if !shown.Success {
		t.Fatalf("desktop_show_session on hidden worker: %s", protocol.Deref(shown.Error))
	}
	testworld.AwaitSessionSince(app, showMark, workerID, func(s protocol.Session) bool { return !protocol.Deref(s.Hidden) })
	reshownTile := w.Launched(workerID)
	if at := slices.Index(reshownTile.Argv, "--conversation"); at < 0 || reshownTile.Argv[at+1] != "forked-worker" {
		t.Errorf("desktop_show_session worker tile ran %q, want --conversation forked-worker", reshownTile.Argv)
	}
}

func TestServerBackedPluginAdoptsWorkersOverMCPAndWithoutWatchOrResume(t *testing.T) {
	t.Run("mcp-caller-header", func(t *testing.T) {
		w := newWorld(t, fakeagent.SampleServer)
		app := w.App()
		setSetting(t, app, "agent_tools_enabled", "true")
		seed := plantForTools(t, w)
		driver := registerToolsServerDriver(t, w)
		awaitDriverAvailable(app, serverPluginAgent)

		parent := spawnToolsServerView(w, app, driver)
		server := w.AppServer(fakeagent.SampleServer)
		server.AwaitMCPRegistration("attn", true)

		server.StartedElsewhere("ext-root", w.Path("shop"), "external work")
		server.Worker("ext-root", "ext-worker", "external task")
		server.Worker(parent.conversation, "plugin-mcp-worker", "plugin mcp")

		called := server.CallTool("plugin-mcp-worker", "seed_note", map[string]any{"seed": seed, "text": "From plugin worker MCP."})
		if called.Refused != "" || called.IsError {
			t.Fatalf("plugin worker MCP seed_note: refused %q, %s", called.Refused, called.Text())
		}
		var resumeParams struct {
			Conversation string `json:"conversation_id"`
		}
		resumeCall := driver.asked("driver.resume", &resumeParams)
		if resumeParams.Conversation != "plugin-mcp-worker" {
			t.Fatalf("adopted plugin worker tile asked for conversation %q, want plugin-mcp-worker", resumeParams.Conversation)
		}
		driver.answer(resumeCall, map[string]any{"argv": []string{filepath.Join(w.Dir, "bin", "sample-server"), "--attach", "--conversation", resumeParams.Conversation}})
		adopted, _ := awaitVisibleWorker(w, app, "plugin mcp")
		if author, _ := newestNoteAuthor(t, w, seed); author != string(adopted.ID) {
			t.Errorf("plugin worker MCP note author = %q, want adopted session %q", author, adopted.ID)
		}

		refused := server.CallTool("ext-worker", "seed_note", map[string]any{"seed": seed, "text": "Should be refused."})
		if refused.Refused == "" {
			t.Errorf("external plugin worker MCP call succeeded, want refusal")
		}
		if outside := openSessionsWithLabel(t, w, "external task"); len(outside) != 0 {
			t.Errorf("external plugin worker adopted into %+v, want none", outside)
		}
		driver.leave(w)
	})

	t.Run("without-watch-or-resume", func(t *testing.T) {
		w := newWorld(t, fakeagent.SampleServer)
		app := w.App()
		decl := serverDriverDeclaration(w)
		decl["abilities"] = []string{}
		driver := dialDriver(t, w, "plain-server")
		driver.agent = serverPluginAgent
		driver.mu.Lock()
		driver.holdLaunch = true
		driver.mu.Unlock()
		if err := driver.call("driver.register", map[string]any{
			"agent":        serverPluginAgent,
			"capabilities": map[string]bool{"server_binding": true},
			"server":       decl,
		}, &driver.registered); err != nil {
			t.Fatal(err)
		}
		awaitDriverAvailable(app, serverPluginAgent)

		_, parentView := spawnPluginServerView(w, app, driver)
		server := w.AppServer(fakeagent.SampleServer)

		server.StartedElsewhere("late-root", w.Path("shop"), "preexisting root")
		server.Worker("late-root", "late-worker", "late plain")
		server.Work("late-worker")

		server.Worker(parentView.ConversationID, "plain-worker", "plain worker")
		server.Work("plain-worker")
		var spawnParams struct {
			Conversation    string `json:"conversation_id"`
			ResumeSessionID string `json:"resume_session_id"`
		}
		workerCall := driver.asked("driver.spawn", &spawnParams)
		if spawnParams.Conversation != "plain-worker" || spawnParams.ResumeSessionID != "" {
			t.Fatalf("non-resume worker tile params = %+v, want conversation_id=plain-worker and empty resume_session_id", spawnParams)
		}
		driver.answer(workerCall, map[string]any{"argv": []string{filepath.Join(w.Dir, "bin", "sample-server"), "--attach", "--conversation", spawnParams.Conversation}})
		awaitVisibleWorker(w, app, "plain worker")

		if err := os.MkdirAll(w.Path("shop"), 0700); err != nil {
			t.Fatal(err)
		}
		lateRootID := uuid.NewString()
		app.Send(protocol.SpawnSessionMessage{
			Cmd: protocol.CmdSpawnSession, ID: protocol.SessionID(lateRootID), Agent: serverPluginAgent,
			Cwd: w.Path("shop"), ProfileID: app.SelectedProfile(), Cols: 80, Rows: 24,
			ResumeSessionID: protocol.Ptr("late-root"),
		})
		var rootParams struct {
			Conversation    string `json:"conversation_id"`
			ResumeSessionID string `json:"resume_session_id"`
		}
		rootCall := driver.asked("driver.spawn", &rootParams)
		if rootParams.Conversation != "late-root" || rootParams.ResumeSessionID != "" {
			t.Fatalf("non-resume root open params = %+v, want conversation_id=late-root and empty resume_session_id", rootParams)
		}
		driver.answer(rootCall, map[string]any{"argv": []string{filepath.Join(w.Dir, "bin", "sample-server"), "--attach", "--conversation", rootParams.Conversation}})
		res := testworld.Await(app, protocol.EventSpawnResult, func(r protocol.SpawnResultMessage) bool { return string(r.ID) == lateRootID })
		if !res.Success {
			t.Fatalf("open late-root: %s", protocol.Deref(res.Error))
		}
		w.Launched(lateRootID)

		var lateParams struct {
			Conversation string `json:"conversation_id"`
		}
		lateCall := driver.asked("driver.spawn", &lateParams)
		if lateParams.Conversation != "late-worker" {
			t.Fatalf("late worker tile asked for %q, want late-worker", lateParams.Conversation)
		}
		driver.answer(lateCall, map[string]any{"argv": []string{filepath.Join(w.Dir, "bin", "sample-server"), "--attach", "--conversation", lateParams.Conversation}})
		awaitVisibleWorker(w, app, "late plain")
		driver.leave(w)
	})
}
