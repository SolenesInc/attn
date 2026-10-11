package daemon_test

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"testing/synctest"
	"time"

	"github.com/victorarias/attn/internal/github/mockserver"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAnIdenticalDashboardRefreshWritesNothing(t *testing.T) {
	gh := mockserver.New()
	t.Cleanup(gh.Close)
	gh.Config.SetKeepAlivesEnabled(false)
	gh.AddPR(mockserver.MockPR{Repo: "acme/shop", Number: 1, Title: "Add checkout", Role: "reviewer"})
	t.Setenv("ATTN_MOCK_GH_URL", gh.URL)
	t.Setenv("ATTN_MOCK_GH_TOKEN", "test-token")
	inBubble(t, func(t *testing.T, w *world) {
		app := w.App()
		refreshPRs(t, app)
		synctest.Wait()
		path := filepath.Join(w.Dir, "attn.db-wal")
		before, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		refreshPRs(t, app)
		synctest.Wait()
		after, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(before, after) {
			t.Fatalf("an identical dashboard refresh changed the WAL: before=%d bytes, after=%d bytes", len(before), len(after))
		}
	})
}

func TestDashboardPollTimesAndChangedContentSurviveRestart(t *testing.T) {
	gh := mockserver.New()
	t.Cleanup(gh.Close)
	gh.Config.SetKeepAlivesEnabled(false)
	gh.AddPR(mockserver.MockPR{Repo: "acme/shop", Number: 1, Title: "Add checkout", Role: "reviewer"})
	gh.AddPR(mockserver.MockPR{Repo: "acme/shop", Number: 2, Title: "Fix cart", Role: "author"})
	t.Setenv("ATTN_MOCK_GH_URL", gh.URL)
	t.Setenv("ATTN_MOCK_GH_TOKEN", "test-token")
	inBubble(t, func(t *testing.T, w *world) {
		cli := w.Client()
		app := w.App()
		refreshPRs(t, app)
		first := prNumbered(t, queryPRs(t, cli, ""), 1)
		w.advance(time.Second)
		refreshPRs(t, app)
		next := prNumbered(t, queryPRs(t, cli, ""), 1)
		if next.LastUpdated == first.LastUpdated || next.LastPolled == first.LastPolled {
			t.Fatal("unchanged content did not refresh the visible poll timestamps")
		}
		w.restart()
		cli = w.Client()
		app = w.App()
		restored := prNumbered(t, queryPRs(t, cli, ""), 1)
		if restored.LastUpdated != next.LastUpdated || restored.LastPolled != next.LastPolled {
			t.Fatalf("poll timestamps after restart=%+v, want %+v", restored, next)
		}
		gh.Reset()
		gh.AddPR(mockserver.MockPR{Repo: "acme/shop", Number: 1, Title: "Checkout is ready", Role: "reviewer"})
		refreshPRs(t, app)
		changed := testworld.Await(app, protocol.EventPRsUpdated, func(e protocol.WebSocketEvent) bool { return len(e.Prs) == 1 && e.Prs[0].Title == "Checkout is ready" })
		if changed.Prs[0].ID != first.ID {
			t.Fatalf("changed PR=%+v", changed.Prs)
		}
		w.restart()
		remaining := queryPRs(t, w.Client(), "")
		if len(remaining) != 1 || remaining[0].Title != "Checkout is ready" {
			t.Fatalf("dashboard after restart=%+v", remaining)
		}
	})
}

func TestARefusedDashboardRefreshKeepsThePreviousList(t *testing.T) {
	gh := mockserver.New()
	t.Cleanup(gh.Close)
	gh.Config.SetKeepAlivesEnabled(false)
	gh.AddPR(mockserver.MockPR{Repo: "acme/shop", Number: 1, Title: "Add checkout", Role: "reviewer"})
	t.Setenv("ATTN_MOCK_GH_URL", gh.URL)
	t.Setenv("ATTN_MOCK_GH_TOKEN", "test-token")
	w := newWorld(t)
	cli := w.Client()
	app := w.App()
	refreshPRs(t, app)
	before := queryPRs(t, cli, "")
	gh.Reset()
	gh.AddPR(mockserver.MockPR{Repo: "acme/shop", Number: 2, Title: "Fix cart", Role: "author"})
	restore := w.RefuseDatabaseCommits()
	defer restore()
	result := testworld.Request(app, protocol.RefreshPRsMessage{Cmd: protocol.CmdRefreshPRs}, protocol.EventRefreshPRsResult, func(protocol.RefreshPRsResultMessage) bool { return true })
	if result.Success {
		t.Fatal("a refused save reported a successful refresh")
	}
	if after := queryPRs(t, cli, ""); !reflect.DeepEqual(before, after) {
		t.Fatalf("a refused refresh replaced the previous list: before=%+v, after=%+v", before, after)
	}
}

func TestUnchangedWatchChecksStayCurrentWithoutRepeatingTheNotice(t *testing.T) {
	gh := serveRefreshedPullRequest(t)
	inBubble(t, func(t *testing.T, w *world) {
		cli := w.Client()
		registerSessions(t, w, cli, "s1")
		watchPullRequestOn(t, cli, "s1", protocol.PullRequestWatchModeGreen)
		w.advance(time.Second)
		first := onlyPullRequest(t, cli, "s1")
		if protocol.Deref(first.ReadinessState) != "ready" {
			t.Fatalf("initial watch=%+v", first)
		}
		if items := readInbox(t, cli, "s1", 0).Items; len(items) != 1 {
			t.Fatalf("initial watch notices=%+v, want one ready notice", items)
		}
		w.advance(protocol.HeatHotInterval)
		next := onlyPullRequest(t, cli, "s1")
		if protocol.Deref(next.WatchHealth) != "current" || protocol.Deref(next.StatusFetchedAt) == protocol.Deref(first.StatusFetchedAt) {
			t.Fatalf("unchanged check lost liveness=%+v", next)
		}
		if items := readInbox(t, cli, "s1", 0).Items; len(items) != 0 {
			t.Fatalf("unchanged check repeated notices=%+v", items)
		}
		gh.set(func(gh *refreshedPullRequest) { gh.readinessFails = true })
		w.advance(protocol.HeatHotInterval)
		if changed := onlyPullRequest(t, cli, "s1"); protocol.Deref(changed.WatchHealth) != "delayed" {
			t.Fatalf("changed checks=%+v, want delayed watch health", changed)
		}
		if items := readInbox(t, cli, "s1", 0).Items; len(items) != 1 {
			t.Fatalf("changed checks notices=%+v, want one failure notice", items)
		}
	})
}
