package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/layouttree"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/store"
)

const (
	notificationKindCodexUpdate = "codex_update_available"
	notificationActionInstall   = "install_codex_update"
)

func (d *Daemon) handleInstallCodexUpdate(client *wsClient, msg *protocol.InstallCodexUpdateMessage) {
	profile := client.selectedProfile()
	err := d.installCodexUpdate(profile, msg.DesktopID)
	result := protocol.InstallCodexUpdateResultMessage{Event: protocol.EventInstallCodexUpdateResult, RequestID: msg.RequestID, Success: err == nil, ProfileID: profile, SharedCodex: d.codexShared().wireState(profile)}
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
	}
	d.sendToClient(client, result)
}

func (d *Daemon) installCodexUpdate(profileID, desktopID string) error {
	r := d.codexShared()
	binary, err := r.binary("")
	if err != nil {
		return err
	}
	command := shellQuote(binary) + " update"
	shell := pty.GetUserLoginShell()
	if !pty.ShellIntegrationExpected(shell, pty.MergeEnvironment(os.Environ(), d.cachedLoginShellEnv())) {
		return fmt.Errorf("your shell %s gives attn no command marks, so attn can't run Codex's update and tell when it finishes; run attn codex update in a terminal instead", shell)
	}
	provider, err := d.terminalText()
	if err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	id := protocol.SessionID(uuid.NewString())
	spawn := &protocol.SpawnSessionMessage{Cmd: protocol.CmdSpawnSession, ID: id, ProfileID: profileID, Agent: protocol.AgentShellValue, Cwd: home, Cols: 80, Rows: 24, Label: protocol.Ptr("Codex update")}
	policy := internalSpawnPolicy{launchPlacement: &launchPlacement{desktopID: desktopID, direction: layouttree.DirectionVertical}, userStarted: true}
	if rejection := d.runSpawnPipeline(spawn, policy); rejection != nil {
		return fmt.Errorf("open a terminal tile for Codex's update: %w", rejection.reason())
	}
	terminal := d.primaryTerminal(id)
	defer r.refreshInstalled()

	ctx, cancel := context.WithTimeout(d.life.Context(), terminalFirstPromptTripwire)
	first, err := provider.WaitBlock(ctx, terminal, pty.WaitBlockParams{Until: "prompt", FirstPrompt: true})
	cancel()
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return fmt.Errorf("the shell in the new tile showed no prompt within limit=%s (%s); nothing was typed, so run %s there yourself", terminalFirstPromptTripwire, terminalFirstPromptReceipt, command)
	case err != nil:
		return fmt.Errorf("wait for the new tile's prompt: %w", err)
	case first.Exited:
		return errors.New("the new tile's shell exited before its first prompt")
	}
	state, err := provider.ReadText(d.life.Context(), terminal, "screen", 0)
	if err != nil {
		return err
	}
	last := state.LastBlockID
	if err := d.writeTerminalPTY(d.life.Context(), terminal, []byte(command+"\r"), "agent"); err != nil {
		return fmt.Errorf("type Codex's update into the new tile: %w", err)
	}
	finished, err := provider.WaitBlock(d.life.Context(), terminal, pty.WaitBlockParams{Until: "prompt", AfterBlockID: &last})
	switch {
	case err != nil || finished.Exited:
		return errors.New("the tile closed before Codex's update finished")
	case finished.Block != nil && finished.Block.ExitCode != nil && *finished.Block.ExitCode != 0:
		return fmt.Errorf("the update exited %d; its output is in the tile", *finished.Block.ExitCode)
	}
	return nil
}

func shellQuote(s string) string {
	if s != "" && strings.Trim(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_./+@%") == "" {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func (r *codexShared) offerUpdate() {
	if r.d.store == nil {
		return
	}
	r.mu.Lock()
	installed, latest := r.installed, r.latest
	r.mu.Unlock()
	offered := ""
	if codexOlder(installed, latest) {
		offered = latest
	}
	existing, err := r.d.store.ListNotifications()
	if err != nil {
		r.d.logf("shared Codex: list notifications: %v", err)
		return
	}
	for _, n := range existing {
		installedIt := codexReleaseVersion(installed) && !codexOlder(installed, n.SourceID)
		replaced := codexOlder(n.SourceID, offered)
		if n.Kind != notificationKindCodexUpdate || !(installedIt || replaced) {
			continue
		}
		resolved, err := r.d.store.ResolveNotification(n.ID, time.Now())
		if err != nil {
			r.d.logf("shared Codex: resolve the Codex %s update notification: %v", n.SourceID, err)
			continue
		}
		if resolved {
			r.d.publishFact(FactNotificationRead, n.ID, nil)
		}
	}
	if offered == "" {
		return
	}
	impact := "Install runs codex update in a terminal tile. Shared Codex moves to it once your agents are idle."
	record, created, err := r.d.store.EnsureNotification(store.NotificationRecord{
		ID:         notificationKindCodexUpdate + ":" + offered,
		Kind:       notificationKindCodexUpdate,
		Severity:   store.NotificationInfo,
		Title:      "Codex " + offered + " is available",
		Body:       impact,
		Trigger:    "The daily codex doctor check found a newer Codex than the installed " + installed + ".",
		Impact:     impact,
		Actions:    []store.NotificationAction{{Kind: notificationActionInstall, Label: "Install"}},
		SourceKind: "codex",
		SourceID:   offered,
	}, time.Now())
	if err != nil {
		r.d.logf("shared Codex: notify about Codex %s: %v", offered, err)
		return
	}
	if created {
		r.d.publishFact(FactNotificationCreated, record.ID, nil)
	}
}

func (d *Daemon) handleCodexStatus(conn net.Conn, msg *protocol.CodexStatusMessage) {
	profile, err := d.resolveGardenProfile(protocol.Deref(msg.CallerSessionID), protocol.Deref(msg.Profile), "")
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	r := d.codexShared()
	if protocol.Deref(msg.Refresh) {
		r.refreshInstalled()
	}
	binary, _ := r.binary("")
	_ = json.NewEncoder(conn).Encode(protocol.Response{Ok: true, CodexStatusResult: &protocol.CodexStatusResult{
		ProfileID:   profile.ID,
		ProfileName: profile.Name,
		Executable:  binary,
		SharedCodex: r.wireState(profile.ID),
	}})
}

func (d *Daemon) handleRestartSharedCodexCLI(conn net.Conn, msg *protocol.RestartSharedCodexMessage) {
	if err := d.codexShared().requestRestart(msg.ProfileID, msg.Now); err != nil {
		d.sendError(conn, err.Error())
		return
	}
	d.sendOK(conn)
}
