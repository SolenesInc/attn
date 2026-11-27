package daemon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/command"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/inbox"
	"github.com/victorarias/attn/internal/layouttree"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/profiles"
	"github.com/victorarias/attn/internal/prompts"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/who"
)

const terminalFirstPromptTripwire = 30 * time.Second
const terminalFirstPromptReceipt = "first prompt measured at 9-176 ms across fish, zsh and bash"
const typedCommandFinishTripwire = 5 * time.Second

type typedCommand struct {
	lastBlockID   string
	promptBlockID string
	typedAt       time.Time
}

const promptBlockUnreportedByRuntime = "0"

func (typed typedCommand) finishedWhen(terminal protocol.TerminalID) protocol.TerminalWaitBlockMessage {
	wait := protocol.TerminalWaitBlockMessage{Cmd: protocol.CmdTerminalWaitBlock, TerminalID: terminal, Until: protocol.TerminalWaitUntilPrompt}
	if typed.promptBlockID == promptBlockUnreportedByRuntime {
		wait.AfterBlockID = protocol.Ptr(typed.lastBlockID)
	} else {
		wait.PastPromptBlockID = protocol.Ptr(typed.promptBlockID)
	}
	return wait
}

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
	match, err := d.resolveLiveSession(ref, caller.ProfileID)
	if errors.Is(err, errAmbiguousSession) {
		return nil, protocol.Refuse(protocol.RefusalInvalidInput, "target rule: %q matches multiple sessions; use a terminal id from `{attn terminal ls --all}`", ref)
	}
	if match == nil {
		member, found, err := d.crewMemberIn(caller.ProfileID, ref)
		if err != nil {
			return nil, err
		}
		if found {
			if session := d.store.Get(member.BindingSession); session != nil && session.ProfileID == caller.ProfileID {
				match = session
			}
		} else {
			match, err = d.liveSessionNamed(ref, caller.ProfileID)
			if errors.Is(err, errAmbiguousSession) {
				return nil, protocol.Refuse(protocol.RefusalInvalidInput, "target rule: %s", err)
			}
		}
	}
	if match == nil {
		return nil, protocol.Refuse(protocol.RefusalNotFound, "target rule: terminal %q not found in the caller's profile; `{attn terminal ls --all}` lists its terminals", ref)
	}
	ids := d.terminalsOf(match.ID)
	if len(ids) != 1 {
		terminals := make([]string, len(ids))
		for i, id := range ids {
			terminals[i] = string(id)
		}
		return nil, protocol.Refuse(protocol.RefusalInvalidInput, "target rule: session %s shows in %d terminals (%s); pass one terminal id from `{attn terminal ls --all}`", match.ID, len(ids), strings.Join(terminals, ", "))
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

func (d *Daemon) terminalAct(caller *protocol.Session, target *selectedTerminal, desktopRef, action string) error {
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
	return d.mayActOnTerminal(caller, target, action)
}

func (d *Daemon) mayActOnTerminal(caller *protocol.Session, target *selectedTerminal, action string) error {
	if target.Agent != protocol.AgentShellValue {
		return d.mayActOnSession(caller.ID, target.Session, action)
	}
	return nil
}

func (d *Daemon) connectionContext(parent context.Context, conn net.Conn) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	d.life.Go("commandDisconnect", func() { _, _ = io.Copy(io.Discard, conn); cancel() })
	return ctx, cancel
}

func (d *Daemon) runTerminalList(_ context.Context, c command.Caller, a command.TerminalLsArgs) (any, error) {
	caller, err := d.terminalCaller(c.Session)
	if err != nil {
		return nil, err
	}
	if a.All && a.Desktop != "" {
		return nil, command.Usage("choose {all} or {desktop}")
	}
	_, desktops, err := d.store.ProfileArrangement(caller.ProfileID)
	if err != nil {
		return nil, err
	}
	filter := ""
	if !a.All {
		desktop, err := d.terminalDesktop(caller, a.Desktop)
		if err != nil {
			return nil, err
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
			entries = append(entries, protocol.TerminalEntry{TerminalID: pane.RuntimeID, SessionID: session.ID, ShortID: shortTerminalID(pane.RuntimeID), Agent: session.Agent, Name: name, State: decorated.State, DesktopID: desktop.ID, DesktopLabel: desktop.Name, Position: protocol.Ptr(positions[pane.PaneID]), OpenedBy: session.ParentSessionID})
		}
	}
	return protocol.TerminalListResult{Terminals: entries}, nil
}

