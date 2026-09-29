package main_test

import (
	"os"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/victorarias/attn/internal/testworld"
)

func TestDaemonEnsureStartsOneDaemonForRacingCallersAndSaysWhyOneCannotStart(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t)
	ensure := func() (status string, got testworld.Result) {
		t.Helper()
		got = s.Attn("daemon", "ensure")
		var result struct {
			Status string `json:"status"`
		}
		if got.Code == 0 {
			got.JSON(t, &result)
		}
		return result.Status, got
	}

	// Without fd inheritance, ensure must refuse the socket held by the stack.
	if got := s.Run(testworld.Invocation{Args: []string{"daemon", "ensure"}, Env: []string{"ATTN_HARNESS_WS_LISTENER_FD="}}); got.Code != 1 || !strings.Contains(got.Stderr, "daemon ensure error: daemon startup failed: refusing to start: cannot bind the WebSocket address "+s.WSAddr) {
		t.Errorf("attn daemon ensure with its port taken exited %d with stderr %q, want the daemon's own startup failure", got.Code, got.Stderr)
	}

	t.Cleanup(func() { s.Attn("daemon", "stop") })
	statuses := make([]string, 3)
	var wg sync.WaitGroup
	for i := range statuses {
		wg.Go(func() {
			var got testworld.Result
			if statuses[i], got = ensure(); got.Code != 0 {
				t.Errorf("a racing attn daemon ensure exited %d: %s", got.Code, got.Stderr)
			}
		})
	}
	wg.Wait()
	slices.Sort(statuses)
	if want := []string{"already_running", "already_running", "started"}; !slices.Equal(statuses, want) {
		t.Errorf("three racing attn daemon ensure calls reported %q, want %q", statuses, want)
	}
	if status, _ := ensure(); status != "already_running" {
		t.Errorf("attn daemon ensure over the running daemon reported %q, want already_running", status)
	}

	if err := os.Remove(s.Socket); err != nil {
		t.Fatal(err)
	}
	if _, got := ensure(); got.Code != 1 || !strings.Contains(got.Stderr, "missing its Unix listener") {
		t.Errorf("attn daemon ensure over a daemon that lost its socket exited %d with stderr %q, want a refusal naming the missing listener", got.Code, got.Stderr)
	}
}
