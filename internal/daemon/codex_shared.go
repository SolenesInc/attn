package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"nhooyr.io/websocket"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/codexshared"
	"github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/hooks"
	"github.com/victorarias/attn/internal/launchcontract"
	"github.com/victorarias/attn/internal/pausepoint"
	profilespkg "github.com/victorarias/attn/internal/profiles"
	"github.com/victorarias/attn/internal/prompts"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/ptybackend"
	"github.com/victorarias/attn/internal/store"
)

const (
	codexServerTerminalPrefix = "codex-server-"
	codexServerAgent          = "codex-app-server"
	codexClientName           = "codex-tui"
	codexServerStartLimit     = 30 * time.Second
	codexServerCallLimit      = 5 * time.Second
)

const codexServerLaunchScript = `
ulimit -Sn "$1"
shift
exec "$@"
`

type codexShared struct {
	d         *Daemon
	mu        sync.Mutex
	servers   map[string]*codexServer
	views     map[harness.TerminalID]*codexView
	seq       uint64
	installed string
	latest    string

	archivingAfterClose map[string]int
}

type codexServer struct {
	profile  string
	terminal harness.TerminalID
	socket   string
	ensureMu sync.Mutex
	mu       sync.Mutex
	control  *codexshared.Client
	held     map[string]codexThreadStatus
	parents  map[string]string
	attempt  *codexAttempt
	events   codexEvents
	running  string
	binary   string
	exited   chan struct{}
	restart  codexRestart
	failed   codexStartFailure
}

type codexAttempt struct {
	connected bool
	early     []codexshared.Message
}

type codexProxy struct {
	drop context.CancelFunc
}

type codexView struct {
	terminal   harness.TerminalID
	profile    string
	socket     string
	server     *http.Server
	proxies    map[*codexProxy]bool
	started    bool
	thread     string
	shownSeq   uint64
	trusted    string
	version    string
	unprompted map[string]bool
}

func (d *Daemon) codexShared() *codexShared {
	d.codexSharedOnce.Do(func() {
		r := &codexShared{d: d, servers: make(map[string]*codexServer), views: make(map[harness.TerminalID]*codexView), archivingAfterClose: make(map[string]int)}
		d.codexSharedState = r
		d.life.Go("codexSharedStop", func() {
			<-d.life.Done()
			r.close()
		})
	})
	return d.codexSharedState
}

func codexProfileKey(profile string) string {
	sum := sha256.Sum256([]byte(profile))
	return hex.EncodeToString(sum[:4])
}

func codexServerTerminal(profile string) harness.TerminalID {
	return harness.TerminalID(codexServerTerminalPrefix + codexProfileKey(profile))
}

func (r *codexShared) runsLink(t harness.TerminalID) bool {
	return strings.HasPrefix(string(t), codexServerTerminalPrefix)
}

func (r *codexShared) dir() string {
	return codexshared.SocketDir(r.d.dataRoot)
}

func (r *codexShared) ensureDir() error {
	dir := r.dir()
	if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if owner, ok := info.Sys().(*syscall.Stat_t); !info.IsDir() || !ok || int(owner.Uid) != os.Getuid() {
		return fmt.Errorf("the shared Codex socket directory %s is not a directory of this user", dir)
	}
	return os.Chmod(dir, 0o700)
}

func (r *codexShared) viewSocket(t harness.TerminalID) string {
	sum := sha256.Sum256([]byte(t))
	return filepath.Join(r.dir(), "t-"+hex.EncodeToString(sum[:8])+".sock")
}

func (r *codexShared) server(profile string) *codexServer {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.servers[profile]
	if s == nil {
		s = &codexServer{
			profile:  profile,
			terminal: codexServerTerminal(profile),
			socket:   filepath.Join(r.dir(), codexProfileKey(profile)+".sock"),
		}
		r.servers[profile] = s
	}
	return s
}

func (s *codexServer) client() *codexshared.Client {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.control != nil && s.control.Connected() {
		return s.control
	}
	return nil
}

func (r *codexShared) kind() string { return codexLinkKind }

func (r *codexShared) canRun(req *spawnRequest) bool {
	return req.agent == string(protocol.SessionAgentCodex) && !req.hasPluginDriver && !req.isShell && req.policy.unattendedLaunch.Executable == ""
}

func (r *codexShared) enabledForNewLaunches() bool {
	return parseBooleanSetting(r.d.homeSetting(SettingCodexSharedEnabled))
}

