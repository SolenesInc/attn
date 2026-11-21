package daemon

import (
	"context"
	"fmt"

	"github.com/victorarias/attn/internal/command"

	"github.com/victorarias/attn/internal/protocol"
)

func (d *Daemon) runTerminalMove(_ context.Context, c command.Caller, a command.TerminalMoveArgs) (any, error) {
	caller, err := d.terminalCaller(c.Session)
	if err != nil {
		return nil, err
	}
	ref := a.Terminal
	if ref == "" {
		ref = string(caller.ID)
	}
	target, err := d.terminalTarget(caller, ref)
	if err != nil {
		return nil, err
	}
	if err := d.mayActOnTerminal(caller, target, "move"); err != nil {
		return nil, err
	}
	return d.moveTerminalToDesktop(target, a.Desktop)
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
		d.publishArrangement(profile.ID, leavesMoved(moved.Move))
	}
	return result, nil
}
