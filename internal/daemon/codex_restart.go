package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/victorarias/attn/internal/codexshared"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/ptybackend"
)

const (
	codexRestartGrace       = 60 * time.Second
	codexServerReleaseGrace = 5 * time.Second
	codexStartCooldown      = 10 * time.Second
	codexRefusalLimit       = 4096
)

var codexServerAgentEnv = []string{"ATTN_TERMINAL_ID=", "ATTN_SESSION_ID=", "ATTN_AGENT="}

type codexRestart struct {
	requested bool
	swapping  bool
	forceAt   time.Time
	refusal   string
	refusedOn string
}

type codexStarting struct {
	terminal harness.TerminalID
	session  protocol.SessionID
}

type codexStartFailure struct {
	at  time.Time
	err error
}

func (s *codexServer) cooledDown() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failed.err == nil || time.Since(s.failed.at) >= codexStartCooldown {
		return nil
	}
	return fmt.Errorf("the shared Codex app-server of profile %s exited before answering %s ago, and attn starts it again %s after that: %w",
		s.profile, time.Since(s.failed.at).Round(time.Second), codexStartCooldown, s.failed.err)
}

func (s *codexServer) startFailed(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failed = codexStartFailure{at: time.Now(), err: err}
}

func (s *codexServer) exitSignal() <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.exited == nil {
		s.exited = make(chan struct{})
	}
	return s.exited
}

func (s *codexServer) markExited() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.exited != nil {
		close(s.exited)
		s.exited = nil
	}
}

func (r *codexShared) forget(s *codexServer) {
	s.mu.Lock()
	gone := s.hold(nil)
	client := s.control
	s.control = nil
	s.running = ""
	s.mu.Unlock()
	if client != nil {
		client.Close()
	}
	r.lose(s, gone)
	r.serverGone(s.profile)
	r.versionsChanged(s.profile)
}

func (r *codexShared) stop(s *codexServer, grace time.Duration) {
	exited := s.exitSignal()
	ctx := context.Background()
	provider, ok := r.d.ptyBackend.(ptybackend.SessionInfoProvider)
	if !ok {
		r.d.logf("shared Codex: the PTY backend reports no process ids; killing the app-server of profile %s", s.profile)
		r.kill(ctx, s)
		return
	}
	info, err := provider.SessionInfo(ctx, s.terminal)
	if err != nil && !errors.Is(err, pty.ErrSessionNotFound) {
		r.d.logf("shared Codex: reading the process of the app-server of profile %s: %v; killing it", s.profile, err)
		r.kill(ctx, s)
		return
	}
	if err != nil || !info.Running || info.PID <= 0 {
		return
	}
	if err := syscall.Kill(info.PID, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		r.d.logf("shared Codex: signalling the app-server of profile %s (pid %d): %v", s.profile, info.PID, err)
	}
	timer := time.NewTimer(grace)
	defer timer.Stop()
	select {
	case <-exited:
		return
	case <-timer.C:
		r.d.logf("shared Codex: the app-server of profile %s still ran %s after SIGTERM; killing it", s.profile, grace)
	case <-r.d.life.Done():
		r.d.logf("shared Codex: attn is stopping while the app-server of profile %s drains; killing it", s.profile)
	}
	r.kill(ctx, s)
}

func (r *codexShared) kill(ctx context.Context, s *codexServer) {
	if err := r.d.ptyBackend.Kill(ctx, s.terminal, syscall.SIGKILL); err != nil && !errors.Is(err, pty.ErrSessionNotFound) {
		r.d.logf("shared Codex: killing the app-server of profile %s: %v", s.profile, err)
	}
}

func (s *codexServer) restartDueLocked(installed string) bool {
	if s.control == nil || !s.control.Connected() || s.running == "" || s.restart.swapping {
		return false
	}
	if s.restart.refusal != "" && s.restart.refusedOn == installed {
		return false
	}
	return s.restart.requested || (codexReleaseVersion(installed) && installed != s.running)
}

func (r *codexShared) waitingOn(s *codexServer) (blockers []protocol.SharedCodexBlocker, quiet bool) {
	s.mu.Lock()
	active := map[string]protocol.SharedCodexBlockerReason{}
	for conversation, status := range s.held {
		if blocks, reason := status.blocks(); blocks {
			active[conversation] = reason
		}
	}
	parents := make(map[string]string, len(s.parents))
	for child, parent := range s.parents {
		parents[child] = parent
	}
	s.mu.Unlock()
	quiet = true
	for conversation, reason := range active {
		quiet = false
		for range 8 {
			if session := r.holder(s.profile, conversation); session != "" {
				blockers = append(blockers, protocol.SharedCodexBlocker{SessionID: session, Reason: reason})
				break
			}
			if conversation = parents[conversation]; conversation == "" {
				break
			}
		}
	}
	for _, starting := range r.startingViews(s.profile) {
		quiet = false
		if starting.session != "" {
			blockers = append(blockers, protocol.SharedCodexBlocker{SessionID: starting.session, Reason: protocol.SharedCodexBlockerReasonStarting})
		}
	}
	slices.SortFunc(blockers, func(a, b protocol.SharedCodexBlocker) int {
		return strings.Compare(string(a.SessionID)+string(a.Reason), string(b.SessionID)+string(b.Reason))
	})
	return slices.CompactFunc(blockers, func(a, b protocol.SharedCodexBlocker) bool { return a == b }), quiet
}

