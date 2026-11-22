package daemon

import (
	"strings"

	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/protocol"
)

func resumedOrCleared(req harness.LaunchRequest) harness.LaunchConversation {
	if req.Conversation != "" {
		return harness.ConversationIs(req.Conversation)
	}
	return harness.ConversationCleared
}

func (d *Daemon) resumeTarget(agent string, session protocol.SessionID, requested string) string {
	requested = strings.TrimSpace(requested)
	if !d.factsOrNone(agent).Options.Resume {
		return requested
	}
	stored := strings.TrimSpace(d.store.GetResumeSessionID(session))
	if stored != "" && (requested == "" || requested == string(session)) {
		return stored
	}
	return requested
}

func (d *Daemon) recordLaunchConversation(session protocol.SessionID, planned harness.Planned, what string) {
	conversation, records := planned.Conversation.Recorded()
	if !records || d.store.GetResumeSessionID(session) == conversation {
		return
	}
	if conversation == "" {
		d.store.SetResumeSessionID(session, "")
		return
	}
	if _, err := d.store.TransitionSessionResumeID(session, conversation); err != nil {
		d.logf("%s: persist launched conversation for session %s: %v", what, session, err)
	}
}
