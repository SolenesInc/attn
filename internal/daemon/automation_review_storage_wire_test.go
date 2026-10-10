package daemon_test

import (
	"fmt"
	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

func TestAReviewCheckoutStaysStoppedWhenSavingItsFailureInitiallyCannotCommit(t *testing.T) {
	r := newAutomationReviewWorld(t)
	var attempts atomic.Int32
	started, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if attempts.Add(1) == 1 {
			close(started)
			<-release
		}
	}))
	defer server.Close()
	filter := filepath.Join(t.TempDir(), "filter")
	script := fmt.Sprintf("#!/bin/sh\ncurl -s %q >/dev/null\nprintf 'database or disk is full\\n' >&2\nexit 1\n", server.URL)
	if err := os.WriteFile(filter, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	runGit(t, r.clone, "config", "filter.storage.clean", "cat")
	runGit(t, r.clone, "config", "filter.storage.smudge", filter)
	runGit(t, r.clone, "config", "filter.storage.required", "true")
	r.head = commitFile(t, r.clone, ".gitattributes", "change.go filter=storage\n")
	r.github.request(42, r.head, false)
	r.refresh()
	<-started
	restore := r.w.RefuseDatabaseCommits()
	defer restore()
	close(release)
	failed := r.awaitNewRun(1, "failed")
	if _, err := r.cli.AutomationSetEnabled(1, true); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		r.refresh()
	}
	if attempts.Load() != 1 {
		t.Fatalf("checkout attempts while storage rejects writes = %d, want one", attempts.Load())
	}
	restore()
	r.github.withdraw(42)
	r.refresh()
	r.w.restart()
	r.app, r.cli = r.w.App(), r.w.Client()
	persisted := r.awaitNewRun(1, "failed")
	if persisted.ID != failed.ID || !strings.Contains(protocol.Deref(persisted.LastError), "database or disk is full") {
		t.Fatalf("persisted failure = %+v, want the original disk-full failure", persisted)
	}
	if attempts.Load() != 1 {
		t.Fatalf("checkout attempts after persistence recovers = %d, want one", attempts.Load())
	}
	if notes := automationSeedNotesMentioning(t, r.cli, protocol.Deref(failed.SeedID), "database or disk is full"); notes != 1 {
		t.Fatalf("failure notes = %d, want one", notes)
	}
	runGit(t, r.clone, "config", "filter.storage.smudge", "cat")
	r.github.request(42, r.head, false)
	r.refresh()
	recovered := r.awaitNewRun(1, "delivered", failed)
	if protocol.Deref(recovered.SessionID) == protocol.Deref(failed.SessionID) || protocol.Deref(recovered.SeedID) == protocol.Deref(failed.SeedID) {
		t.Fatalf("recovered run %+v reused the never-started reviewer %+v", recovered, failed)
	}
}

func TestAContinuationRollbackPreservesAClaimAcquiredDuringPreparation(t *testing.T) {
	r := newAutomationReviewWorld(t)
	r.github.request(42, r.head, false)
	r.refresh()
	first := r.awaitNewRun(1, "delivered")
	reviewer := r.w.Launched(string(protocol.Deref(first.SessionID)))
	reviewer.Prompted()
	reviewer.Reply("Reviewed.")
	seedID := protocol.Deref(first.SeedID)
	if _, err := r.cli.SeedTransition(protocol.Deref(first.SessionID), seedID, "harvest", "Review completed.", false, client.SeedTransitionOptions{}); err != nil {
		t.Fatal(err)
	}
	r.stop(reviewer)
	other := r.w.Spawn(r.app, fakeagent.Claude, r.w.Path("new-tender"))
	r.w.Launched(other)
	started, release := make(chan struct{}), make(chan struct{})
	finish := sync.OnceFunc(func() { close(release) })
	var announced sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { announced.Do(func() { close(started) }); <-release }))
	defer func() { finish(); server.Close() }()
	filter := filepath.Join(t.TempDir(), "filter")
	if err := os.WriteFile(filter, []byte(fmt.Sprintf("#!/bin/sh\ncurl -s %q >/dev/null\nprintf 'ownership fetch failed: database or disk is full\\n' >&2\nexit 1\n", server.URL)), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_SSH_COMMAND", filter)
	runGit(t, r.clone, "config", "core.sshCommand", filter)
	runGit(t, r.clone, "config", "ssh.variant", "ssh")
	upstream := newRepo(t, "next-head")
	newer := commitFile(t, upstream, "change.go", "package changed\n")
	r.github.withdraw(42)
	r.refresh()
	r.github.request(42, newer, false)
	r.readPullRequest(42, newer)
	r.app.Send(protocol.RefreshPRsMessage{Cmd: protocol.CmdRefreshPRs})
	<-started
	if _, err := r.cli.SeedTransition(protocol.SessionID(other), seedID, "tend", "", false, client.SeedTransitionOptions{}); err != nil {
		t.Fatal(err)
	}
	finish()
	failed := r.awaitNewRun(1, "failed", first)
	if protocol.Deref(failed.SeedID) != seedID || protocol.Deref(failed.SessionID) != protocol.Deref(first.SessionID) {
		t.Fatalf("continuation identities: %+v", failed)
	}
	shown, err := r.cli.SeedShow("", seedID)
	if err != nil || shown.Seed.Status != "growing" || !shown.Seed.Claimed || shown.Seed.Tender == nil || shown.Seed.Tender.Ref != protocol.PartyRef("session:"+other) {
		t.Fatalf("rollback took the new claim: %+v %v", shown, err)
	}
}
