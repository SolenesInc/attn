package daemon_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAttachingWithReviveRelaunchesARecoverableSession(t *testing.T) {
	for _, tc := range []struct {
		name     string
		agent    fakeagent.Harness
		withTask bool
		start    func(t *testing.T, w *world, app *testworld.Peer) string
		want     func(t *testing.T, w *world, argv []string)
	}{
		{
			name:  "claude keeps its executable, model, effort and skipped permissions",
			agent: fakeagent.Claude,
			start: func(t *testing.T, w *world, app *testworld.Peer) string {
				executable := attachRevivePinnedClaude(t, w)
				return w.Spawn(app, fakeagent.Claude, w.Path("shop"), func(m *protocol.SpawnSessionMessage) {
					m.YoloMode = protocol.Ptr(true)
					m.Executable = protocol.Ptr(executable)
					m.Model = protocol.Ptr("claude-sonnet-5")
					m.Effort = protocol.Ptr("high")
				})
			},
			want: func(t *testing.T, w *world, argv []string) {
				model, _ := flagValue(argv, "--model")
				effort, _ := flagValue(argv, "--effort")
				if argv[0] != w.Path("pinned-bin", "claude") || !slices.Contains(argv, "--dangerously-skip-permissions") || model != "claude-sonnet-5" || effort != "high" {
					t.Errorf("revived claude ran as %q, want the pinned executable skipping permissions with model claude-sonnet-5 and effort high", argv)
				}
			},
		},
		{
			name:  "codex keeps its model and effort",
			agent: fakeagent.Codex,
			start: func(t *testing.T, w *world, app *testworld.Peer) string {
				return w.Spawn(app, fakeagent.Codex, w.Path("api"), func(m *protocol.SpawnSessionMessage) {
					m.Model = protocol.Ptr("gpt-5")
					m.Effort = protocol.Ptr("high")
				})
			},
			want: func(t *testing.T, w *world, argv []string) {
				if !slices.Contains(argv, "gpt-5") || !slices.Contains(argv, `model_reasoning_effort="high"`) {
					t.Errorf("revived codex ran as %q, want model gpt-5 and effort high", argv)
				}
			},
		},
		{
			name:  "claude keeps the reviewer route after auto-approve is turned off",
			agent: fakeagent.Claude,
			start: func(t *testing.T, w *world, app *testworld.Peer) string {
				setSetting(t, app, "auto_approve_enabled", "true")
				session := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
				setSetting(t, app, "auto_approve_enabled", "false")
				return session
			},
			want: func(t *testing.T, w *world, argv []string) {
				if mode, _ := flagValue(argv, "--permission-mode"); mode != "auto" || slices.Contains(argv, "--dangerously-skip-permissions") {
					t.Errorf("revived claude ran as %q, want the reviewer's auto permission mode", argv)
				}
			},
		},
		{
			name:     "an automation's agent keeps its unattended contract and nothing else",
			agent:    fakeagent.Claude,
			withTask: true,
			start: func(t *testing.T, w *world, app *testworld.Peer) string {
				return attachReviveAutomationSession(t, w, app)
			},
			want: func(t *testing.T, w *world, argv []string) {
				mode, _ := flagValue(argv, "--permission-mode")
				model, _ := flagValue(argv, "--model")
				effort, _ := flagValue(argv, "--effort")
				if mode != "auto" || model != "sonnet" || effort != "high" || slices.Contains(argv, "--dangerously-skip-permissions") {
					t.Errorf("revived automation agent ran as %q, want its contract: model sonnet, effort high, auto permission mode", argv)
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newWorld(t, tc.agent)
			app := w.App()
			session := tc.start(t, w, app)
			first := w.Launched(session)
			tc.want(t, w, first.Argv)
			app = attachReviveMakeRecoverable(t, w, app, session, first, tc.withTask)

			revived := attachRevive(app, session, 101, 37)
			if !revived.Success || !protocol.Deref(revived.Revived) || protocol.Deref(revived.Cols) != 101 || protocol.Deref(revived.Rows) != 37 {
				t.Fatalf("attach with revive answered %+v, want the session revived at 101x37", revived)
			}
			relaunched := w.Launched(session)
			if !relaunched.Resumed || relaunched.ConversationID != first.ConversationID {
				t.Errorf("the revive ran %s %q, want it resuming conversation %s", tc.agent, relaunched.Argv, first.ConversationID)
			}
			tc.want(t, w, relaunched.Argv)
		})
	}
}

func TestAttachRefusesToReviveWhatItShouldNot(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	recoverable := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	app = attachReviveMakeRecoverable(t, w, app, recoverable, w.Launched(recoverable), false)
	exited := w.Spawn(app, fakeagent.Claude, w.Path("blog"))
	w.Launched(exited).Exit(0)
	testworld.Await(app, protocol.EventSessionExited, func(e protocol.SessionExitedMessage) bool { return e.ID == exited })

	for _, tc := range []struct {
		name    string
		attach  protocol.AttachSessionMessage
		refusal string
	}{
		{
			name:    "without the revive policy",
			attach:  protocol.AttachSessionMessage{Cmd: protocol.CmdAttachSession, ID: recoverable},
			refusal: "session not found",
		},
		{
			name: "without geometry",
			attach: protocol.AttachSessionMessage{Cmd: protocol.CmdAttachSession, ID: recoverable,
				AttachPolicy: protocol.Ptr(protocol.AttachPolicyRevive), Cols: protocol.Ptr(0), Rows: protocol.Ptr(24)},
			refusal: "revive requires pty geometry",
		},
		{
			name: "a session that is not recoverable",
			attach: protocol.AttachSessionMessage{Cmd: protocol.CmdAttachSession, ID: exited,
				AttachPolicy: protocol.Ptr(protocol.AttachPolicyRevive), Cols: protocol.Ptr(80), Rows: protocol.Ptr(24)},
			refusal: "session not recoverable",
		},
	} {
		result := testworld.Request(app, tc.attach, protocol.EventAttachResult,
			func(r protocol.AttachResultMessage) bool { return r.ID == tc.attach.ID })
		if result.Success || !strings.Contains(protocol.Deref(result.Error), tc.refusal) {
			t.Errorf("attach %s answered %+v, want a refusal saying %q", tc.name, result, tc.refusal)
		}
	}
	if state := queriedSession(t, w.Client(), recoverable).State; state != protocol.SessionStateRecoverable {
		t.Errorf("after the refused attaches the session is %s, want it still recoverable and not relaunched", state)
	}
}

func TestAFailedReviveLeavesTheSessionRecoverableAndTheConnectionResponsive(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	cwd := w.Path("shop")
	session := w.Spawn(app, fakeagent.Claude, cwd)
	run := w.Launched(session)
	app.TypeLine(session, "deploy it")
	run.Prompted()
	run.Reply("Error: the registry refused the push <!-- attn:state=idle -->")
	run.Exit(1)
	testworld.Await(app, protocol.EventSessionExited, func(e protocol.SessionExitedMessage) bool { return e.ID == session })
	w.restart()
	app = w.App()
	cli := w.Client()
	if state := queriedSession(t, cli, session).State; state != protocol.SessionStateRecoverable {
		t.Fatalf("after the restart the session is %s, want recoverable", state)
	}
	before, _ := peekExit(t, cli, session, "the registry refused the push")
	if err := os.RemoveAll(cwd); err != nil {
		t.Fatal(err)
	}
	browsed := w.Path("browse")
	if err := os.MkdirAll(filepath.Join(browsed, "child"), 0o755); err != nil {
		t.Fatal(err)
	}

	app.Send(protocol.AttachSessionMessage{Cmd: protocol.CmdAttachSession, ID: session,
		AttachPolicy: protocol.Ptr(protocol.AttachPolicyRevive), Cols: protocol.Ptr(80), Rows: protocol.Ptr(24)})
	listing := browseForPicker(app, browsed+string(os.PathSeparator), nil)
	failed := testworld.Await(app, protocol.EventAttachResult, func(r protocol.AttachResultMessage) bool { return r.ID == session })

	if failed.Success || !strings.Contains(protocol.Deref(failed.Error), cwd) {
		t.Errorf("reviving in a removed directory answered %+v, want a failure naming %s", failed, cwd)
	}
	if !listing.Success || !slices.Equal(pickerEntryNames(listing.Entries), []string{"child"}) {
		t.Errorf("the browse sent after the failed revive answered %+v, want the child directory", listing)
	}
	if state := queriedSession(t, cli, session).State; state != protocol.SessionStateRecoverable {
		t.Errorf("after the failed revive the session is %s, want it recoverable", state)
	}
	if after, _ := peekExit(t, cli, session, "the registry refused the push"); after != before {
		t.Errorf("after the failed revive the last exit is %+v, want %+v kept", after, before)
	}
}

func attachReviveMakeRecoverable(t *testing.T, w *world, app *testworld.Peer, session string, run *fakeagent.Run, launchedWithTask bool) *testworld.Peer {
	t.Helper()
	if !launchedWithTask {
		app.TypeLine(session, "add a discount field to checkout")
	}
	run.Prompted()
	run.Reply("Done. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	w.restart()
	app = w.App()
	for _, s := range app.Initial.Sessions {
		if s.ID == session && s.State == protocol.SessionStateRecoverable {
			return app
		}
	}
	t.Fatalf("after the restart session %s is not recoverable: %+v", session, app.Initial.Sessions)
	return nil
}

func attachRevive(app *testworld.Peer, session string, cols, rows int) protocol.AttachResultMessage {
	app.T.Helper()
	return testworld.Request(app, protocol.AttachSessionMessage{Cmd: protocol.CmdAttachSession, ID: session,
		AttachPolicy: protocol.Ptr(protocol.AttachPolicyRevive), Cols: protocol.Ptr(cols), Rows: protocol.Ptr(rows)},
		protocol.EventAttachResult, func(r protocol.AttachResultMessage) bool { return r.ID == session })
}

func attachRevivePinnedClaude(t *testing.T, w *world) string {
	t.Helper()
	installed, err := exec.LookPath("claude")
	if err != nil {
		t.Fatalf("find the installed claude: %v", err)
	}
	if err := os.MkdirAll(w.Path(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Dir(installed), w.Path("pinned-bin")); err != nil {
		t.Fatal(err)
	}
	return w.Path("pinned-bin", "claude")
}

func attachReviveAutomationSession(t *testing.T, w *world, app *testworld.Peer) string {
	t.Helper()
	if err := os.MkdirAll(w.Path("check"), 0o755); err != nil {
		t.Fatal(err)
	}
	applyAutomation(t, w.Client(), fmt.Sprintf(`api_version: attn.dev/automations/v1alpha1
id: nightly
name: Nightly check
trigger: {type: manual}
prompt: Check the build.
launch: {driver: claude, model: sonnet, effort: high}
location: {type: directory, path: %q}
`, w.Path("check")))
	awaitAutomationChanged(app, "nightly")
	run := testworld.Request(app, protocol.AutomationRunMessage{Cmd: protocol.CmdAutomationRun, DefinitionID: "nightly", RequestID: "tonight"},
		protocol.EventAutomationRunResult, automationAnswer[protocol.AutomationRunResultMessage]("tonight"))
	if !run.Success || protocol.Deref(run.Run.SessionID) == "" {
		t.Fatalf("automation_run = %+v, want a delivered run with its session", run)
	}
	return protocol.Deref(run.Run.SessionID)
}
