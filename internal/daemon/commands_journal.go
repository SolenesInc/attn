package daemon

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/command"
	"github.com/victorarias/attn/internal/notebook"
	"github.com/victorarias/attn/internal/protocol"
)

func (d *Daemon) journalCommands() map[string]command.Handler {
	return map[string]command.Handler{
		"journal append": command.Typed(d.runJournalAppend),
	}
}

func (d *Daemon) runJournalAppend(_ context.Context, c command.Caller, a command.JournalAppendArgs) (any, error) {
	entry := strings.TrimSpace(a.Text)
	if entry == "" {
		return nil, command.Usage("{text} is required")
	}
	date := strings.TrimSpace(a.Date)
	if date == "" {
		date = time.Now().Format("2006-01-02")
	}
	fail := func(err error) error { return errors.New("journal append: " + err.Error()) }
	profileID, err := d.sessionNotebookProfile(c.Session)
	if err != nil {
		return nil, fail(err)
	}
	store, err := d.notebookStoreFor(profileID)
	if err != nil {
		return nil, fail(err)
	}
	var rel, hash string
	d.mutateRoot(store.Root(), func() []notebook.SelfWrite {
		if rel, hash, err = store.AppendJournal(date, entry); err != nil {
			return nil
		}
		return []notebook.SelfWrite{{Rel: rel, Hash: hash}}
	})
	if err != nil {
		return nil, fail(err)
	}
	d.broadcastFsChanged(store.Root(), originAgent, rel)
	return protocol.JournalAppendResult{RelPath: rel, Hash: hash}, nil
}
