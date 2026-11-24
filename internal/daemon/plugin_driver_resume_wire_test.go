package daemon_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func stopDriven(app *testworld.Peer, driver *driverPeer, session string) {
	app.T.Helper()
	terminal := app.Terminal(session)
	app.Send(protocol.KillSessionMessage{Cmd: protocol.CmdKillSession, ID: protocol.SessionID(session)})
	if closed := driver.closed(); closed.SessionID != terminal {
		app.T.Fatalf("the driver was told %+v, want terminal %s of %s to have stopped", closed, terminal, session)
	}
	testworld.Await(app, protocol.EventSessionExited, func(e protocol.SessionExitedMessage) bool { return string(e.SessionID) == session })
}

func relaunchDriven(w *world, driver *driverPeer, session, cwd string) driverLaunch {
	w.T.Helper()
	w.Spawn(w.App(), fakeagent.Harness(driver.agent), cwd, func(m *protocol.SpawnSessionMessage) { m.ID = protocol.SessionID(session) })
	return driver.launched()
}

func hasNoMetadata(raw json.RawMessage) bool {
	return len(raw) == 0 || string(raw) == "null"
}

func TestADriverThatResumesIsHandedTheConversationItWasNamedOrLastReported(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	driver := connectDriver(t, w, "snipe-plugin", "snipe", map[string]bool{"resume": true, "state_reporting": true})
	awaitDriverAvailable(app, "snipe")
	cwd := w.Path("shop")
	session, named := spawnDriven(w, app, driver, cwd, func(m *protocol.SpawnSessionMessage) {
		m.ResumeSessionID = protocol.Ptr("snipe-conv-7")
	})
	if named.Method != "driver.resume" || named.ResumeSessionID != "snipe-conv-7" || !hasNoMetadata(named.Metadata) {
		t.Errorf("a spawn naming a conversation asked the driver %s for %q with metadata %s, want driver.resume for snipe-conv-7 and none", named.Method, named.ResumeSessionID, named.Metadata)
	}

	metadata := func(seq uint64, native string) {
		t.Helper()
		driver.mustReport("session.report_metadata", map[string]any{
			"session_id": named.SessionID, "run_id": named.RunID, "seq": seq,
			"metadata": map[string]string{"snipe_session_id": native}, "resume_session_id": native,
		})
	}
	metadata(1, "native-id")
	metadata(1, "older")
	stopDriven(app, driver, session)

	relaunch := relaunchDriven(w, driver, session, cwd)
	if relaunch.Method != "driver.resume" || relaunch.ResumeSessionID != "native-id" || string(relaunch.Metadata) != `{"snipe_session_id":"native-id"}` {
		t.Errorf("the relaunch asked the driver %s for %q with metadata %s, want driver.resume of native-id with the metadata reported first", relaunch.Method, relaunch.ResumeSessionID, relaunch.Metadata)
	}
}

func TestADriverWithoutResumeRelaunchesFreshWhateverConversationItReported(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	driver := connectDriver(t, w, "snipe-plugin", "snipe", map[string]bool{"state_reporting": true})
	awaitDriverAvailable(app, "snipe")
	cwd := w.Path("shop")
	session, run := spawnDriven(w, app, driver, cwd)
	driver.mustReport("session.report_metadata", map[string]any{
		"session_id": run.SessionID, "run_id": run.RunID, "seq": 1,
		"metadata": map[string]string{"snipe_session_id": "native-id"}, "resume_session_id": "native-id",
	})
	stopDriven(app, driver, session)

	if relaunch := relaunchDriven(w, driver, session, cwd); relaunch.Method != "driver.spawn" || relaunch.ResumeSessionID != "" {
		t.Errorf("the relaunch asked the driver %s for %q, want a fresh driver.spawn", relaunch.Method, relaunch.ResumeSessionID)
	}
}

func TestAStalledResumeInspectionTimesOutAndIsRetriedOnTheNextRead(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		driver := connectDriver(t, w, "snipe-plugin", "snipe", map[string]bool{
			"resume": true, "resume_availability": true, "initial_prompt": true, "state_reporting": true,
		})
		awaitDriverAvailable(app, "snipe")
		delegations := make(chan *protocol.DelegateResult, 1)
		go func() { delegations <- seedResumeDelegate(t, w, scriptedAgent, "inspection") }()
		run := driver.launched()
		if err := driver.state(run, 1, "working"); err != nil {
			t.Fatal(err)
		}
		delegated := <-delegations
		driver.mustReport("session.report_metadata", map[string]any{
			"session_id": run.SessionID, "run_id": run.RunID, "seq": 2,
			"metadata": map[string]string{"native_id": "saved-conversation"}, "resume_session_id": "saved-conversation",
		})
		closePane(app, sessionPane{session: string(delegated.SessionID)})

		shown := make(chan *protocol.SeedShowResult, 1)
		go func() { shown <- lifeShow(t, cli, delegated.SeedID) }()
		var check resumeInspection
		driver.asked("driver.resume_available", &check)
		if len(check.Conversations) != 1 || check.Conversations[0].CWD != delegated.Directory || check.Conversations[0].ResumeID != "saved-conversation" {
			t.Fatalf("inspection = %+v", check)
		}
		w.advance(10 * time.Second)
		result := <-shown
		if continuation := result.Seed.Continuation; continuation == nil || continuation.ResumeAvailable || !strings.Contains(protocol.Deref(continuation.ResumeReason), "resume_availability_timeout=10s") {
			t.Fatalf("stalled inspection = %+v, want the named timeout", continuation)
		}

		go func() { shown <- lifeShow(t, cli, delegated.SeedID) }()
		request := driver.asked("driver.resume_available", &check)
		answerResumeInspection(driver, request, check, func(string, string) bool { return true }, "")
		if result := <-shown; result.Seed.Continuation == nil || !result.Seed.Continuation.ResumeAvailable {
			t.Fatalf("next read after the driver recovered = %+v", result.Seed.Continuation)
		}
	})
}

