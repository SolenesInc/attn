package daemon

import (
	"encoding/json"
	"net"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/notebook"
	"github.com/victorarias/attn/internal/protocol"
)

// Appends through the per-root cached notebook.Store, so concurrent agent writes
// serialize instead of racing the way direct file edits do.
func (d *Daemon) handleJournalAppend(conn net.Conn, msg *protocol.JournalAppendMessage) {
	entry := strings.TrimSpace(msg.Entry)
	if entry == "" {
		d.sendError(conn, "journal append: entry is required")
		return
	}
	date := ""
	if msg.Date != nil {
		date = strings.TrimSpace(*msg.Date)
	}
	if date == "" {
		date = time.Now().Format("2006-01-02")
	}
	store, err := d.notebookStoreFor()
	if err != nil {
		d.sendError(conn, "journal append: "+err.Error())
		return
	}
	rel, hash, err := store.AppendJournal(date, entry)
	if err != nil {
		d.sendError(conn, "journal append: "+err.Error())
		return
	}
	// Content-aware self-write: the watcher must not re-announce this as an
	// external edit.
	d.noteNotebookSelfWrite(notebook.SelfWrite{Rel: rel, Hash: hash})
	d.broadcastNotebookChanged(originAgent, rel)
	_ = json.NewEncoder(conn).Encode(protocol.Response{
		Ok: true,
		JournalAppendResult: &protocol.JournalAppendResult{
			RelPath: rel,
			Hash:    hash,
		},
	})
}
