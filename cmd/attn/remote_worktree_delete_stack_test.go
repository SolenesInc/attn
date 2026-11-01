package main_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestDeletingAWorktreeOnAnOutpostAnswersToTheHome(t *testing.T) {
	home, outpost := testworld.NewStack(t), testworld.NewStack(t)
	outpost.Vars = append(outpost.Vars, "SHELL=/bin/bash")
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	pair.Start()
	app, host := pair.App, pair.Host
	onOutpost := protocol.Ptr(host)

	repo := outpost.Path("shop")
	gitRepo(t, repo)
	createOnOutpost := func(branch string) string {
		t.Helper()
		path := outpost.Path("shop--" + branch)
		id := "create-" + branch
		created := testworld.Request(app, protocol.CreateWorktreeMessage{Cmd: protocol.CmdCreateWorktree, Host: onOutpost, MainRepo: repo, Branch: branch, Path: protocol.Ptr(path), RequestID: protocol.Ptr(id)},
			protocol.EventCreateWorktreeResult, func(r protocol.CreateWorktreeResultMessage) bool { return protocol.Deref(r.RequestID) == id })
		if !created.Success {
			t.Fatalf("create %s on the outpost: %s", branch, protocol.Deref(created.Error))
		}
		return path
	}
	worktree := createOnOutpost("feat")
	git := func(dir string, args ...string) (string, error) {
		out, err := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=attn", "-c", "user.email=attn@example.invalid"}, args...)...).CombinedOutput()
		return strings.TrimSpace(string(out)), err
	}
	if err := os.WriteFile(filepath.Join(worktree, "unmerged.txt"), []byte("work the seed continues from\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := git(worktree, "add", "unmerged.txt"); err != nil {
		t.Fatalf("git add: %s", out)
	}
	if out, err := git(worktree, "commit", "-q", "-m", "unmerged work"); err != nil {
		t.Fatalf("git commit: %s", out)
	}
	unmerged, _ := git(worktree, "rev-parse", "HEAD")

	shell := home.Spawn(app, fakeagent.Harness(protocol.SessionAgentShell), worktree, func(m *protocol.SpawnSessionMessage) { m.Host = onOutpost })
	app.TypeLine(shell, "echo up-$((1+1))")
	app.AwaitScreen(shell, "up-2")
	testworld.AwaitSession(app, shell, func(s protocol.Session) bool { return protocol.Deref(s.Branch) == "feat" })
	cli := home.Client()
	planted, err := cli.SeedPlant(protocol.SessionID(shell), "Continue the feat work", "Finish feat.", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cli.SeedTransition(protocol.SessionID(shell), planted.Seed.ID, "tend", "", false, client.SeedTransitionOptions{}); err != nil {
		t.Fatal(err)
	}

	deleted := testworld.Request(app, protocol.DeleteWorktreeMessage{Cmd: protocol.CmdDeleteWorktree, Host: onOutpost, Path: worktree, RequestID: protocol.Ptr("delete")},
		protocol.EventDeleteWorktreeResult, func(r protocol.DeleteWorktreeResultMessage) bool { return protocol.Deref(r.RequestID) == "delete" })
	if !deleted.Success {
		t.Fatalf("delete on the outpost: %s", protocol.Deref(deleted.Error))
	}
	if _, err := os.Stat(worktree); !os.IsNotExist(err) {
		t.Errorf("the worktree is still on disk: %v", err)
	}
	if kept, err := git(repo, "rev-parse", "--verify", "feat"); err != nil || kept != unmerged {
		t.Errorf("branch feat = %q (%v); want it kept at the open seed's unmerged commit %s", kept, err, unmerged)
	}
	testworld.Await(app, protocol.EventSessionUnregistered, func(e protocol.WebSocketEvent) bool {
		return e.Session != nil && string(e.Session.ID) == shell
	})
	sessions, err := cli.Query("")
	if err != nil {
		t.Fatal(err)
	}
	if querySessionsHas(sessions, shell) {
		t.Errorf("the home still lists the outpost shell %s whose worktree was deleted", shell)
	}
	notes := home.Attn("seed", "notes", planted.Seed.ID)
	if notes.Code != 0 || !strings.Contains(notes.Stdout, "deleted the worktree "+worktree) {
		t.Errorf("seed notes = %+v; want the removal of %s noted on the home's seed", notes, worktree)
	}

	stranded := createOnOutpost("later")
	lost := app.EventCount()
	pair.Disconnect()
	outpostStateSince(app, "outpost", protocol.OutpostStateLost, lost)
	onTheOutpost := outpost.App()
	refused := testworld.Request(onTheOutpost, protocol.DeleteWorktreeMessage{Cmd: protocol.CmdDeleteWorktree, Path: stranded, RequestID: protocol.Ptr("delete-alone")},
		protocol.EventDeleteWorktreeResult, func(r protocol.DeleteWorktreeResultMessage) bool {
			return protocol.Deref(r.RequestID) == "delete-alone"
		})
	if refused.Success {
		t.Errorf("delete on the outpost without its home = %+v; want it refused", refused)
	}
	if _, err := os.Stat(stranded); err != nil {
		t.Errorf("a refused delete must leave the worktree in place: %v", err)
	}
	if _, err := git(repo, "rev-parse", "--verify", "later"); err != nil {
		t.Errorf("a refused delete must leave the branch: %v", err)
	}
	t.Logf("kept feat at %s; seed noted; shell closed on the home; without the home: %s", unmerged, protocol.Deref(refused.Error))
}