func (r *codexShared) restartWhenQuiet(s *codexServer) {
	if !r.enabledForNewLaunches() {
		return
	}
	if _, quiet := r.waitingOn(s); !quiet {
		return
	}
	r.mu.Lock()
	installed := r.installed
	r.mu.Unlock()
	s.mu.Lock()
	due := s.restartDueLocked(installed)
	if due {
		s.restart.swapping = true
	}
	s.mu.Unlock()
	if due {
		r.beginRestart(s, false)
	}
}

func (r *codexShared) blockersChanged(s *codexServer) {
	r.mu.Lock()
	installed := r.installed
	r.mu.Unlock()
	s.mu.Lock()
	shown := s.restart.swapping || s.restart.requested || (s.running != "" && codexReleaseVersion(installed) && installed != s.running)
	s.mu.Unlock()
	if shown {
		r.versionsChanged(s.profile)
	}
	r.restartWhenQuiet(s)
}

func (r *codexShared) viewsChanged(profile string) {
	r.mu.Lock()
	s := r.servers[profile]
	r.mu.Unlock()
	if s != nil {
		s.events.run(r.d, func() { r.blockersChanged(s) })
	}
}

func (r *codexShared) restartAllWhenQuiet() {
	r.mu.Lock()
	servers := make([]*codexServer, 0, len(r.servers))
	for _, s := range r.servers {
		servers = append(servers, s)
	}
	r.mu.Unlock()
	for _, s := range servers {
		r.restartWhenQuiet(s)
	}
}

func (r *codexShared) beginRestart(s *codexServer, now bool) {
	r.versionsChanged(s.profile)
	if !r.d.life.Go("codexRestart", func() { r.restart(s, now) }) {
		s.mu.Lock()
		s.restart.swapping = false
		s.mu.Unlock()
	}
}

func (r *codexShared) restart(s *codexServer, now bool) {
	defer func() {
		s.mu.Lock()
		s.restart.swapping, s.restart.forceAt = false, time.Time{}
		s.mu.Unlock()
		r.versionsChanged(s.profile)
	}()
	r.mu.Lock()
	installed := r.installed
	r.mu.Unlock()
	binary := r.binary("")
	version, err := r.preflight(s, binary)
	if err == nil && codexReleaseVersion(installed) && version != installed {
		err = fmt.Errorf("it starts as Codex %s, not the installed %s", version, installed)
	}
	if err != nil {
		r.d.logf("shared Codex: the app-server of profile %s keeps running %s: %s at %s refused to start: %v", s.profile, s.versionRunning(), installed, binary, err)
		r.refused(s, installed, err)
		return
	}
	for _, starting := range r.swap(s, binary, installed, now) {
		r.relaunchStarting(starting)
	}
}

func (r *codexShared) refused(s *codexServer, installed string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.restart.refusal, s.restart.refusedOn, s.restart.requested = refusalLine(err), installed, false
}

func (s *codexServer) versionRunning() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}

func (r *codexShared) swap(s *codexServer, binary, installed string, now bool) (relaunch []codexStarting) {
	s.ensureMu.Lock()
	defer s.ensureMu.Unlock()
	client := s.client()
	if client == nil {
		r.d.logf("shared Codex: the app-server of profile %s went away before its restart; the next connection starts the installed Codex", s.profile)
		s.mu.Lock()
		s.restart.requested = false
		s.mu.Unlock()
		return nil
	}
	if blockers, quiet := r.waitingOn(s); !now && !quiet {
		r.d.logf("shared Codex: the restart of the app-server of profile %s waits on %v", s.profile, blockers)
		return nil
	}
	starting := r.startingViews(s.profile)
	r.dropViews(s.profile, starting)
	ctx := r.d.life.Context()
	if now {
		r.interruptWaiting(ctx, s, client)
		s.mu.Lock()
		s.restart.forceAt = time.Now().Add(codexRestartGrace)
		s.mu.Unlock()
		r.versionsChanged(s.profile)
	} else if busy := r.busy(ctx, s, client); busy != "" {
		r.d.logf("shared Codex: the restart of the app-server of profile %s waits: %s", s.profile, busy)
		return nil
	}
	previous := s.versionRunning()
	r.stop(s, codexRestartGrace)
	r.forget(s)
	if r.d.life.Ended() {
		return nil
	}
	err := r.startServer(ctx, s, binary)
	if err == nil {
		err = r.connect(ctx, s)
	}
	s.mu.Lock()
	s.restart.requested = false
	s.mu.Unlock()
	if err != nil {
		r.d.logf("shared Codex: restarting the app-server of profile %s on %s: %v", s.profile, binary, err)
		s.startFailed(err)
		r.refused(s, installed, err)
		return starting
	}
	r.d.logf("shared Codex: restarted the app-server of profile %s from %s on %s", s.profile, previous, s.versionRunning())
	return starting
}

