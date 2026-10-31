package daemon

import (
	"context"
	"encoding/json"
	"net"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

const agentPeekMessageMaxChars = annotatableMessageMaxChars

const agentShortIDLength = 8

const agentPeekSnapshotTimeout = 3 * time.Second

func (d *Daemon) handleAgentPeek(conn net.Conn, msg *protocol.AgentPeekMessage) {
	session, errCode := d.resolveAgentPeekTarget(askerOf(msg.SourceSessionID, msg.ProfileID), msg.TargetSessionID)
	if session == nil {
		d.sendError(conn, errCode)
		return
	}
	if err := d.requireSessionHost(session.ID); err != nil {
		d.sendError(conn, err.Error())
		return
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{
		Ok:              true,
		AgentPeekResult: d.agentPeekResult(session),
	})
}

func (d *Daemon) resolveAgentPeekTarget(asker crewAsker, target string) (*protocol.Session, string) {
	target = protocol.TrimID(target)
	if target == "" {
		return nil, "session_not_found"
	}
	if session := d.store.Get(protocol.SessionID(target)); session != nil {
		return session, ""
	}
	profileID, err := d.crewAskerProfile(asker)
	if err != nil {
		if session, code := d.resolveSessionByIDOrPrefix(target); session != nil || code != "session_not_found" {
			return session, code
		}
		return nil, "crew names resolve in a profile: " + err.Error()
	}
	member, found, err := d.crewMemberIn(profileID, target)
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
	return d.resolveSessionByIDOrPrefix(target)
}

func (d *Daemon) resolveSessionByIDOrPrefix(target string) (*protocol.Session, string) {
	return d.resolveSessionReference(target, "")
}

func (d *Daemon) resolveSessionReference(target, profileID string) (*protocol.Session, string) {
	target = protocol.TrimID(target)
	if target == "" {
		return nil, "session_not_found"
	}
	if session := d.store.Get(protocol.SessionID(target)); session != nil && (profileID == "" || session.ProfileID == profileID) {
		return session, ""
	}
	var match *protocol.Session
	for _, session := range d.store.List("") {
		if profileID != "" && session.ProfileID != profileID {
			continue
		}
		if !strings.HasPrefix(string(session.ID), target) {
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
	answer, err := Call[protocol.MonitorLastAssistantResult](context.Background(), d.router(), protocol.MonitorLastAssistantMessage{Cmd: protocol.CmdMonitorLastAssistant, SessionID: session.ID})
	if err == nil && strings.TrimSpace(answer.Text) != "" {
		result.LastAssistantMessage = protocol.Ptr(answer.Text)
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
	ctx, cancel := context.WithTimeout(context.Background(), agentPeekSnapshotTimeout)
	defer cancel()
	snapshot, err := Call[protocol.GetScreenSnapshotResultMessage](ctx, d.router(), protocol.GetScreenSnapshotMessage{Cmd: protocol.CmdGetScreenSnapshot, ID: protocol.TerminalID(d.primaryTerminal(sessionID))})
	if err != nil {
		d.logf("agent peek snapshot unavailable: session=%s err=%v", sessionID, err)
		return nil
	}
	text, err := Call[protocol.TerminalText](ctx, d.router(), protocol.TerminalReadTextMessage{Cmd: protocol.CmdTerminalReadText, TerminalID: d.primaryTerminal(sessionID), Mode: protocol.TerminalReadModeScreen})
	if err != nil || text.Text == "" {
		return nil
	}
	return &protocol.AgentPeekScreen{
		Text: text.Text,
		Cols: protocol.Deref(snapshot.Cols),
		Rows: protocol.Deref(snapshot.Rows),
	}
}