func (r *codexShared) admit(profile, conversation string) error {
	if other := r.loadedElsewhere(profile, conversation); conversation != "" && other != "" {
		return fmt.Errorf("conversation %s is open in the shared Codex of profile %s; it can open here once that profile lets it go, about a minute after no terminal shows it", conversation, other)
	}
	return nil
}

func (r *codexShared) prepareLaunch(opts *ptybackend.SpawnOptions, profile string) error {
	remote, err := r.openView(opts.ID, profile)
	if err != nil {
		return err
	}
	installed := r.binary(configuredLaunchExecutable(agentdriver.MustGet(string(protocol.SessionAgentCodex)), opts))
	if _, err := r.ensureServer(r.d.life.Context(), profile, installed); err != nil {
		return err
	}
	r.answerTrustIfKnown(opts)
	opts.CodexRemote = remote
	opts.LaunchExecutable = r.terminalBinary(profile, installed)
	r.refreshInstalledSoon()
	return nil
}

func (r *codexShared) answerTrustIfKnown(opts *ptybackend.SpawnOptions) {
	if opts.UnattendedLaunch.IsZero() && !r.d.store.HasStartedConversationIn(append([]string{opts.CWD}, git.RepositoryWorktrees(opts.CWD)...)) {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if v := r.views[opts.ID]; v != nil {
		v.trusted = opts.CWD
	}
}

func (r *codexShared) ensureServer(ctx context.Context, profile, executable string) (*codexServer, error) {
	s := r.server(profile)
	s.ensureMu.Lock()
	defer s.ensureMu.Unlock()
	if s.client() != nil {
		return s, nil
	}
	if r.serverRunning(ctx, s.terminal) {
		r.recallBinary(ctx, s)
	} else {
		if err := s.cooledDown(); err != nil {
			return nil, err
		}
		if err := r.startServer(ctx, s, executable); err != nil {
			return nil, err
		}
	}
	if err := r.connect(ctx, s); err != nil {
		if !r.serverRunning(ctx, s.terminal) {
			s.startFailed(err)
		}
		return nil, err
	}
	return s, nil
}

func (r *codexShared) serverRunning(ctx context.Context, t harness.TerminalID) bool {
	if _, live := r.d.liveTerminals(ctx)[t]; !live {
		return false
	}
	if provider, ok := r.d.ptyBackend.(ptybackend.SessionInfoProvider); ok {
		info, err := provider.SessionInfo(ctx, t)
		return err == nil && info.Running
	}
	return true
}

func (r *codexShared) startServer(ctx context.Context, s *codexServer, executable string) error {
	if err := r.d.ptyBackend.Remove(ctx, s.terminal); err != nil && !errors.Is(err, pty.ErrSessionNotFound) && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("clear the exited Codex app-server: %w", err)
	}
	if err := r.ensureDir(); err != nil {
		return err
	}
	if err := os.Remove(s.socket); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	var files syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &files); err != nil {
		return fmt.Errorf("read the open-file limit for the shared Codex app-server: %w", err)
	}
	binary := r.binary(executable)
	command := append([]string{"/bin/sh", "-c", codexServerLaunchScript, "attn-codex-server", strconv.FormatUint(files.Cur, 10), binary}, r.serverArgs(s.profile, s.socket)...)
	opts := ptybackend.SpawnOptions{
		ID: s.terminal, Agent: codexServerAgent, Label: "Codex app-server", CWD: r.dir(), Cols: 80, Rows: 24,
		Executable:    resolvedBinary(binary),
		AgentArgv:     command,
		AgentEnv:      codexServerAgentEnv,
		LoginShellEnv: r.d.loginShellEnvironment(),
		DaemonEnv:     r.d.spawnRoutingEnv(),
	}
	if err := r.d.ptyBackend.Spawn(ctx, withLifecycleID(opts)); err != nil {
		return fmt.Errorf("start the shared Codex app-server: %w", err)
	}
	s.mu.Lock()
	s.binary = opts.Executable
	s.mu.Unlock()
	r.d.logf("shared Codex: started the app-server of profile %s in terminal %s with an open-file limit of %d", s.profile, s.terminal, files.Cur)
	return nil
}

