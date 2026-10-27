package daemon

import (
	"encoding/json"
	"fmt"
	"net"

	"github.com/victorarias/attn/internal/protocol"
)

func (d *Daemon) handleTerminalMove(conn net.Conn, msg *protocol.TerminalMoveMessage) {
	caller, err := d.terminalCaller(msg.CallerSessionID)
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	ref := msg.Target
	if ref == "" {
		ref = string(caller.ID)
	}
	target, err := d.terminalTarget(caller, ref)
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	if err := d.mayActOnTerminal(caller, target, "move"); err != nil {
		d.sendError(conn, err.Error())
		return
	}
	result, err := d.moveTerminalToDesktop(target, msg.Desktop)
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{Ok: true, TerminalMoveResult: result})
}

func (d *Daemon) moveTerminalToDesktop(target *selectedTerminal, ref string) (*protocol.TerminalMoveResult, error) {
	profile, err := d.liveLaunchProfile(target.ProfileID)
	if err != nil {
		return nil, fmt.Errorf("session %s: %w", target.ID, err)
	}
	desktopID, err := d.resolveDesktopTarget(profile, ref)
	if err != nil {
		return nil, err
	}
	moved, err := d.store.MoveTerminalToDesktop(target.terminal, target.ID, desktopID, target.Label)
	if err != nil {
		return nil, err
	}
	result := &protocol.TerminalMoveResult{TerminalID: target.terminal, SessionID: target.ID, DesktopID: moved.Move.Target.ID, PaneID: moved.Move.FinalLeafID}
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
