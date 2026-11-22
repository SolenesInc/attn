package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"golang.org/x/mod/semver"

	"github.com/victorarias/attn/internal/codexshared"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/setting"
)

const (
	codexUpdateCheckKind     = "codex_update_check"
	codexUpdateCheckInterval = 24 * time.Hour
	agentVersionLimit        = 5 * time.Second
	factSharedCodexChanged   = "shared_codex.changed"
	snapshotSharedCodex      = "shared_codex_changed"
)

func (r *codexShared) binary(configured string) (string, error) {
	return r.host.Executable(r.facts(), configured)
}

func codexReleaseVersion(version string) bool {
	return version != "" && semver.IsValid("v"+version)
}

func codexOlder(version, than string) bool {
	return codexReleaseVersion(version) && codexReleaseVersion(than) && semver.Compare("v"+version, "v"+than) < 0
}

func (r *codexShared) installedVersion(ctx context.Context) (string, error) {
	binary, err := r.binary("")
	if err != nil {
		return "", err
	}
	return r.versionOf(ctx, binary)
}

func (r *codexShared) versionOf(ctx context.Context, binary string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, agentVersionLimit)
	defer cancel()
	out, err := r.host.RunBinary(ctx, binary, "--version")
	if err != nil {
		return "", err
	}
	version, ok := strings.CutPrefix(strings.TrimSpace(string(out)), "codex-cli ")
	if !ok || !codexReleaseVersion(version) {
		return "", fmt.Errorf("%s --version printed %q, not codex-cli <version>", binary, strings.TrimSpace(string(out)))
	}
	return version, nil
}

func resolvedBinary(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	return path
}

func (r *codexShared) recallBinary(ctx context.Context, s *codexServer) {
	s.mu.Lock()
	known := s.binary != ""
	s.mu.Unlock()
	if known {
		return
	}
	info, supported, err := r.terminals.Info(ctx, s.terminal)
	if !supported {
		return
	}
	if err != nil {
		r.host.Logf("shared Codex: reading which Codex the app-server of profile %s runs: %v", s.profile, err)
		return
	}
	if info.Executable == "" {
		return
	}
	s.mu.Lock()
	s.binary = info.Executable
	s.mu.Unlock()
}

func (r *codexShared) terminalBinary(profile, installed string) string {
	r.mu.Lock()
	s := r.servers[profile]
	r.mu.Unlock()
	if s == nil {
		return installed
	}
	s.mu.Lock()
	binary, running := s.binary, s.running
	s.mu.Unlock()
	if binary == "" || running == "" || binary == resolvedBinary(installed) {
		return installed
	}
	version, err := r.versionOf(r.host.Life().Context(), binary)
	if err == nil && version != running {
		err = fmt.Errorf("it reports Codex %s", version)
	}
	if err != nil {
		r.host.Logf("shared Codex: new terminals of profile %s run %s, not the app-server's Codex %s at %s: %v", profile, installed, running, binary, err)
		return installed
	}
	r.host.Logf("shared Codex: a new terminal of profile %s runs the app-server's Codex at %s instead of %s", profile, binary, installed)
	return binary
}

func (r *codexShared) refreshInstalled() {
	r.probing.Lock()
	defer r.probing.Unlock()
	version, err := r.installedVersion(r.host.Life().Context())
	if err != nil {
		r.host.Logf("shared Codex: reading the installed version: %v", err)
	}
	r.learn(&version, nil)
}

func (r *codexShared) refreshInstalledSoon() {
	r.host.Life().Go("codexInstalledVersion", r.refreshInstalled)
}

func (r *codexShared) learnTUI(v *codexView, params json.RawMessage) {
	var p struct {
		ClientInfo codexshared.ClientInfo `json:"clientInfo"`
	}
	if json.Unmarshal(params, &p) != nil {
		return
	}
	r.mu.Lock()
	changed := v.version != p.ClientInfo.Version
	v.version = p.ClientInfo.Version
	r.mu.Unlock()
	if changed {
		r.versionsChanged(v.profile)
	}
}

func (r *codexShared) terminalMoved(t harness.TerminalID, profile string) {
	r.mu.Lock()
	_, shared := r.views[t]
	r.mu.Unlock()
	if shared {
		r.versionsChanged(profile)
	}
}

