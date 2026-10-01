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

func exitDriven(app *testworld.Peer, driver *driverPeer, session string) {
	app.T.Helper()
	app.Send(protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: session, Data: "\x04"})
	if closed := driver.closed(); closed.SessionID != session || closed.Reason != "exited" {
		app.T.Fatalf("the driver was told %+v, want %s to have exited", closed, session)
	}
	testworld.Await(app, protocol.EventSessionExited, func(e protocol.SessionExitedMessage) bool { return e.ID == session })
}

func relaunchDriven(w *world, driver *driverPeer, session, cwd string) driverLaunch {
	w.T.Helper()
	w.Spawn(w.App(), fakeagent.Harness(driver.agent), cwd, func(m *protocol.SpawnSessionMessage) { m.ID = session })
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
			"session_id": session, "run_id": named.RunID, "seq": seq,
			"metadata": map[string]string{"snipe_session_id": native}, "resume_session_id": native,
		})
	}
	metadata(1, "native-id")
	metadata(1, "older")
	exitDriven(app, driver, session)

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
		"session_id": session, "run_id": run.RunID, "seq": 1,
		"metadata": map[string]string{"snipe_session_id": "native-id"}, "resume_session_id": "native-id",
	})
	exitDriven(app, driver, session)

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
			"session_id": delegated.SessionID, "run_id": run.RunID, "seq": 2,
			"metadata": map[string]string{"native_id": "saved-conversation"}, "resume_session_id": "saved-conversation",
		})
		closePane(app, seedResumePane(t, w, protocol.Deref(delegated.WorkspaceID), delegated.SessionID))

		shown := make(chan *protocol.SeedShowResult, 1)
		go func() { shown <- lifeShow(t, cli, delegated.SeedID) }()
		var check struct {
			CWD      string `json:"cwd"`
			ResumeID string `json:"resume_session_id"`
		}
		driver.asked("driver.resume_available", &check)
		if check.CWD != delegated.Directory || check.ResumeID != "saved-conversation" {
			t.Fatalf("inspection = %+v", check)
		}
		w.advance(10 * time.Second)
		result := <-shown
		if continuation := result.Seed.Continuation; continuation == nil || continuation.ResumeAvailable || !strings.Contains(protocol.Deref(continuation.ResumeReason), "resume_availability_timeout=10s") {
			t.Fatalf("stalled inspection = %+v, want the named timeout", continuation)
		}

		go func() { shown <- lifeShow(t, cli, delegated.SeedID) }()
		request := driver.asked("driver.resume_available", &check)
		driver.answer(request, map[string]any{"available": true})
		if result := <-shown; result.Seed.Continuation == nil || !result.Seed.Continuation.ResumeAvailable {
			t.Fatalf("next read after the driver recovered = %+v", result.Seed.Continuation)
		}
	})
}

func TestAGardenReviewWaitsForAStalledResumeDriverOnlyOncePerCapture(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		driver := connectDriver(t, w, "snipe-plugin", "snipe", map[string]bool{
			"resume": true, "resume_availability": true, "initial_prompt": true, "state_reporting": true,
		})
		awaitDriverAvailable(app, "snipe")
		for _, name := range []string{"first", "second"} {
			delegations := make(chan *protocol.DelegateResult, 1)
			go func() { delegations <- seedResumeDelegate(t, w, scriptedAgent, name) }()
			run := driver.launched()
			if err := driver.state(run, 1, "working"); err != nil {
				t.Fatal(err)
			}
			delegated := <-delegations
			driver.mustReport("session.report_metadata", map[string]any{
				"session_id": delegated.SessionID, "run_id": run.RunID, "seq": 2,
				"metadata": map[string]string{"native_id": name}, "resume_session_id": name,
			})
			closePane(app, seedResumePane(t, w, protocol.Deref(delegated.WorkspaceID), delegated.SessionID))
		}
		shown := make(chan *protocol.SeedReviewResult, 1)
		go func() { shown <- gardenReviewShow(t, cli, "") }()
		driver.asked("driver.resume_available", nil)
		w.advance(10 * time.Second)
		select {
		case <-shown:
		default:
			t.Fatal("review did not finish after one stalled-driver timeout")
		}
		select {
		case extra := <-driver.requests:
			t.Fatalf("review queried stalled driver again: %s", extra.Method)
		default:
		}

		go func() { shown <- gardenReviewShow(t, cli, "") }()
		for range 2 {
			request := driver.asked("driver.resume_available", nil)
			driver.answer(request, map[string]any{"available": true})
		}
		<-shown
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
			settings := filepath.Join(repo, ".pi", "settings.json")
			if err := os.MkdirAll(filepath.Dir(settings), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(settings, []byte(`{"sessionDir":"/external/pi-sessions"}`), 0o644); err != nil {
				t.Fatal(err)
			}
			runGit(t, repo, "add", ".pi/settings.json")
			runGit(t, repo, "commit", "-m", "Configure external Pi storage")
			worktree := reopenWorktree(t, repo, "feat/project-storage")
			session, run := spawnDriven(w, app, driver, worktree)
			driver.mustReport("session.report_metadata", map[string]any{"session_id": session, "run_id": run.RunID, "seq": 1, "metadata": map[string]string{"native_id": "saved-conversation"}, "resume_session_id": "saved-conversation"})
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
				_, err := cli.SessionReopen(client.SessionReopenOptions{SessionID: session, Action: string(protocol.SessionReopenActionRecreateWorktreeAndReopen)})
				done <- err
			}()
			var inspection struct {
				CWD string `json:"cwd"`
			}
			request := driver.asked("driver.resume_available", &inspection)
			if inspection.CWD != worktree {
				t.Fatalf("inspection cwd = %q, want %q", inspection.CWD, worktree)
			}
			if _, err := os.Stat(filepath.Join(inspection.CWD, ".pi", "settings.json")); err != nil {
				t.Fatalf("storage checked before project settings returned: %v", err)
			}
			driver.answer(request, map[string]any{"available": available, "reason": "saved conversation missing from configured storage"})
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
				if _, err := os.Stat(worktree); !os.IsNotExist(err) {
					t.Fatalf("refusal left recreated worktree: %v", err)
				}
				if row := showSession(t, cli, session); protocol.Deref(row.ClosedAt) == "" {
					t.Fatal("refusal reopened ledger row")
				}
			}
		})
	}
}
