package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"time"

	"golang.org/x/mod/semver"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/codexshared"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/jobs"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
)

const (
	codexUpdateCheckKind     = "codex_update_check"
	codexUpdateCheckInterval = 24 * time.Hour
	codexVersionLimit        = 5 * time.Second
)

func (r *codexShared) binary(configured string) string {
	codex := agentdriver.MustGet(string(protocol.SessionAgentCodex))
	env := pty.MergeEnvironment(os.Environ(), r.d.loginShellEnvironment())
	name := strings.TrimSpace(configured)
	if name == "" {
		name = strings.TrimSpace(r.d.daemonSetting(SettingCodexExecutable))
	}
	if name == "" {
		name, _ = pty.LookupEnv(env, codex.ExecutableEnvVar())
		name = strings.TrimSpace(name)
	}
	if name == "" {
		name = codex.DefaultExecutable()
	}
	if path, ok := pty.ResolveCommandPath(name, env); ok {
		return path
	}
	return name
}

func (r *codexShared) run(ctx context.Context, binary string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Env = pty.MergeEnvironment(os.Environ(), r.d.loginShellEnvironment())
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return out, fmt.Errorf("%s %s: %w: %s", binary, strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

func codexReleaseVersion(version string) bool {
	return version != "" && semver.IsValid("v"+version)
}

func codexOlder(version, than string) bool {
	return codexReleaseVersion(version) && codexReleaseVersion(than) && semver.Compare("v"+version, "v"+than) < 0
}

func (r *codexShared) installedVersion(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, codexVersionLimit)
	defer cancel()
	binary := r.binary("")
	out, err := r.run(ctx, binary, "--version")
	if err != nil {
		return "", err
	}
	version, ok := strings.CutPrefix(strings.TrimSpace(string(out)), "codex-cli ")
	if !ok || !codexReleaseVersion(version) {
		return "", fmt.Errorf("%s --version printed %q, not codex-cli <version>", binary, strings.TrimSpace(string(out)))
	}
	return version, nil
}

func (r *codexShared) refreshInstalled() {
	if !r.enabledForNewLaunches() {
		return
	}
	version, err := r.installedVersion(r.d.life.Context())
	if err != nil {
		r.d.logf("shared Codex: reading the installed version: %v", err)
	}
	r.learn(&version, nil)
}

func (r *codexShared) refreshInstalledSoon() {
	r.d.life.Go("codexInstalledVersion", r.refreshInstalled)
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
		r.versionsChanged()
	}
	if moved {
		r.restartAllWhenQuiet()
	}
}

func (d *Daemon) registerCodexUpdateCheckCron(runner *jobs.Runner) {
	if err := runner.RegisterCron(codexUpdateCheckKind, codexUpdateCheckInterval, d.codexUpdateCheckHandler, jobs.HandlerConfig{}); err != nil {
		d.logf("shared Codex: register the daily update check: %v", err)
	}
}

func (d *Daemon) queueCodexUpdateCheck() {
	queue := d.jobQueueRef()
	if queue == nil {
		return
	}
	if _, err := queue.Enqueue(codexUpdateCheckKind, jobs.EnqueueOptions{UniqueKey: jobs.CronKey, RunNow: true}); err != nil {
		d.logf("shared Codex: queue the update check: %v", err)
	}
}

func (d *Daemon) codexUpdateCheckHandler(ctx context.Context, _ *jobs.Job) (any, error) {
	r := d.codexShared()
	if !r.enabledForNewLaunches() {
		return nil, nil
	}
	out, err := r.run(ctx, r.binary(""), "doctor", "--json")
	var report struct {
		Checks map[string]struct {
			Details map[string]any `json:"details"`
		} `json:"checks"`
	}
	if parseErr := json.Unmarshal(out, &report); parseErr != nil {
		if err == nil {
			err = parseErr
		}
		return nil, fmt.Errorf("codex doctor --json: %w", err)
	}
	r.refreshInstalled()
	latest, _ := report.Checks["updates.status"].Details["latest version"].(string)
	if !codexReleaseVersion(latest) {
		return nil, fmt.Errorf("codex doctor --json reported no latest version: %q", latest)
	}
	r.learn(nil, &latest)
	return nil, nil
}

func (r *codexShared) versionsChanged(profiles ...string) {
	if len(profiles) == 0 {
		all, err := r.d.store.ListProfiles(false)
		if err != nil {
			r.d.logf("shared Codex: %v", err)
			return
		}
		for _, profile := range all {
			profiles = append(profiles, profile.ID)
		}
	}
	r.d.coalesceSnapshots(func() {
		for _, profile := range profiles {
			r.d.publishFact(FactSharedCodexChanged, profile, nil)
		}
	})
}

func (r *codexShared) settingChanged(key string) {
	switch key {
	case SettingCodexSharedEnabled:
		if r.enabledForNewLaunches() {
			r.d.queueCodexUpdateCheck()
		}
		r.versionsChanged()
	case SettingCodexExecutable:
		r.refreshInstalledSoon()
	}
}

func (r *codexShared) wireState(profile string) *protocol.SharedCodexState {
	if profile == "" || !r.enabledForNewLaunches() {
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
		if session, ok := r.d.terminals().Showing(terminal); ok && !slices.Contains(state.StaleSessions, protocol.SessionID(session)) {
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

func (d *Daemon) projectSharedCodex() {
	if d.store == nil || d.wsHub == nil {
		return
	}
	r := d.codexShared()
	d.projectSnapshot(snapshotSharedCodex, func() {
		snapshots := map[string][]byte{}
		d.wsHub.ForEachClient(func(client *wsClient) {
			profile := client.selectedProfile()
			if profile == "" {
				return
			}
			data, ok := snapshots[profile]
			if !ok {
				var err error
				if data, err = json.Marshal(r.changedMessage(profile)); err != nil {
					d.logf("shared Codex snapshot: %v", err)
					return
				}
				snapshots[profile] = data
			}
			d.sendOutbound(client, outboundMessage{kind: messageKindText, payload: data})
		})
	})
}
