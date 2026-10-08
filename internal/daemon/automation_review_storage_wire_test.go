package daemon_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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
