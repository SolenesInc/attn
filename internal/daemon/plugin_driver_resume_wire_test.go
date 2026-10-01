package daemon_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/garden"
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
		var check resumeInspection
		request := driver.asked("driver.resume_available", &check)
		answerResumeInspection(driver, request, check, func(string, string) bool { return true }, "")
		<-shown
	})
}

func TestAGardenReviewWaitsForIndependentDriversTogether(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		drivers := make([]*driverPeer, 0, 2)
		for _, agent := range []string{"snipe", "wren"} {
			driver := connectDriver(t, w, agent+"-plugin", agent, map[string]bool{
				"resume": true, "resume_availability": true, "initial_prompt": true, "state_reporting": true,
			})
			drivers = append(drivers, driver)
			awaitDriverAvailable(app, agent)
			delegations := make(chan *protocol.DelegateResult, 1)
			go func() { delegations <- seedResumeDelegate(t, w, fakeagent.Harness(agent), agent) }()
			run := driver.launched()
			if err := driver.state(run, 1, "working"); err != nil {
				t.Fatal(err)
			}
			delegated := <-delegations
			driver.mustReport("session.report_metadata", map[string]any{
				"session_id": delegated.SessionID, "run_id": run.RunID, "seq": 2,
				"resume_session_id": agent + "-conversation", "metadata": map[string]string{"native_id": agent + "-conversation"},
			})
			closePane(app, seedResumePane(t, w, protocol.Deref(delegated.WorkspaceID), delegated.SessionID))
		}
		shown := make(chan *protocol.SeedReviewResult, 1)
		go func() { shown <- gardenReviewShow(t, cli, "") }()
		started := time.Now()
		for _, driver := range drivers {
			driver.asked("driver.resume_available", nil)
		}
		if elapsed := time.Since(started); elapsed != 0 {
			t.Fatalf("independent calls began %s apart", elapsed)
		}
		w.advance(10 * time.Second)
		select {
		case <-shown:
		default:
			t.Fatal("review did not finish after one deadline for independent stalled drivers")
		}
	})
}

func TestAGardenReviewJoinsResumeInspectionWhenItsAdvisorReturnsDuringShutdown(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	var now atomic.Int64
	now.Store(time.Now().UnixNano())
	w.gardenClock = func() time.Time { return time.Unix(0, now.Load()) }
	w.restart()
	app, cli := w.App(), w.Client()
	driver := connectDriver(t, w, "snipe-plugin", "snipe", map[string]bool{
		"resume": true, "resume_availability": true, "initial_prompt": true, "state_reporting": true,
	})
	awaitDriverAvailable(app, "snipe")
	delegations := make(chan *protocol.DelegateResult, 1)
	go func() { delegations <- seedResumeDelegate(t, w, scriptedAgent, "shutdown-inspection") }()
	run := driver.launched()
	if err := driver.state(run, 1, "working"); err != nil {
		t.Fatal(err)
	}
	delegated := <-delegations
	driver.mustReport("session.report_metadata", map[string]any{
		"session_id": delegated.SessionID, "run_id": run.RunID, "seq": 2,
		"resume_session_id": "saved-conversation", "metadata": map[string]string{"native_id": "saved-conversation"},
	})
	closePane(app, seedResumePane(t, w, protocol.Deref(delegated.WorkspaceID), delegated.SessionID))
	now.Store(time.Now().Add(garden.DefaultStaleWindow).UnixNano())
	t.Setenv("ATTN_HEADLESS_TASKS", "on")
	reviews := make(chan protocol.GardenReview, 1)
	go func() { reviews <- gardenReviewStart(t, cli) }()
	answerInspection := func() {
		t.Helper()
		var inspection resumeInspection
		request := driver.asked("driver.resume_available", &inspection)
		answerResumeInspection(driver, request, inspection, func(string, string) bool { return true }, "")
	}
	answerInspection()
	review := <-reviews
	if len(review.Items) != 1 || review.Items[0].SeedID != delegated.SeedID {
		t.Fatalf("review items = %+v, want the stopped seed", review.Items)
	}
	answerInspection()
	advice := w.HeadlessTask()

	// An admitted CLI inspection holds shutdown before it reaches the job runner.
	shown := make(chan error, 1)
	go func() {
		_, err := cli.SeedShow("", delegated.SeedID)
		shown <- err
	}()
	var heldInspection resumeInspection
	heldRequest := driver.asked("driver.resume_available", &heldInspection)
	ctx, cancel := context.WithTimeout(context.Background(), fakeagent.HangGuard)
	defer cancel()
	silent := transportDial(t, ctx, w)
	stopped := make(chan error, 1)
	go func() { stopped <- w.daemon.Stop() }()
	if _, _, err := silent.Read(ctx); err == nil || ctx.Err() != nil {
		t.Fatalf("shutdown did not close its listener connections: %v", err)
	}

	advice.Answer(`{"recommendation":"keep_growing","explanation":"Continue the work.","evidence":["Saved conversation."]}`)
	answerInspection()
	answerResumeInspection(driver, heldRequest, heldInspection, func(string, string) bool { return true }, "")
	if err := <-shown; err != nil {
		t.Fatal(err)
	}
	if err := <-stopped; err != nil {
		t.Fatal(err)
	}
	w.daemon = nil
}

