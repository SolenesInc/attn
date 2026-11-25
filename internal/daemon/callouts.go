package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/victorarias/attn/internal/command"
	"github.com/victorarias/attn/internal/profiles"
	"github.com/victorarias/attn/internal/protocol"
)

const calloutMaxSteps = 20
const calloutMaxTextCharacters = 160
const calloutReplyTimeout = 10 * time.Second

var calloutAnchorDirectory = []protocol.CalloutAnchorInfo{
	{Name: "sidebar", Description: "The sidebar in its current shape"},
	{Name: "home", Description: "Home"},
	{Name: "commands", Description: "Search and run commands"},
	{Name: "garden", Description: "Work tracked in the garden"},
	{Name: "asks", Description: "Questions from agents, answered in one place"},
	{Name: "desktops", Description: "The desktop chips"},
	{Name: "queue", Description: "The waiting-turns card in queue flow"},
	{Name: "crew", Description: "The crew"},
	{Name: "settings-button", Description: "Open Settings"},
	{Name: "agent:<who>", Description: "An agent by its name, session id, short id or crew name; Enter on the last step goes there"},
	{Name: "desktop:<which>", Description: "A desktop by slot digit, name or id; Enter on the last step goes there"},
}

type calloutReply struct {
	result protocol.CalloutResultMessage
	err    error
}

func (d *Daemon) calloutSteps(session *protocol.Session, raw []protocol.CalloutShowStep) ([]protocol.CalloutStep, error) {
	if len(raw) == 0 {
		return nil, command.Usage("a callout needs at least one step")
	}
	if len(raw) > calloutMaxSteps {
		return nil, command.Refuse(command.CodeTooManySteps, "a callout has at most %d steps; this one has %d", calloutMaxSteps, len(raw))
	}
	steps := make([]protocol.CalloutStep, 0, len(raw))
	for i, input := range raw {
		length := utf8.RuneCountInString(input.Text)
		if length > calloutMaxTextCharacters {
			return nil, command.Refuse(command.CodeStepTooLong, "step %d is %d characters; the limit is %d", i+1, length, calloutMaxTextCharacters)
		}
		if strings.TrimSpace(input.Text) == "" {
			return nil, command.Refuse(command.CodeStepNeedsText, "step %d needs its text", i+1)
		}
		anchor := strings.TrimSpace(input.Anchor)
		step := protocol.CalloutStep{Anchor: protocol.CalloutAnchor(anchor), Text: input.Text}
		switch {
		case strings.HasPrefix(anchor, "agent:"):
			target, err := d.resolveAgentPeekTarget(crewAsker{profileID: session.ProfileID}, strings.TrimPrefix(anchor, "agent:"))
			if err != nil || target.ProfileID != session.ProfileID {
				return nil, command.Refuse(command.CodeUnknownAnchor, "%s is not an agent in your profile", anchor)
			}
			step.Anchor = protocol.CalloutAnchorAgent
			step.SessionID = protocol.Ptr(target.ID)
			step.Label = protocol.Ptr(sessionDisplayName(target))
		case strings.HasPrefix(anchor, "desktop:"):
			profile, err := d.store.GetProfile(session.ProfileID)
			if err != nil {
				return nil, err
			}
			desktop, err := d.resolveDesktopRef(profile, strings.TrimPrefix(anchor, "desktop:"))
			if err != nil {
				return nil, command.Refuse(command.CodeUnknownAnchor, "%s is not a desktop in your profile", anchor)
			}
			step.Anchor = protocol.CalloutAnchorDesktop
			step.DesktopID = protocol.Ptr(desktop.ID)
			_, desktops, err := d.store.ProfileArrangement(profile.ID)
			if err != nil {
				return nil, err
			}
			step.Label = protocol.Ptr(profiles.DesktopLabel(desktop, desktops))
		default:
			if !slices.ContainsFunc(calloutAnchorDirectory, func(info protocol.CalloutAnchorInfo) bool {
				return info.Name == anchor && !strings.Contains(info.Name, ":")
			}) {
				names := make([]string, 0, len(calloutAnchorDirectory))
				for _, info := range calloutAnchorDirectory {
					names = append(names, info.Name)
				}
				return nil, command.Refuse(command.CodeUnknownAnchor, "unknown anchor %q; anchors: %s", anchor, strings.Join(names, ", "))
			}
		}
		steps = append(steps, step)
	}
	return steps, nil
}

func (d *Daemon) calloutApp(profileID string) (*wsClient, error) {
	if d.wsHub != nil {
		if app := d.wsHub.NewestClientMatching(func(client *wsClient) bool {
			return client.isTrustedAppClient() && client.selectedProfile() == profileID
		}); app != nil {
			return app, nil
		}
	}
	return nil, command.Refuse(command.CodeAppNotShowingProfile, "the attn app is not showing your profile")
}

