package daemon

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"syscall"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/ptybackend"
	"github.com/victorarias/attn/internal/sessionmonitor"
	"github.com/victorarias/attn/internal/sessionstate"
)

func (d *Daemon) machineParts() map[string]machinePart {
	parts := map[string]machinePart{}
	runs(parts, d.startTerminal)
	runs(parts, d.restartTerminal)
	runs(parts, d.readTerminalLaunch)
	runs(parts, d.writeTerminal)
	runs(parts, d.readTerminalText)
	runs(parts, d.waitTerminalBlock)
	runs(parts, d.killTerminal)
	runs(parts, d.removeTerminal)
	runs(parts, d.themeTerminals)
	runs(parts, d.loginShell)
	runs(parts, d.screenSnapshot)
	runs(parts, d.kittyImage)
	runs(parts, d.resizeTerminal)
	runs(parts, d.notePointerActivity)
	return parts
}

func encodeLaunch(opts ptybackend.SpawnOptions) (string, error) {
	opts.Theme, opts.LoginShellEnv, opts.DaemonEnv = pty.TerminalTheme{}, nil, nil
	launch, err := json.Marshal(opts)
	return string(launch), err
}

func (d *Daemon) decodeLaunch(launch string) (ptybackend.SpawnOptions, error) {
	var opts ptybackend.SpawnOptions
	if err := json.Unmarshal([]byte(launch), &opts); err != nil {
		return opts, fmt.Errorf("decode terminal launch: %w", err)
	}
	opts.Theme, opts.LoginShellEnv = d.currentTerminalTheme(), d.cachedLoginShellEnv()
	return opts, nil
}

func (d *Daemon) startTerminal(_ context.Context, msg protocol.TerminalStartMessage) (struct{}, error) {
	opts, err := d.decodeLaunch(msg.Launch)
	if err != nil {
		return struct{}{}, err
	}
	session := msg.Session
	d.expectTerminal(msg.TerminalID, session.ID)
	opts.ID = msg.TerminalID
	branchInfo, _ := d.readBranchInfo(context.Background(), gitTask{Kind: gitTaskSessionIdentity, Lane: gitInteractive}, session.Directory)
	applyBranchInfo(&session, branchInfo)
	d.startSessionMonitorSeeded(&session, sessionmonitor.Evidence{TerminalEvidence: sessionstate.TerminalEvidence{ReviewerInLoop: opts.ApprovalRoute.ReviewerInLoop()}})
	d.sessionMonitor(session.ID).RememberInitialPrompt("")
	if err := d.spawnSessionRuntime(session.ID, opts); err != nil {
		d.sessionMonitor(session.ID).ForgetInitialPrompt()
		return struct{}{}, err
	}
	return struct{}{}, nil
}

func (d *Daemon) restartTerminal(ctx context.Context, msg protocol.TerminalRestartMessage) (struct{}, error) {
	opts, err := d.decodeLaunch(msg.Launch)
	if err != nil {
		return struct{}{}, err
	}
	terminal, sessionID := msg.TerminalID, msg.SessionID
	d.sessionMonitor(sessionID).FenceInput()
	unlockEnds := d.replaceRuntime(sessionID, terminal)
	opts = withLifecycleID(opts)
	if killErr := d.ptyBackend.Kill(ctx, terminal, syscall.SIGTERM); killErr != nil {
		d.logf("reload: kill returned error for %s (continuing): %v", sessionID, killErr)
	}
	if removeErr := d.ptyBackend.Remove(ctx, terminal); removeErr != nil {
		d.logf("reload: remove returned error for %s (continuing): %v", sessionID, removeErr)
	}
	opts.DaemonEnv = d.spawnRoutingEnv()
	if spawnErr := d.ptyBackend.Spawn(ctx, opts); spawnErr != nil {
		unlockEnds()
		d.logf("reload: respawn failed for %s: %v; finalizing as exited", sessionID, spawnErr)
		d.handlePTYExit(ptybackend.ExitInfo{ID: terminal, ExitCode: 1, LifecycleID: opts.LifecycleID})
		return struct{}{}, fmt.Errorf("respawn failed for %s: %w", sessionID, spawnErr)
	}
	unlockEnds()
	d.sessionMonitor(sessionID).ResetInput()
	d.sessionMonitor(sessionID).RecordReviewer(opts.ApprovalRoute.ReviewerInLoop())
	return struct{}{}, nil
}

