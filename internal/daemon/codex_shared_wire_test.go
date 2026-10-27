package daemon_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptyworker"
	"github.com/victorarias/attn/internal/testworld"
)

func TestASharedCodexLaunchIsOneSessionThatWorksWaitsAndCountsItsUsage(t *testing.T) {
	for _, direct := range []bool{false, true} {
		t.Run(strconv.FormatBool(direct), func(t *testing.T) {
			w := newWorld(t, fakeagent.Codex)
			app := w.App()
			setSetting(t, app, "codex_shared_enabled", "true")
			if direct {
				setSetting(t, app, "direct_agent_launch_enabled", "true")
			}
			session := w.Spawn(app, fakeagent.Codex, w.Path("shop"))
			codex := w.Launched(session)
			if !slices.Contains(codex.Argv, "--remote") {
				t.Fatalf("the shared launch ran %q, want Codex connected to an app-server", codex.Argv)
			}

			app.TypeLine(session, "find the flaky checkout test")
			if got := codex.Prompted(); got != "find the flaky checkout test" {
				t.Fatalf("codex received %q", got)
			}
			testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
			question := "It races the tax lookup. Add a lock? <!-- attn:state=waiting_input -->"
			codex.Reply(question)
			testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateWaitingInput })
			if usage := awaitUsageTokens(app, session, claudeTokens(question)); usage.MeasurementIncomplete != nil {
				t.Errorf("usage = %+v, want it complete", usage)
			}

			var codexSessions []string
			for _, s := range w.App().Initial.Sessions {
				if s.Agent == protocol.SessionAgentCodex {
					codexSessions = append(codexSessions, string(s.ID))
				}
			}
			if !slices.Equal(codexSessions, []string{session}) {
				t.Errorf("Codex sessions = %v, want only %s", codexSessions, session)
			}
			if servers := w.CodexServers(); len(servers) != 1 {
				t.Errorf("app-servers launched = %v, want one", servers)
			}
		})
	}
}

func TestSharedCodexRunsInADataDirAtTheSocketPathLimit(t *testing.T) {
	w := newWorldInLongestDataDir(t, fakeagent.Codex)
	app := w.App()
	setSetting(t, app, "codex_shared_enabled", "true")
	session := w.Spawn(app, fakeagent.Codex, w.Path("shop"))
	codex := w.Launched(session)
	if !slices.Contains(codex.Argv, "--remote") {
		t.Fatalf("the shared launch ran %q, want Codex connected to an app-server", codex.Argv)
	}
	app.TypeLine(session, "find the flaky checkout test")
	if got := codex.Prompted(); got != "find the flaky checkout test" {
		t.Fatalf("codex received %q", got)
	}
	codex.Reply("Found it. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
}

func TestASharedCodexAppServerRunsCommandsWithTheDaemonsOpenFileLimitWhenLaunchedUnderALowerOne(t *testing.T) {
	if os.Getenv("ATTN_TEST_UNDER_LOW_FILE_LIMIT") == "" {
		self, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		lowered := exec.Command("/bin/sh", "-c", `ulimit -Sn 256 && exec "$@"`, "low-file-limit", self, "-test.v", "-test.run", "^"+t.Name()+"$")
		lowered.Env = append(os.Environ(), "ATTN_TEST_UNDER_LOW_FILE_LIMIT=1", "ATTN_E2E_BIN="+testworld.AttnBinary(t))
		if out, err := lowered.CombinedOutput(); err != nil {
			t.Fatalf("under a soft open-file limit of 256: %v\n%s", err, out)
		}
		return
	}
	var daemonFiles syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &daemonFiles); err != nil {
		t.Fatal(err)
	}
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	setSetting(t, app, "codex_shared_enabled", "true")
	codex := w.Launched(w.Spawn(app, fakeagent.Codex, w.Path("shop")))

	got := strings.TrimSpace(w.CodexServer().RunTool(codex.ConversationID, "ulimit -Sn"))
	if want := strconv.FormatUint(daemonFiles.Cur, 10); got != want {
		t.Errorf("a command the app-server ran had an open-file limit of %s, want the daemon's %s", got, want)
	}
}