func (d *Daemon) requestCallout(app *wsClient, profileID string, message func(string) any) (protocol.CalloutResultMessage, error) {
	reply, err := d.calloutRequests.ask(app, calloutReplyTimeout, func(requestID string) bool {
		payload, err := json.Marshal(message(requestID))
		if err != nil {
			return false
		}
		return d.sendOutbound(app, outboundMessage{kind: messageKindText, payload: payload})
	}, calloutReply{err: fmt.Errorf("the attn app is unavailable")}, calloutReply{err: fmt.Errorf("the attn app did not answer within %s", calloutReplyTimeout)})
	if err != nil {
		return protocol.CalloutResultMessage{}, err
	}
	if app.selectedProfile() != profileID {
		return protocol.CalloutResultMessage{}, fmt.Errorf("the attn app switched away from your profile")
	}
	return reply.result, reply.err
}

func (d *Daemon) calloutCommands() map[string]command.Handler {
	return map[string]command.Handler{
		"callout anchors": command.Typed(d.runCalloutAnchors),
		"callout show":    command.Typed(d.runCalloutShow),
	}
}

func (d *Daemon) calloutSession(c command.Caller) (*protocol.Session, error) {
	if c.Session == "" {
		return nil, command.Refuse(command.CodeNoSession, "no session; pass {session} <id>, or run inside an attn session")
	}
	session := d.store.Get(c.Session)
	if session == nil {
		return nil, command.Refuse(command.CodeUnknownSession, "session not found")
	}
	return session, nil
}

func (d *Daemon) runCalloutShow(ctx context.Context, c command.Caller, a command.CalloutShowArgs) (any, error) {
	session, err := d.calloutSession(c)
	if err != nil {
		return nil, err
	}
	var raw []protocol.CalloutShowStep
	if err := json.Unmarshal(a.Steps, &raw); err != nil {
		return nil, command.Usage("{steps}: %v", err)
	}
	steps, err := d.calloutSteps(session, raw)
	if err != nil {
		return nil, err
	}
	app, err := d.calloutApp(session.ProfileID)
	if err != nil {
		return nil, err
	}
	result, err := d.requestCallout(app, session.ProfileID, func(id string) any {
		return protocol.CalloutShowRequestMessage{Event: protocol.EventCalloutShowRequest, RequestID: id, ProfileID: session.ProfileID, From: sessionDisplayName(session), Steps: steps}
	})
	if err != nil {
		return nil, err
	}
	if len(result.Missing) > 0 {
		return nil, command.Refuse(command.CodeNotOnScreen, "not on screen: %s; on screen now: %s", calloutAnchorNames(result.Missing), calloutAnchorNames(result.OnScreen))
	}
	return protocol.CalloutShowResult{Steps: len(steps)}, nil
}

func (d *Daemon) runCalloutAnchors(ctx context.Context, c command.Caller, _ command.CalloutAnchorsArgs) (any, error) {
	session, err := d.calloutSession(c)
	if err != nil {
		return nil, err
	}
	app, err := d.calloutApp(session.ProfileID)
	result := protocol.CalloutAnchorsResult{Anchors: slices.Clone(calloutAnchorDirectory), AppShowingProfile: app != nil}
	if err != nil {
		return result, nil
	}
	reply, err := d.requestCallout(app, session.ProfileID, func(id string) any {
		return protocol.CalloutAnchorsRequestMessage{Event: protocol.EventCalloutAnchorsRequest, RequestID: id, ProfileID: session.ProfileID}
	})
	if err != nil {
		return nil, err
	}
	for i, info := range result.Anchors {
		if strings.Contains(info.Name, ":") {
			continue
		}
		result.Anchors[i].OnScreen = protocol.Ptr(slices.Contains(reply.OnScreen, protocol.CalloutAnchor(result.Anchors[i].Name)))
	}
	return result, nil
}

func calloutAnchorNames(anchors []protocol.CalloutAnchor) string {
	if len(anchors) == 0 {
		return "none"
	}
	names := make([]string, len(anchors))
	for i, anchor := range anchors {
		names[i] = string(anchor)
	}
	return strings.Join(names, ", ")
}

func (d *Daemon) handleCalloutResult(client *wsClient, msg *protocol.CalloutResultMessage) {
	if !client.isTrustedAppClient() {
		d.sendCommandError(client, protocol.CmdCalloutResult, "trusted app required")
		return
	}
	d.calloutRequests.answer(client, msg.RequestID, calloutReply{result: *msg})
}

func plural(count int, one, many string) string {
	if count == 1 {
		return one
	}
	return many
}
