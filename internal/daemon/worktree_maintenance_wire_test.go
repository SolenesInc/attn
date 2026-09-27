package daemon_test

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
)

func TestAutomaticWorktreeCleanupYieldsToForegroundWork(t *testing.T) {
	t.Setenv("ATTN_WORKTREE_SWEEP_IDLE_DAYS", "0")
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	shop := newRepo(t, "shop")
	first := createWorktree(t, app, shop, "feat-first")
	second := createWorktree(t, app, shop, "feat-second")
	observation := newMaintenanceGitGate(t, "status --porcelain --untracked-files=all")

	observation.arm(t)
	refreshWorktrees(t, cli)
	observation.awaitBlocked(t)
	session := w.Spawn(app, fakeagent.Codex, first)

	observation.arm(t)
	refreshWorktrees(t, cli)
	if blocked := observation.awaitBlocked(t); blocked != second {
		t.Errorf("the next sweep inspected %s first; want %s, the one no session holds", blocked, second)
	}
	for _, e := range app.Received() {
		if e.Event == protocol.EventWorktreeSwept || e.Event == protocol.EventWorktreeStateChanged && e.Worktrees[0].Path != first {
			t.Errorf("the preempted sweep went on to decide %s: %+v", e.Event, e.Worktrees)
		}
	}
	for _, path := range []string{first, second} {
		if listed := sweepListed(t, cli, shop, path); listed.ObservedAt != nil || listed.RefreshError != nil || listed.DirtyFiles != nil {
			t.Errorf("the preempted sweep recorded what it saw of %s: %+v", filepath.Base(path), listed)
		}
	}

	observation.release(t)
	if swept := sweepAwaitSwept(app, second); swept.Action != "removed" {
		t.Errorf("the sweep after the preemption recorded %s for the free worktree; want it removed", swept.Action)
	}
	if kept := sweepListed(t, cli, shop, first); protocol.Deref(kept.SweepStatus) != "kept_live_session" || !strings.Contains(protocol.Deref(kept.SweepReason), session) {
		t.Errorf("the worktree the session started in reads %+v; want it kept for %s", kept, session)
	}
}

type maintenanceGitGate struct {
	armed, blocked, released string
}

func newMaintenanceGitGate(t *testing.T, command string) *maintenanceGitGate {
	t.Helper()
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	gate := &maintenanceGitGate{
		armed:    filepath.Join(dir, "armed"),
		blocked:  filepath.Join(dir, "blocked"),
		released: filepath.Join(dir, "release"),
	}
	for _, fifo := range []string{gate.blocked, gate.released} {
		if err := syscall.Mkfifo(fifo, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	bin := filepath.Join(dir, "bin")
	if err := os.Mkdir(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\n" +
		"if [ \"$*\" = '" + command + "' ] && mv '" + gate.armed + "' '" + gate.armed + ".taken' 2>/dev/null; then\n" +
		"  pwd -P > '" + gate.blocked + "'\n" +
		"  read ignored < '" + gate.released + "'\n" +
		"fi\n" +
		"exec '" + realGit + "' \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return gate
}

func (g *maintenanceGitGate) arm(t *testing.T) {
	t.Helper()
	if err := os.WriteFile(g.armed, nil, 0o600); err != nil {
		t.Fatal(err)
	}
}

func (g *maintenanceGitGate) awaitBlocked(t *testing.T) string {
	t.Helper()
	cwd := make(chan string, 1)
	go func() {
		fifo, err := os.Open(g.blocked)
		if err != nil {
			cwd <- ""
			return
		}
		defer fifo.Close()
		line, _ := bufio.NewReader(fifo).ReadString('\n')
		cwd <- strings.TrimSpace(line)
	}()
	select {
	case dir := <-cwd:
		return dir
	case <-time.After(fakeagent.HangGuard):
		t.Fatalf("no git call reached the gate within %s", fakeagent.HangGuard)
		return ""
	}
}

func (g *maintenanceGitGate) release(t *testing.T) {
	t.Helper()
	if err := os.WriteFile(g.released, []byte("go\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}
