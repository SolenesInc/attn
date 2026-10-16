package daemon

import (
	"encoding/json"
	"fmt"
	"net"

	"github.com/victorarias/attn/internal/protocol"
)

func (d *Daemon) handleDesktopMoveSession(conn net.Conn, msg *protocol.DesktopMoveSessionMessage) {
	result, err := d.moveSessionToDesktop(protocol.TrimID(protocol.Deref(msg.CallerSessionID)), protocol.TrimID(msg.SessionID), msg.Desktop)
	if err != nil {
		d.sendError(conn, "desktop move: "+err.Error())
		return
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{Ok: true, DesktopMoveSessionResult: result})
}

func (d *Daemon) moveSessionToDesktop(callerID protocol.SessionID, sessionID protocol.SessionID, ref string) (*protocol.DesktopMoveSessionResult, error) {
	if sessionID == "" {
		sessionID = callerID
	}
	if sessionID == "" {
		return nil, fmt.Errorf("no session to move; pass --session or run inside attn")
	}
	session := d.store.Get(sessionID)
	if session == nil {
		return nil, fmt.Errorf("session %s is not a live session", sessionID)
	}
	if err := d.mayActOnSession(callerID, session, "move"); err != nil {
		return nil, err
	}
	profile, err := d.liveLaunchProfile(session.ProfileID)
	if err != nil {
		return nil, fmt.Errorf("session %s: %w", sessionID, err)
	}
	desktopID, err := d.resolveDesktopTarget(profile, ref)
	if err != nil {
		return nil, err
	}
	moved, err := d.store.MoveSessionToDesktop(sessionID, desktopID, session.Label)
	if err != nil {
		return nil, err
	}
	result := &protocol.DesktopMoveSessionResult{SessionID: sessionID, DesktopID: moved.Move.Target.ID, PaneID: moved.Move.FinalLeafID}
	if moved.Created {
		result.DesktopCreated = protocol.Ptr(true)
	}
	switch {
	case moved.Placed:
		d.publishArrangementChanged(profile.ID)
	case moved.Move.Source.ID == "":
		result.Unchanged = protocol.Ptr(true)
	default:
		result.FromDesktopID = protocol.Ptr(moved.Move.Source.ID)
		d.publishArrangement(profile.ID, &protocol.LeafMoved{
			FromDesktopID: moved.Move.Source.ID, FromLeafID: moved.FromLeafID,
			ToDesktopID: moved.Move.Target.ID, ToLeafID: moved.Move.FinalLeafID,
		})
	}
	return result, nil
}
