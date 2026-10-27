package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/layouttree"
	"github.com/victorarias/attn/internal/profiles"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/store"
)

const terminalFirstPromptTripwire = 30 * time.Second
const terminalFirstPromptReceipt = "first prompt measured at 9-176 ms across fish, zsh and bash"

func (d *Daemon) terminalCaller(id protocol.SessionID) (*protocol.Session, error) {
	caller := d.store.Get(protocol.TrimID(id))
	if caller == nil {
		return nil, fmt.Errorf("caller rule: attn terminal requires a live calling agent; run it inside an attn agent session")
	}
	return caller, nil
}

type selectedTerminal struct {
	*protocol.Session
	terminal harness.TerminalID
}

func (d *Daemon) terminalTarget(caller *protocol.Session, ref string) (*selectedTerminal, error) {
	ref = strings.TrimSpace(ref)
	var terminalMatch *selectedTerminal
	var terminalMatches []string
	for _, binding := range d.terminals().Bindings() {
		session := d.store.Get(binding.SessionID)
		if session == nil || session.ProfileID != caller.ProfileID {
			continue
		}
		target := &selectedTerminal{Session: session, terminal: binding.TerminalID}
		if string(binding.TerminalID) == ref {
			return target, nil
		}
		if ref != "" && strings.HasPrefix(string(binding.TerminalID), ref) {
			terminalMatch = target
			terminalMatches = append(terminalMatches, string(binding.TerminalID))
		}
	}
	if len(terminalMatches) > 1 {
		return nil, fmt.Errorf("target rule: %q matches terminals %s; use a longer terminal id", ref, strings.Join(terminalMatches, ", "))
	}
	if terminalMatch != nil {
		return terminalMatch, nil
	}
	var match *protocol.Session
	for _, session := range d.store.List("") {
		if session.ProfileID != caller.ProfileID {
			continue
		}
		if string(session.ID) == ref {
			match = session
			break
		}
		if ref != "" && strings.HasPrefix(string(session.ID), ref) {
			if match != nil {
				return nil, fmt.Errorf("target rule: %q matches multiple sessions; use a terminal id from attn terminal ls --all", ref)
			}
			match = session
		}
	}
	if match == nil {
		member, found, err := d.resolveCrewMember(ref)
		if err != nil {
			return nil, err
		}
		if found && d.crewProfileID(member.ID) == caller.ProfileID {
			if session := d.store.Get(member.BindingSession); session != nil && session.ProfileID == caller.ProfileID {
				match = session
			}
		}
	}
	if match == nil {
		return nil, fmt.Errorf("target rule: terminal %q not found in the caller's profile; attn terminal ls --all lists its terminals", ref)
	}
	ids := d.terminalsOf(match.ID)
	if len(ids) != 1 {
		terminals := make([]string, len(ids))
		for i, id := range ids {
			terminals[i] = string(id)
		}
		return nil, fmt.Errorf("target rule: session %s shows in %d terminals (%s); pass one terminal id from attn terminal ls --all", match.ID, len(ids), strings.Join(terminals, ", "))
	}
	return &selectedTerminal{Session: match, terminal: ids[0]}, nil
}

func (d *Daemon) terminalDesktop(caller *protocol.Session, ref string) (profiles.Desktop, error) {
	profile, desktops, err := d.store.ProfileArrangement(caller.ProfileID)
	if err != nil {
		return profiles.Desktop{}, err
	}
	if ref != "" {
		return profiles.ResolveDesktopRef(profile, desktops, ref)
	}
	var found profiles.Desktop
	for _, desktop := range desktops {
		for _, pane := range desktop.Panes {
			if pane.SessionID != caller.ID {
				continue
			}
			if found.ID != "" && found.ID != desktop.ID {
				return profiles.Desktop{}, fmt.Errorf("desktop rule: caller has terminals on several desktops; pass --desktop <ref>")
			}
			found = desktop
		}
	}
	if found.ID == "" {
		return profiles.Desktop{}, fmt.Errorf("desktop rule: caller has no tile; pass --desktop <ref>")
	}
	return found, nil
}

func (d *Daemon) terminalAct(caller *protocol.Session, target *selectedTerminal, desktopRef string) error {
	_, desktops, err := d.store.ProfileArrangement(caller.ProfileID)
	if err != nil {
		return err
	}
	placed := ""
	for _, desktop := range desktops {
		for _, pane := range desktop.Panes {
			if pane.RuntimeID == target.terminal {
				placed = desktop.ID
			}
		}
	}
	if placed == "" {
		return fmt.Errorf("terminal rule: terminal %s has no tile", target.terminal)
	}
	desktop, err := d.terminalDesktop(caller, desktopRef)
	if err != nil {
		return err
	}
	if desktop.ID != placed {
		return fmt.Errorf("desktop rule: terminal %s is on another desktop; pass --desktop %s to act there", target.terminal, placed)
	}
	if target.Agent != protocol.AgentShellValue {
		return d.mayActOnSession(caller.ID, target.Session, "type into")
	}
	return nil
}

