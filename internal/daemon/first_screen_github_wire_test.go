package daemon_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

const signedInHosts = "ghe.example.test,github.example.test"

func useSignedInGH(t *testing.T) {
	t.Helper()
	bin := t.TempDir()
	script := `#!/bin/sh
case "$1 $2" in
  "--version "*) echo 'gh version 2.98.0 (2026-08-20)' ;;
  "auth status") echo '{"hosts":{"ghe.example.test":[{"state":"success","active":true,"login":"ada"}],"github.example.test":[{"state":"success","active":true,"login":"ada"}]}}' ;;
  "auth token") echo 'test-token' ;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ATTN_MOCK_GH_URL", "")
	t.Setenv("ATTN_GITHUB_POLLING", "on")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+ghWarningPath(t, ""))
}

func awaitBothGitHubHosts(app *testworld.Peer) []string {
	hosts := app.Initial.GithubHosts
	if len(hosts) < 2 {
		hosts = testworld.Await(app, protocol.EventGitHubHostsUpdated, func(m protocol.GitHubHostsUpdatedMessage) bool {
			return len(m.GithubHosts) >= 2
		}).GithubHosts
	}
	slices.Sort(hosts)
	return hosts
}

func TestTheFirstScreenCarriesTheWarningOfASlowGHCheck(t *testing.T) {
	bin := t.TempDir()
	script := "#!/bin/sh\nsleep 0.5\necho 'gh version 2.0.0 (2020-01-01)'\n"
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ATTN_MOCK_GH_URL", "")
	t.Setenv("ATTN_GITHUB_POLLING", "on")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+ghWarningPath(t, ""))
	warnings := newWorld(t).App().Initial.Warnings
	if !slices.ContainsFunc(warnings, func(w protocol.DaemonWarning) bool { return w.Code == "gh_version_too_old" }) {
		t.Fatalf("the first initial_state warns %+v, want gh_version_too_old", warnings)
	}
}

func TestTheFirstScreenDoesNotWaitForGitHubHostDiscovery(t *testing.T) {
	useSignedInGH(t)
	w := &world{World: prepareWorld(t)}
	discovery := w.PauseAt(pausepoint.DaemonGitHubHostDiscovery)
	w.start()
	app := w.App()
	discovery.Await()
	if len(app.Initial.GithubHosts) != 0 {
		t.Fatalf("the first screen, sent while discovery is held, names hosts %v", app.Initial.GithubHosts)
	}
	discovery.Release()
	if got := strings.Join(awaitBothGitHubHosts(app), ","); got != signedInHosts {
		t.Fatalf("after discovery the app is told of %q, want %q", got, signedInHosts)
	}
}

func TestAClientWhoseFirstScreenRacesHostDiscoveryEndsUpWithTheHosts(t *testing.T) {
	useSignedInGH(t)
	w := &world{World: prepareWorld(t)}
	discovery := w.PauseAt(pausepoint.DaemonGitHubHostDiscovery)
	built := w.PauseAt(pausepoint.DaemonInitialStateBuilt)
	w.start()
	app := w.ConnectApp()
	built.Await()
	discovery.Await()
	discovery.Release()
	found := func(m protocol.GitHubHostsUpdatedMessage) bool { return len(m.GithubHosts) == 2 }
	testworld.Await(app, protocol.EventGitHubHostsUpdated, found)
	mark := app.EventCount()
	built.Release()

	initial := testworld.AwaitSince[protocol.InitialStateMessage](app, mark, protocol.EventInitialState, nil)
	if len(initial.GithubHosts) != 0 {
		t.Fatalf("the paused initial_state names hosts %v, want the empty list it was built with", initial.GithubHosts)
	}
	testworld.AwaitSince(app, mark, protocol.EventGitHubHostsUpdated, found)
}

func TestAFirstScreenSentBetweenTwoHostRegistrationsIsFollowedByBothHosts(t *testing.T) {
	useSignedInGH(t)
	w := &world{World: prepareWorld(t)}
	oneRegistered := w.PauseAt(pausepoint.DaemonGitHubHostRegistered)
	w.start()
	oneRegistered.Await()
	app := w.App()
	if len(app.Initial.GithubHosts) != 1 {
		t.Fatalf("the first screen, sent with one host registered, names hosts %v", app.Initial.GithubHosts)
	}
	oneRegistered.Release()
	if got := strings.Join(awaitBothGitHubHosts(app), ","); got != signedInHosts {
		t.Fatalf("after discovery the app is told of %q, want %q", got, signedInHosts)
	}
}

func TestAPendingRunThatNeedsNoGitHubIsRecoveredBeforeAFirstScreenThatDoesNotWaitForDiscovery(t *testing.T) {
	w := &world{World: prepareWorld(t), terms: testworld.NewTerminals(t)}
	w.start()
	app, cli := w.App(), w.Client()
	if err := os.MkdirAll(w.Path("check"), 0o755); err != nil {
		t.Fatal(err)
	}
	applyAutomation(t, cli, strings.Replace(manualAutomation(w, "Check the code."), "driver: codex", "driver: claude", 1))
	awaitAutomationChanged(app, 1)
	launched := make(chan *testworld.Terminal, 1)
	w.terms.OnNextSpawn(func(term *testworld.Terminal) { launched <- term })
	finalizing := w.PauseAt(pausepoint.AutomationRunFinalizing)
	app.Send(protocol.AutomationRunMessage{Cmd: protocol.CmdAutomationRun, DefinitionID: 1, RequestID: "now"})
	finalizing.Await()
	agent := <-launched
	restore := w.RefuseDatabaseCommits()
	defer restore()
	finalizing.Release()
	testworld.Await(app, protocol.EventAutomationRunResult, automationAnswer[protocol.AutomationRunResultMessage]("now"))
	restore()
	agent.Exit(0)

	useSignedInGH(t)
	discovery := w.PauseAt(pausepoint.DaemonGitHubHostDiscovery)
	w.restart()
	discovery.Await()
	finalizing.Await()
	finalizing.Release()
	w.App()
	if runs := automationRuns(t, w.Client(), 1); runs[0].State != "delivered" {
		t.Fatalf("at the first screen, with discovery held, the run is %+v, want it delivered", runs[0])
	}
	discovery.Release()
}

func TestAPendingRunThatNeedsGitHubHoldsRecoveryUntilDiscoveryAndIsDelivered(t *testing.T) {
	t.Setenv("GIT_SSH_COMMAND", "false")
	r := newAutomationReviewWorld(t)
	applyAutomation(t, r.cli, automationReviewSpec("manual-review", "manual", automationReviewOverride(r.clone)))
	upstream := newRepo(t, "upstream")
	unfetched := commitFile(t, upstream, "later.go", "package later\n")
	if _, err := r.cli.AutomationRun(2, "held", automationReviewInput(46, unfetched)); err == nil {
		t.Fatal("a run whose head cannot be fetched started")
	}
	held := automationRuns(t, r.cli, 2)
	if len(held) != 1 || held[0].State != "pending" {
		t.Fatalf("runs = %+v, want the run held pending", held)
	}
	runGit(t, r.clone, "fetch", upstream, "main")

	useSignedInGH(t)
	discovery := r.w.PauseAt(pausepoint.DaemonGitHubHostDiscovery)
	waiting := r.w.PauseAt(pausepoint.AutomationRecoveryAwaitsGitHub)
	r.w.restart()
	discovery.Await()
	waiting.Await()
	app := r.w.ConnectApp()
	app.Send(protocol.TerminalCloseMessage{Cmd: protocol.CmdTerminalClose, CallerSessionID: "nobody", Target: "nothing"})
	refused := testworld.Await(app, protocol.EventCommandError, func(e protocol.CommandErrorMessage) bool {
		return protocol.Deref(e.Cmd) == protocol.CmdTerminalClose
	})
	if refused.Error != "daemon_recovering" {
		t.Fatalf("with discovery held a terminal command answers %q, want daemon_recovering", refused.Error)
	}
	if slices.ContainsFunc(app.Received(), func(e protocol.WebSocketEvent) bool { return e.Event == protocol.EventInitialState }) {
		t.Fatal("the first screen arrived before the run that needs GitHub was recovered")
	}

	waiting.Release()
	discovery.Release()
	testworld.Await[protocol.InitialStateMessage](app, protocol.EventInitialState, nil)
	if runs := automationRuns(t, r.w.Client(), 2); automationRunState(runs, held[0].ID) != "delivered" {
		t.Fatalf("at the first screen the run is %+v, want it delivered", runs)
	}
}
