package appserver

import (
	"encoding/json"
	"os"

	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/tooldetect"
)

func (c *Client) serverToolFinished(sessionID protocol.SessionID, name string, input json.RawMessage, output string) {
	session := c.host.Sessions().Get(sessionID)
	if session == nil {
		return
	}
	home, _ := os.UserHomeDir()
	use := harness.ToolUse{EditedFiles: tooldetect.MarkdownEdits(name, input, session.Directory, home), PullRequests: tooldetect.PullRequestsCreated(name, input, output)}
	if len(use.EditedFiles) == 0 && len(use.PullRequests) == 0 {
		return
	}
	if err := c.host.Events().Tool(sessionID, use); err != nil {
		c.host.Logf("session %s: recording what its %s call did: %v", sessionID, name, err)
	}
}