func TestWithSharedCodexOffCodexRunsAloneInItsTerminal(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	session := w.Spawn(app, fakeagent.Codex, w.Path("shop"))
	codex := w.Launched(session)
	app.TypeLine(session, "find the flaky checkout test")
	codex.Prompted()
	codex.Reply("Found it. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	if slices.Contains(codex.Argv, "--remote") {
		t.Errorf("Codex ran %q, want it on its own", codex.Argv)
	}
	if servers := w.CodexServers(); len(servers) != 0 {
		t.Errorf("app-servers launched = %v, want none", servers)
	}
}

func TestASharedCodexAppServerThatExitsStartsAgainAndTheTerminalCarriesOn(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	setSetting(t, app, "codex_shared_enabled", "true")
	session := w.Spawn(app, fakeagent.Codex, w.Path("shop"))
	codex := w.Launched(session)
	conversation := codex.ConversationID

	codex.CrashAppServer()
	app.TypeLine(session, "add a discount field")
	if got := codex.Prompted(); got != "add a discount field" {
		t.Fatalf("codex received %q after its app-server exited", got)
	}
	if codex.ConversationID != conversation {
		t.Fatalf("the terminal shows conversation %s, want %s", codex.ConversationID, conversation)
	}
	codex.Reply("Added. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	if servers := w.CodexServers(); len(servers) != 2 {
		t.Errorf("app-servers launched = %v, want the first and the one that replaced it", servers)
	}
}