func TestRecreatingAWorktreeChecksPluginStorageAfterProjectSettingsReturn(t *testing.T) {
	for _, available := range []bool{true, false} {
		t.Run(map[bool]string{true: "present", false: "missing"}[available], func(t *testing.T) {
			w := newWorld(t)
			app, cli := w.App(), w.Client()
			driver := connectDriver(t, w, "snipe-plugin", "snipe", map[string]bool{"resume": true, "resume_availability": true})
			awaitDriverAvailable(app, "snipe")
			repo := reopenRepoWithOrigin(t)
			settings := filepath.Join(repo, ".snipe", "settings.json")
			if err := os.MkdirAll(filepath.Dir(settings), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(settings, []byte(`{"sessionDir":"/external/snipe-sessions"}`), 0o644); err != nil {
				t.Fatal(err)
			}
			runGit(t, repo, "add", ".snipe/settings.json")
			runGit(t, repo, "commit", "-m", "Configure external plugin storage")
			worktree := reopenWorktree(t, repo, "feat/project-storage")
			session, run := spawnDriven(w, app, driver, worktree)
			driver.mustReport("session.report_metadata", map[string]any{"session_id": run.SessionID, "run_id": run.RunID, "seq": 1, "metadata": map[string]string{"native_id": "saved-conversation"}, "resume_session_id": "saved-conversation"})
			closeSession(t, cli, session, "finished for now")
			awaitClosed(app, session)
			if err := os.RemoveAll(worktree); err != nil {
				t.Fatal(err)
			}
			runGit(t, repo, "worktree", "prune")
			verdict := reopenVerdict(t, cli, session)
			if !slices.Contains(verdict.Actions, protocol.SessionReopenActionRecreateWorktreeAndReopen) {
				t.Fatalf("missing worktree offers %v", verdict.Actions)
			}
			done := make(chan error, 1)
			go func() {
				_, err := cli.SessionReopen(client.SessionReopenOptions{SessionID: protocol.SessionID(session), Action: string(protocol.SessionReopenActionRecreateWorktreeAndReopen)})
				done <- err
			}()
			var inspection resumeInspection
			request := driver.asked("driver.resume_available", &inspection)
			if len(inspection.Conversations) != 1 || inspection.Conversations[0].CWD != worktree {
				t.Fatalf("inspection = %+v", inspection)
			}
			if _, err := os.Stat(filepath.Join(inspection.Conversations[0].CWD, ".snipe", "settings.json")); err != nil {
				t.Fatalf("storage checked before project settings returned: %v", err)
			}
			answerResumeInspection(driver, request, inspection, func(string, string) bool { return available }, "saved conversation missing from configured storage")
			err := <-done
			if available {
				if err != nil {
					t.Fatal(err)
				}
				if resumed := driver.launched(); resumed.Method != "driver.resume" || resumed.ResumeSessionID != "saved-conversation" {
					t.Fatalf("recreated launch = %+v", resumed)
				}
			} else {
				if err == nil || !strings.Contains(err.Error(), "saved conversation missing") {
					t.Fatalf("missing conversation reopened: %v", err)
				}
				if info, err := os.Stat(worktree); err != nil || !info.IsDir() {
					t.Fatalf("refusal removed recreated worktree: %v", err)
				}
				if row := showSession(t, cli, session); protocol.Deref(row.ClosedAt) == "" {
					t.Fatal("refusal reopened ledger row")
				}
			}
		})
	}
}

type resumeInspection struct {
	Conversations []struct {
		Agent    string `json:"agent"`
		CWD      string `json:"cwd"`
		ResumeID string `json:"resume_session_id"`
	} `json:"conversations"`
}

func answerResumeInspection(driver *driverPeer, request json.RawMessage, check resumeInspection, available func(string, string) bool, reason string) {
	driver.t.Helper()
	answers := make([]map[string]any, 0, len(check.Conversations))
	for _, conversation := range check.Conversations {
		answers = append(answers, map[string]any{"agent": conversation.Agent, "cwd": conversation.CWD, "resume_session_id": conversation.ResumeID, "available": available(conversation.Agent, conversation.ResumeID), "reason": reason})
	}
	driver.answer(request, map[string]any{"availability": answers})
}
