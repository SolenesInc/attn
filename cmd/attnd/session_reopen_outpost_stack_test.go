package main_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/victorarias/attn/internal/bridge"
	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestLedgerResumesAnOutpostAgentOnItsMachineWithItsConversationAndCost(t *testing.T) {
	for _, scenario := range []struct {
		name    string
		harness fakeagent.Harness
		shared  bool
	}{
		{"claude", fakeagent.Claude, false}, {"codex", fakeagent.Codex, false}, {"shared codex", fakeagent.Codex, true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			harness := scenario.harness
			home := testworld.NewStack(t)
			outpost := testworld.NewStack(t, testworld.WithAgents(harness))
			pair := testworld.PrepareOutpostPair(t, home, outpost)
			pair.Start()
			awaitNotebookState(t, pair, protocol.OutpostNotebookStateInSync)
			if scenario.shared {
				shareOutpostCodex(t, pair)
			}
			cwd := outpost.Path("agent")
			outpost.SetCodexFolderTrust(cwd, "trusted")
			id := home.Spawn(pair.App, harness, cwd, func(m *protocol.SpawnSessionMessage) { m.Host = &pair.Host })
			first := outpost.LaunchedCarrying(pair.App.Terminal(id))
			pair.App.TypeLine(id, "first work")
			first.Prompted()
			conversation := first.ConversationID
			reply := "First work done. <!-- attn:state=waiting_input -->"
			first.Reply(reply)
			testworld.AwaitSession(pair.App, id, func(s protocol.Session) bool { return s.Usage != nil && s.Usage.TotalTokens == 2*len(reply) })
			if err := home.Client().Unregister(protocol.SessionID(id)); err != nil {
				t.Fatal(err)
			}
			testworld.Await(pair.App, protocol.EventSessionClosed, func(e protocol.WebSocketEvent) bool {
				return e.SessionLedgerEntry != nil && string(e.SessionLedgerEntry.ID) == id
			})
			if scenario.shared {
				outpost.CodexServer().AwaitStopped()
			}
			mark := len(pair.App.Log())
			outpost.Stop()
			outpostReachSince(pair.App, mark, "outpost", protocol.OutpostReachUnreachable)
			mark = pair.App.EventCount()
			outpost.Start()
			stopRetries := home.FireTimers(bridge.RetryTimer)
			outpostReachSince(pair.App, mark, "outpost", protocol.OutpostReachReachable)
			stopRetries()
			reopened, err := home.Client().SessionReopen(client.SessionReopenOptions{SessionID: protocol.SessionID(id)})
			if err != nil {
				t.Fatal(err)
			}
			second := outpost.LaunchedCarrying(pair.App.Terminal(id))
			if !second.Resumed || second.ConversationID != conversation || reopened.Directory != cwd {
				t.Fatalf("resume=%t conversation=%s want=%s directory=%s", second.Resumed, second.ConversationID, conversation, reopened.Directory)
			}
			pair.App.TypeLine(id, "second work")
			second.Prompted()
			second.Reply(reply)
			resumed := testworld.AwaitSession(pair.App, id, func(s protocol.Session) bool { return s.Usage != nil && s.Usage.TotalTokens == 4*len(reply) })
			if protocol.Deref(resumed.Host) != pair.Host {
				t.Fatalf("resumed host=%s want=%s", protocol.Deref(resumed.Host), pair.Host)
			}
			t.Logf("%s ledger resume: host=%s conversation=%s tokens=%d after outpost restart", scenario.name, pair.Host, conversation, resumed.Usage.TotalTokens)
		})
	}
}