func (r *codexShared) relaunchStarting(starting codexStarting) {
	if starting.session == "" {
		return
	}
	provider, ok := r.d.ptyBackend.(ptybackend.SessionInfoProvider)
	if !ok {
		return
	}
	info, err := provider.SessionInfo(context.Background(), starting.terminal)
	if err == nil {
		err = r.d.reloadSessionForClient(starting.session, int(info.Cols), int(info.Rows))
	}
	if err != nil {
		r.d.logf("shared Codex: relaunching terminal %s, whose Codex showed no conversation when its app-server restarted: %v", starting.terminal, err)
		return
	}
	r.d.logf("shared Codex: relaunched terminal %s, whose Codex showed no conversation when its app-server restarted", starting.terminal)
}

func (r *codexShared) startingViews(profile string) []codexStarting {
	r.mu.Lock()
	var terminals []harness.TerminalID
	for _, v := range r.views {
		if v.profile == profile && !v.started {
			terminals = append(terminals, v.terminal)
		}
	}
	r.mu.Unlock()
	slices.Sort(terminals)
	starting := make([]codexStarting, 0, len(terminals))
	for _, t := range terminals {
		session, _ := r.d.terminals().Showing(t)
		starting = append(starting, codexStarting{terminal: t, session: protocol.SessionID(session)})
	}
	return starting
}

func (r *codexShared) dropViews(profile string, keep []codexStarting) {
	r.mu.Lock()
	var drops []*codexProxy
	for _, v := range r.views {
		if v.profile != profile || slices.ContainsFunc(keep, func(k codexStarting) bool { return k.terminal == v.terminal }) {
			continue
		}
		for proxy := range v.proxies {
			drops = append(drops, proxy)
		}
	}
	r.mu.Unlock()
	for _, proxy := range drops {
		proxy.drop()
	}
}

func (r *codexShared) busy(ctx context.Context, s *codexServer, client *codexshared.Client) string {
	callCtx, cancel := context.WithTimeout(ctx, codexServerCallLimit)
	defer cancel()
	raw, err := client.Call(callCtx, "thread/loaded/list", map[string]any{})
	var loaded struct {
		Data []string `json:"data"`
	}
	if err == nil {
		err = json.Unmarshal(raw, &loaded)
	}
	if err != nil {
		return "listing its conversations: " + err.Error()
	}
	for _, conversation := range loaded.Data {
		status, err := r.readStatus(callCtx, client, conversation)
		if err != nil {
			return fmt.Sprintf("reading conversation %s: %v", conversation, err)
		}
		s.mu.Lock()
		if _, held := s.held[conversation]; held {
			s.held[conversation] = status
		}
		s.mu.Unlock()
		if status.Type == "active" {
			return fmt.Sprintf("conversation %s runs a turn", conversation)
		}
	}
	return ""
}

func (r *codexShared) readStatus(ctx context.Context, client *codexshared.Client, conversation string) (codexThreadStatus, error) {
	raw, err := client.Call(ctx, "thread/read", map[string]any{"threadId": conversation})
	var read struct {
		Thread struct {
			Status codexThreadStatus `json:"status"`
		} `json:"thread"`
	}
	if err == nil {
		err = json.Unmarshal(raw, &read)
	}
	return read.Thread.Status, err
}

func (r *codexShared) interruptWaiting(ctx context.Context, s *codexServer, client *codexshared.Client) {
	s.mu.Lock()
	var waiting []string
	for conversation, status := range s.held {
		if blocks, reason := status.blocks(); blocks && reason != protocol.SharedCodexBlockerReasonTurn {
			waiting = append(waiting, conversation)
		}
	}
	s.mu.Unlock()
	for _, conversation := range waiting {
		callCtx, cancel := context.WithTimeout(ctx, codexServerCallLimit)
		raw, err := client.Call(callCtx, "thread/turns/list", map[string]any{"threadId": conversation, "limit": 1, "sortDirection": "desc"})
		var turns struct {
			Data []struct {
				ID     string `json:"id"`
				Status string `json:"status"`
			} `json:"data"`
		}
		if err == nil {
			err = json.Unmarshal(raw, &turns)
		}
		if err == nil && len(turns.Data) > 0 && turns.Data[0].Status == "inProgress" {
			_, err = client.Call(callCtx, "turn/interrupt", map[string]any{"threadId": conversation, "turnId": turns.Data[0].ID})
		}
		cancel()
		if err != nil {
			r.d.logf("shared Codex: stopping the turn of conversation %s, which waits on an answer nobody can give during the restart: %v", conversation, err)
		}
	}
}

