package daemon_test

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"testing/synctest"

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