func TestAStoppedSharedCodexSessionResumesUnderTheApprovalsItLaunchedWith(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	setSetting(t, app, "codex_shared_enabled", "true")
	setSetting(t, app, "auto_approve_enabled", "true")
	unguarded := w.Spawn(app, fakeagent.Codex, w.Path("shop"), func(m *protocol.SpawnSessionMessage) { m.YoloMode = protocol.Ptr(true) })
	reviewed := w.Spawn(app, fakeagent.Codex, w.Path("shop"))

	for _, tc := range []struct {
		session   string
		approvals fakeagent.CodexPermissions
	}{
		{unguarded, fakeagent.CodexPermissions{ApprovalPolicy: "never", ApprovalsReviewer: "user", Sandbox: "danger-full-access"}},
		{reviewed, fakeagent.CodexPermissions{ApprovalPolicy: "on-request", ApprovalsReviewer: "auto_review", Sandbox: "workspace-write"}},
	} {
		codex := w.Launched(tc.session)
		app.TypeLine(tc.session, "find the flaky checkout test")
		codex.Prompted()
		codex.Reply("It races the tax lookup. <!-- attn:state=idle -->")
		testworld.AwaitSession(app, tc.session, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
		conversation := codex.ConversationID
		if got := w.CodexServer().Permissions(conversation); got != tc.approvals {
			t.Errorf("conversation %s started with %+v, want %+v", conversation, got, tc.approvals)
		}
		codex.Exit(143)
		testworld.Await(app, protocol.EventSessionExited, func(e protocol.SessionExitedMessage) bool { return string(e.SessionID) == tc.session })

		launchIntentReload(t, app, tc.session)
		resumed := w.Launched(tc.session)
		if !resumed.Resumed || resumed.ConversationID != conversation {
			t.Fatalf("resuming the stopped session ran %q showing %s (resumed=%v), want conversation %s", resumed.Argv, resumed.ConversationID, resumed.Resumed, conversation)
		}
		app.TypeLine(tc.session, "lock the tax lookup")
		if got := resumed.Prompted(); got != "lock the tax lookup" {
			t.Errorf("the resumed Codex received %q", got)
		}
		if got := w.CodexServer().Permissions(conversation); got != tc.approvals {
			t.Errorf("conversation %s resumed with %+v, want %+v", conversation, got, tc.approvals)
		}
	}
}

func TestAYoloSharedCodexConversationKeepsFullAccessWhenItsTerminalOrAttnResumesIt(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	setSetting(t, app, "codex_shared_enabled", "true")
	unguarded := fakeagent.CodexPermissions{ApprovalPolicy: "never", ApprovalsReviewer: "user", Sandbox: "danger-full-access"}
	checkout := w.Spawn(app, fakeagent.Codex, w.Path("shop"), func(m *protocol.SpawnSessionMessage) { m.YoloMode = protocol.Ptr(true) })
	codex := w.Launched(checkout)
	terminal := app.Terminal(checkout)
	app.TypeLine(checkout, "find the flaky checkout test")
	codex.Prompted()
	codex.Reply("It races the tax lookup. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, checkout, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	conversation := codex.ConversationID

	moveOn(t, app, codex, terminal, "/new", "add a discount field")
	codex.Reply("Added. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, checkout, func(s protocol.Session) bool { return protocol.Deref(s.Hidden) })
	if got := submitSessionAnnotationFeedback(app, checkout, sessionAnnotationFeedback); !got.success {
		t.Fatalf("feedback to the hidden session = %+v", got)
	}
	w.CodexServer().Prompted(conversation)
	if got := w.CodexServer().Permissions(conversation); got != unguarded {
		t.Errorf("attn reloaded hidden conversation %s with %+v, want %+v", conversation, got, unguarded)
	}
	w.CodexServer().Reply(conversation, "Fixed what the notes asked for. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, checkout, func(s protocol.Session) bool { return protocol.Deref(s.Hidden) && s.State == protocol.SessionStateIdle })

	moveOn(t, app, codex, terminal, "/resume "+conversation, "lock the tax lookup")
	if codex.ConversationID != conversation {
		t.Fatalf("after /resume %s the terminal took the prompt in %s", conversation, codex.ConversationID)
	}
	if got := w.CodexServer().Permissions(conversation); got != unguarded {
		t.Errorf("/resume %s runs it with %+v, want %+v", conversation, got, unguarded)
	}
}

func TestASharedCodexLaunchThatCannotBePlacedLetsItsAppServerStop(t *testing.T) {
	w := newWorld(t, fakeagent.Codex, fakeagent.Claude)
	app := w.App()
	setSetting(t, app, "codex_shared_enabled", "true")
	notes, desktop, anchor := w.RequestSpawn(app, fakeagent.Claude, w.Path("notes"))
	if !notes.Success {
		t.Fatalf("spawn the session beside which Codex launches: %s", protocol.Deref(notes.Error))
	}
	w.Launched(string(notes.ID))
	cwd := w.Path("shop")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	w.SetCodexFolderTrust(cwd, "trusted")
	shop := protocol.SpawnSessionMessage{
		Cmd: protocol.CmdSpawnSession, ID: protocol.SessionID(uuid.NewString()), Agent: string(fakeagent.Codex),
		Cwd: cwd, ProfileID: app.SelectedProfile(), Cols: 100, Rows: 30,
		Placement: &protocol.SessionPlacement{DesktopID: protocol.Ptr(desktop), AnchorPaneID: protocol.Ptr(anchor)},
	}

	boot := w.HoldNextBoot()
	app.Send(shop)
	w.AwaitHeldBoot()
	if closed := closeTileFromApp(w.ConnectApp(), desktop, anchor); !closed.Success {
		t.Fatalf("close the tile Codex launches beside: %s", protocol.Deref(closed.Error))
	}
	boot()
	if placed := testworld.Await(app, protocol.EventSpawnResult, func(r protocol.SpawnResultMessage) bool { return r.ID == shop.ID }); placed.Success {
		t.Fatalf("a shared Codex launch beside a tile that closed under it was placed")
	}
	w.CodexServer().AwaitStopped()
}

func TestASharedCodexLaunchWhoseTerminalCannotStartLetsItsAppServerStop(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	setSetting(t, app, "codex_shared_enabled", "true")
	id := protocol.SessionID(uuid.NewString())
	refused := testworld.Request(app, protocol.SpawnSessionMessage{
		Cmd: protocol.CmdSpawnSession, ID: id, Agent: string(fakeagent.Codex), Cwd: w.Path("missing"),
		ProfileID: app.SelectedProfile(), Placement: &protocol.SessionPlacement{}, Cols: 100, Rows: 30,
	}, protocol.EventSpawnResult, func(r protocol.SpawnResultMessage) bool { return r.ID == id })
	if refused.Success {
		t.Fatal("shared Codex started in a missing directory")
	}
	w.CodexServer().AwaitStopped()
}

func TestASharedCodexCrewMemberWakesWithItsAwarenessDirsAsWorkspaceRoots(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Codex)
	app := w.App()
	cli := w.Client()
	setSetting(t, app, "codex_shared_enabled", "true")
	workDir, notes := w.Path("keel-work"), w.Path("keel-notes")
	for _, dir := range []string{workDir, notes} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	w.SetCodexFolderTrust(workDir, "trusted")
	setCrew(t, cli, "keel", protocol.CrewSetMessage{Cwd: protocol.Ptr(workDir), Agent: protocol.Ptr("codex"), AwarenessDirs: []string{notes}})
	keel := string(wakeCrew(t, cli, "keel", "").SessionID)
	codex := w.Launched(keel)
	if !slices.Contains(codex.Argv, "--remote") {
		t.Fatalf("keel ran %q, want Codex connected to an app-server", codex.Argv)
	}
	if got := codex.Prompted(); !strings.Contains(got, "You have been woken") {
		t.Fatalf("keel's codex received %q, want its wake prompt", got)
	}
	if roots := w.CodexServer().WorkspaceRoots(codex.ConversationID); !slices.Equal(roots, []string{workDir, notes}) {
		t.Errorf("keel's conversation has workspace roots %q, want its cwd and awareness dir", roots)
	}
}

func newWorktree(t *testing.T, name, branch string) (repo, worktree string) {
	t.Helper()
	repo = newRepo(t, name)
	worktree = filepath.Join(filepath.Dir(repo), name+"--"+branch)
	runGit(t, repo, "worktree", "add", "-b", branch, worktree)
	return repo, worktree
}

func TestSharedCodexSkipsTheTrustQuestionOnlyInARepositoryWhereAnAgentSessionStarted(t *testing.T) {
	w := newWorld(t, fakeagent.Codex, fakeagent.Claude)
	app := w.App()
	setSetting(t, app, "codex_shared_enabled", "true")
	worked, workedWorktree := newWorktree(t, "shop", "discount")
	browsed, browsedWorktree := newWorktree(t, "warehouse", "audit")
	w.LeaveCodexFolderTrustUnanswered(workedWorktree)
	w.LeaveCodexFolderTrustUnanswered(browsedWorktree)

	w.Launched(w.Spawn(app, fakeagent.Claude, worked))
	workIn(t, w, app, browsed)

	w.Launched(w.Spawn(app, fakeagent.Codex, workedWorktree))
	if got := w.CodexFolderTrust(workedWorktree); got != "" {
		t.Errorf("Codex saves %s as %q, want attn to write nothing to the Codex config", workedWorktree, got)
	}
	app.AwaitScreen(w.Spawn(app, fakeagent.Codex, browsedWorktree), "Trust this folder?")
}

func TestASharedCodexReloadedAfterADaemonRestartSkipsTheTrustQuestionAttnAnsweredAtLaunch(t *testing.T) {
	t.Setenv("ATTN_PTY_BACKEND", "worker")
	t.Setenv("ATTN_PTY_WORKER_BINARY", testworld.AttnBinary(t))
	w := newWorld(t, fakeagent.Codex, fakeagent.Claude)
	t.Cleanup(func() { ptyworker.ReapDataDir(w.Dir) })
	app := w.App()
	setSetting(t, app, "codex_shared_enabled", "true")
	repo, worktree := newWorktree(t, "shop", "discount")
	w.LeaveCodexFolderTrustUnanswered(worktree)
	w.Launched(w.Spawn(app, fakeagent.Claude, repo))
	session := w.Spawn(app, fakeagent.Codex, worktree)
	w.Launched(session)

	w.restart()
	app = w.App()
	reloaded := testworld.Request(app, protocol.ReloadSessionMessage{Cmd: protocol.CmdReloadSession, ID: protocol.SessionID(session), Cols: 100, Rows: 30},
		protocol.EventReloadSessionResult, func(r protocol.ReloadSessionResultMessage) bool { return string(r.ID) == session })
	if !reloaded.Success {
		t.Fatalf("reload: %s", protocol.Deref(reloaded.Error))
	}
	w.Launched(session)
}

func TestWithSharedCodexOnAnAutomationRunsSharedCodexUnderItsLaunchContract(t *testing.T) {
	for _, saved := range []string{"", "untrusted"} {
		t.Run("saved trust "+strconv.Quote(saved), func(t *testing.T) {
			w := newWorld(t, fakeagent.Codex)
			app, cli := w.App(), w.Client()
			setSetting(t, app, "codex_shared_enabled", "true")
			folder := w.Path("check")
			if err := os.MkdirAll(folder, 0o755); err != nil {
				t.Fatal(err)
			}
			if saved != "" {
				w.SetCodexFolderTrust(folder, saved)
			}
			applyAutomation(t, cli, fmt.Sprintf(`api_version: attn.dev/automations/v1alpha1
name: Nightly check
trigger: {type: manual}
prompt: Report the flaky tests.
launch: {driver: codex, model: gpt-5.5, effort: high}
location: {type: directory, path: %q}
`, folder))
			awaitAutomationChanged(app, 1)
			run := testworld.Request(app, protocol.AutomationRunMessage{Cmd: protocol.CmdAutomationRun, DefinitionID: 1, RequestID: "now"},
				protocol.EventAutomationRunResult, automationAnswer[protocol.AutomationRunResultMessage]("now"))
			if !run.Success || protocol.Deref(run.Run.SessionID) == "" {
				t.Fatalf("automation_run = %+v, want a delivered run with a session", run)
			}
			codex := w.Launched(string(protocol.Deref(run.Run.SessionID)))
			for _, flag := range [][]string{{"--remote", ""}, {"--model", "gpt-5.5"}, {"-c", `model_reasoning_effort="high"`}} {
				if !slices.Contains(codex.Argv, flag[0]) || flag[1] != "" && !containsAutomationFlag(codex.Argv, flag[0], flag[1]) {
					t.Errorf("the automation ran %q, want shared Codex with %s %s", codex.Argv, flag[0], flag[1])
				}
			}
			if got := codex.Prompted(); !strings.Contains(got, "Report the flaky tests.") {
				t.Errorf("codex received %q, want the automation's prompt", got)
			}
			reviewed := fakeagent.CodexPermissions{ApprovalPolicy: "on-request", ApprovalsReviewer: "auto_review", Sandbox: "workspace-write"}
			if got := w.CodexServer().Permissions(codex.ConversationID); got != reviewed {
				t.Errorf("the automation's conversation runs with %+v, want %+v", got, reviewed)
			}
			if servers := w.CodexServers(); len(servers) != 1 {
				t.Errorf("app-servers launched = %v, want one", servers)
			}
			if got := w.CodexFolderTrust(folder); got != saved {
				t.Errorf("Codex saves %s as %q, want the user's %q", folder, got, saved)
			}
		})
	}
}

func TestWithSharedCodexOnAnAutomationThatNamesItsCodexRunsItPlain(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	setSetting(t, app, "codex_shared_enabled", "true")
	folder := w.Path("check")
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(w.Dir, "bin", string(fakeagent.Codex))
	applyAutomation(t, cli, fmt.Sprintf(`api_version: attn.dev/automations/v1alpha1
name: Nightly check
trigger: {type: manual}
prompt: Report the flaky tests.
launch: {driver: codex, executable: %q}
location: {type: directory, path: %q}
`, executable, folder))
	awaitAutomationChanged(app, 1)
	run := testworld.Request(app, protocol.AutomationRunMessage{Cmd: protocol.CmdAutomationRun, DefinitionID: 1, RequestID: "now"},
		protocol.EventAutomationRunResult, automationAnswer[protocol.AutomationRunResultMessage]("now"))
	if !run.Success || protocol.Deref(run.Run.SessionID) == "" {
		t.Fatalf("automation_run = %+v, want a delivered run with a session", run)
	}
	codex := w.Launched(string(protocol.Deref(run.Run.SessionID)))
	if codex.Argv[0] != executable || slices.Contains(codex.Argv, "--remote") {
		t.Errorf("the automation ran %q, want its own %s on its own", codex.Argv, executable)
	}
	if servers := w.CodexServers(); len(servers) != 0 {
		t.Errorf("app-servers launched = %v, want none", servers)
	}
}