func (st codexThreadStatus) blocks() (bool, protocol.SharedCodexBlockerReason) {
	if st.Type != "active" {
		return false, ""
	}
	switch {
	case slices.Contains(st.ActiveFlags, "waitingOnApproval"):
		return true, protocol.SharedCodexBlockerReasonApproval
	case slices.Contains(st.ActiveFlags, "waitingOnUserInput"):
		return true, protocol.SharedCodexBlockerReasonQuestion
	}
	return true, protocol.SharedCodexBlockerReasonTurn
}

func (r *codexShared) preflight(s *codexServer, binary string) (string, error) {
	if err := r.ensureDir(); err != nil {
		return "", err
	}
	socket := filepath.Join(r.dir(), codexProfileKey(s.profile)+"-preflight.sock")
	if err := os.Remove(socket); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	defer os.Remove(socket)
	cmd := exec.Command(binary, r.serverArgs(s.profile, socket)...)
	cmd.Dir = r.dir()
	cmd.Env = pty.MergeEnvironment(pty.MergeEnvironment(pty.MergeEnvironment(os.Environ(), r.d.loginShellEnvironment()), codexServerAgentEnv), r.d.spawnRoutingEnv())
	refusal := &tailBuffer{limit: codexRefusalLimit}
	cmd.Stdout, cmd.Stderr = nil, refusal
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return "", err
	}
	exited := make(chan error, 1)
	if !r.d.life.Go("codexPreflightWait", func() { exited <- cmd.Wait() }) {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		return "", errDaemonStopping
	}
	ctx, cancel := context.WithTimeout(r.d.life.Context(), codexServerStartLimit)
	defer cancel()
	version := ""
	for {
		attempt, cancelAttempt := context.WithTimeout(ctx, codexServerCallLimit)
		client, err := codexshared.Connect(attempt, socket, codexshared.ClientInfo{Name: codexClientName, Version: "1"}, nil)
		cancelAttempt()
		if err == nil {
			version = client.ServerVersion()
			client.Close()
			break
		}
		select {
		case waited := <-exited:
			if line := lastLine(refusal.String()); line != "" {
				return "", errors.New(line)
			}
			return "", fmt.Errorf("it exited before answering: %v", waited)
		case <-ctx.Done():
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			<-exited
			return "", fmt.Errorf("it did not answer within %s: %w", codexServerStartLimit, err)
		case <-time.After(25 * time.Millisecond):
		}
	}
	_ = syscall.Kill(cmd.Process.Pid, syscall.SIGTERM)
	select {
	case <-exited:
	case <-time.After(codexServerReleaseGrace):
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		<-exited
	}
	return version, nil
}

func refusalLine(err error) string {
	return lastLine(err.Error())
}

func lastLine(text string) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

type tailBuffer struct {
	mu    sync.Mutex
	limit int
	buf   bytes.Buffer
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf.Write(p)
	if over := b.buf.Len() - b.limit; over > 0 {
		b.buf.Next(over)
	}
	return len(p), nil
}

func (b *tailBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func (d *Daemon) handleRestartSharedCodex(client *wsClient, msg *protocol.RestartSharedCodexMessage) {
	err := d.codexShared().requestRestart(msg.ProfileID, msg.Now)
	result := protocol.RestartSharedCodexResultMessage{Event: protocol.EventRestartSharedCodexResult, RequestID: msg.RequestID, ProfileID: msg.ProfileID, Success: err == nil}
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
	}
	d.sendToClient(client, result)
}

func (r *codexShared) requestRestart(profile string, now bool) error {
	if !r.enabledForNewLaunches() {
		return errors.New("shared Codex is off; turn it on in Settings → Experimental")
	}
	r.mu.Lock()
	s := r.servers[profile]
	r.mu.Unlock()
	if s == nil || s.client() == nil {
		return fmt.Errorf("no shared Codex app-server runs in profile %s; the next shared Codex terminal starts one on the installed Codex", profile)
	}
	r.refreshInstalled()
	s.mu.Lock()
	s.restart.requested, s.restart.refusal, s.restart.refusedOn = true, "", ""
	begin := now && !s.restart.swapping
	if begin {
		s.restart.swapping = true
	}
	s.mu.Unlock()
	if begin {
		r.beginRestart(s, true)
		return nil
	}
	r.versionsChanged(profile)
	r.restartWhenQuiet(s)
	return nil
}