func TestLedgerResumeNamesAnUnavailableOutpostAndNeverReopensItsRecord(t *testing.T) {
	home := testworld.NewStack(t)
	outpost := testworld.NewStack(t, testworld.WithAgents(fakeagent.Codex))
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	pair.Start()
	awaitNotebookState(t, pair, protocol.OutpostNotebookStateInSync)
	cwd := outpost.Path("agent")
	outpost.SetCodexFolderTrust(cwd, "trusted")
	id := home.Spawn(pair.App, fakeagent.Codex, cwd, func(m *protocol.SpawnSessionMessage) { m.Host = &pair.Host })
	run := outpost.LaunchedCarrying(pair.App.Terminal(id))
	pair.App.TypeLine(id, "save a conversation")
	run.Prompted()
	run.Reply("Saved. <!-- attn:state=waiting_input -->")
	testworld.AwaitSession(pair.App, id, func(s protocol.Session) bool { return s.Usage != nil })
	if err := home.Client().Unregister(protocol.SessionID(id)); err != nil {
		t.Fatal(err)
	}
	testworld.Await(pair.App, protocol.EventSessionClosed, func(e protocol.WebSocketEvent) bool {
		return e.SessionLedgerEntry != nil && string(e.SessionLedgerEntry.ID) == id
	})
	archivedRollout := outpost.CodexServer().AwaitArchived(run.ConversationID)
	checkRefusal := func(inspected bool, want ...string) {
		t.Helper()
		_, err := home.Client().SessionReopen(client.SessionReopenOptions{SessionID: protocol.SessionID(id)})
		for _, text := range want {
			if err == nil || !strings.Contains(err.Error(), text) {
				t.Fatalf("resume error=%v, want %q", err, text)
			}
		}
		shown, showErr := home.Client().SessionShow(protocol.SessionID(id))
		if showErr != nil || protocol.Deref(shown.Entry.ClosedAt) == "" || protocol.Deref(shown.Entry.Host) != pair.Host {
			t.Fatalf("failed resume changed ledger: %+v, %v", shown, showErr)
		}
		if inspected && shown.Reopen == nil {
			t.Fatal("the ledger has no verdict from the outpost inspection")
		}
		if shown.Reopen != nil {
			for _, text := range want {
				if !strings.Contains(protocol.Deref(shown.Reopen.Reason), text) {
					t.Fatalf("ledger reason=%q, want %q", protocol.Deref(shown.Reopen.Reason), text)
				}
			}
		}
		t.Logf("resume refused, ledger remains closed on %s: %v", pair.Host, err)
	}
	mark := pair.App.EventCount()
	requireStdout(t, home.Attn("settings", "set", "outposts_enabled", "false"), "false")
	outpostReachSince(pair.App, mark, "outpost", protocol.OutpostReachUnreachable)
	checkRefusal(false, "outpost", "unreachable")
	mark = pair.App.EventCount()
	requireStdout(t, home.Attn("settings", "set", "outposts_enabled", "true"), "true")
	outpostReachSince(pair.App, mark, "outpost", protocol.OutpostReachReachable)
	if err := os.Rename(cwd, cwd+".saved"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Base(cwd), cwd); err != nil {
		t.Fatal(err)
	}
	_, cause := os.Stat(cwd)
	if cause == nil {
		t.Fatal("the outpost directory can still be inspected")
	}
	checkRefusal(true, `on outpost "outpost"`, cause.Error())
	refused := home.Attn("session", "reopen", id)
	if refused.Code == 0 || !strings.Contains(refused.Stderr, cause.Error()) || !strings.Contains(refused.Stderr, "outpost") {
		t.Fatalf("CLI resume=%+v, want outpost and inspection cause %q", refused, cause)
	}
	t.Logf("CLI refusal: %s", refused.Stderr)
	if err := os.Remove(cwd); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(cwd+".saved", cwd); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(archivedRollout); err != nil {
		t.Fatal(err)
	}
	checkRefusal(true, `on outpost "outpost"`, "conversation "+run.ConversationID, "no longer in codex's storage")
	if err := os.RemoveAll(cwd); err != nil {
		t.Fatal(err)
	}
	checkRefusal(true, `on outpost "outpost"`, "no longer exists")
	mark = len(pair.App.Log())
	pair.App.Send(protocol.RemoveOutpostMessage{Cmd: protocol.CmdRemoveOutpost, ID: pair.Host})
	testworld.AwaitSince(pair.App, mark, protocol.EventOutpostsUpdated, func(m protocol.OutpostsUpdatedMessage) bool { return len(m.Outposts) == 0 })
	outpost.AwaitStopped()
	checkRefusal(false, "outpost", pair.Host, "does not exist")
}

