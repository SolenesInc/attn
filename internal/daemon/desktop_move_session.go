package daemon

import (
	"encoding/json"
	"fmt"
	"net"
	"strings"

	"github.com/victorarias/attn/internal/protocol"
)

func (d *Daemon) handleDesktopMoveSession(conn net.Conn, msg *protocol.DesktopMoveSessionMessage) {
	result, err := d.moveSessionToDesktop(strings.TrimSpace(protocol.Deref(msg.CallerSessionID)), strings.TrimSpace(msg.SessionID), msg.Desktop)
	if err != nil {
		d.sendError(conn, "desktop move: "+err.Error())
		return
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{Ok: true, DesktopMoveSessionResult: result})
}

func (d *Daemon) moveSessionToDesktop(callerID, sessionID, ref string) (*protocol.DesktopMoveSessionResult, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("no session to move; pass --session or run inside attn")
	}
	session := d.store.Get(sessionID)
	if session == nil {
		return nil, fmt.Errorf("session %s is not a live session", sessionID)
	}
	if err := d.mayMoveSession(callerID, session); err != nil {
		return nil, err
	}
	profile, err := d.liveLaunchProfile(session.ProfileID)
	if err != nil {
		return nil, fmt.Errorf("session %s: %w", sessionID, err)
	}
	desktop, err := d.resolveDesktopRef(profile, ref)
	if err != nil {
		return nil, err
	}
	moved, err := d.store.MoveSessionToDesktop(sessionID, desktop.ID, session.Label)
	if err != nil {
		return nil, err
	}
	result := &protocol.DesktopMoveSessionResult{SessionID: sessionID, DesktopID: moved.Move.Target.ID, PaneID: moved.Move.FinalLeafID}
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

// mayMoveSession lets a session move itself and its delegates, and the chief
// move any session of its profile; no caller is the user.
func (d *Daemon) mayMoveSession(callerID string, session *protocol.Session) error {
	if callerID == "" || callerID == session.ID {
		return nil
	}
	if dispatcher, ok := d.liveSessionForTender(d.gardenDispatchersBySession()[session.ID]); ok && dispatcher == callerID {
		return nil
	}
	if d.isChiefOfStaffSession(callerID) && d.chiefOfProfile(session.ProfileID) == callerID {
		return nil
	}
	return fmt.Errorf("session %s may move itself, the sessions it dispatched, or any session as its profile's chief; %s (%s) is none of those", callerID, session.ID, session.Label)
}
