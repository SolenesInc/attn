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

const agentPeekMessageMaxChars = annotatableMessageMaxChars

const agentShortIDLength = 8

const agentPeekSnapshotTimeout = modelCaptureSnapshotTimeout

func (d *Daemon) handleAgentPeek(conn net.Conn, msg *protocol.AgentPeekMessage) {
	b, err := d.bindings()
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	r, err := d.requestFromMessage(msg.SourceSessionID, msg.ProfileID, b)
	if err != nil {
		d.replyTargetError(conn, err)
		return
	}
	session, err := d.resolveSession(r, b, msg.To)
	if err != nil {
		d.replyTargetError(conn, err)
		return
	}
	if endpoint := d.sessionOwnerEndpoint(session.ID); endpoint != "" {
		d.replyAgentMsgError(conn, "remote_delivery_unsupported", "session "+shortSessionID(session.ID)+" runs on outpost "+endpoint+"; peeking outpost sessions is unsupported")
		return
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{Ok: true, AgentPeekResult: d.agentPeekResult(session)})
}

func (d *Daemon) resolveSessionByIDOrPrefix(target, profileID string) (*protocol.Session, string) {
	target = protocol.TrimID(target)
	if target == "" {
		return nil, "session_not_found"
	}
	if session := d.store.Get(protocol.SessionID(target)); session != nil && (profileID == "" || session.ProfileID == profileID) {
		return session, ""
	}
	if d.hubManager != nil {
		if s := d.hubManager.RemoteSession(protocol.SessionID(target)); s != nil && (profileID == "" || s.ProfileID == profileID) {
			return s, ""
		}
	}
	var match *protocol.Session
	for _, session := range d.agentCloseCandidates() {
		if (profileID != "" && session.ProfileID != profileID) || !strings.HasPrefix(string(session.ID), target) {
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
		SessionID:      decorated.ID,
		Label:          decorated.Label,
		Agent:          decorated.Agent,
		State:          string(decorated.State),
		StateSince:     decorated.StateSince,
		LastSeen:       decorated.LastSeen,
		StateReason:    decorated.StateReason,
		TurnOwed:       decorated.TurnOwed,
		CrewMember:     decorated.CrewMember,
		CrewMemberName: decorated.CrewMemberName,
	}
	if profile, err := d.store.GetProfile(decorated.ProfileID); err == nil {
		result.ProfileName = protocol.Ptr(profile.Name)
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

func (d *Daemon) agentPeekScreen(sessionID protocol.SessionID) *protocol.AgentPeekScreen {
	provider, ok := d.ptyBackend.(ptybackend.ScreenSnapshotProvider)
	if !ok {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), agentPeekSnapshotTimeout)
	defer cancel()
	snapshot, err := provider.ScreenSnapshot(ctx, d.primaryTerminal(sessionID))
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