const terminalShortIDLength = 8

func shortTerminalID(id protocol.TerminalID) string {
	return string(id[:min(len(id), terminalShortIDLength)])
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

func (d *Daemon) runTerminalOpen(ctx context.Context, c command.Caller, a command.TerminalOpenArgs) (any, error) {
	caller, err := d.terminalCaller(c.Session)
	if err != nil {
		return nil, err
	}
	host, err := d.resolveCallerHost(caller.ID, nonEmptyText(a.Host))
	if err != nil {
		return nil, err
	}
	line := shellCommand(a.Command)
	if line != "" {
		shell, err := Call[protocol.TerminalShellResult](context.Background(), d.router(), protocol.TerminalShellMessage{Cmd: protocol.CmdTerminalShell, Host: host})
		if err != nil {
			return nil, err
		}
		if !shell.IntegrationExpected {
			return nil, fmt.Errorf("OSC 133 rule: shell %s is known to provide no command marks (or ATTN_NO_SHELL_INTEGRATION is set); open with a command needs its first prompt. No terminal was opened; use open without a command and send instead", shell.Shell)
		}
	}
	var desktop profiles.Desktop
	if ref := a.Desktop; ref != "" {
		var profile profiles.Profile
		profile, err = d.liveLaunchProfile(caller.ProfileID)
		if err == nil {
			desktop.ID, err = d.resolveDesktopTarget(profile, ref)
		}
		if err == nil {
			if existing, loadErr := d.store.GetDesktop(desktop.ID); loadErr == nil {
				desktop = existing
			} else if !profiles.IsNotFound(loadErr) {
				err = loadErr
			}
		}
	} else {
		desktop, err = d.terminalDesktop(caller, "")
	}
	if err != nil {
		return nil, err
	}
	direction := layouttree.DirectionVertical
	if a.Down {
		direction = layouttree.DirectionHorizontal
	}
	anchor := desktop.ActivePaneID
	if a.Desktop == "" {
		callerPanes := 0
		for _, pane := range desktop.Panes {
			if pane.SessionID == caller.ID {
				callerPanes++
				if callerPanes > 1 {
					return nil, errors.New("placement rule: caller has several terminal tiles; pass --desktop <ref> to open on that desktop")
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
	if direction == layouttree.DirectionHorizontal {
		if newest := slices.IndexFunc(previous, func(earlier opened) bool { return layouttree.SameColumn(desktop.Tree, anchor, earlier.pane) }); newest >= 0 {
			anchor = previous[newest].pane
		}
	}
	cwd := a.Cwd
	if cwd == "" {
		cwd = caller.Directory
		if host != d.machineOnWire(caller.Host).ID() {
			cwd = "~"
		}
	}
	id := protocol.SessionID(uuid.NewString())
	placed, rejection := d.runSpawnPipelineReporting(&protocol.SpawnSessionMessage{Cmd: protocol.CmdSpawnSession, ID: id, ProfileID: caller.ProfileID, Agent: protocol.AgentShellValue, Host: d.wireHost(host), Cwd: cwd, Cols: 80, Rows: 24}, internalSpawnPolicy{launchPlacement: &launchPlacement{desktopID: desktop.ID, anchorPaneID: anchor, direction: direction}, parentSessionID: caller.ID})
	if rejection != nil {
		return nil, rejection.reason()
	}
	terminal := d.primaryTerminal(id)
	if line != "" && !a.WaitForShell {
		if err := d.store.RecordTerminalOpenCommand(store.TerminalOpenCommand{Terminal: terminal, Opener: caller.ID, Command: line}, time.Now()); err != nil {
			return nil, fmt.Errorf("opened terminal %s, but could not keep its command, so nothing will be typed: %w", terminal, err)
		}
		opener := caller.ID
		d.life.Go("type the command of an opened terminal", func() {
			markSubmitting := func() error { return d.store.MarkTerminalOpenCommandSubmitting(terminal, time.Now()) }
			err := d.typeFirstCommand(d.life.Context(), terminal, id, line, markSubmitting)
			switch {
			case err == nil:
				pausepoint.At(pausepoint.TerminalOpenCommandTyped)
				if err := d.store.ForgetTerminalOpenCommand(terminal); err != nil {
					d.logf("terminal open: command for %s typed; forgetting it: %v", terminal, err)
				}
			case d.stopping():
				d.logf("terminal open: command for %s not typed across daemon stop; the next start tells %s: %v", terminal, opener, err)
			default:
				d.noticeCommandNotTyped(opener, terminal, err)
			}
		})
	}
	if line != "" && a.WaitForShell {
		if err := d.typeFirstCommand(ctx, terminal, id, line, nil); err != nil {
			return nil, err
		}
	}
	return protocol.TerminalOpenResult{TerminalID: terminal, SessionID: id, DesktopID: placed.desktopID, PaneID: placed.paneID}, nil
}

func (d *Daemon) noticeCommandNotTyped(opener protocol.SessionID, terminal protocol.TerminalID, failure error) {
	notice, savedAt := commandNotTypedNotice(opener, terminal, failure), time.Now()
	if err := d.store.NoticeTerminalOpenCommandNotTyped(terminal, notice, savedAt); err != nil {
		d.logf("terminal open: command for %s not typed (%v); telling %s: %v", terminal, failure, opener, err)
		return
	}
	d.deliverSavedInbox(notice.To, notice.ID, savedAt)
}

func (d *Daemon) noticeCommandsCutByRestart() {
	pending, err := d.store.TerminalOpenCommands()
	if err != nil {
		d.logf("terminal open: reading the commands the last run did not type: %v", err)
		return
	}
	for _, command := range pending {
		if err := d.store.NoticeTerminalOpenCommandNotTyped(command.Terminal, commandCutByRestartNotice(command), time.Now()); err != nil {
			d.logf("terminal open: command for %s cut by a restart; telling %s: %v", command.Terminal, command.Opener, err)
		}
	}
}

func commandCutByRestartNotice(command store.TerminalOpenCommand) inbox.Item {
	if !command.Submitting {
		return commandNotTypedNotice(command.Opener, command.Terminal, fmt.Errorf("the daemon restarted before attn typed %s", command.Command))
	}
	text := prompts.RenderText("terminal", "command-may-have-run", prompts.Values{"terminal": string(command.Terminal), "command": command.Command})
	return terminalCommandNotice(command.Opener, command.Terminal, text)
}

func commandNotTypedNotice(opener protocol.SessionID, terminal protocol.TerminalID, failure error) inbox.Item {
	text := prompts.RenderText("terminal", "command-not-typed", prompts.Values{"terminal": string(terminal), "failure": failure.Error()})
	return terminalCommandNotice(opener, terminal, text)
}

func terminalCommandNotice(opener protocol.SessionID, terminal protocol.TerminalID, text string) inbox.Item {
	return inbox.Item{ID: "terminal-command-not-typed/" + string(terminal), To: who.ToSession(opener), Kind: inbox.Notice, Source: "terminal", Text: text}
}

func (d *Daemon) typeFirstCommand(ctx context.Context, terminal protocol.TerminalID, id protocol.SessionID, line string, beforeWrite func() error) error {
	ctx, cancel := context.WithTimeout(ctx, terminalFirstPromptTripwire)
	defer cancel()
	err := d.waitForTerminalFirstPrompt(ctx, terminal)
	if err == nil && ctx.Err() == nil {
		var state protocol.TerminalText
		state, err = Call[protocol.TerminalText](ctx, d.router(), protocol.TerminalReadTextMessage{Cmd: protocol.CmdTerminalReadText, TerminalID: terminal, Mode: protocol.TerminalReadModeScreen})
		if err == nil && beforeWrite != nil {
			err = beforeWrite()
		}
		if err == nil {
			err = d.typeInput(ctx, terminal, state, line+"\r")
		}
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return protocol.Refuse(protocol.RefusalLimit, "first prompt wait exceeded limit=%s; %s. Terminal %s stays open; nothing was typed. Use `{attn terminal send %s}` to continue", terminalFirstPromptTripwire, terminalFirstPromptReceipt, id, id)
	}
	if err != nil {
		return fmt.Errorf("opened terminal %s, but command was not typed: %v", id, err)
	}
	return ctx.Err()
}

func shellCommand(words []string) string {
	if len(words) == 1 {
		return words[0]
	}
	quoted := make([]string, len(words))
	for i, word := range words {
		quoted[i] = "'" + strings.ReplaceAll(word, "'", `'\''`) + "'"
	}
	return strings.Join(quoted, " ")
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

func (d *Daemon) typeInput(ctx context.Context, terminal protocol.TerminalID, state protocol.TerminalText, data string) error {
	if _, err := Call[struct{}](ctx, d.router(), protocol.TerminalWriteMessage{Cmd: protocol.CmdTerminalWrite, TerminalID: terminal, Data: data, Source: "agent"}); err != nil {
		return err
	}
	if state.IntegrationSeen {
		d.typedCommands.Store(terminal, typedCommand{lastBlockID: state.LastBlockID, promptBlockID: state.PromptBlockID, typedAt: time.Now()})
	}
	return nil
}

func (d *Daemon) typedCommandOf(terminal protocol.TerminalID, shell protocol.TerminalText) (typedCommand, bool) {
	stored, ok := d.typedCommands.Load(terminal)
	if !ok {
		return typedCommand{}, false
	}
	typed := stored.(typedCommand)
	if !shell.IntegrationSeen {
		d.typedCommands.CompareAndDelete(terminal, typed)
		return typedCommand{}, false
	}
	return typed, true
}

func (d *Daemon) awaitTypedCommandFinish(ctx context.Context, terminal protocol.TerminalID, typed typedCommand) error {
	finishBy := typed.typedAt.Add(typedCommandFinishTripwire)
	finishing, cancel := context.WithDeadline(ctx, finishBy)
	defer cancel()
	_, err := Call[protocol.TerminalWaitResult](finishing, d.router(), typed.finishedWhen(terminal))
	if err == nil {
		d.typedCommands.CompareAndDelete(terminal, typed)
		return nil
	}
	if time.Now().Before(finishBy) {
		return err
	}
	return nil
}

func (d *Daemon) typedCommandUnfinished(terminal protocol.TerminalID, state protocol.TerminalText) bool {
	typed, ok := d.typedCommandOf(terminal, state)
	if !ok {
		return false
	}
	if state.LastBlockID != typed.lastBlockID || state.PromptBlockID != typed.promptBlockID || time.Since(typed.typedAt) > typedCommandFinishTripwire {
		d.typedCommands.CompareAndDelete(terminal, typed)
		return false
	}
	return true
}

func (d *Daemon) readTerminalScreen(ctx context.Context, terminal protocol.TerminalID) (protocol.TerminalText, error) {
	return Call[protocol.TerminalText](ctx, d.router(), protocol.TerminalReadTextMessage{Cmd: protocol.CmdTerminalReadText, TerminalID: terminal, Mode: protocol.TerminalReadModeScreen})
}

type terminalInput struct {
	target, desktop, data   string
	run, wait, waitForShell bool
}

func (d *Daemon) inputTerminal(ctx context.Context, caller *protocol.Session, in terminalInput) (*protocol.TerminalInputResult, *selectedTerminal, error) {
	target, err := d.terminalTarget(caller, in.target)
	if err == nil {
		err = d.terminalAct(caller, target, in.desktop, "type into")
	}
	if err != nil {
		return nil, nil, err
	}
	result := &protocol.TerminalInputResult{TerminalID: target.terminal}
	var state protocol.TerminalText
	if in.run {
		if target.Agent != protocol.AgentShellValue {
			return nil, nil, errors.New("run rule: run requires a shell; use send to type into an agent")
		}
		if ctx.Err() != nil {
			return nil, nil, ctx.Err()
		}
		state, err = d.readTerminalScreen(ctx, target.terminal)
		if err != nil {
			return nil, nil, err
		}
		if typed, ok := d.typedCommandOf(target.terminal, state); ok && in.waitForShell {
			if err := d.awaitTypedCommandFinish(ctx, target.terminal, typed); err != nil {
				return nil, nil, err
			}
			if ctx.Err() != nil {
				return nil, nil, ctx.Err()
			}
			state, err = d.readTerminalScreen(ctx, target.terminal)
			if err != nil {
				return nil, nil, err
			}
		}
		if !state.IntegrationSeen && in.wait {
			prompt, cancel := context.WithTimeout(ctx, terminalFirstPromptTripwire)
			defer cancel()
			if err := d.waitForTerminalFirstPrompt(prompt, target.terminal); err != nil {
				return nil, nil, err
			}
			state, err = d.readTerminalScreen(prompt, target.terminal)
			if err != nil {
				return nil, nil, err
			}
		}
		if d.typedCommandUnfinished(target.terminal, state) {
			return nil, nil, errors.New("prompt rule: the command attn typed into this shell has not finished; read the terminal, and run again once it shows its prompt")
		}
		if !state.AtPrompt {
			return nil, nil, errors.New("prompt rule: shell is busy; use send to type into its running program")
		}
		result.LastBlockID = protocol.Ptr(state.LastBlockID)
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if err := d.typeInput(ctx, target.terminal, state, in.data); err != nil {
		return nil, nil, err
	}
	return result, target, nil
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

func (d *Daemon) runTerminalRead(_ context.Context, c command.Caller, a command.TerminalReadArgs) (any, error) {
	caller, err := d.terminalCaller(c.Session)
	if err != nil {
		return nil, err
	}
	mode := protocol.TerminalReadModeScreen
	if a.Mode != "" {
		mode = protocol.TerminalReadMode(a.Mode)
	}
	lines := 0
	if a.Lines != nil {
		if *a.Lines < 1 {
			return nil, command.Usage("{lines} must be positive; got %d", *a.Lines)
		}
		if mode != protocol.TerminalReadModeScrollback {
			return nil, command.Usage("{lines} reads the scrollback only")
		}
		lines = int(*a.Lines)
	}
	target, err := d.terminalTarget(caller, a.Terminal)
	if err != nil {
		return nil, err
	}
	info, err := Call[protocol.TerminalText](context.Background(), d.router(), protocol.TerminalReadTextMessage{Cmd: protocol.CmdTerminalReadText, TerminalID: target.terminal, Mode: mode, Lines: lines})
	if err != nil {
		return nil, err
	}
	return protocol.TerminalReadResult{Text: info.Text, Block: info.Block}, nil
}

func (d *Daemon) waitTerminal(ctx context.Context, target *selectedTerminal, wait protocol.TerminalWaitBlockMessage) (protocol.TerminalWaitResult, error) {
	if wait.Until == protocol.TerminalWaitUntilPrompt && target.Agent != protocol.AgentShellValue {
		return protocol.TerminalWaitResult{}, errors.New("prompt rule: wait --until prompt requires a shell; use wait --until exit for an agent")
	}
	wait.Cmd, wait.TerminalID = protocol.CmdTerminalWaitBlock, target.terminal
	result, err := Call[protocol.TerminalWaitResult](ctx, d.router(), wait)
	if err != nil && ctx.Err() == nil {
		if terminals := d.terminalsOf(target.ID); len(terminals) == 1 && terminals[0] == target.terminal {
			if exit := d.store.GetSessionExit(target.ID); exit != nil {
				result, err = protocol.TerminalWaitResult{Exited: protocol.Ptr(true), ExitCode: protocol.Ptr(exit.Code)}, nil
			}
		}
	}
	return result, err
}

func (d *Daemon) runTerminalClose(_ context.Context, c command.Caller, a command.TerminalCloseArgs) (any, error) {
	caller, err := d.terminalCaller(c.Session)
	if err != nil {
		return nil, err
	}
	target, err := d.terminalTarget(caller, a.Terminal)
	if err != nil {
		return nil, err
	}
	if target.Agent != protocol.AgentShellValue {
		return nil, protocol.Refuse(protocol.RefusalInvalidInput, "close rule: terminal close only closes shells; use `{attn agent close}` for an agent")
	}
	if err := d.terminalAct(caller, target, a.Desktop, "close"); err != nil {
		return nil, err
	}
	closing, err := d.beginSessionClose(target.ID, store.SessionClose{By: who.Session(caller.ID).Actor(), Reason: "shell closed by attn terminal close"}, nil)
	if err != nil {
		return nil, err
	}
	return command.Then{Result: protocol.TerminalCloseResult{TerminalID: target.terminal}, After: func() { d.finishSessionClose(target.ID, closing) }}, nil
}
