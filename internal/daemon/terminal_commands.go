package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/layouttree"
	"github.com/victorarias/attn/internal/profiles"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/ptybackend"
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

func (d *Daemon) terminalTarget(caller *protocol.Session, ref string) (*protocol.Session, error) {
	ref = strings.TrimSpace(ref)
	var match *protocol.Session
	for _, session := range d.store.List("") {
		if session.ProfileID != caller.ProfileID {
			continue
		}
		if string(session.ID) == ref {
			return session, nil
		}
		if ref != "" && strings.HasPrefix(string(session.ID), ref) {
			if match != nil {
				return nil, fmt.Errorf("target rule: %q matches multiple terminals; use a longer id", ref)
			}
			match = session
		}
	}
	if match != nil {
		return match, nil
	}
	member, found, err := d.resolveCrewMember(ref)
	if err != nil {
		return nil, err
	}
	if found && d.crewProfileID(member.ID) == caller.ProfileID {
		if session := d.store.Get(member.BindingSession); session != nil && session.ProfileID == caller.ProfileID {
			return session, nil
		}
	}
	return nil, fmt.Errorf("target rule: terminal %q not found in the caller's profile; attn terminal ls --all lists its terminals", ref)
}

func (d *Daemon) terminalDesktop(caller *protocol.Session, ref string) (profiles.Desktop, error) {
	profile, desktops, err := d.store.ProfileArrangement(caller.ProfileID)
	if err != nil {
		return profiles.Desktop{}, err
	}
	if ref != "" {
		return profiles.ResolveDesktopRef(profile, desktops, ref)
	}
	placement, placed, err := d.store.SessionPlacement(caller.ID)
	if err != nil {
		return profiles.Desktop{}, err
	}
	if !placed {
		return profiles.Desktop{}, fmt.Errorf("desktop rule: caller has no tile; pass --desktop <ref>")
	}
	return d.store.GetDesktop(placement.DesktopID)
}

func (d *Daemon) terminalAct(caller, target *protocol.Session, desktopRef string) error {
	placement, placed, err := d.store.SessionPlacement(target.ID)
	if err != nil {
		return err
	}
	if !placed {
		return fmt.Errorf("terminal rule: session %s has no terminal tile", target.ID)
	}
	desktop, err := d.terminalDesktop(caller, desktopRef)
	if err != nil {
		return err
	}
	if desktop.ID != placement.DesktopID {
		return fmt.Errorf("desktop rule: terminal %s is on another desktop; pass --desktop %s to act there", target.ID, placement.DesktopID)
	}
	if target.Agent != protocol.AgentShellValue {
		return d.mayActOnSession(caller.ID, target, "type into")
	}
	return nil
}

func (d *Daemon) terminalText() (ptybackend.TerminalTextProvider, error) {
	provider, ok := d.ptyBackend.(ptybackend.TerminalTextProvider)
	if !ok {
		return nil, fmt.Errorf("terminal backend does not support read_text and wait_block")
	}
	return provider, nil
}