func TestAGardenReviewInspectsSharedPluginStorageOncePerCapture(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		var now atomic.Int64
		now.Store(time.Now().UnixNano())
		w.gardenClock = func() time.Time { return time.Unix(0, now.Load()) }
		w.restart()
		app, cli := w.App(), w.Client()
		seeds := make(map[string]string)
		driver := connectDriver(t, w, "snipe-plugin", "snipe", map[string]bool{
			"resume": true, "resume_availability": true, "initial_prompt": true, "state_reporting": true,
		})
		awaitDriverAvailable(app, "snipe")
		driver.register("wren", map[string]bool{"resume": true, "resume_availability": true, "initial_prompt": true, "state_reporting": true})
		awaitDriverAvailable(app, "wren")
		for _, name := range []string{"snipe", "wren"} {
			delegations := make(chan *protocol.DelegateResult, 1)
			go func() { delegations <- seedResumeDelegate(t, w, fakeagent.Harness(name), "shared-storage") }()
			run := driver.launched()
			if err := driver.state(run, 1, "working"); err != nil {
				t.Fatal(err)
			}
			delegated := <-delegations
			seeds[name] = delegated.SeedID
			driver.mustReport("session.report_metadata", map[string]any{
				"session_id": delegated.SessionID, "run_id": run.RunID, "seq": 2,
				"metadata": map[string]string{"native_id": "shared-id"}, "resume_session_id": "shared-id",
			})
			closePane(app, seedResumePane(t, w, protocol.Deref(delegated.WorkspaceID), delegated.SessionID))
		}
		shown := make(chan *protocol.SeedReviewResult, 1)
		for _, firstAvailable := range []bool{true, false} {
			go func() { shown <- gardenReviewShow(t, cli, "") }()
			var check resumeInspection
			request := driver.asked("driver.resume_available", &check)
			ids := make([]string, 0, len(check.Conversations))
			for _, conversation := range check.Conversations {
				if conversation.CWD != w.Path("shared-storage") || conversation.ResumeID != "shared-id" {
					t.Fatalf("batch cwd = %q", conversation.CWD)
				}
				ids = append(ids, conversation.Agent)
			}
			slices.Sort(ids)
			if !slices.Equal(ids, []string{"snipe", "wren"}) {
				t.Fatalf("batch IDs = %v", ids)
			}
			answerResumeInspection(driver, request, check, func(agent string, _ string) bool { return (agent == "snipe") == firstAvailable }, "")
			<-shown
			select {
			case extra := <-driver.requests:
				t.Fatalf("shared storage inspected again in one capture: %s", extra.Method)
			default:
			}
		}
		now.Store(time.Now().Add(garden.DefaultStaleWindow).UnixNano())
		reviews := make(chan protocol.GardenReview, 1)
		go func() { reviews <- gardenReviewStart(t, cli) }()
		var check resumeInspection
		request := driver.asked("driver.resume_available", &check)
		answerResumeInspection(driver, request, check, func(agent string, _ string) bool { return agent == "snipe" }, "")
		review := <-reviews
		for name, seedID := range seeds {
			item := gardenReviewItem(t, &review, seedID)
			if resumable := slices.Contains(item.Actions, "resume"); resumable != (name == "snipe") {
				t.Fatalf("review actions for %s = %v", name, item.Actions)
			}
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
			var inspection resumeInspection
			request := driver.asked("driver.resume_available", &inspection)
			if len(inspection.Conversations) != 1 || inspection.Conversations[0].CWD != worktree {
				t.Fatalf("inspection = %+v", inspection)
			}
			if _, err := os.Stat(filepath.Join(inspection.Conversations[0].CWD, ".pi", "settings.json")); err != nil {
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
