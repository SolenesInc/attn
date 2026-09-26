package daemon

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/store"
)

func TestAppTicketRowsBareNonArchived(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	now := time.Now()
	if _, err := d.store.CreateTicket(store.Ticket{
		ID: "open-one", Title: "Open", Status: store.TicketStatusWorking, Assignee: "sess-1",
	}, "chief-1", now); err != nil {
		t.Fatalf("create open: %v", err)
	}
	if _, err := d.store.CreateTicket(store.Ticket{
		ID: "done-one", Title: "Done", Status: store.TicketStatusDone,
	}, "chief-1", now); err != nil {
		t.Fatalf("create done: %v", err)
	}
	if err := d.store.ArchiveTicket("done-one", now.Add(time.Minute)); err != nil {
		t.Fatalf("archive: %v", err)
	}
	if _, err := d.store.AddTicketComment("open-one", "chief-1", "note", now.Add(2*time.Minute)); err != nil {
		t.Fatalf("comment: %v", err)
	}

	board := d.appTicketRows()
	if len(board) != 1 || board[0].ID != "open-one" {
		t.Fatalf("board = %+v, want only the non-archived open-one", board)
	}
}

func TestAppTicketRowCarriesTheBoardAndNotTheBrief(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	now := time.Now()
	brief := strings.Repeat("a delegation brief nobody renders from a board row. ", 200)
	if _, err := d.store.CreateTicket(store.Ticket{
		ID:          "store-migration",
		Title:       "Migrate the store",
		Description: brief,
		Status:      store.TicketStatusWorking,
		Assignee:    "sess-1",
		Cwd:         "/repo",
		LastAgentID: "codex",
	}, "chief-1", now); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := d.store.AddTicketComment("store-migration", "chief-1", "a note", now.Add(time.Minute)); err != nil {
		t.Fatalf("comment: %v", err)
	}

	board := d.appTicketRows()
	if len(board) != 1 {
		t.Fatalf("board = %+v, want one row", board)
	}
	raw, err := json.Marshal(board[0])
	if err != nil {
		t.Fatalf("marshal row: %v", err)
	}
	var wire map[string]any
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatalf("unmarshal row: %v", err)
	}
	for _, field := range []string{"description", "activity", "artifacts"} {
		if _, present := wire[field]; present {
			t.Fatalf("board row carries %q: %s", field, raw)
		}
	}
	if strings.Contains(string(raw), "delegation brief") {
		t.Fatalf("the brief reached the board row: %s", raw)
	}
	for field, want := range map[string]any{
		"id":            "store-migration",
		"title":         "Migrate the store",
		"status":        string(store.TicketStatusWorking),
		"assignee":      "sess-1",
		"cwd":           "/repo",
		"last_agent_id": "codex",
	} {
		if wire[field] != want {
			t.Fatalf("row %s = %v, want %v", field, wire[field], want)
		}
	}
	if wire["updated_at"] == "" || wire["updated_at"] == nil {
		t.Fatalf("row is missing updated_at: %s", raw)
	}
}