func (d *Daemon) terminalContext(conn net.Conn) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(d.life.Context())
	d.life.Go("terminalCommandDisconnect", func() { _, _ = io.Copy(io.Discard, conn); cancel() })
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
			entries = append(entries, protocol.TerminalEntry{SessionID: session.ID, ShortID: shortSessionID(session.ID), Agent: session.Agent, Name: name, State: decorated.State, DesktopID: desktop.ID, DesktopLabel: desktop.Name, Position: protocol.Ptr(positions[pane.PaneID]), OpenedBy: session.ParentSessionID})
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
		shell := pty.GetUserLoginShell()
		env := pty.MergeEnvironment(os.Environ(), d.cachedLoginShellEnv())
		if !pty.ShellIntegrationExpected(shell, env) {
			d.sendError(conn, fmt.Sprintf("OSC 133 rule: shell %s is known to provide no command marks (or ATTN_NO_SHELL_INTEGRATION is set); open with a command needs its first prompt. No terminal was opened; use open without a command and send instead", shell))
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
		placement, placed, err := d.store.SessionPlacement(caller.ID)
		if err != nil {
			d.sendError(conn, err.Error())
			return
		}
		if placed {
			anchor = placement.PaneID
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
	}
	id := protocol.SessionID(uuid.NewString())
	placed, rejection := d.runSpawnPipelineReporting(&protocol.SpawnSessionMessage{Cmd: protocol.CmdSpawnSession, ID: id, ProfileID: caller.ProfileID, Agent: protocol.AgentShellValue, Cwd: cwd, Cols: 80, Rows: 24}, internalSpawnPolicy{launchPlacement: &launchPlacement{desktopID: desktop.ID, anchorPaneID: anchor, direction: direction}, parentSessionID: caller.ID})
	if rejection != nil {
		d.sendError(conn, rejection.reason().Error())
		return
	}
	if command := protocol.Deref(msg.Command); command != "" {
		connectionCtx, cancelConnection := d.terminalContext(conn)
		defer cancelConnection()
		ctx, cancel := context.WithTimeout(connectionCtx, terminalFirstPromptTripwire)
		defer cancel()
		provider, err := d.terminalText()
		if err == nil {
			var result pty.WaitBlockResult
			result, err = provider.WaitBlock(ctx, d.primaryTerminal(id), pty.WaitBlockParams{Until: "prompt", FirstPrompt: true})
			if err == nil && result.Exited {
				err = fmt.Errorf("terminal %s exited before its first prompt (exit code %v)", id, protocol.Deref(result.ExitCode))
			}
		}
		if err == nil && ctx.Err() == nil {
			err = d.writeTerminalPTY(ctx, d.primaryTerminal(id), []byte(command+"\r"), "agent")
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
	_ = json.NewEncoder(conn).Encode(protocol.Response{Ok: true, TerminalOpenResult: &protocol.TerminalOpenResult{SessionID: id, DesktopID: placed.desktopID, PaneID: placed.paneID}})
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
	result := &protocol.TerminalInputResult{}
	if protocol.Deref(msg.Run) {
		if target.Agent != protocol.AgentShellValue {
			d.sendError(conn, "run rule: run requires a shell; use send to type into an agent")
			return
		}
		provider, err := d.terminalText()
		if err != nil {
			d.sendError(conn, err.Error())
			return
		}
		state, err := provider.ReadText(context.Background(), d.primaryTerminal(target.ID), "screen", 0)
		if err != nil {
			d.sendError(conn, err.Error())
			return
		}
		if !state.IntegrationSeen && protocol.Deref(msg.Wait) {
			d.sendError(conn, pty.ErrNoShellIntegration.Error())
			return
		}
		if !state.AtPrompt {
			d.sendError(conn, "prompt rule: shell is busy; use send to type into its running program")
			return
		}
		result.LastBlockID = protocol.Ptr(strconv.FormatUint(state.LastBlockID, 10))
	}
	if err := d.writeTerminalPTY(context.Background(), d.primaryTerminal(target.ID), []byte(msg.Data), "agent"); err != nil {
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
	provider, err := d.terminalText()
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	if protocol.Deref(msg.Lines) < 0 {
		d.sendError(conn, "lines rule: --lines must be positive")
		return
	}
	info, err := provider.ReadText(context.Background(), d.primaryTerminal(target.ID), string(msg.Mode), protocol.Deref(msg.Lines))
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{Ok: true, TerminalReadResult: &protocol.TerminalReadResult{Text: info.Text, Block: protocolTerminalBlock(info.Block)}})
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
	provider, err := d.terminalText()
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	params := pty.WaitBlockParams{Until: string(msg.Until)}
	if msg.AfterBlockID != nil {
		id, err := strconv.ParseUint(*msg.AfterBlockID, 10, 64)
		if err != nil {
			d.sendError(conn, "block rule: after_block_id must be an unsigned integer")
			return
		}
		params.AfterBlockID = &id
	}
	ctx, cancel := d.terminalContext(conn)
	defer cancel()
	result, err := provider.WaitBlock(ctx, d.primaryTerminal(target.ID), params)
	if err != nil && ctx.Err() == nil {
		if exit := d.store.GetSessionExit(target.ID); exit != nil {
			result, err = pty.WaitBlockResult{Exited: true, ExitCode: protocol.Ptr(exit.Code)}, nil
		}
	}
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{Ok: true, TerminalWaitResult: &protocol.TerminalWaitResult{Block: protocolTerminalBlock(result.Block), Exited: protocol.Ptr(result.Exited), ExitCode: result.ExitCode}})
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
