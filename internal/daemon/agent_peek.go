package daemon

import (
	"context"
	"encoding/base64"
	"errors"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/victorarias/attn/internal/command"
	"github.com/victorarias/attn/internal/protocol"
)

const agentPeekMessageMaxChars = annotatableMessageMaxChars

const agentShortIDLength = 8

const agentPeekSnapshotTimeout = 3 * time.Second

var claudeSuggestionCells = sync.OnceValue(func() *regexp.Regexp {
	const sgr = `\x1b\[[0-9;]*m`
	return regexp.MustCompile(`❯[ \x{a0}](?:` + sgr + `)*\x1b\[(?:[0-9;]*;)?7m[^\x1b](?:` + sgr + `)*\x1b\[(?:[0-9;]*;)?2m`)
})

func (d *Daemon) runAgentPeek(ctx context.Context, c command.Caller, a command.AgentPeekArgs) (any, error) {
	session, errCode := d.resolveAgentPeekTarget(crewAsker{session: c.Session, profileID: c.Profile}, a.Target)
	if session != nil {
		if scope := d.callerScope(c); scope != "" && session.ProfileID != scope {
			session, errCode = nil, "session_not_found"
		}
	}
	target := strings.TrimSpace(a.Target)
	switch {
	case session != nil:
	case errCode == "session_not_found":
		return nil, command.Refuse(errCode, "no session or crew member matches %q; `attn agent list` names sessions and `attn crew list` names members", target)
	case errCode == "ambiguous_session":
		return nil, command.Refuse(errCode, "%q matches more than one session; give more of the id (`attn agent list --json` carries full ids)", target)
	case errCode == "crew_member_asleep":
		return nil, command.Refuse(errCode, "%s is between sessions; `attn agent peek` never starts a session for a crew member. `attn crew start %s` starts one", target, target)
	default:
		return nil, errors.New(errCode)
	}
	if err := d.requireSessionHost(session.ID); err != nil {
		return nil, err
	}
	return d.agentPeekResult(session), nil
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

	result.Screen = d.agentPeekScreen(session)
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

func (d *Daemon) agentPeekScreen(session *protocol.Session) *protocol.AgentPeekScreen {
	sessionID := session.ID
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
	screen := &protocol.AgentPeekScreen{
		Text: text.Text,
		Cols: protocol.Deref(snapshot.Cols),
		Rows: protocol.Deref(snapshot.Rows),
	}
	if session.Agent == protocol.SessionAgentClaude && snapshotShowsClaudeSuggestion(snapshot) {
		screen.ComposerSuggestion = protocol.Ptr(true)
	}
	return screen
}

func snapshotShowsClaudeSuggestion(snapshot protocol.GetScreenSnapshotResultMessage) bool {
	cells, err := base64.StdEncoding.DecodeString(protocol.Deref(snapshot.ScreenSnapshot))
	return err == nil && claudeSuggestionCells().Match(cells)
}

func (d *Daemon) sendTerminalReadTextWSResult(client *wsClient, msg *protocol.TerminalReadTextMessage) {
	ctx, cancel := context.WithTimeout(context.Background(), agentPeekSnapshotTimeout)
	defer cancel()
	reply := protocol.TerminalReadTextResultMessage{
		Event:     protocol.EventTerminalReadTextResult,
		RequestID: protocol.Deref(msg.RequestID),
	}
	text, err := Call[protocol.TerminalText](ctx, d.router(), protocol.TerminalReadTextMessage{Cmd: protocol.CmdTerminalReadText, TerminalID: msg.TerminalID, Mode: msg.Mode, Lines: msg.Lines})
	if err != nil {
		reply.Error = protocol.Ptr(err.Error())
	} else {
		reply.Success = true
		reply.Text = protocol.Ptr(text.Text)
	}
	d.sendToClient(client, reply)
}
