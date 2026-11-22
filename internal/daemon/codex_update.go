package daemon

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/protocol"
)

const (
	notificationKindCodexUpdate = "codex_update_available"
	notificationActionInstall   = "install_codex_update"
	notificationSeverityInfo    = "info"
)

func (h codexHarness) handleInstallCodexUpdate(profile string, raw any) any {
	msg := raw.(*protocol.InstallCodexUpdateMessage)
	err := h.shared.installUpdate(profile, msg.DesktopID)
	result := protocol.InstallCodexUpdateResultMessage{Event: protocol.EventInstallCodexUpdateResult, RequestID: msg.RequestID, Success: err == nil, ProfileID: profile, SharedCodex: h.shared.wireState(profile)}
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
	}
	return result
}

func (r *codexShared) installUpdate(profileID, desktopID string) error {
	binary, err := r.binary("")
	if err != nil {
		return err
	}
	command := codexShellQuote(binary) + " update"
	ran, err := r.host.RunInShellTile(r.host.Life().Context(), profileID, desktopID, "Codex update", command)
	if ran {
		r.refreshInstalled()
	}
	var noMarks harness.NoShellMarksError
	if errors.As(err, &noMarks) {
		return fmt.Errorf("your shell %s gives attn no command marks, so attn can't run Codex's update and tell when it finishes; run attn codex update in a terminal instead", noMarks.Shell)
	}
	var exited harness.ShellExitError
	if errors.As(err, &exited) {
		return fmt.Errorf("the update exited %d; its output is in the tile", exited.Code)
	}
	return err
}

func codexShellQuote(s string) string {
	if s != "" && strings.Trim(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_./+@%") == "" {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func (r *codexShared) offerUpdate() {
	r.mu.Lock()
	installed, latest := r.installed, r.latest
	r.mu.Unlock()
	offered := ""
	if codexOlder(installed, latest) {
		offered = latest
	}
	existing, err := r.host.Notifications().List()
	if err != nil {
		r.host.Logf("shared Codex: list notifications: %v", err)
		return
	}
	for _, n := range existing {
		installedIt := codexReleaseVersion(installed) && !codexOlder(installed, n.SourceID)
		replaced := codexOlder(n.SourceID, offered)
		if n.Kind != notificationKindCodexUpdate || !(installedIt || replaced) {
			continue
		}
		if err := r.host.Notifications().Resolve(n.ID); err != nil {
			r.host.Logf("shared Codex: resolve the Codex %s update notification: %v", n.SourceID, err)
		}
	}
	if offered == "" {
		return
	}
	impact := "Install runs codex update in a terminal tile. Shared Codex moves to it once your agents are idle."
	if err := r.host.Notifications().Ensure(harness.Notification{
		ID:         notificationKindCodexUpdate + ":" + offered,
		Kind:       notificationKindCodexUpdate,
		Severity:   notificationSeverityInfo,
		Title:      "Codex " + offered + " is available",
		Body:       impact,
		Trigger:    "The daily codex doctor check found a newer Codex than the installed " + installed + ".",
		Impact:     impact,
		Actions:    []harness.NotificationAction{{Kind: notificationActionInstall, Label: "Install"}},
		SourceKind: codexID,
		SourceID:   offered,
	}); err != nil {
		r.host.Logf("shared Codex: notify about Codex %s: %v", offered, err)
	}
}

func (h codexHarness) handleCodexStatus(_ context.Context, raw any) protocol.Response {
	msg := raw.(*protocol.CodexStatusMessage)
	profileID, profileName, err := h.host.Profiles().Resolve(protocol.Deref(msg.CallerSessionID), protocol.Deref(msg.Profile))
	if err != nil {
		return protocol.Response{Ok: false, Error: protocol.Ptr(err.Error())}
	}
	if protocol.Deref(msg.Refresh) {
		h.shared.refreshInstalled()
	}
	binary, _ := h.shared.binary("")
	return protocol.Response{Ok: true, CodexStatusResult: &protocol.CodexStatusResult{
		ProfileID:   profileID,
		ProfileName: profileName,
		Executable:  binary,
		SharedCodex: h.shared.wireState(profileID),
	}}
}

func (h codexHarness) handleRestartSharedCodexCLI(_ context.Context, raw any) protocol.Response {
	msg := raw.(*protocol.RestartSharedCodexMessage)
	if err := h.shared.requestRestart(msg.ProfileID, msg.Now); err != nil {
		return protocol.Response{Ok: false, Error: protocol.Ptr(err.Error())}
	}
	return protocol.Response{Ok: true}
}