func (r *codexShared) serverArgs(profile, socket string) []string {
	args := []string{"app-server"}
	for _, override := range hooks.GenerateCodexServerConfigOverrides(r.d.wrapperPath(), r.d.socketPath, profile) {
		args = append(args, "-c", override)
	}
	return append(args, "--listen", "unix://"+socket)
}

func (r *codexShared) connect(ctx context.Context, s *codexServer) error {
	defer func() {
		s.mu.Lock()
		if s.attempt != nil && !s.attempt.connected {
			s.attempt = nil
		}
		s.mu.Unlock()
	}()
	deadline := time.Now().Add(codexServerStartLimit)
	for {
		a := &codexAttempt{}
		s.mu.Lock()
		s.attempt = a
		s.mu.Unlock()
		attempt, cancel := context.WithTimeout(ctx, codexServerCallLimit)
		client, err := codexshared.Connect(attempt, s.socket, codexshared.ClientInfo{Name: codexClientName, Version: "1"}, func(m codexshared.Message) { r.observeServer(s, a, m) })
		var loaded json.RawMessage
		if err == nil {
			loaded, err = client.Call(attempt, "thread/loaded/list", map[string]any{})
			if err != nil {
				client.Close()
			}
		}
		cancel()
		if err == nil {
			var list struct {
				Data []string `json:"data"`
			}
			_ = json.Unmarshal(loaded, &list)
			s.mu.Lock()
			s.control = client
			s.running = client.ServerVersion()
			if s.running == s.restart.refusedOn {
				s.restart.refusal, s.restart.refusedOn = "", ""
			}
			gone := s.hold(list.Data)
			for _, m := range a.early {
				gone = append(gone, s.trackLocked(m)...)
			}
			a.connected, a.early = true, nil
			s.mu.Unlock()
			r.lose(s, gone)
			r.versionsChanged(s.profile)
			if !r.d.life.Go("codexServerControl", func() { r.watchControl(s, client) }) {
				client.Close()
				return errDaemonStopping
			}
			r.readLoaded(s, client, list.Data)
			s.events.run(r.d, func() { r.readHidden(s, client) })
			s.events.run(r.d, func() {
				r.refreshInstalled()
				r.restartWhenQuiet(s)
			})
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("the shared Codex app-server of profile %s did not answer within %s: %w", s.profile, codexServerStartLimit, err)
		}
		if !r.serverRunning(ctx, s.terminal) {
			return fmt.Errorf("the shared Codex app-server of profile %s exited before answering: %w", s.profile, err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
}

func (r *codexShared) watchControl(s *codexServer, client *codexshared.Client) {
	select {
	case <-r.d.life.Done():
	case <-client.Done():
	}
	s.mu.Lock()
	if s.control == client {
		s.control = nil
	}
	s.mu.Unlock()
	r.reconnect(s)
}

func (r *codexShared) reconnect(s *codexServer) {
	ctx := r.d.life.Context()
	s.ensureMu.Lock()
	defer s.ensureMu.Unlock()
	if ctx.Err() != nil || s.client() != nil || !r.serverRunning(ctx, s.terminal) {
		return
	}
	if err := r.connect(ctx, s); err != nil {
		r.d.logf("shared Codex: reconnecting to the app-server of profile %s: %v", s.profile, err)
	}
}

func (s *codexServer) hold(loaded []string) []string {
	fresh := make(map[string]codexThreadStatus, len(loaded))
	for _, conversation := range loaded {
		fresh[conversation] = codexThreadStatus{}
	}
	var gone []string
	for conversation := range s.held {
		if _, still := fresh[conversation]; !still {
			gone = append(gone, conversation)
			delete(s.parents, conversation)
		}
	}
	s.held = fresh
	return gone
}

func (r *codexShared) lose(s *codexServer, conversations []string) {
	for _, conversation := range conversations {
		s.events.run(r.d, func() { r.lost(s, conversation) })
	}
}

func (r *codexShared) observeServer(s *codexServer, a *codexAttempt, m codexshared.Message) {
	var gone []string
	tracked := false
	s.mu.Lock()
	if s.attempt == a {
		switch m.Method {
		case "thread/name/updated":
			r.observeName(s, m)
		case "thread/status/changed", "thread/started", "thread/closed":
			if m.Method == "thread/status/changed" {
				r.observeStatus(s, m)
			}
			if a.connected {
				gone, tracked = s.trackLocked(m), true
			} else {
				a.early = append(a.early, m)
			}
		}
	}
	s.mu.Unlock()
	r.lose(s, gone)
	if tracked {
		s.events.run(r.d, func() { r.blockersChanged(s) })
	}
}

func (s *codexServer) trackLocked(m codexshared.Message) []string {
	var p struct {
		ThreadID string `json:"threadId"`
		Thread   struct {
			ID        string            `json:"id"`
			Ephemeral bool              `json:"ephemeral"`
			Parent    string            `json:"parentThreadId"`
			Status    codexThreadStatus `json:"status"`
		} `json:"thread"`
		Status codexThreadStatus `json:"status"`
	}
	if json.Unmarshal(m.Params, &p) != nil {
		return nil
	}
	if s.held == nil {
		s.held = make(map[string]codexThreadStatus)
	}
	if p.Thread.Parent != "" && p.Thread.ID != "" {
		s.adoptLocked(p.Thread.ID, p.Thread.Parent)
	}
	switch {
	case m.Method == "thread/started" && !p.Thread.Ephemeral && p.Thread.ID != "":
		s.held[p.Thread.ID] = p.Thread.Status
	case m.Method == "thread/status/changed" && p.Status.Type != "notLoaded" && p.ThreadID != "":
		s.held[p.ThreadID] = p.Status
	case m.Method == "thread/closed" || p.Status.Type == "notLoaded":
		_, gone := s.held[p.ThreadID]
		delete(s.held, p.ThreadID)
		delete(s.parents, p.ThreadID)
		if gone {
			return []string{p.ThreadID}
		}
	}
	return nil
}

func (r *codexShared) lost(s *codexServer, conversation string) {
	s.mu.Lock()
	_, again := s.held[conversation]
	s.mu.Unlock()
	sessionID := r.holder(s.profile, conversation)
	if again || r.d.store.Get(sessionID) == nil {
		return
	}
	r.d.sessionMonitor(sessionID).FinishUsage()()
	r.observe(sessionID, pty.ProgramIdle)
}

func (r *codexShared) openView(t harness.TerminalID, profile string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if v := r.views[t]; v != nil {
		return "unix://" + v.socket, nil
	}
	if err := r.ensureDir(); err != nil {
		return "", err
	}
	v := &codexView{terminal: t, profile: profile, socket: r.viewSocket(t), proxies: make(map[*codexProxy]bool)}
	listener, err := listenUnixAtomically(v.socket)
	if err != nil {
		return "", fmt.Errorf("open the Codex proxy of terminal %s: %w", t, err)
	}
	v.server = &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) { r.serveView(v, w, req) })}
	if err := r.d.store.SaveTerminalView(store.TerminalView{TerminalID: string(t), Link: codexLinkKind, ProfileID: profile}); err != nil {
		_ = listener.Close()
		_ = os.Remove(v.socket)
		return "", err
	}
	if !r.d.life.Go("codexView", func() { _ = v.server.Serve(listener) }) {
		_ = listener.Close()
		_ = os.Remove(v.socket)
		return "", errDaemonStopping
	}
	r.views[t] = v
	return "unix://" + v.socket, nil
}

