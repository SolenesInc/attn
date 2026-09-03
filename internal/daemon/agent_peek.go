package daemon

import (
	"context"
	"encoding/json"
	"net"
	"strings"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
	"github.com/victorarias/attn/internal/transcript"
)

// Receipt (shared with ws_session_message.go): the largest prose block across
// 120 transcripts was 18,713 chars, so 64KiB is a tripwire.
const agentPeekMessageMaxChars = annotatableMessageMaxChars

const agentShortIDLength = 8

const agentPeekSnapshotTimeout = modelCaptureSnapshotTimeout

func (d *Daemon) handleAgentPeek(conn net.Conn, msg *protocol.AgentPeekMessage) {
	session, errCode := d.resolveAgentPeekTarget(msg.TargetSessionID)
	if session == nil {
		d.sendError(conn, errCode)
		return
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{
		Ok:              true,
		AgentPeekResult: d.agentPeekResult(session),
	})
}

func (d *Daemon) resolveAgentPeekTarget(target string) (*protocol.Session, string) {
	target = strings.TrimSpace(target)
	if target == "" {
		return nil, "session_not_found"
	}
	if session := d.store.Get(target); session != nil {
		return session, ""
	}
	if status, err := d.enrollmentStatus(); err == nil && status.IsHome() {
		member, found, err := d.resolveCrewMember(target)
		if err != nil {
			d.logf("agent peek crew resolution: target=%q err=%v", target, err)
			return nil, "internal_error"
		}
		if found {
			if !d.crewBindingLive(member) {
				return nil, "crew_member_asleep"
			}
			if session := d.store.Get(member.BindingSession); session != nil {
				return session, ""
			}
			return nil, "crew_member_asleep"
		}
	}
	return d.resolveSessionByIDOrPrefix(target)
}

func (d *Daemon) resolveSessionByIDOrPrefix(target string) (*protocol.Session, string) {
	target = strings.TrimSpace(target)
	if target == "" {
		return nil, "session_not_found"
	}
	if session := d.store.Get(target); session != nil {
		return session, ""
	}
	var match *protocol.Session
	for _, session := range d.store.List("") {
		if !strings.HasPrefix(session.ID, target) {
			continue
		}
		if match != nil {
			return nil, "ambiguous_session"
		}
		match = session
	}
	if match == nil {
		return nil, "session_not_found"
	}
	return match, ""
}

func (d *Daemon) agentPeekResult(session *protocol.Session) *protocol.AgentPeekResult {
	decorated := d.sessionForBroadcast(session)
	result := &protocol.AgentPeekResult{
		SessionID:   decorated.ID,
		Label:       decorated.Label,
		Agent:       string(decorated.Agent),
		WorkspaceID: decorated.WorkspaceID,
		State:       string(decorated.State),
		StateSince:  decorated.StateSince,
		LastSeen:    decorated.LastSeen,
		StateReason: decorated.StateReason,
		TurnOwed:    decorated.TurnOwed,
		Todos:       decorated.Todos,
		CrewMember:  decorated.CrewMember,
	}
	if result.Todos == nil {
		result.Todos = []string{}
	}
	if workspace := d.store.GetWorkspace(decorated.WorkspaceID); workspace != nil {
		result.WorkspaceTitle = protocol.Ptr(workspace.Title)
	}
	if path := d.inspectableTranscriptPath(session); path != "" {
		if message, err := transcript.ExtractLastAssistantMessage(path, agentPeekMessageMaxChars); err == nil && strings.TrimSpace(message) != "" {
			result.LastAssistantMessage = protocol.Ptr(message)
		}
	}
	result.Screen = d.agentPeekScreen(session.ID)
	if exit := d.store.GetSessionExitScreen(session.ID); exit != nil {
		result.Exit = &protocol.AgentPeekExit{Code: exit.ExitCode, At: exit.ExitedAt}
		if exit.ExitSignal != "" {
			result.Exit.Signal = protocol.Ptr(exit.ExitSignal)
		}
		if result.Screen == nil && strings.TrimSpace(exit.Text) != "" {
			result.Screen = &protocol.AgentPeekScreen{Text: exit.Text, Cols: exit.Cols, Rows: exit.Rows}
		}
	}
	return result
}

func (d *Daemon) agentPeekScreen(sessionID string) *protocol.AgentPeekScreen {
	provider, ok := d.ptyBackend.(ptybackend.ScreenSnapshotProvider)
	if !ok {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), agentPeekSnapshotTimeout)
	defer cancel()
	snapshot, err := provider.ScreenSnapshot(ctx, sessionID)
	if err != nil {
		d.logf("agent peek snapshot unavailable: session=%s err=%v", sessionID, err)
		return nil
	}
	if snapshot.Screen == nil || !snapshot.Screen.HasText {
		return nil
	}
	return &protocol.AgentPeekScreen{
		Text: snapshot.Screen.Text,
		Cols: int(snapshot.Screen.Cols),
		Rows: int(snapshot.Screen.Rows),
	}
}
