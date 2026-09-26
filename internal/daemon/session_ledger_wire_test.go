package daemon_test

import (
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestTheLedgerPagesClosedSessionsNewestFirst(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		cli := w.Client()
		registerSessions(t, w, cli, "live", "a", "b", "c", "d", "e")
		for _, id := range []string{"a", "b", "c", "d", "e"} {
			w.advance(time.Second)
			if err := cli.Unregister(id); err != nil {
				t.Fatalf("close %s: %v", id, err)
			}
		}

		first := ledger(t, cli, client.SessionListOptions{Closed: true, Limit: 2})
		if got := ledgerIDs(first); !slices.Equal(got, []string{"e", "d"}) || first.Omitted != 3 || protocol.Deref(first.NextBefore) != "d" {
			t.Fatalf("first page = %v omitting %d before %q, want [e d] omitting 3 before d", got, first.Omitted, protocol.Deref(first.NextBefore))
		}
		second := ledger(t, cli, client.SessionListOptions{Closed: true, Limit: 2, Before: "d"})
		if got := ledgerIDs(second); !slices.Equal(got, []string{"c", "b"}) || second.Omitted != 1 {
			t.Fatalf("second page = %v omitting %d, want [c b] omitting 1", got, second.Omitted)
		}
		last := ledger(t, cli, client.SessionListOptions{Closed: true, Limit: 2, Before: protocol.Deref(second.NextBefore)})
		if got := ledgerIDs(last); !slices.Equal(got, []string{"a"}) || last.Omitted != 0 || last.NextBefore != nil {
			t.Fatalf("last page = %v omitting %d before %q, want [a] with nothing left", got, last.Omitted, protocol.Deref(last.NextBefore))
		}

		if got := ledgerIDs(ledger(t, cli, client.SessionListOptions{})); !slices.Equal(got, []string{"live"}) {
			t.Errorf("live scope = %v, want only the open session", got)
		}
		if got := ledgerIDs(ledger(t, cli, client.SessionListOptions{All: true})); len(got) != 6 || !slices.Contains(got, "live") || !slices.Contains(got, "a") {
			t.Errorf("all scope = %v, want the open session and every closed one", got)
		}

		_, err := cli.SessionList(client.SessionListOptions{Limit: 1001})
		if err == nil || !strings.Contains(err.Error(), "1001") || !strings.Contains(err.Error(), "1000") {
			t.Errorf("an over-limit page = %v, want a refusal naming the ask and the limit", err)
		}
		_, err = cli.SessionList(client.SessionListOptions{Before: "never-existed"})
		if err == nil || !strings.Contains(err.Error(), "never-existed") {
			t.Errorf("paging from an unknown row = %v, want a refusal naming the cursor", err)
		}
	})
}

func TestTheLedgerWindowIsHalfOpenOverEachRowsInstant(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		cli := w.Client()
		since := time.Now().Add(time.Minute).UTC()
		until := since.Add(time.Minute)

		registerSessions(t, w, cli, "before", "closed-inside")
		w.advance(time.Minute)
		registerSessions(t, w, cli, "inside", "closed-after")
		w.advance(30 * time.Second)
		if err := cli.Unregister("closed-inside"); err != nil {
			t.Fatalf("close: %v", err)
		}
		w.advance(30 * time.Second)
		registerSessions(t, w, cli, "at-until")
		if err := cli.Unregister("closed-after"); err != nil {
			t.Fatalf("close: %v", err)
		}

		page := ledger(t, cli, client.SessionListOptions{
			All: true, Since: since.Format(time.RFC3339), Until: until.Format(time.RFC3339),
		})
		if got := sortedLedgerIDs(page); !slices.Equal(got, []string{"closed-inside", "inside"}) {
			t.Errorf("window [%s, %s) = %v, want the row seen at its start and the one closed inside it", since, until, got)
		}
	})
}

func TestTheLedgerFiltersByWorkspaceAndRepositoryAndCountsEveryChoice(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	cli := w.Client()
	attn := newRepo(t, "attn")
	other := newRepo(t, "other")
	appDir := filepath.Join(attn, "app")
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for id, dir := range map[string]string{"attn-one": attn, "attn-two": appDir, "other": other} {
		if err := cli.Register(id, id, dir); err != nil {
			t.Fatalf("register %s: %v", id, err)
		}
	}

	byRepository := ledger(t, cli, client.SessionListOptions{Repository: attn})
	if got := sortedLedgerIDs(byRepository); !slices.Equal(got, []string{"attn-one", "attn-two"}) {
		t.Errorf("repository %s = %v, want both sessions in it", attn, got)
	}
	if got := sortedLedgerIDs(ledger(t, cli, client.SessionListOptions{WorkspaceID: "workspace-attn-two"})); !slices.Equal(got, []string{"attn-two"}) {
		t.Errorf("workspace-attn-two = %v, want its one session", got)
	}
	if got := sortedLedgerIDs(ledger(t, cli, client.SessionListOptions{WorkspaceID: "workspace-other", Repository: attn})); len(got) != 0 {
		t.Errorf("workspace-other in repository %s = %v, want nothing matching both", attn, got)
	}

	requestID := "facets"
	faceted := testworld.Request(app, protocol.SessionListMessage{
		Cmd: protocol.CmdSessionList, RequestID: protocol.Ptr(requestID), Repository: protocol.Ptr(attn),
	}, protocol.EventSessionListResult, func(r protocol.SessionListResultMessage) bool { return r.RequestID == requestID })
	if !faceted.Success || faceted.Result == nil || faceted.Result.Facets == nil {
		t.Fatalf("faceted list = %+v, want a page with its facets", faceted)
	}
	facets := faceted.Result.Facets
	if got := facetCounts(facets.Workspaces); !maps.Equal(got, map[string]int{"workspace-attn-one": 1, "workspace-attn-two": 1, "workspace-other": 1}) {
		t.Errorf("workspace facets = %v, want every workspace the repository filter hides too", got)
	}
	if got := facetCounts(facets.Repositories); !maps.Equal(got, map[string]int{attn: 2, other: 1}) {
		t.Errorf("repository facets = %v, want every repository the filter hides too", got)
	}
}

func ledger(t *testing.T, cli *client.Client, opts client.SessionListOptions) *protocol.SessionListResult {
	t.Helper()
	page, err := cli.SessionList(opts)
	if err != nil {
		t.Fatalf("session list %+v: %v", opts, err)
	}
	return page
}

func sortedLedgerIDs(page *protocol.SessionListResult) []string {
	ids := ledgerIDs(page)
	slices.Sort(ids)
	return ids
}

func facetCounts(facets []protocol.SessionLedgerFacet) map[string]int {
	counts := make(map[string]int, len(facets))
	for _, facet := range facets {
		counts[facet.Value] = facet.Count
	}
	return counts
}

func ledgerIDs(page *protocol.SessionListResult) []string {
	ids := make([]string, 0, len(page.Entries))
	for _, entry := range page.Entries {
		ids = append(ids, entry.ID)
	}
	return ids
}
