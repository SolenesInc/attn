package daemon

import (
	"encoding/json"
	"os"

	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/tooldetect"
)

func (d *Daemon) serverToolFinished(sessionID protocol.SessionID, name string, input json.RawMessage, output string) {
	session := d.store.Get(sessionID)
	if session == nil {
		return
	}
	home, _ := os.UserHomeDir()
	use := harness.ToolUse{EditedFiles: tooldetect.MarkdownEdits(name, input, session.Directory, home), PullRequests: tooldetect.PullRequestsCreated(name, input, output)}
	if len(use.EditedFiles) == 0 && len(use.PullRequests) == 0 {
		return
	}
	if err := d.events().Tool(sessionID, use); err != nil {
		d.logf("session %s: recording what its %s call did: %v", sessionID, name, err)
	}
}