func (d *Daemon) readTerminalLaunch(ctx context.Context, msg protocol.TerminalLaunchMessage) (protocol.TerminalLaunchResult, error) {
	var result protocol.TerminalLaunchResult
	if infoProvider, ok := d.ptyBackend.(ptybackend.SessionInfoProvider); ok {
		if info, err := infoProvider.SessionInfo(ctx, msg.TerminalID); err == nil {
			if info.Cols > 0 {
				result.Cols = protocol.Ptr(int(info.Cols))
			}
			if info.Rows > 0 {
				result.Rows = protocol.Ptr(int(info.Rows))
			}
		}
	}
	provider, ok := d.ptyBackend.(ptybackend.SessionLaunchParamsProvider)
	if !ok {
		result.Unrecordable = protocol.Ptr(true)
		return result, nil
	}
	params, err := provider.SessionLaunchParams(ctx, msg.TerminalID)
	switch {
	case errors.Is(err, pty.ErrSessionNotFound) || errors.Is(err, os.ErrNotExist):
		result.Unrecorded = protocol.Ptr(fmt.Sprintf("read launch params: %v", err))
	case err != nil:
		result.ReadError = protocol.Ptr(fmt.Sprintf("read launch params: %v", err))
	case !params.Recorded:
		result.Unrecorded = protocol.Ptr("launch params not recorded (pre-reload worker)")
	default:
		encoded, err := json.Marshal(params)
		if err != nil {
			return result, fmt.Errorf("encode launch params: %w", err)
		}
		result.Params = protocol.Ptr(string(encoded))
	}
	return result, nil
}

func (d *Daemon) writeTerminal(ctx context.Context, msg protocol.TerminalWriteMessage) (struct{}, error) {
	if sessionmonitor.IsComposerKeystroke(msg.Source, []byte(msg.Data)) {
		// Hold the existing timer before the agent can take this input and arm a new one.
		if sessionID, shown := d.shownIn(msg.TerminalID); shown {
			d.sessionMonitor(sessionID).HoldAutoSettle()
		}
	}
	return struct{}{}, d.writeTerminalPTY(ctx, msg.TerminalID, []byte(msg.Data), msg.Source)
}

func (d *Daemon) terminalText() (ptybackend.TerminalTextProvider, error) {
	provider, ok := d.ptyBackend.(ptybackend.TerminalTextProvider)
	if !ok {
		return nil, fmt.Errorf("terminal backend does not support read_text and wait_block")
	}
	return provider, nil
}

func (d *Daemon) readTerminalText(ctx context.Context, msg protocol.TerminalReadTextMessage) (protocol.TerminalText, error) {
	provider, err := d.terminalText()
	if err != nil {
		return protocol.TerminalText{}, err
	}
	info, err := provider.ReadText(ctx, msg.TerminalID, string(msg.Mode), msg.Lines)
	if err != nil {
		return protocol.TerminalText{}, err
	}
	return protocol.TerminalText{Text: info.Text, Block: protocolTerminalBlock(info.Block), AtPrompt: info.AtPrompt, IntegrationSeen: info.IntegrationSeen, LastBlockID: strconv.FormatUint(info.LastBlockID, 10)}, nil
}

func (d *Daemon) waitTerminalBlock(ctx context.Context, msg protocol.TerminalWaitBlockMessage) (protocol.TerminalWaitResult, error) {
	provider, err := d.terminalText()
	if err != nil {
		return protocol.TerminalWaitResult{}, err
	}
	params := pty.WaitBlockParams{Until: string(msg.Until), FirstPrompt: protocol.Deref(msg.FirstPrompt)}
	if msg.AfterBlockID != nil {
		id, err := strconv.ParseUint(*msg.AfterBlockID, 10, 64)
		if err != nil {
			return protocol.TerminalWaitResult{}, errors.New("block rule: after_block_id must be an unsigned integer")
		}
		params.AfterBlockID = &id
	}
	result, err := provider.WaitBlock(ctx, msg.TerminalID, params)
	if err != nil {
		return protocol.TerminalWaitResult{}, err
	}
	return protocol.TerminalWaitResult{Block: protocolTerminalBlock(result.Block), Exited: protocol.Ptr(result.Exited), ExitCode: result.ExitCode}, nil
}

func (d *Daemon) killTerminal(ctx context.Context, msg protocol.TerminalKillMessage) (struct{}, error) {
	terminal, stop := msg.TerminalID, protocol.Deref(msg.Stop)
	ending := protocol.Deref(msg.EndingSessionID)
	if stop {
		d.markTerminalExitIntent(terminal, terminalExitStop)
	}
	if ending != "" {
		d.terminals().noteEnding(terminal, ending, time.Now())
	}
	err := d.ptyBackend.Kill(ctx, terminal, parseSignal(msg.Signal))
	if err != nil && stop {
		d.clearTerminalExitIntent(terminal, terminalExitStop)
	}
	if err != nil && ending != "" {
		d.terminals().takeEnding(terminal)
	}
	return struct{}{}, err
}

func (d *Daemon) removeTerminal(ctx context.Context, msg protocol.TerminalRemoveMessage) (struct{}, error) {
	return struct{}{}, d.ptyBackend.Remove(ctx, msg.TerminalID)
}