func (r *codexShared) learn(installed, latest *string) {
	r.mu.Lock()
	changed := false
	moved := installed != nil && *installed != r.installed
	if moved {
		r.installed, changed = *installed, true
	}
	if latest != nil && *latest != r.latest {
		r.latest, changed = *latest, true
	}
	r.mu.Unlock()
	if changed {
		r.offerUpdate()
		r.versionsChanged()
	}
	if moved {
		r.restartAllWhenQuiet()
	}
}

func (r *codexShared) setEnqueueUpdateCheck(fn func()) {
	r.mu.Lock()
	r.enqueueUpdateCheck = fn
	r.mu.Unlock()
}

func (r *codexShared) runUpdateCheck(ctx context.Context) error {
	binary, err := r.binary("")
	if err != nil {
		r.host.Logf("shared Codex: no update check: %v", err)
		return nil
	}
	out, err := r.host.RunBinary(ctx, binary, "doctor", "--json")
	var report struct {
		Checks map[string]struct {
			Details map[string]any `json:"details"`
		} `json:"checks"`
	}
	if parseErr := json.Unmarshal(out, &report); parseErr != nil {
		if err == nil {
			err = parseErr
		}
		return fmt.Errorf("codex doctor --json: %w", err)
	}
	r.refreshInstalled()
	latest, _ := report.Checks["updates.status"].Details["latest version"].(string)
	if !codexReleaseVersion(latest) {
		return fmt.Errorf("codex doctor --json reported no latest version: %q", latest)
	}
	r.learn(nil, &latest)
	return nil
}

func (r *codexShared) versionsChanged(profiles ...string) {
	if r.notifySnapshot != nil {
		r.notifySnapshot(profiles...)
	}
}

func (r *codexShared) settingChanged(key string) {
	if key == setting.CodexExecutable.Key {
		r.refreshInstalledSoon()
		r.mu.Lock()
		enqueue := r.enqueueUpdateCheck
		r.mu.Unlock()
		if enqueue != nil {
			enqueue()
		}
	}
}

func (r *codexShared) versionLabel(ctx context.Context) string {
	version, err := r.installedVersion(ctx)
	if err != nil || version == "" {
		return "Codex version unknown"
	}
	return "Codex " + version
}

func (r *codexShared) wireState(profile string) *protocol.SharedCodexState {
	if profile == "" {
		return nil
	}
	r.mu.Lock()
	installed, latest, s := r.installed, r.latest, r.servers[profile]
	tuis := map[harness.TerminalID]string{}
	for _, v := range r.views {
		if v.profile == profile && v.version != "" {
			tuis[v.terminal] = v.version
		}
	}
	r.mu.Unlock()
	running, restart := "", codexRestart{}
	if s != nil {
		s.mu.Lock()
		running, restart = s.running, s.restart
		s.mu.Unlock()
	}
	state := &protocol.SharedCodexState{
		RunningVersion:   optionalVersion(running),
		InstalledVersion: optionalVersion(installed),
		LatestVersion:    optionalVersion(latest),
		Restart:          protocol.SharedCodexRestartNone,
	}
	switch {
	case restart.swapping:
		state.Restart = protocol.SharedCodexRestartRestarting
	case restart.refusal != "" && restart.refusedOn == installed:
		state.Restart, state.Error = protocol.SharedCodexRestartFailed, &restart.refusal
	case running != "" && (restart.requested || (installed != "" && running != installed)):
		state.Restart = protocol.SharedCodexRestartPending
	}
	if state.Restart == protocol.SharedCodexRestartPending || state.Restart == protocol.SharedCodexRestartRestarting {
		state.Blockers, _ = r.waitingOn(s)
	}
	if !restart.forceAt.IsZero() {
		state.ForceAt = protocol.Ptr(restart.forceAt.UTC().Format(time.RFC3339Nano))
	}
	for terminal, version := range tuis {
		if !codexOlder(version, running) {
			continue
		}
		if session, ok := r.host.Sessions().Showing(terminal); ok && !slices.Contains(state.StaleSessions, protocol.SessionID(session)) {
			state.StaleSessions = append(state.StaleSessions, protocol.SessionID(session))
		}
	}
	slices.Sort(state.StaleSessions)
	return state
}

func optionalVersion(version string) *string {
	if version == "" {
		return nil
	}
	return &version
}

func (r *codexShared) changedMessage(profile string) *protocol.SharedCodexChangedMessage {
	return &protocol.SharedCodexChangedMessage{Event: protocol.EventSharedCodexChanged, ProfileID: profile, SharedCodex: r.wireState(profile)}
}