func TestLedgerRecreatesAnOutpostWorktreeAndContinuesItsConversationAndCost(t *testing.T) {
	for _, scenario := range []struct {
		name       string
		action     protocol.SessionReopenAction
		remoteOnly bool
		seedFolder string
	}{
		{"stale local worktree", protocol.SessionReopenActionRecreateWorktreeAndReopen, false, ""},
		{"branch on archive remote", protocol.SessionReopenActionFetchRecreateAndReopen, true, ""},
		{"a seed's nested folder", protocol.SessionReopenActionRecreateWorktreeAndReopen, false, "subdir"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			home := testworld.NewStack(t)
			outpost := testworld.NewStack(t, testworld.WithAgents(fakeagent.Codex))
			if scenario.seedFolder != "" {
				homesOwnCheckout := home.Path("home-checkout")
				gitRepo(t, homesOwnCheckout)
				giveTheHomeItsOwnCheckoutAt(t, home, "*/worktree|*/worktree/*", homesOwnCheckout)
			}
			pair := testworld.PrepareOutpostPair(t, home, outpost)
			pair.Start()
			awaitNotebookState(t, pair, protocol.OutpostNotebookStateInSync)
			git := func(cwd string, args ...string) string {
				t.Helper()
				cmd := exec.Command("git", args...)
				cmd.Dir = cwd
				data, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("git %v: %v: %s", args, err, data)
				}
				return strings.TrimSpace(string(data))
			}
			repo := outpost.Path("repo")
			gitRepo(t, repo)

			worktree := outpost.Path("worktree")
			git(repo, "worktree", "add", "-qb", "feat/resume", worktree)
			if scenario.remoteOnly {
				archive := outpost.Path("archive.git")
				git(repo, "init", "--bare", "-q", archive)
				git(repo, "remote", "add", "archive", archive)
				git(worktree, "push", "-qu", "archive", "feat/resume")
			}
			cwd := worktree
			if scenario.seedFolder != "" {
				cwd = filepath.Join(worktree, scenario.seedFolder)
				if err := os.MkdirAll(cwd, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(cwd, "tracked.txt"), []byte("nested\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				git(worktree, "add", scenario.seedFolder)
				git(worktree, "-c", "user.name=attn", "-c", "user.email=attn@example.invalid", "commit", "-qm", "nested folder")
			}
			outpost.SetCodexFolderTrust(cwd, "trusted")
			id := home.Spawn(pair.App, fakeagent.Codex, cwd, func(m *protocol.SpawnSessionMessage) { m.Host = &pair.Host })
			first := outpost.LaunchedCarrying(pair.App.Terminal(id))
			pair.App.TypeLine(id, "first work")
			first.Prompted()
			conversation := first.ConversationID
			reply := "Done. <!-- attn:state=waiting_input -->"
			first.Reply(reply)
			testworld.AwaitSession(pair.App, id, func(s protocol.Session) bool { return s.Usage != nil && s.Usage.TotalTokens == 2*len(reply) })
			if scenario.seedFolder != "" {
				testworld.AwaitSession(pair.App, id, func(s protocol.Session) bool { return protocol.Deref(s.Branch) == "feat/resume" })
				planted, err := home.Client().SeedPlant(protocol.SessionID(id), "Continue the nested work", "Finish it.", "", "")
				if err != nil {
					t.Fatal(err)
				}
				if _, err := home.Client().SeedTransition(protocol.SessionID(id), planted.Seed.ID, "tend", "", false, client.SeedTransitionOptions{}); err != nil {
					t.Fatal(err)
				}
			}
			if err := home.Client().Unregister(protocol.SessionID(id)); err != nil {
				t.Fatal(err)
			}
			testworld.Await(pair.App, protocol.EventSessionClosed, func(e protocol.WebSocketEvent) bool {
				return e.SessionLedgerEntry != nil && string(e.SessionLedgerEntry.ID) == id
			})
			if err := os.RemoveAll(worktree); err != nil {
				t.Fatal(err)
			}
			if scenario.remoteOnly {
				git(repo, "worktree", "prune")
				git(repo, "branch", "-qD", "feat/resume")
			}
			shown, err := home.Client().SessionShow(protocol.SessionID(id))
			if err != nil {
				t.Fatal(err)
			}
			if shown.Reopen == nil || !slices.Contains(shown.Reopen.Actions, scenario.action) {
				t.Fatalf("missing worktree verdict=%+v, want action %s", shown.Reopen, scenario.action)
			}
			if _, err := os.Stat(worktree); !os.IsNotExist(err) {
				t.Fatalf("reading the verdict changed the missing worktree: %v", err)
			}
			reopened, err := home.Client().SessionReopen(client.SessionReopenOptions{SessionID: protocol.SessionID(id), Action: string(scenario.action)})
			if err != nil {
				t.Fatal(err)
			}
			second := outpost.LaunchedCarrying(pair.App.Terminal(id))
			if created := protocol.Deref(reopened.WorktreeCreated); created != worktree {
				t.Fatalf("worktree recreated at %q, want %q for a session in %q", created, worktree, cwd)
			}
			if reopened.Directory != cwd || !second.Resumed || second.ConversationID != conversation {
				t.Fatalf("reopened=%+v resumed=%t conversation=%s, want %s in %s", reopened, second.Resumed, second.ConversationID, conversation, cwd)
			}
			if branch := git(worktree, "branch", "--show-current"); branch != "feat/resume" {
				t.Fatalf("recreated branch=%s", branch)
			}
			pair.App.TypeLine(id, "second work")
			second.Prompted()
			second.Reply(reply)
			resumed := testworld.AwaitSession(pair.App, id, func(s protocol.Session) bool { return s.Usage != nil && s.Usage.TotalTokens == 4*len(reply) })
			if protocol.Deref(resumed.Host) != pair.Host {
				t.Fatalf("resumed host=%s, want %s", protocol.Deref(resumed.Host), pair.Host)
			}
			t.Logf("%s: recreated %s, cwd=%s, host=%s, conversation=%s, tokens=%d", scenario.name, worktree, reopened.Directory, pair.Host, conversation, resumed.Usage.TotalTokens)
		})
	}
}