func (r *codexShared) terminalDropped(t harness.TerminalID) {
	r.mu.Lock()
	v := r.views[t]
	r.mu.Unlock()
	if v != nil {
		r.closeView(t)
		r.d.life.Go("codexServerIdle", func() {
			r.profileReleased(v.profile)
			pausepoint.At(pausepoint.CodexViewReleaseChecked)
		})
	}
}

func (r *codexShared) closeView(t harness.TerminalID) {
	r.mu.Lock()
	v := r.views[t]
	delete(r.views, t)
	r.mu.Unlock()
	if v != nil {
		_ = v.server.Close()
		_ = os.Remove(v.socket)
		r.versionsChanged(v.profile)
	}
	if err := r.d.store.DeleteTerminalView(string(t)); err != nil {
		r.d.logf("shared Codex: %v", err)
	}
}

func (r *codexShared) close() {
	r.mu.Lock()
	views := make([]*codexView, 0, len(r.views))
	for _, v := range r.views {
		views = append(views, v)
	}
	servers := make([]*codexServer, 0, len(r.servers))
	for _, s := range r.servers {
		servers = append(servers, s)
	}
	r.mu.Unlock()
	for _, v := range views {
		_ = v.server.Close()
		_ = os.Remove(v.socket)
	}
	for _, s := range servers {
		if client := s.client(); client != nil {
			client.Close()
		}
		if r.d.ptyBackendMode() == "embedded" {
			r.kill(context.Background(), s)
		}
	}
}