func (d *Daemon) connectionContext(parent context.Context, conn net.Conn) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	d.life.Go("commandDisconnect", func() { _, _ = io.Copy(io.Discard, conn); cancel() })
	return ctx, cancel
}

func (d *Daemon) handleTerminalList(conn net.Conn, msg *protocol.TerminalListMessage) {
	caller, err := d.terminalCaller(msg.CallerSessionID)
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	_, desktops, err := d.store.ProfileArrangement(caller.ProfileID)
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	filter := ""
	if !protocol.Deref(msg.All) || protocol.Deref(msg.Desktop) != "" {
		desktop, err := d.terminalDesktop(caller, protocol.Deref(msg.Desktop))
		if err != nil {
			d.sendError(conn, err.Error())
			return
		}
		filter = desktop.ID
	}
	entries := []protocol.TerminalEntry{}
	for _, desktop := range desktops {
		if filter != "" && desktop.ID != filter {
			continue
		}
		positions := map[string]string{}
		terminalPositions(desktop.Tree, "", positions)
		for _, pane := range desktop.Panes {
			session := d.store.Get(pane.SessionID)
			if session == nil || session.ProfileID != caller.ProfileID {
				continue
			}
			decorated := d.sessionForBroadcast(session)
			name := sessionDisplayName(decorated)
			entries = append(entries, protocol.TerminalEntry{TerminalID: pane.RuntimeID, SessionID: session.ID, ShortID: shortSessionID(protocol.SessionID(pane.RuntimeID)), Agent: session.Agent, Name: name, State: decorated.State, DesktopID: desktop.ID, DesktopLabel: desktop.Name, Position: protocol.Ptr(positions[pane.PaneID]), OpenedBy: session.ParentSessionID})
		}
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{Ok: true, TerminalEntries: entries})
}

func terminalPositions(node layouttree.Node, path string, out map[string]string) {
	if node.PaneID != "" {
		out[node.PaneID] = strings.TrimPrefix(path, "/")
		return
	}
	for i, child := range node.Children {
		side := "left"
		if i > 0 {
			side = "right"
		}
		if node.Direction == layouttree.DirectionHorizontal {
			side = "top"
			if i > 0 {
				side = "bottom"
			}
		}
		terminalPositions(child, path+"/"+side, out)
	}
}