func TestLedgerResumesAnOutpostSessionInItsOwnFolderAfterALocalCrewHomeAtThatPathMoves(t *testing.T) {
	home := testworld.NewStack(t)
	outpost := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	pair.Start()
	awaitNotebookState(t, pair, protocol.OutpostNotebookStateInSync)
	created, err := home.Client().CrewCreate("Keel")
	if err != nil {
		t.Fatal(err)
	}
	folder := filepath.Join(created.Member.HomeDir, "notes")
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	id := home.Spawn(pair.App, fakeagent.Claude, folder, func(m *protocol.SpawnSessionMessage) { m.Host = &pair.Host })
	first := outpost.LaunchedCarrying(pair.App.Terminal(id))
	pair.App.TypeLine(id, "work on the outpost")
	first.Prompted()
	first.Reply("Done. <!-- attn:state=waiting_input -->")
	testworld.AwaitSession(pair.App, id, func(s protocol.Session) bool { return s.Usage != nil })
	if err := home.Client().Unregister(protocol.SessionID(id)); err != nil {
		t.Fatal(err)
	}
	testworld.Await(pair.App, protocol.EventSessionClosed, func(e protocol.WebSocketEvent) bool {
		return e.SessionLedgerEntry != nil && string(e.SessionLedgerEntry.ID) == id
	})

	renamed, err := home.Client().CrewRename("Keel", "Keeper")
	if err != nil {
		t.Fatal(err)
	}
	if moved := protocol.Deref(renamed.HomeDir); moved == "" || moved == created.Member.HomeDir {
		t.Fatalf("the rename left Keel's home at %s: %+v", created.Member.HomeDir, renamed)
	}
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	reopened, err := home.Client().SessionReopen(client.SessionReopenOptions{SessionID: protocol.SessionID(id)})
	if err != nil {
		t.Fatal(err)
	}
	second := outpost.LaunchedCarrying(pair.App.Terminal(id))
	if !second.Resumed || second.ConversationID != first.ConversationID || reopened.Directory != folder {
		t.Fatalf("resume=%t conversation=%s want=%s directory=%s want=%s", second.Resumed, second.ConversationID, first.ConversationID, reopened.Directory, folder)
	}
}

func TestClosingAnOutpostsSharedCodexLeavesTheHomesRunning(t *testing.T) {
	home := testworld.NewStack(t, testworld.WithAgents(fakeagent.Codex))
	outpost := testworld.NewStack(t, testworld.WithAgents(fakeagent.Codex))
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	serverGone := outpost.PauseAt(pausepoint.CodexServerExitHandled)
	pair.Start()
	awaitNotebookState(t, pair, protocol.OutpostNotebookStateInSync)
	homeDir := home.Path("agent")
	home.SetCodexFolderTrust(homeDir, "trusted")
	outpostDir := outpost.Path("agent")
	outpost.SetCodexFolderTrust(outpostDir, "trusted")

	atHome := home.Spawn(pair.App, fakeagent.Codex, homeDir)
	homeRun := home.LaunchedCarrying(pair.App.Terminal(atHome))
	homeServer := home.CodexServers()[0]
	away := home.Spawn(pair.App, fakeagent.Codex, outpostDir, func(m *protocol.SpawnSessionMessage) { m.Host = &pair.Host })
	outpost.LaunchedCarrying(pair.App.Terminal(away))

	if err := home.Client().Unregister(protocol.SessionID(away)); err != nil {
		t.Fatal(err)
	}
	serverGone.Await()
	serverGone.Release()

	pair.App.TypeLine(atHome, "still here")
	homeRun.Prompted()
	homeRun.Reply("Still here. <!-- attn:state=waiting_input -->")
	testworld.AwaitSession(pair.App, atHome, func(s protocol.Session) bool { return s.Usage != nil })
	if servers := home.CodexServers(); len(servers) != 1 || syscall.Kill(homeServer, 0) != nil {
		t.Fatalf("the home's Codex app-server (pid %d) stopped; the home has started %v", homeServer, servers)
	}
}
