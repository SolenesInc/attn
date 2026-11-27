package codex

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

	"github.com/victorarias/attn/internal/codexshared"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/launchcontract"
	"github.com/victorarias/attn/internal/launchenv"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/prompts"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/unixsock"
)

const (
	codexID                   = "codex"
	codexSharedTerminalOwner  = "codex-shared"
	codexServerTerminalPrefix = "codex-server-"
	codexServerAgent          = "codex-app-server"
	codexClientName           = "codex-tui"
	codexServerStartLimit     = 30 * time.Second
	codexServerCallLimit      = 5 * time.Second
	codexOpeningNoteLimit     = 60 * time.Second
)

var errCodexStopping = errors.New("daemon stopping")

const codexServerLaunchScript = `
ulimit -Sn "$1"
shift
exec "$@"
`

type codexShared struct {
	host               harness.Host
	terminals          harness.HostTerminals
	facts              func() harness.Facts
	notifySnapshot     func(profiles ...string)
	enqueueUpdateCheck func()

	mu        sync.Mutex
	servers   map[string]*codexServer
	views     map[harness.TerminalID]*codexView
	seq       uint64
	installed string
	latest    string
	probing   sync.Mutex

	archivingAfterClose map[string]int
	ownInterrupts       map[string]bool
}

type codexServer struct {
	profile    string
	terminal   harness.TerminalID
	socket     string
	ensureMu   sync.Mutex
	mu         sync.Mutex
	control    *codexshared.Client
	held       map[string]codexThreadStatus
	compacting map[string]*codexCompaction
	idleWaits  map[string]chan struct{}
	parents    map[string]string
	attempt    *codexAttempt
	events     linkEvents
	running    string
	binary     string
	exited     chan struct{}
	restart    codexRestart
	failed     codexStartFailure
}

type codexAttempt struct {
	connected bool
	early     []codexshared.Message
}

type codexProxy struct {
	drop       context.CancelFunc
	mcpStartup codexViewMCPStartup
}

type codexView struct {
	terminal   harness.TerminalID
	profile    string
	socket     string
	server     *http.Server
	proxies    map[*codexProxy]bool
	replies    sync.Mutex
	started    bool
	opening    int
	thread     string
	shownSeq   uint64
	trusted    string
	version    string
	unprompted map[string]bool
}

func newCodexShared(host harness.Host, facts func() harness.Facts) *codexShared {
	r := &codexShared{
		host:                host,
		terminals:           host.Terminals(),
		facts:               facts,
		servers:             make(map[string]*codexServer),
		views:               make(map[harness.TerminalID]*codexView),
		archivingAfterClose: make(map[string]int),
	}
	r.notifySnapshot = host.RegisterProfileSnapshot(harness.ProfileSnapshot{
		Fact:     factSharedCodexChanged,
		Snapshot: snapshotSharedCodex,
		Initial: func(profile string, msg *protocol.InitialStateMessage) {
			msg.SharedCodex = r.wireState(profile)
		},
		Changed: func(profile string) any {
			return r.changedMessage(profile)
		},
	})
	return r
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
	return codexshared.SocketDir(r.host.DataDir())
}