func (d *Daemon) themeTerminals(ctx context.Context, msg protocol.TerminalThemeMessage) (struct{}, error) {
	theme := pty.TerminalTheme{Foreground: msg.Foreground, Background: msg.Background, Cursor: msg.Cursor}
	copy(theme.ANSIPalette[:], msg.AnsiPalette)
	d.setCurrentTerminalTheme(theme)
	for _, terminal := range d.ptyBackend.TerminalIDs(ctx) {
		if err := d.ptyBackend.SetTheme(ctx, terminal, theme); err != nil {
			d.logf("set_terminal_theme: SetTheme failed for %s: %v", terminal, err)
		}
	}
	return struct{}{}, nil
}

func (d *Daemon) loginShell(_ context.Context, _ protocol.TerminalShellMessage) (protocol.TerminalShellResult, error) {
	shell := pty.GetUserLoginShell()
	env := pty.MergeEnvironment(os.Environ(), d.cachedLoginShellEnv())
	home, _ := os.UserHomeDir()
	return protocol.TerminalShellResult{Shell: shell, IntegrationExpected: pty.ShellIntegrationExpected(shell, env), Home: home}, nil
}

func (d *Daemon) screenSnapshot(ctx context.Context, msg protocol.GetScreenSnapshotMessage) (protocol.GetScreenSnapshotResultMessage, error) {
	provider, ok := d.ptyBackend.(ptybackend.ScreenSnapshotProvider)
	if !ok {
		return protocol.GetScreenSnapshotResultMessage{}, errors.New("screen snapshot not supported")
	}
	info, err := provider.ScreenSnapshot(ctx, msg.ID)
	if err != nil {
		return protocol.GetScreenSnapshotResultMessage{}, err
	}
	result := protocol.GetScreenSnapshotResultMessage{
		Event:   protocol.EventGetScreenSnapshotResult,
		ID:      msg.ID,
		Success: true,
		LastSeq: protocol.Ptr(int(info.LastSeq)),
		Cols:    protocol.Ptr(int(info.Cols)),
		Rows:    protocol.Ptr(int(info.Rows)),
		Running: protocol.Ptr(info.Running),
	}
	snapshotBytes := 0
	screenCols := 0
	screenRows := 0
	if info.Screen != nil {
		snapshotBytes = len(info.Screen.Payload)
		screenCols = int(info.Screen.Cols)
		screenRows = int(info.Screen.Rows)
		result.ScreenSnapshot = protocol.Ptr(base64.StdEncoding.EncodeToString(info.Screen.Payload))
		result.ScreenRows = protocol.Ptr(screenRows)
		result.ScreenCols = protocol.Ptr(screenCols)
	}
	d.logf(
		"PTY screen snapshot: id=%s running=%v last_seq=%d snapshot_bytes=%d screen=%dx%d have_screen=%v",
		msg.ID, info.Running, info.LastSeq, snapshotBytes,
		screenCols, screenRows, info.Screen != nil,
	)
	return result, nil
}

func (d *Daemon) kittyImage(ctx context.Context, msg protocol.GetKittyImageMessage) (pty.KittyImage, error) {
	provider, ok := d.ptyBackend.(ptybackend.KittyImageProvider)
	if !ok {
		return pty.KittyImage{}, errors.New("pty backend serves no kitty images")
	}
	return provider.KittyImage(ctx, msg.ID, uint32(msg.ImageID))
}

func (d *Daemon) resizeTerminal(ctx context.Context, msg protocol.PtyResizeMessage) (struct{}, error) {
	xpixel, ypixel := protocol.Deref(msg.Xpixel), protocol.Deref(msg.Ypixel)
	if xpixel < 0 || ypixel < 0 || xpixel > maxPTYPixelValue || ypixel > maxPTYPixelValue {
		d.logf("pty_resize: id=%s ignoring pixel geometry xpixel=%d ypixel=%d (expected 0..%d)", msg.ID, xpixel, ypixel, maxPTYPixelValue)
		xpixel, ypixel = 0, 0
	}
	if xpixel == 0 || ypixel == 0 {
		xpixel, ypixel = 0, 0
	}
	d.logf("pty_resize: id=%s cols=%d rows=%d xpixel=%d ypixel=%d", msg.ID, msg.Cols, msg.Rows, xpixel, ypixel)
	result, err := d.ptyBackend.Resize(ctx, msg.ID, uint16(msg.Cols), uint16(msg.Rows), uint16(xpixel), uint16(ypixel))
	if err != nil {
		return struct{}{}, err
	}
	if !result.StreamOrdered {
		d.publishFact(FactSessionPTYResized, string(msg.ID), ptyGeometry{
			Cols: msg.Cols, Rows: msg.Rows, XPixel: xpixel, YPixel: ypixel,
		})
	}
	return struct{}{}, nil
}

func (d *Daemon) notePointerActivity(_ context.Context, msg protocol.TerminalPointerActivityMessage) (struct{}, error) {
	sessionID, shown := d.shownIn(msg.ID)
	if shown && d.sessionMonitor(sessionID).NoteActivity() {
		d.sessionMonitor(sessionID).HoldAutoSettle()
	}
	return struct{}{}, nil
}