func (d *Daemon) handleTerminalOpen(conn net.Conn, msg *protocol.TerminalOpenMessage) {
	caller, err := d.terminalCaller(msg.CallerSessionID)
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	if protocol.Deref(msg.Command) != "" {
		shell, err := Call[protocol.TerminalShellResult](context.Background(), d.router(), protocol.TerminalShellMessage{Cmd: protocol.CmdTerminalShell, Host: protocol.Deref(msg.Host)})
		if err != nil {
			d.sendError(conn, err.Error())
			return
		}
		if !shell.IntegrationExpected {
			d.sendError(conn, fmt.Sprintf("OSC 133 rule: shell %s is known to provide no command marks (or ATTN_NO_SHELL_INTEGRATION is set); open with a command needs its first prompt. No terminal was opened; use open without a command and send instead", shell.Shell))
			return
		}
	}
	desktop, err := d.terminalDesktop(caller, protocol.Deref(msg.Desktop))
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	direction := layouttree.DirectionVertical
	if protocol.Deref(msg.Direction) == protocol.TerminalDirectionDown {
		direction = layouttree.DirectionHorizontal
	}
	anchor := desktop.ActivePaneID
	if protocol.Deref(msg.Desktop) == "" {
		callerPanes := 0
		for _, pane := range desktop.Panes {
			if pane.SessionID == caller.ID {
				callerPanes++
				if callerPanes > 1 {
					d.sendError(conn, "placement rule: caller has several terminal tiles; pass --desktop <ref> to open beside its active tile")
					return
				}
				anchor = pane.PaneID
			}
		}
	}
	type opened struct {
		pane string
		at   time.Time
	}
	previous := []opened{}
	for _, pane := range desktop.Panes {
		session := d.store.Get(pane.SessionID)
		if session != nil && session.Agent == protocol.AgentShellValue && protocol.Deref(session.ParentSessionID) == caller.ID && d.sessionHasLiveWorker(session.ID) {
			previous = append(previous, opened{pane: pane.PaneID, at: d.store.SessionLaunchedAt(session.ID)})
		}
	}
	sort.Slice(previous, func(i, j int) bool { return previous[i].at.After(previous[j].at) })
	if len(previous) > 0 {
		anchor = previous[0].pane
		direction = layouttree.DirectionHorizontal
	}
	cwd := protocol.Deref(msg.Cwd)
	if cwd == "" {
		cwd = caller.Directory
		if protocol.Deref(msg.Host) != "" {
			cwd = "~"
		}
	}
	id := protocol.SessionID(uuid.NewString())
	placed, rejection := d.runSpawnPipelineReporting(&protocol.SpawnSessionMessage{Cmd: protocol.CmdSpawnSession, ID: id, ProfileID: caller.ProfileID, Agent: protocol.AgentShellValue, Host: msg.Host, Cwd: cwd, Cols: 80, Rows: 24}, internalSpawnPolicy{launchPlacement: &launchPlacement{desktopID: desktop.ID, anchorPaneID: anchor, direction: direction}, parentSessionID: caller.ID})
	if rejection != nil {
		d.sendError(conn, rejection.reason().Error())
		return
	}
	terminal := d.primaryTerminal(id)
	if command := protocol.Deref(msg.Command); command != "" {
		connectionCtx, cancelConnection := d.connectionContext(d.life.Context(), conn)
		defer cancelConnection()
		ctx, cancel := context.WithTimeout(connectionCtx, terminalFirstPromptTripwire)
		defer cancel()
		err := d.waitForTerminalFirstPrompt(ctx, terminal)
		if err == nil && ctx.Err() == nil {
			_, err = Call[struct{}](ctx, d.router(), protocol.TerminalWriteMessage{Cmd: protocol.CmdTerminalWrite, TerminalID: terminal, Data: command + "\r", Source: "agent"})
		}
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			d.sendError(conn, fmt.Sprintf("first prompt wait exceeded limit=%s; %s. Terminal %s stays open; nothing was typed. Use attn terminal send %s to continue", terminalFirstPromptTripwire, terminalFirstPromptReceipt, id, id))
			return
		}
		if err != nil {
			d.sendError(conn, fmt.Sprintf("opened terminal %s, but command was not typed: %v", id, err))
			return
		}
		if ctx.Err() != nil {
			return
		}
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{Ok: true, TerminalOpenResult: &protocol.TerminalOpenResult{TerminalID: terminal, SessionID: id, DesktopID: placed.desktopID, PaneID: placed.paneID}})
}

func (d *Daemon) waitForTerminalFirstPrompt(ctx context.Context, terminal protocol.TerminalID) error {
	result, err := Call[protocol.TerminalWaitResult](ctx, d.router(), protocol.TerminalWaitBlockMessage{Cmd: protocol.CmdTerminalWaitBlock, TerminalID: terminal, Until: protocol.TerminalWaitUntilPrompt, FirstPrompt: protocol.Ptr(true)})
	if err == nil && protocol.Deref(result.Exited) {
		return fmt.Errorf("terminal %s exited before its first prompt (exit code %v)", terminal, protocol.Deref(result.ExitCode))
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("first prompt wait exceeded limit=%s; %s; terminal %s: nothing was typed", terminalFirstPromptTripwire, terminalFirstPromptReceipt, terminal)
	}
	return err
}