func (r *codexShared) ensureDir() error {
	return unixsock.EnsurePrivateDir(r.dir())
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

func (r *codexShared) admit(profile, conversation string) error {
	if other := r.loadedElsewhere(profile, conversation); conversation != "" && other != "" {
		return fmt.Errorf("conversation %s is open in the shared Codex of profile %s; it can open here once that profile lets it go, about a minute after no terminal shows it", conversation, other)
	}
	return nil
}

func (r *codexShared) prepareLaunch(terminal harness.TerminalID, cwd, profile string, unattended bool, configured string) (remote, launchExecutable string, err error) {
	remote, err = r.openView(terminal, profile)
	if err != nil {
		return "", "", err
	}
	installed, err := r.binary(configured)
	if err != nil {
		return "", "", err
	}
	if _, err := r.ensureServer(r.host.Life().Context(), profile, installed); err != nil {
		return "", "", err
	}
	r.answerTrustIfKnown(terminal, cwd, profile, unattended)
	r.refreshInstalledSoon()
	return remote, r.terminalBinary(profile, installed), nil
}

func (r *codexShared) answerTrustIfKnown(terminal harness.TerminalID, cwd, profile string, unattended bool) {
	if !unattended {
		if trust, err := r.readCodexFolderTrust(cwd, profile); err != nil || !trust.known() {
			return
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if v := r.views[terminal]; v != nil {
		v.trusted = cwd
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
		r.terminals.Adopt(ctx, s.terminal)
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
	if _, live := r.terminals.Live(ctx)[t]; !live {
		return false
	}
	if info, supported, err := r.terminals.Info(ctx, t); supported {
		return err == nil && info.Running
	}
	return true
}

func (r *codexShared) startServer(ctx context.Context, s *codexServer, executable string) error {
	if err := r.terminals.Remove(ctx, s.terminal); err != nil {
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
	binary, err := r.binary(executable)
	if err != nil {
		return err
	}
	command := append([]string{"/bin/sh", "-c", codexServerLaunchScript, "attn-codex-server", strconv.FormatUint(files.Cur, 10), binary}, r.serverArgs(s.profile, s.socket)...)
	resolved := resolvedBinary(binary)
	if err := r.terminals.Start(ctx, harness.TerminalSpec{
		ID:         s.terminal,
		Agent:      codexServerAgent,
		Label:      "Codex app-server",
		Dir:        r.dir(),
		Executable: resolved,
		Argv:       command,
	}); err != nil {
		return fmt.Errorf("start the shared Codex app-server: %w", err)
	}
	s.mu.Lock()
	s.binary = resolved
	s.mu.Unlock()
	r.host.Logf("shared Codex: started the app-server of profile %s in terminal %s with an open-file limit of %d", s.profile, s.terminal, files.Cur)
	return nil
}

func (r *codexShared) serverArgs(profile, socket string) []string {
	args := []string{"app-server"}
	for _, override := range GenerateCodexServerConfigOverrides(launchenv.ActiveAttnExecutable(), r.host.DaemonSocket(), profile) {
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
				removed, _ := s.trackLocked(m)
				gone = append(gone, removed...)
			}
			held := make([]string, 0, len(s.held))
			for id := range s.held {
				held = append(held, id)
			}
			a.connected, a.early = true, nil
			s.mu.Unlock()
			r.lose(s, gone)
			r.versionsChanged(s.profile)
			if !r.host.Life().Go("codexServerControl", func() { r.watchControl(s, client) }) {
				client.Close()
				return errCodexStopping
			}
			r.readLoaded(s, client, held)
			s.events.run(r.host, func() { r.readHidden(s, client) })
			s.events.run(r.host, func() {
				r.refreshInstalled()
				r.restartWhenQuiet(s)
			})
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("the shared Codex app-server of profile %s did not answer within %s (%s): %w", s.profile, codexServerStartLimit, r.versionLabel(ctx), err)
		}
		if !r.serverRunning(ctx, s.terminal) {
			return fmt.Errorf("the shared Codex app-server of profile %s exited before answering (%s): %w", s.profile, r.versionLabel(ctx), err)
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
	case <-r.host.Life().Context().Done():
		client.Close()
		<-client.Done()
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
	ctx := r.host.Life().Context()
	s.ensureMu.Lock()
	defer s.ensureMu.Unlock()
	if ctx.Err() != nil || s.client() != nil || !r.serverRunning(ctx, s.terminal) {
		return
	}
	if err := r.connect(ctx, s); err != nil {
		r.host.Logf("shared Codex: reconnecting to the app-server of profile %s: %v", s.profile, err)
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
		s.events.run(r.host, func() { r.lost(s, conversation) })
	}
}

func (r *codexShared) observeServer(s *codexServer, a *codexAttempt, m codexshared.Message) {
	var gone []string
	var discovered string
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
				gone, discovered = s.trackLocked(m)
				tracked = true
			} else {
				a.early = append(a.early, m)
			}
		}
	}
	s.mu.Unlock()
	r.lose(s, gone)
	if tracked {
		s.events.run(r.host, func() {
			if discovered != "" {
				if client := s.client(); client != nil {
					r.readLoaded(s, client, []string{discovered})
				}
			}
			r.blockersChanged(s)
		})
	}
}

func (s *codexServer) trackLocked(m codexshared.Message) ([]string, string) {
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
		return nil, ""
	}
	if s.held == nil {
		s.held = make(map[string]codexThreadStatus)
	}
	switch {
	case m.Method == "thread/started" && !p.Thread.Ephemeral && p.Thread.ID != "":
		s.held[p.Thread.ID] = p.Thread.Status
		if p.Thread.Parent != "" {
			s.adoptLocked(p.Thread.ID, p.Thread.Parent)
		}
	case m.Method == "thread/status/changed" && p.Status.Type != "notLoaded" && p.ThreadID != "":
		_, known := s.held[p.ThreadID]
		s.held[p.ThreadID] = p.Status
		if !known {
			return nil, p.ThreadID
		}
	case m.Method == "thread/closed" || p.Status.Type == "notLoaded":
		_, gone := s.held[p.ThreadID]
		delete(s.held, p.ThreadID)
		delete(s.parents, p.ThreadID)
		if gone {
			return []string{p.ThreadID}, ""
		}
	}
	return nil, ""
}

func (r *codexShared) lost(s *codexServer, conversation string) {
	s.mu.Lock()
	_, again := s.held[conversation]
	s.mu.Unlock()
	sessionID := r.holder(s.profile, conversation)
	if again || r.host.Sessions().Get(sessionID) == nil {
		return
	}
	r.host.Sessions().FinishUsage(sessionID)
	r.observe(sessionID, harness.TurnEnded)
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
	listener, err := unixsock.ListenAtomically(v.socket)
	if err != nil {
		return "", fmt.Errorf("open the Codex proxy of terminal %s: %w", t, err)
	}
	v.server = &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) { r.serveView(v, w, req) })}
	if err := r.host.Records().SaveTerminalView(harness.TerminalView{Terminal: t, Kind: codexLinkKind, Profile: profile}); err != nil {
		_ = listener.Close()
		_ = os.Remove(v.socket)
		return "", err
	}
	if !r.host.Life().Go("codexView", func() { _ = v.server.Serve(listener) }) {
		_ = listener.Close()
		_ = os.Remove(v.socket)
		return "", errCodexStopping
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
		r.host.Life().Go("codexServerIdle", func() {
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
	if err := r.host.Records().DeleteTerminalView(t); err != nil {
		r.host.Logf("shared Codex: %v", err)
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
	}
}

func (r *codexShared) serveView(v *codexView, w http.ResponseWriter, req *http.Request) {
	release, admitted := r.host.Life().Hold("codexViewConnection")
	if !admitted {
		http.Error(w, "attn is stopping", http.StatusServiceUnavailable)
		return
	}
	defer release()
	ctx, proxy := r.proxying(v)
	defer r.proxied(v, proxy)
	s, err := r.ensureServer(r.host.Life().Context(), v.profile, "")
	if err == nil && ctx.Err() != nil {
		err = fmt.Errorf("attn restarted the shared Codex app-server of profile %s while this connection waited; connect again", v.profile)
	}
	if err != nil {
		r.host.Logf("shared Codex: terminal %s cannot reach its app-server: %v", v.terminal, err)
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
	codexshared.Proxy(ctx, down, up, func(connection context.Context, m *codexshared.Message) (func(*codexshared.Message) func(), error) {
		if m.Method == "thread/resume" {
			proxy.mcpStartup.resuming(s, m)
			r.unarchiveForResume(v.profile, m)
		}
		return r.prepare(connection, v, m)
	}, func(m codexshared.Message) bool {
		return r.forwardsToView(s, proxy, m)
	})
}

func (r *codexShared) forwardsToView(s *codexServer, proxy *codexProxy, m codexshared.Message) bool {
	if m.Method != "mcpServer/startupStatus/updated" {
		return true
	}
	var p struct {
		ThreadID string `json:"threadId"`
		Name     string `json:"name"`
		Status   string `json:"status"`
	}
	if json.Unmarshal(m.Params, &p) != nil {
		return true
	}
	forwards, lastStarted := proxy.mcpStartup.record(p.ThreadID, p.Name, p.Status)
	if lastStarted {
		s.events.run(r.host, func() { r.checkWorkingSoon(r.holder(s.profile, p.ThreadID)) })
	}
	return forwards
}

func (r *codexShared) mcpServerStarting(conversation string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, v := range r.views {
		for proxy := range v.proxies {
			if proxy.mcpStartup.starting(conversation) {
				return true
			}
		}
	}
	return false
}

type codexViewMCPStartup struct {
	mu              sync.Mutex
	joined          map[string]bool
	startingServers map[string]map[string]bool
}

func (t *codexViewMCPStartup) resuming(s *codexServer, m *codexshared.Message) {
	var p struct {
		ThreadID string `json:"threadId"`
	}
	if json.Unmarshal(m.Params, &p) != nil {
		return
	}
	s.mu.Lock()
	_, loaded := s.held[p.ThreadID]
	s.mu.Unlock()
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.startingServers, p.ThreadID)
	if t.joined == nil {
		t.joined = make(map[string]bool)
	}
	if _, decided := t.joined[p.ThreadID]; !decided {
		t.joined[p.ThreadID] = loaded
	}
}

func (t *codexViewMCPStartup) record(conversation, server, status string) (forwards, lastStarted bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.joined[conversation] {
		return false, false
	}
	servers := t.startingServers[conversation]
	if status == "starting" {
		if servers == nil {
			servers = make(map[string]bool)
			if t.startingServers == nil {
				t.startingServers = make(map[string]map[string]bool)
			}
			t.startingServers[conversation] = servers
		}
		servers[server] = true
		return true, false
	}
	if !servers[server] {
		return true, false
	}
	delete(servers, server)
	if len(servers) > 0 {
		return true, false
	}
	delete(t.startingServers, conversation)
	return true, true
}

func (t *codexViewMCPStartup) starting(conversation string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.startingServers[conversation]) > 0
}

func (r *codexShared) proxying(v *codexView) (context.Context, *codexProxy) {
	ctx, drop := context.WithCancel(r.host.Life().Context())
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

func (r *codexShared) identifySideConversationTools(m *codexshared.Message, params map[string]any, terminal harness.TerminalID) error {
	config, _ := params["config"].(map[string]any)
	if config == nil {
		config = make(map[string]any)
	}
	config["shell_environment_policy.set.ATTN_TERMINAL_ID"] = terminal
	if tools, ok := r.host.TerminalTools(terminal); ok {
		for _, s := range CodexAgentToolServer(tools.URL, CodexSetting{Key: "http_headers.Authorization", Value: "Bearer " + tools.Credential}) {
			config[s.Key] = s.Value
		}
	}
	params["config"] = config
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	m.Params = raw
	return nil
}

func (r *codexShared) prepare(connection context.Context, v *codexView, m *codexshared.Message) (func(*codexshared.Message) func(), error) {
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
		if method == "thread/fork" {
			return nil, r.identifySideConversationTools(m, params, v.terminal)
		}
		return nil, nil
	}
	sessionID, launchAs := r.launching(v, method, params)
	info := r.host.Sessions().LaunchInfo(sessionID, codexID)
	config, _ := params["config"].(map[string]any)
	if config == nil {
		config = make(map[string]any)
	}
	_, hasTools := r.host.ServerTools(sharedCodexServerPrefix + v.profile)
	r.loadConfig(config, info.ContextCap, v.profile)
	if info.CrewBound {
		config["features.memories"] = false
		config["memories.generate_memories"] = false
	}
	grantCodexApprovals(params, info.ApprovalRoute)
	if method != "thread/resume" {
		if instructions := r.host.Sessions().Instructions(launchAs, hasTools); instructions != "" {
			prior, _ := params["developerInstructions"].(string)
			params["developerInstructions"] = strings.TrimSpace(prior + "\n\n" + instructions)
		}
		if cwd, _ := params["cwd"].(string); cwd == "" {
			if session := r.host.Sessions().Get(sessionID); session != nil {
				params["cwd"] = session.Directory
			}
		}
		if len(info.AwarenessDirs) > 0 {
			cwd, _ := params["cwd"].(string)
			params["runtimeWorkspaceRoots"] = append([]string{cwd}, info.AwarenessDirs...)
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
	forkedFrom, _ := params["threadId"].(string)
	if method != "thread/fork" {
		forkedFrom = ""
	}
	return func(reply *codexshared.Message) func() {
		opening := false
		settle := func() {
			if opening {
				r.openingChanged(v, -1)
			}
		}
		var result struct {
			Thread struct {
				ID     string `json:"id"`
				Path   string `json:"path"`
				Parent string `json:"parentThreadId"`
			} `json:"thread"`
		}
		established := len(reply.Error) == 0 && json.Unmarshal(reply.Result, &result) == nil && result.Thread.ID != ""
		thread := result.Thread
		if established && thread.Parent == "" && opened {
			r.openingChanged(v, 1)
			opening = true
			err := r.materialize(connection, v.terminal, v.profile, thread.ID)
			if connection.Err() != nil {
				return settle
			}
			if err != nil {
				established = false
				r.host.Logf("shared Codex: refused conversation %s, which could not be written: %v", thread.ID, err)
				reply.Result = nil
				reply.Error, _ = json.Marshal(map[string]any{"code": -32603, "message": "attn could not write the new conversation to disk: " + err.Error()})
			}
		}
		if established && thread.Parent == "" && !opened {
			r.host.Sessions().MirrorLabel(r.holder(v.profile, thread.ID))
		}
		v.replies.Lock()
		defer v.replies.Unlock()
		if connection.Err() != nil {
			return settle
		}
		switch {
		case !established:
		case thread.Parent != "":
			r.adopt(v.profile, thread.ID, thread.Parent)
		default:
			forked := forkedFrom != "" && forkedFrom == r.shownThread(v)
			r.show(v, thread.ID, opened)
			if !firstOfItsSession {
				r.host.Events().Moved(harness.FromTerminal(v.terminal), harness.Move{Conversation: thread.ID, Path: thread.Path, ServerConfirmed: true, Forked: forked})
			}
		}
		return func() {
			defer settle()
			v.replies.Lock()
			defer v.replies.Unlock()
			if connection.Err() == nil {
				r.started(v)
			}
		}
	}, nil
}

func (r *codexShared) openingChanged(v *codexView, by int) {
	r.mu.Lock()
	v.opening += by
	r.mu.Unlock()
	r.viewsChanged(v.profile)
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

func (r *codexShared) answerTrust(v *codexView) func(*codexshared.Message) func() {
	r.mu.Lock()
	folder := v.trusted
	r.mu.Unlock()
	if folder == "" {
		return nil
	}
	return func(reply *codexshared.Message) func() {
		var result map[string]any
		if len(reply.Error) > 0 || json.Unmarshal(reply.Result, &result) != nil {
			return nil
		}
		config, _ := result["config"].(map[string]any)
		if config == nil {
			return nil
		}
		projects, _ := config["projects"].(map[string]any)
		if projects == nil {
			projects = make(map[string]any)
			config["projects"] = projects
		}
		if project, _ := projects[folder].(map[string]any); project["trust_level"] != nil {
			return nil
		}
		projects[folder] = map[string]any{"trust_level": "trusted"}
		if raw, err := json.Marshal(result); err == nil {
			reply.Result = raw
		}
		return nil
	}
}

func (r *codexShared) launching(v *codexView, method string, params map[string]any) (session, launchAs protocol.SessionID) {
	session, ok := r.host.Sessions().Showing(v.terminal)
	if !ok {
		session = protocol.SessionID(v.terminal)
	}
	if conversation, _ := params["threadId"].(string); method == "thread/resume" && conversation != "" {
		if holder := r.holder(v.profile, conversation); holder != "" {
			session = holder
		} else if owner := r.host.Sessions().ConversationOwner(session, conversation); owner != "" {
			session = owner
		}
	}
	if method != "thread/resume" && r.conversation(session) != "" {
		return session, ""
	}
	return session, session
}

func (r *codexShared) loadConfig(config map[string]any, contextCap int, profile string) {
	if contextCap > 0 {
		config["model_auto_compact_token_limit"] = contextCap
	}
	if tools, ok := r.host.ServerTools(sharedCodexServerPrefix + profile); ok {
		for _, s := range CodexAgentToolServer(tools.URL, CodexSetting{Key: "http_headers.Authorization", Value: "Bearer " + tools.Credential}) {
			config[s.Key] = s.Value
		}
	}
}

func grantCodexApprovals(params map[string]any, route launchcontract.ApprovalRoute) {
	switch route {
	case launchcontract.ApprovalRouteBypass:
		params["approvalPolicy"], params["sandbox"] = "never", "danger-full-access"
	case launchcontract.ApprovalRouteReviewer:
		params["approvalPolicy"], params["approvalsReviewer"] = "on-request", "auto_review"
	}
}

func (r *codexShared) materialize(connection context.Context, terminal protocol.TerminalID, profile, conversation string) error {
	r.mu.Lock()
	s := r.servers[profile]
	r.mu.Unlock()
	var client *codexshared.Client
	if s != nil {
		client = s.client()
	}
	if client == nil {
		r.host.Logf("shared Codex materialize: terminal=%s profile=%s conversation=%s op=thread/inject_items outcome=no_connection", terminal, profile, conversation)
		return errors.New("no connection to its app-server")
	}
	ctx, cancel := context.WithTimeout(connection, codexOpeningNoteLimit)
	defer cancel()
	note := prompts.RenderText("session", "codex-opened", nil)
	started := time.Now()
	_, err := client.Call(ctx, "thread/inject_items", map[string]any{
		"threadId": conversation,
		"items": []any{map[string]any{
			"type": "message", "role": "developer",
			"content": []any{map[string]any{"type": "input_text", "text": note}},
		}},
	})
	if connection.Err() != nil {
		err = connection.Err()
	}
	r.host.Logf("shared Codex materialize: terminal=%s profile=%s conversation=%s op=thread/inject_items elapsed=%s deadline=%s outcome=%s",
		terminal, profile, conversation, time.Since(started).Round(time.Millisecond), codexOpeningNoteLimit, codexCallOutcome(err))
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("its app-server did not answer thread/inject_items within %s", codexOpeningNoteLimit)
	}
	return err
}

func codexCallOutcome(err error) string {
	var refused *codexshared.RPCError
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, context.DeadlineExceeded):
		return "local_deadline"
	case errors.Is(err, context.Canceled):
		return "connection_ended"
	case errors.As(err, &refused):
		return "server_error"
	default:
		return "disconnected"
	}
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

func (r *codexShared) shownThread(v *codexView) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return v.thread
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

func (r *codexShared) relaunch(terminal harness.TerminalID, cwd string, unattended bool, configured string) (remote, launchExecutable string, err error) {
	installed, err := r.binary(configured)
	if err != nil {
		return "", "", err
	}
	r.mu.Lock()
	v := r.views[terminal]
	r.mu.Unlock()
	if v == nil {
		return "", installed, nil
	}
	r.answerTrustIfKnown(terminal, cwd, v.profile, unattended)
	if _, err := r.ensureServer(r.host.Life().Context(), v.profile, ""); err != nil {
		r.host.Logf("shared Codex: relaunching terminal %s before its app-server answers: %v", terminal, err)
	}
	return "unix://" + v.socket, r.terminalBinary(v.profile, installed), nil
}

func (r *codexShared) retireConnections(t harness.TerminalID) {
	r.mu.Lock()
	v := r.views[t]
	r.mu.Unlock()
	if v == nil {
		return
	}
	v.replies.Lock()
	defer v.replies.Unlock()
	r.mu.Lock()
	defer r.mu.Unlock()
	v.started = false
	for proxy := range v.proxies {
		proxy.drop()
	}
}

func (r *codexShared) terminalExited(t harness.TerminalID, e harness.Exited) {
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
		r.host.Logf("shared Codex: the app-server of profile %s exited; the next connection starts it again", exited.profile)
		r.forget(exited)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	err := r.terminals.RemoveExited(ctx, t, e)
	cancel()
	if err != nil {
		r.host.Logf("pty backend remove on exit failed for %s: %v", t, err)
	}
	pausepoint.At(pausepoint.CodexServerExitHandled)
}

func (r *codexShared) recoverProcesses(ctx context.Context) {
	live := r.terminals.Live(ctx)
	profiles, err := r.host.Profiles().List()
	if err != nil {
		r.host.Logf("shared Codex: %v", err)
		return
	}
	known := make(map[string]bool, len(profiles))
	for _, profile := range profiles {
		known[profile] = true
	}
	for _, session := range r.host.Sessions().List() {
		if !known[session.ProfileID] && r.launchedShared(session.ID) {
			profiles = append(profiles, session.ProfileID)
			known[session.ProfileID] = true
		}
	}
	owned := make(map[harness.TerminalID]bool, len(profiles))
	for _, profile := range profiles {
		owned[codexServerTerminal(profile)] = true
		if _, running := live[codexServerTerminal(profile)]; !running {
			r.serverGone(profile)
			continue
		}
		connectCtx, cancel := context.WithTimeout(ctx, codexServerCallLimit)
		if _, err := r.ensureServer(connectCtx, profile, ""); err != nil {
			r.host.Logf("shared Codex: reconnect to the app-server of profile %s: %v", profile, err)
		}
		cancel()
	}
	for terminal := range r.terminals.LocalLive(ctx) {
		if r.terminals.Owned(terminal) && !owned[terminal] {
			if err := r.terminals.Remove(ctx, terminal); err != nil {
				r.host.Logf("shared Codex: remove app-server %s without an owning profile: %v", terminal, err)
			}
		}
	}
}

func (r *codexShared) recoverViews(ctx context.Context) {
	live := r.terminals.Live(ctx)
	terminals, err := r.host.Records().TerminalViews(codexLinkKind)
	if err != nil {
		r.host.Logf("shared Codex: %v", err)
		return
	}
	for _, t := range terminals {
		if _, running := live[t.Terminal]; !running {
			if err := r.host.Records().DeleteTerminalView(t.Terminal); err != nil {
				r.host.Logf("shared Codex: %v", err)
			}
			continue
		}
		if _, err := r.openView(t.Terminal, t.Profile); err != nil {
			r.host.Logf("shared Codex: %v", err)
			continue
		}
		r.mu.Lock()
		if v := r.views[t.Terminal]; v != nil {
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
		ctx, cancel := context.WithTimeout(r.host.Life().Context(), codexServerCallLimit)
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
			r.host.Logf("shared Codex: reading conversation %s from the app-server of profile %s: %v", id, s.profile, err)
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
	if _, loaded := s.held[subagent]; !loaded {
		return
	}
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

func (r *codexShared) resolveParent(profile, conversation string) string {
	r.mu.Lock()
	s := r.servers[profile]
	r.mu.Unlock()
	if s == nil {
		return ""
	}
	s.mu.Lock()
	parent := s.parents[conversation]
	s.mu.Unlock()
	if parent == "" {
		if client := s.client(); client != nil {
			r.readLoaded(s, client, []string{conversation})
			s.mu.Lock()
			parent = s.parents[conversation]
			s.mu.Unlock()
		}
	}
	return parent
}

func (r *codexShared) resolveCaller(c harness.ServerCaller) (protocol.SessionID, harness.TerminalID, bool) {
	profile, conversation := c.Scope, strings.TrimSpace(c.Conversation)
	if conversation == "" {
		return "", "", false
	}
	view, _ := r.terminalShowing(profile, conversation)
	for range 8 {
		if shown, ok := r.terminalShowing(profile, conversation); ok {
			if s, ok := r.host.Sessions().Showing(shown); ok {
				return s, view, true
			}
		}
		if s := r.holder(profile, conversation); s != "" {
			return s, view, true
		}
		if conversation = r.resolveParent(profile, conversation); conversation == "" {
			break
		}
	}
	return "", view, true
}
