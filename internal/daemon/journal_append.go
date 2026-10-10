package daemon

import (
	"encoding/json"
	"net"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/notebook"
	"github.com/victorarias/attn/internal/protocol"
)

func (d *Daemon) handleJournalAppend(conn net.Conn, msg *protocol.JournalAppendMessage) {
	entry := strings.TrimSpace(msg.Entry)
	if entry == "" {
		d.sendError(conn, "journal append: entry is required")
		return
	}
	if err := d.requireHome("the Notebook"); err != nil {
		d.sendError(conn, err.Error())
		return
	}
	date := ""
	if msg.Date != nil {
		date = strings.TrimSpace(*msg.Date)
	}
	if date == "" {
		date = time.Now().Format("2006-01-02")
	}
	profile, err := d.callerProfile(protocol.Deref(msg.SourceSessionID))
	if err != nil {
		d.sendError(conn, "journal append: "+err.Error())
		return
	}
	store, err := d.notebookStoreFor(profile.ID)
	if err != nil {
		d.sendError(conn, "journal append: "+err.Error())
		return
	}
	rel, hash, err := store.AppendJournal(date, entry)
	if err != nil {
		d.sendError(conn, "journal append: "+err.Error())
		return
	}
	d.noteSelfWrite(store.Root(), notebook.SelfWrite{Rel: rel, Hash: hash})
	d.broadcastNotebookChanged(originAgent, rel)
	d.broadcastFsChanged(store.Root(), originAgent, rel)
	_ = json.NewEncoder(conn).Encode(protocol.Response{
		Ok: true,
		JournalAppendResult: &protocol.JournalAppendResult{
			RelPath: rel,
			Hash:    hash,
		},
	})
}