func (d *Daemon) handleTerminalInput(conn net.Conn, msg *protocol.TerminalInputMessage) {
	caller, err := d.terminalCaller(msg.CallerSessionID)
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	target, err := d.terminalTarget(caller, msg.Target)
	if err == nil {
		err = d.terminalAct(caller, target, protocol.Deref(msg.Desktop))
	}
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	result := &protocol.TerminalInputResult{TerminalID: target.terminal}
	if protocol.Deref(msg.Run) {
		if target.Agent != protocol.AgentShellValue {
			d.sendError(conn, "run rule: run requires a shell; use send to type into an agent")
			return
		}
		state, err := Call[protocol.TerminalText](context.Background(), d.router(), protocol.TerminalReadTextMessage{Cmd: protocol.CmdTerminalReadText, TerminalID: target.terminal, Mode: protocol.TerminalReadModeScreen})
		if err != nil {
			d.sendError(conn, err.Error())
			return
		}
		if !state.IntegrationSeen && protocol.Deref(msg.Wait) {
			connectionCtx, cancelConnection := d.connectionContext(d.life.Context(), conn)
			defer cancelConnection()
			ctx, cancel := context.WithTimeout(connectionCtx, terminalFirstPromptTripwire)
			defer cancel()
			if err := d.waitForTerminalFirstPrompt(ctx, target.terminal); err != nil {
				d.sendError(conn, err.Error())
				return
			}
			state, err = Call[protocol.TerminalText](ctx, d.router(), protocol.TerminalReadTextMessage{Cmd: protocol.CmdTerminalReadText, TerminalID: target.terminal, Mode: protocol.TerminalReadModeScreen})
			if err != nil {
				d.sendError(conn, err.Error())
				return
			}
		}
		if !state.AtPrompt {
			d.sendError(conn, "prompt rule: shell is busy; use send to type into its running program")
			return
		}
		result.LastBlockID = protocol.Ptr(state.LastBlockID)
	}
	if _, err := Call[struct{}](context.Background(), d.router(), protocol.TerminalWriteMessage{Cmd: protocol.CmdTerminalWrite, TerminalID: target.terminal, Data: msg.Data, Source: "agent"}); err != nil {
		d.sendError(conn, err.Error())
		return
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{Ok: true, TerminalInputResult: result})
}

func protocolTerminalBlock(block *pty.TextBlock) *protocol.TerminalBlock {
	if block == nil {
		return nil
	}
	var code *int
	if block.ExitCode != nil {
		code = protocol.Ptr(int(*block.ExitCode))
	}
	return &protocol.TerminalBlock{ID: strconv.FormatUint(block.ID, 10), Command: block.Command, ExitCode: code, Output: block.Output}
}

func (d *Daemon) handleTerminalRead(conn net.Conn, msg *protocol.TerminalReadMessage) {
	caller, err := d.terminalCaller(msg.CallerSessionID)
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	target, err := d.terminalTarget(caller, msg.Target)
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	if protocol.Deref(msg.Lines) < 0 {
		d.sendError(conn, "lines rule: --lines must be positive")
		return
	}
	info, err := Call[protocol.TerminalText](context.Background(), d.router(), protocol.TerminalReadTextMessage{Cmd: protocol.CmdTerminalReadText, TerminalID: target.terminal, Mode: msg.Mode, Lines: protocol.Deref(msg.Lines)})
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{Ok: true, TerminalReadResult: &protocol.TerminalReadResult{Text: info.Text, Block: info.Block}})
}

func (d *Daemon) handleTerminalWait(conn net.Conn, msg *protocol.TerminalWaitMessage) {
	caller, err := d.terminalCaller(msg.CallerSessionID)
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	target, err := d.terminalTarget(caller, msg.Target)
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	if msg.Until == protocol.TerminalWaitUntilPrompt && target.Agent != protocol.AgentShellValue {
		d.sendError(conn, "prompt rule: wait --until prompt requires a shell; use wait --until exit for an agent")
		return
	}
	ctx, cancel := d.connectionContext(d.life.Context(), conn)
	defer cancel()
	result, err := Call[protocol.TerminalWaitResult](ctx, d.router(), protocol.TerminalWaitBlockMessage{Cmd: protocol.CmdTerminalWaitBlock, TerminalID: target.terminal, Until: msg.Until, AfterBlockID: msg.AfterBlockID})
	if err != nil && ctx.Err() == nil {
		if terminals := d.terminalsOf(target.ID); len(terminals) == 1 && terminals[0] == target.terminal {
			if exit := d.store.GetSessionExit(target.ID); exit != nil {
				result, err = protocol.TerminalWaitResult{Exited: protocol.Ptr(true), ExitCode: protocol.Ptr(exit.Code)}, nil
			}
		}
	}
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{Ok: true, TerminalWaitResult: &result})
}

func (d *Daemon) handleTerminalClose(conn net.Conn, msg *protocol.TerminalCloseMessage) {
	caller, err := d.terminalCaller(msg.CallerSessionID)
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	target, err := d.terminalTarget(caller, msg.Target)
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	if target.Agent != protocol.AgentShellValue {
		d.sendError(conn, "close rule: terminal close only closes shells; use attn agent close for an agent")
		return
	}
	if err := d.terminalAct(caller, target, protocol.Deref(msg.Desktop)); err != nil {
		d.sendError(conn, err.Error())
		return
	}
	closing, err := d.beginSessionClose(target.ID, store.SessionClose{By: string(caller.ID), Reason: "shell closed by attn terminal close"}, nil)
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	d.sendOK(conn)
	d.finishSessionClose(target.ID, closing)
}
