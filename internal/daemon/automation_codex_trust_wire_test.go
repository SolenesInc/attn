package daemon_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestACodexAutomationRefusesAFolderTheUserIsNotKnownToTrust(t *testing.T) {
	for _, c := range []struct {
		name                 string
		shared, otherProfile bool
	}{
		{name: "plain Codex in a repository an agent worked in from another profile", otherProfile: true},
		{name: "shared Codex in a repository nobody worked in", shared: true},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := newWorld(t, fakeagent.Codex, fakeagent.Claude)
			app := w.App()
			if c.shared {
				setSetting(t, app, "codex_shared_enabled", "true")
			}
			repo := newRepo(t, "shop")
			folder := filepath.Join(repo, "pkg2")
			if err := os.MkdirAll(folder, 0o755); err != nil {
				t.Fatal(err)
			}
			spec := strings.Replace(manualAutomation(w, "Check the code."), strconv.Quote(w.Path("check")), strconv.Quote(folder), 1)
			if c.otherProfile {
				w.Launched(w.Spawn(app, fakeagent.Claude, filepath.Join(repo, "pkg1")))
				side := createProfile(app, "Side")
				selectProfile(app, side.ID)
				applied := testworld.Request(app, protocol.AutomationApplyMessage{Cmd: protocol.CmdAutomationApply, RequestID: protocol.Ptr("side"), ProfileID: protocol.Ptr(side.ID), DefinitionYaml: spec}, protocol.EventAutomationApplyResult, automationAnswer[protocol.AutomationApplyResultMessage]("side"))
				if !applied.Success {
					t.Fatalf("apply in Side: %+v", applied)
				}
			} else {
				applyAutomation(t, w.Client(), spec)
			}
			awaitAutomationChanged(app, 1)

			answered := runAutomationFromApp(app, "refused")
			if answered.Success || !strings.Contains(protocol.Deref(answered.Error), folder) || !strings.Contains(protocol.Deref(answered.Error), "trust") {
				t.Fatalf("the run answered %s, want it refused with a reason naming %s and its trust", describeRunAnswer(answered), folder)
			}
			if answered.Run != nil && protocol.Deref(answered.Run.SessionID) != "" {
				t.Errorf("the refused run started session %s, want no Codex", protocol.Deref(answered.Run.SessionID))
			}
			for _, dir := range []string{folder, repo} {
				if got := w.CodexFolderTrust(dir); got != "" {
					t.Errorf("Codex saves %s as %q, want it left untrusted", dir, got)
				}
			}
			if servers := w.CodexServers(); len(servers) != 0 {
				t.Errorf("app-servers launched = %v, want none", servers)
			}
			requireTrustFix(t, app, "codex", repo)
		})
	}
}

func requireTrustFix(t *testing.T, app *testworld.Peer, agent, directory string) {
	t.Helper()
	runs := testworld.Request(app, protocol.AutomationRunsGetMessage{Cmd: protocol.CmdAutomationRunsGet, DefinitionID: 1, RequestID: protocol.Ptr("runs")}, protocol.EventAutomationRunsResult, automationAnswer[protocol.AutomationRunsResultMessage]("runs"))
	if len(runs.Runs) != 1 || runs.Runs[0].Fix == nil || runs.Runs[0].Fix.Agent != agent || runs.Runs[0].Fix.Directory != directory {
		t.Fatalf("the failed run answered %+v, want a fix opening %s at %s", runs.Runs, agent, directory)
	}
}

func TestAPlainCodexAutomationStartsInsideARepositoryTheUserTrusts(t *testing.T) {
	w := newWorld(t, fakeagent.Codex, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	repo, worktree := newWorktree(t, "shop", "nightly")
	w.Launched(w.Spawn(app, fakeagent.Claude, repo))
	applyAutomation(t, cli, manualCodexAutomationIn(worktree))
	awaitAutomationChanged(app, 1)

	answered := runAutomationFromApp(app, "trusted")
	if !answered.Success || answered.Run == nil || protocol.Deref(answered.Run.SessionID) == "" {
		t.Fatalf("the run answered %s, want it started", describeRunAnswer(answered))
	}
	if got := w.CodexFolderTrust(repo); got != "trusted" {
		t.Errorf("Codex saves the repository %s as %q, want trusted before Codex starts", repo, got)
	}
	if got := w.CodexFolderTrust(worktree); got != "" {
		t.Errorf("Codex saves %s as %q, want the trust on its repository alone", worktree, got)
	}
	if got := w.Launched(string(protocol.Deref(answered.Run.SessionID))).Prompted(); !strings.Contains(got, "Report the flaky tests.") {
		t.Errorf("codex received %q, want the automation's prompt", got)
	}
}

func TestAPlainCodexAutomationStartsInARepositoryAPluginAgentStartedAConversationIn(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	driver := connectDriver(t, w, "snipe-plugin", "snipe", map[string]bool{"state_reporting": true})
	awaitDriverAvailable(app, "snipe")
	repo := newRepo(t, "shop")
	session, run := spawnDriven(w, app, driver, repo)
	if err := driver.report("session.report_transcript_path", map[string]any{"session_id": run.SessionID, "run_id": run.RunID, "path": w.Path("snipe-session.jsonl")}); err != nil {
		t.Fatalf("the plugin's transcript report was refused: %v", err)
	}
	testworld.Await(app, protocol.EventSessionStateChanged, func(e protocol.WebSocketEvent) bool {
		return e.Session != nil && string(e.Session.ID) == session && protocol.Deref(e.Session.ConversationStarted)
	})
	applyAutomation(t, cli, manualCodexAutomationIn(repo))
	awaitAutomationChanged(app, 1)

	answered := runAutomationFromApp(app, "trusted")
	if !answered.Success || answered.Run == nil || protocol.Deref(answered.Run.SessionID) == "" {
		t.Fatalf("the run answered %s, want it started", describeRunAnswer(answered))
	}
	if got := w.CodexFolderTrust(repo); got != "trusted" {
		t.Errorf("Codex saves the repository %s as %q, want trusted before Codex starts", repo, got)
	}
}

func manualCodexAutomationIn(folder string) string {
	return fmt.Sprintf(`api_version: attn.dev/automations/v1alpha1
name: Nightly check
trigger: {type: manual}
prompt: Report the flaky tests.
launch: {driver: codex}
location: {type: directory, path: %q}
`, folder)
}
