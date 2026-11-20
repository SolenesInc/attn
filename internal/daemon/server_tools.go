package daemon

import (
	"encoding/json"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/tooldetect"
)

func (d *Daemon) serverToolFinished(sessionID protocol.SessionID, name string, input json.RawMessage, output string) {
	session := d.store.Get(sessionID)
	if session == nil {
		return
	}
	if edited := tooldetect.MarkdownEdits(name, input, session.Directory); len(edited) > 0 {
		d.recordEditedFiles(sessionID, edited)
	}
	for _, url := range tooldetect.PullRequestsCreated(name, input, output) {
		rec, err := d.sessionPullRequestIdentity("", sessionID, url)
		if err == nil {
			err = d.recordSessionPullRequest(rec)
		}
		if err != nil {
			d.logf("session %s: recording pull request %s from its %s call: %v", sessionID, url, name, err)
		}
	}
}

func (d *Daemon) recordEditedFiles(sessionID protocol.SessionID, paths []string) bool {
	now := time.Now()
	if monitor := d.sessionMonitor(sessionID); monitor != nil {
		return monitor.ReportEditedFiles(paths, now, d.awayFromHome(sessionID))
	}
	for _, path := range paths {
		d.store.RecordFileActivity(store.FileActivity{Path: path, Source: store.FileActivitySourceEdited, SessionID: sessionID, At: now})
	}
	return true
}