func (r *codexShared) serveView(v *codexView, w http.ResponseWriter, req *http.Request) {
	release, admitted := r.d.life.Hold("codexViewConnection")
	if !admitted {
		http.Error(w, "attn is stopping", http.StatusServiceUnavailable)
		return
	}
	defer release()
	ctx, proxy := r.proxying(v)
	defer r.proxied(v, proxy)
	s, err := r.ensureServer(r.d.life.Context(), v.profile, "")
	if err == nil && ctx.Err() != nil {
		err = fmt.Errorf("attn restarted the shared Codex app-server of profile %s while this connection waited; connect again", v.profile)
	}
	if err != nil {
		r.d.logf("shared Codex: terminal %s cannot reach its app-server: %v", v.terminal, err)
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	down, err := websocket.Accept(w, req, nil)
	if err != nil {
		return
	}
	down.SetReadLimit(-1)
	up, err := codexshared.Dial(ctx, s.socket)
	if err != nil {
		_ = down.Close(websocket.StatusInternalError, err.Error())
		return
	}
	codexshared.Proxy(ctx, down, up, func(m *codexshared.Message) (func(*codexshared.Message), error) { return r.prepare(v, m) }, nil)
}

func (r *codexShared) proxying(v *codexView) (context.Context, *codexProxy) {
	ctx, drop := context.WithCancel(r.d.life.Context())
	proxy := &codexProxy{drop: drop}
	r.mu.Lock()
	v.proxies[proxy] = true
	r.mu.Unlock()
	return ctx, proxy
}

func (r *codexShared) proxied(v *codexView, proxy *codexProxy) {
	proxy.drop()
	r.mu.Lock()
	delete(v.proxies, proxy)
	r.mu.Unlock()
	r.viewsChanged(v.profile)
}

func (r *codexShared) prepare(v *codexView, m *codexshared.Message) (func(*codexshared.Message), error) {
	method := m.Method
	switch method {
	case "initialize":
		r.learnTUI(v, m.Params)
		return nil, nil
	case "config/read":
		return r.answerTrust(v), nil
	case "turn/start":
		r.followPrompt(v, m)
		return nil, nil
	case "thread/start", "thread/fork", "thread/resume":
	default:
		return nil, nil
	}
	var params map[string]any
	if err := json.Unmarshal(m.Params, &params); err != nil {
		return nil, err
	}
	if ephemeral, _ := params["ephemeral"].(bool); ephemeral {
		return nil, nil
	}
	sessionID, launchAs, chief := r.launching(v, method, params)
	config, _ := params["config"].(map[string]any)
	if config == nil {
		config = make(map[string]any)
	}
	r.capContext(config, sessionID, chief)
	if _, bound := r.d.crewMemberForSession(sessionID); bound && r.d.crewMemoryEnabled() {
		config["features.memories"] = false
		config["memories.generate_memories"] = false
	}
	r.grantApprovals(params, sessionID)
	if method != "thread/resume" {
		if instructions := r.instructions(launchAs, v.profile, chief); instructions != "" {
			prior, _ := params["developerInstructions"].(string)
			params["developerInstructions"] = strings.TrimSpace(prior + "\n\n" + instructions)
		}
		if cwd, _ := params["cwd"].(string); cwd == "" {
			if session := r.d.store.Get(sessionID); session != nil {
				params["cwd"] = session.Directory
			}
		}
		if member, ok := r.d.crewMemberForSession(sessionID); ok && len(member.AwarenessDirs) > 0 {
			cwd, _ := params["cwd"].(string)
			params["runtimeWorkspaceRoots"] = append([]string{cwd}, member.AwarenessDirs...)
		}
	}
	params["config"] = config
	raw, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	m.Params = raw
	opened := method != "thread/resume"
	firstOfItsSession := opened && r.conversation(sessionID) == ""
	return func(reply *codexshared.Message) {
		var result struct {
			Thread struct {
				ID     string `json:"id"`
				Path   string `json:"path"`
				Parent string `json:"parentThreadId"`
			} `json:"thread"`
		}
		r.started(v)
		if len(reply.Error) > 0 || json.Unmarshal(reply.Result, &result) != nil || result.Thread.ID == "" {
			return
		}
		thread := result.Thread
		if thread.Parent != "" {
			r.adopt(v.profile, thread.ID, thread.Parent)
			return
		}
		if !opened {
			r.d.mirrorLabel(r.holder(v.profile, thread.ID))
		} else if err := r.materialize(v.profile, thread.ID); err != nil {
			r.d.logf("shared Codex: refused conversation %s, which could not be written: %v", thread.ID, err)
			reply.Result = nil
			reply.Error, _ = json.Marshal(map[string]any{"code": -32603, "message": "attn could not write the new conversation to disk: " + err.Error()})
			return
		}
		r.show(v, thread.ID, opened)
		if !firstOfItsSession {
			r.d.conversationIn(v.terminal, agentConversationObservation{NativeID: thread.ID, TranscriptPath: thread.Path})
		}
	}, nil
}

func (r *codexShared) started(v *codexView) {
	r.mu.Lock()
	was := v.started
	v.started = true
	r.mu.Unlock()
	if !was {
		r.viewsChanged(v.profile)
	}
}

func (r *codexShared) followPrompt(v *codexView, m *codexshared.Message) {
	var p struct {
		ThreadID string `json:"threadId"`
	}
	if json.Unmarshal(m.Params, &p) != nil {
		return
	}
	r.mu.Lock()
	returned := v.unprompted[p.ThreadID] && v.thread != p.ThreadID
	delete(v.unprompted, p.ThreadID)
	r.mu.Unlock()
	if returned {
		r.show(v, p.ThreadID, false)
	}
}

func (r *codexShared) answerTrust(v *codexView) func(*codexshared.Message) {
	r.mu.Lock()
	folder := v.trusted
	r.mu.Unlock()
	if folder == "" {
		return nil
	}
	return func(reply *codexshared.Message) {
		var result map[string]any
		if len(reply.Error) > 0 || json.Unmarshal(reply.Result, &result) != nil {
			return
		}
		config, _ := result["config"].(map[string]any)
		if config == nil {
			return
		}
		projects, _ := config["projects"].(map[string]any)
		if projects == nil {
			projects = make(map[string]any)
			config["projects"] = projects
		}
		if project, _ := projects[folder].(map[string]any); project["trust_level"] != nil {
			return
		}
		projects[folder] = map[string]any{"trust_level": "trusted"}
		if raw, err := json.Marshal(result); err == nil {
			reply.Result = raw
		}
	}
}

func (r *codexShared) launching(v *codexView, method string, params map[string]any) (session, launchAs protocol.SessionID, chief bool) {
	session = r.d.sessionInTerminal(v.terminal)
	if conversation, _ := params["threadId"].(string); method == "thread/resume" && conversation != "" {
		if holder := r.holder(v.profile, conversation); holder != "" {
			session = holder
		} else if owner := r.d.store.ConversationOwner(session, conversation); owner != "" {
			session = owner
		}
	}
	if method != "thread/resume" && r.conversation(session) != "" {
		return session, "", false
	}
	return session, session, r.d.isChiefOfStaffSession(session)
}

func (r *codexShared) capContext(config map[string]any, sessionID protocol.SessionID, chief bool) {
	if limit := r.d.launchContextWindowCap(sessionID, string(protocol.SessionAgentCodex), chief); limit > 0 {
		config["model_auto_compact_token_limit"] = limit
	}
}

func (r *codexShared) grantApprovals(params map[string]any, sessionID protocol.SessionID) {
	route, _ := r.d.intendedApprovalRoute(sessionID)
	switch route {
	case launchcontract.ApprovalRouteBypass:
		params["approvalPolicy"], params["sandbox"] = "never", "danger-full-access"
	case launchcontract.ApprovalRouteReviewer:
		params["approvalPolicy"], params["approvalsReviewer"] = "on-request", "auto_review"
	}
}

func (r *codexShared) instructions(sessionID protocol.SessionID, profile string, chief bool) string {
	launch, err := r.d.preparePluginLaunchInstructions(sessionID, profile, chief, false)
	if err != nil {
		r.d.logf("shared Codex: no attn instructions for session %s: %v", sessionID, err)
		return ""
	}
	return launch.Content
}

func (r *codexShared) materialize(profile, conversation string) error {
	r.mu.Lock()
	s := r.servers[profile]
	r.mu.Unlock()
	var client *codexshared.Client
	if s != nil {
		client = s.client()
	}
	if client == nil {
		return errors.New("no connection to its app-server")
	}
	ctx, cancel := context.WithTimeout(context.Background(), codexServerCallLimit)
	defer cancel()
	note := prompts.RenderText("session", "codex-opened", nil)
	_, err := client.Call(ctx, "thread/inject_items", map[string]any{
		"threadId": conversation,
		"items": []any{map[string]any{
			"type": "message", "role": "developer",
			"content": []any{map[string]any{"type": "input_text", "text": note}},
		}},
	})
	return err
}

func (r *codexShared) show(v *codexView, conversation string, opened bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seq++
	v.thread, v.shownSeq = conversation, r.seq
	if opened {
		if v.unprompted == nil {
			v.unprompted = make(map[string]bool)
		}
		v.unprompted[conversation] = true
	}
}

func (r *codexShared) terminalShowing(profile, conversation string) (harness.TerminalID, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var found *codexView
	for _, v := range r.views {
		if v.profile == profile && v.thread == conversation && (found == nil || v.shownSeq > found.shownSeq) {
			found = v
		}
	}
	if found == nil {
		return "", false
	}
	return found.terminal, true
}

func (r *codexShared) relaunch(opts *ptybackend.SpawnOptions) {
	installed := r.binary(configuredLaunchExecutable(agentdriver.MustGet(string(protocol.SessionAgentCodex)), opts))
	r.mu.Lock()
	v := r.views[opts.ID]
	if v != nil {
		v.started = false
	}
	r.mu.Unlock()
	if v == nil {
		opts.LaunchExecutable = installed
		return
	}
	opts.CodexRemote = "unix://" + v.socket
	r.answerTrustIfKnown(opts)
	if _, err := r.ensureServer(r.d.life.Context(), v.profile, ""); err != nil {
		r.d.logf("shared Codex: relaunching terminal %s before its app-server answers: %v", opts.ID, err)
	}
	opts.LaunchExecutable = r.terminalBinary(v.profile, installed)
}

func (r *codexShared) terminalExited(t harness.TerminalID) {
	if !r.runsLink(t) {
		r.terminalDropped(t)
		return
	}
	r.mu.Lock()
	var exited *codexServer
	for _, s := range r.servers {
		if s.terminal == t {
			exited = s
		}
	}
	r.mu.Unlock()
	if exited != nil {
		exited.markExited()
		exited.ensureMu.Lock()
		defer exited.ensureMu.Unlock()
	}
	if r.serverRunning(context.Background(), t) {
		return
	}
	if exited != nil {
		r.d.logf("shared Codex: the app-server of profile %s exited; the next connection starts it again", exited.profile)
		r.forget(exited)
	}
	if err := r.d.removePTYSession(t); err != nil {
		r.d.logf("pty backend remove on exit failed for %s: %v", t, err)
	}
}

func (r *codexShared) recoverProcesses(ctx context.Context) {
	live := r.d.liveTerminals(ctx)
	profiles, err := r.d.store.ListProfiles(false)
	if err != nil {
		r.d.logf("shared Codex: %v", err)
		return
	}
	known := make(map[string]bool, len(profiles))
	for _, profile := range profiles {
		known[profile.ID] = true
	}
	for _, session := range r.d.store.List("") {
		if !known[session.ProfileID] && r.d.store.SessionLink(session.ID) == codexLinkKind {
			profiles = append(profiles, profilespkg.Profile{ID: session.ProfileID})
			known[session.ProfileID] = true
		}
	}
	owned := make(map[harness.TerminalID]bool, len(profiles))
	for _, profile := range profiles {
		owned[codexServerTerminal(profile.ID)] = true
		if _, running := live[codexServerTerminal(profile.ID)]; !running {
			r.serverGone(profile.ID)
			continue
		}
		if s := r.server(profile.ID); r.serverRunning(ctx, s.terminal) && !r.listening(s) {
			r.stopUnreachable(s)
			continue
		}
		connectCtx, cancel := context.WithTimeout(ctx, codexServerCallLimit)
		if _, err := r.ensureServer(connectCtx, profile.ID, ""); err != nil {
			r.d.logf("shared Codex: reconnect to the app-server of profile %s: %v", profile.ID, err)
		}
		cancel()
	}
	for terminal := range r.d.localLiveTerminals(ctx) {
		if r.runsLink(terminal) && !owned[terminal] {
			if err := r.d.removePTYSession(terminal); err != nil {
				r.d.logf("shared Codex: remove app-server %s without an owning profile: %v", terminal, err)
			}
		}
	}
}

func (r *codexShared) listening(s *codexServer) bool {
	_, err := os.Stat(s.socket)
	return !errors.Is(err, os.ErrNotExist)
}

func (r *codexShared) stopUnreachable(s *codexServer) {
	s.ensureMu.Lock()
	defer s.ensureMu.Unlock()
	r.stop(s, codexServerReleaseGrace)
	r.d.logf("shared Codex: stopped the app-server of profile %s, which does not listen at %s; the next connection starts it again", s.profile, s.socket)
}

func (r *codexShared) recoverViews(ctx context.Context) {
	live := r.d.liveTerminals(ctx)
	terminals, err := r.d.store.TerminalViews(codexLinkKind)
	if err != nil {
		r.d.logf("shared Codex: %v", err)
		return
	}
	for _, t := range terminals {
		id := harness.TerminalID(t.TerminalID)
		if _, running := live[id]; !running {
			if err := r.d.store.DeleteTerminalView(t.TerminalID); err != nil {
				r.d.logf("shared Codex: %v", err)
			}
			continue
		}
		if _, err := r.openView(id, t.ProfileID); err != nil {
			r.d.logf("shared Codex: %v", err)
			continue
		}
		r.mu.Lock()
		if v := r.views[id]; v != nil {
			v.started = true
		}
		r.mu.Unlock()
	}
	r.mu.Lock()
	profiles := make([]string, 0, len(r.servers))
	for profile := range r.servers {
		profiles = append(profiles, profile)
	}
	r.mu.Unlock()
	for _, profile := range profiles {
		r.idleSoon(profile)
	}
}

func (r *codexShared) readLoaded(s *codexServer, client *codexshared.Client, loaded []string) {
	for _, id := range loaded {
		ctx, cancel := context.WithTimeout(r.d.life.Context(), codexServerCallLimit)
		raw, err := client.Call(ctx, "thread/read", map[string]any{"threadId": id})
		cancel()
		var read struct {
			Thread struct {
				Parent string            `json:"parentThreadId"`
				Status codexThreadStatus `json:"status"`
			} `json:"thread"`
		}
		if err == nil {
			err = json.Unmarshal(raw, &read)
		}
		if err != nil {
			r.d.logf("shared Codex: reading conversation %s that the app-server of profile %s holds: %v", id, s.profile, err)
			continue
		}
		s.mu.Lock()
		if read.Thread.Parent != "" {
			s.adoptLocked(id, read.Thread.Parent)
		}
		if known, held := s.held[id]; held && known.Type == "" {
			s.held[id] = read.Thread.Status
		}
		s.mu.Unlock()
	}
}

func (s *codexServer) adoptLocked(subagent, parent string) {
	if s.parents == nil {
		s.parents = make(map[string]string)
	}
	s.parents[subagent] = parent
}

func (r *codexShared) adopt(profile, subagent, parent string) {
	r.mu.Lock()
	s := r.servers[profile]
	r.mu.Unlock()
	if s == nil {
		return
	}
	s.mu.Lock()
	s.adoptLocked(subagent, parent)
	s.mu.Unlock()
}

func (r *codexShared) parent(profile, conversation string) string {
	r.mu.Lock()
	s := r.servers[profile]
	r.mu.Unlock()
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.parents[conversation]
}

func (r *codexShared) resolveCaller(t protocol.TerminalID) (protocol.SessionID, harness.TerminalID, bool) {
	profile, conversation, ok := harness.ParseCodexThreadTerminal(t)
	if !ok {
		return "", "", false
	}
	view, _ := r.terminalShowing(profile, conversation)
	for range 8 {
		if shown, ok := r.terminalShowing(profile, conversation); ok {
			if s, ok := r.d.terminals().Showing(shown); ok {
				return s, view, true
			}
		}
		if s := r.holder(profile, conversation); s != "" {
			return s, view, true
		}
		if conversation = r.parent(profile, conversation); conversation == "" {
			break
		}
	}
	return "", view, true
}

func (d *Daemon) wrapperPath() string {
	if path := strings.TrimSpace(os.Getenv("ATTN_WRAPPER_PATH")); path != "" {
		return path
	}
	if path, err := os.Executable(); err == nil {
		return path
	}
	return "attn"
}
