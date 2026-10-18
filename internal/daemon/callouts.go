package daemon

import (
	"encoding/json"
	"fmt"
	"net"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

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
	{Name: "desktops", Description: "The desktop chips"},
	{Name: "queue", Description: "The waiting-turns card in queue flow"},
	{Name: "crew", Description: "The crew"},
	{Name: "settings-button", Description: "Open Settings"},
	{Name: "agent:<who>", Description: "An agent by session id, short id or crew name; Enter goes there"},
	{Name: "desktop:<which>", Description: "A desktop by slot digit, name or id; Enter goes there"},
}

type calloutReply struct {
	result protocol.CalloutResultMessage
	err    error
}

func (d *Daemon) calloutsAllowed(session *protocol.Session) error {
	if session == nil {
		return fmt.Errorf("session not found")
	}
	if !parseBooleanSetting(d.daemonSetting(settingCalloutsEnabled)) {
		return fmt.Errorf("callouts are off: the user can turn them on in Settings > Experimental > Agent callouts")
	}
	return nil
}

func (d *Daemon) calloutSteps(session *protocol.Session, raw []protocol.CalloutShowStep) ([]protocol.CalloutStep, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("a callout needs at least one step")
	}
	if len(raw) > calloutMaxSteps {
		return nil, fmt.Errorf("a callout has at most %d steps; this one has %d", calloutMaxSteps, len(raw))
	}
	steps := make([]protocol.CalloutStep, 0, len(raw))
	for i, input := range raw {
		length := utf8.RuneCountInString(input.Text)
		if length > calloutMaxTextCharacters {
			return nil, fmt.Errorf("step %d is %d characters; the limit is %d", i+1, length, calloutMaxTextCharacters)
		}
		if strings.TrimSpace(input.Text) == "" {
			return nil, fmt.Errorf("step %d needs its text", i+1)
		}
		anchor := strings.TrimSpace(input.Anchor)
		step := protocol.CalloutStep{Anchor: protocol.CalloutAnchor(anchor), Text: input.Text}
		switch {
		case strings.HasPrefix(anchor, "agent:"):
			b, err := d.bindings()
			if err != nil {
				return nil, err
			}
			r, err := d.requestFromSession(session.ID, b)
			if err != nil {
				return nil, err
			}
			target, err := d.resolveSession(r, b, strings.TrimPrefix(anchor, "agent:"))
			if err != nil || target.ProfileID != session.ProfileID {
				return nil, fmt.Errorf("%s is not an agent in your profile", anchor)
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
				return nil, fmt.Errorf("%s is not a desktop in your profile", anchor)
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
				return nil, fmt.Errorf("unknown anchor %q; anchors: %s", anchor, strings.Join(names, ", "))
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
	return nil, fmt.Errorf("the attn app is not showing your profile")
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

func (d *Daemon) handleCalloutShow(conn net.Conn, msg *protocol.CalloutShowMessage) {
	session := d.store.Get(msg.SessionID)
	if err := d.calloutsAllowed(session); err != nil {
		d.sendError(conn, err.Error())
		return
	}
	steps, err := d.calloutSteps(session, msg.Steps)
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	app, err := d.calloutApp(session.ProfileID)
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	result, err := d.requestCallout(app, session.ProfileID, func(id string) any {
		return protocol.CalloutShowRequestMessage{Event: protocol.EventCalloutShowRequest, RequestID: id, ProfileID: session.ProfileID, From: sessionDisplayName(session), Steps: steps}
	})
	if err == nil && len(result.Missing) > 0 {
		err = fmt.Errorf("not on screen: %s; on screen now: %s", calloutAnchorNames(result.Missing), calloutAnchorNames(result.OnScreen))
	}
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{Ok: true, Data: protocol.Ptr(fmt.Sprintf("showing a callout with %d %s; the user steps through it", len(steps), plural(len(steps), "step", "steps")))})
}

func (d *Daemon) handleCalloutAnchors(conn net.Conn, msg *protocol.CalloutAnchorsMessage) {
	session := d.store.Get(msg.SessionID)
	if err := d.calloutsAllowed(session); err != nil {
		d.sendError(conn, err.Error())
		return
	}
	app, err := d.calloutApp(session.ProfileID)
	result := protocol.CalloutAnchorsResult{Anchors: slices.Clone(calloutAnchorDirectory), AppShowingProfile: app != nil}
	if err == nil {
		reply, err := d.requestCallout(app, session.ProfileID, func(id string) any {
			return protocol.CalloutAnchorsRequestMessage{Event: protocol.EventCalloutAnchorsRequest, RequestID: id, ProfileID: session.ProfileID}
		})
		if err != nil {
			d.sendError(conn, err.Error())
			return
		}
		for i, info := range result.Anchors {
			if strings.Contains(info.Name, ":") {
				continue
			}
			result.Anchors[i].OnScreen = protocol.Ptr(slices.Contains(reply.OnScreen, protocol.CalloutAnchor(result.Anchors[i].Name)))
		}
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{Ok: true, CalloutAnchorsResult: &result})
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
