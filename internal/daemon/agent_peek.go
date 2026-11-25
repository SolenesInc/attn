package daemon

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/command"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/protocol"
)

const agentPeekMessageMaxChars = annotatableMessageMaxChars

const agentPeekSnapshotTimeout = 3 * time.Second

var errCrewMemberAsleep = errors.New("crew_member_asleep")

func (d *Daemon) runAgentPeek(ctx context.Context, c command.Caller, a command.AgentPeekArgs) (any, error) {
	session, err := d.resolveAgentPeekTarget(crewAsker{session: c.Session, profileID: c.Profile}, a.Target)
	if err == nil && !c.Sees(session.ProfileID) {
		err = errSessionNotFound
	}
	target := strings.TrimSpace(a.Target)
	switch {
	case err == nil:
	case errors.Is(err, errSessionNotFound):
		return nil, command.Refuse(command.CodeSessionNotFound, "no session or crew member matches %q; `{attn agent list}` names sessions and `{attn crew list}` names members", target)
	case errors.Is(err, errAmbiguousSession):
		return nil, command.Refuse(command.CodeAmbiguousSession, "%s", ambiguousSessionMessage(target, err, "%q matches more than one session; give more of the full id (`{attn agent list --json}` carries full ids)"))
	case errors.Is(err, errCrewMemberAsleep):
		return nil, command.Refuse(err.Error(), "%s is between sessions; `{attn agent peek}` never starts a session for a crew member. `{attn crew start %s}` starts one", target, target)
	default:
		return nil, err
	}
	if err := d.requireSessionHost(session.ID); err != nil {
		return nil, err
	}
	return d.agentPeekResult(session), nil
}

func (d *Daemon) resolveAgentPeekTarget(asker crewAsker, target string) (*protocol.Session, error) {
	target = protocol.TrimID(target)
	if target == "" {
		return nil, errSessionNotFound
	}
	if session := d.store.Get(protocol.SessionID(target)); session != nil {
		return session, nil
	}
	profileID, err := d.crewAskerProfile(asker)
	if err != nil {
		if session, resolveErr := d.resolveLiveSession(target, ""); !errors.Is(resolveErr, errSessionNotFound) {
			return session, resolveErr
		}
		namesMember, crewErr := d.namesMemberOfALiveProfile(target)
		if crewErr != nil {
			d.logf("agent peek crew resolution: target=%q err=%v", target, crewErr)
			return nil, errors.New("internal_error")
		}
		if namesMember {
			return nil, errors.New("crew names resolve in a profile: " + err.Error())
		}
		return d.liveSessionNamed(target, asker.profileID)
	}
	member, found, err := d.crewMemberIn(profileID, target)
	if err != nil {
		d.logf("agent peek crew resolution: target=%q err=%v", target, err)
		return nil, errors.New("internal_error")
	}
	if found {
		if !d.crewBindingLive(member) {
			return nil, errCrewMemberAsleep
		}
		if session := d.store.Get(member.BindingSession); session != nil {
			return session, nil
		}
		return nil, errCrewMemberAsleep
	}
	session, err := d.resolveLiveSession(target, "")
	if errors.Is(err, errSessionNotFound) {
		return d.liveSessionNamed(target, profileID)
	}
	return session, err
}

func (d *Daemon) namesMemberOfALiveProfile(target string) (bool, error) {
	live, err := d.store.ListProfiles(false)
	if err != nil {
		return false, err
	}
	for _, profile := range live {
		if _, found, err := d.crewMemberIn(profile.ID, target); err != nil || found {
			return found, err
		}
	}
	return false, nil
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
	if reader, ok := harness.As[harness.ScreenReader](d.harnessOf(session.Agent)); ok && reader.ShowsComposerSuggestion(snapshot) {
		screen.ComposerSuggestion = protocol.Ptr(true)
	}
	return screen
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
