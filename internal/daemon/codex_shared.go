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
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"nhooyr.io/websocket"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/codexshared"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/hooks"
	"github.com/victorarias/attn/internal/prompts"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/ptybackend"
)

const (
	codexServerTerminalPrefix = "codex-server-"
	codexServerAgent          = "codex-app-server"
	// The first client to initialize names the server's originator for every conversation; match the TUI's.
	codexClientName = "codex-tui"
	// Stock 0.160.0 answered within a second of launch; the first start may fetch plugins.
	codexServerStartLimit = 30 * time.Second
	codexServerCallLimit  = 5 * time.Second
)

type codexShared struct {
	d       *Daemon
	mu      sync.Mutex
	servers map[string]*codexServer
	views   map[harness.TerminalID]*codexView
	seq     uint64
}

type codexServer struct {
	profile  string
	terminal harness.TerminalID
	socket   string
	ensureMu sync.Mutex
	mu       sync.Mutex
	control  *codexshared.Client
	loaded   map[string]bool
	dropped  map[string]bool
	parents  map[string]string
	epoch    string
	seq      atomic.Uint64
	events   codexEvents
}

type codexView struct {
	terminal harness.TerminalID
	profile  string
	socket   string
	server   *http.Server
	thread   string
	shownSeq uint64
}

func (d *Daemon) codexShared() *codexShared {
	d.codexSharedOnce.Do(func() {
		r := &codexShared{d: d, servers: make(map[string]*codexServer), views: make(map[harness.TerminalID]*codexView)}
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

func isCodexServerTerminal(t harness.TerminalID) bool {
	return strings.HasPrefix(string(t), codexServerTerminalPrefix)
}

func (r *codexShared) dir() string { return filepath.Join(r.d.dataRoot, "cx") }

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

// A session keeps the mode it launched with.
func (d *Daemon) launchesSharedCodex(req *spawnRequest) bool {
	if req.agent != string(protocol.SessionAgentCodex) || req.hasPluginDriver || req.isShell || !req.policy.unattendedLaunch.IsZero() {
		return false
	}
	if intent, ok := d.store.LaunchIntent(req.msg.ID); ok {
		return intent.CodexShared
	}
	return parseBooleanSetting(d.store.GetSetting(SettingCodexSharedEnabled))
}

func (r *codexShared) prepareLaunch(t harness.TerminalID, profile, executable string) (string, error) {
	remote, err := r.openView(t, profile)
	if err != nil {
		return "", err
	}
	if _, err := r.ensureServer(r.d.life.Context(), profile, executable); err != nil {
		return "", err
	}
	return remote, nil
}

func (r *codexShared) codexExecutable(configured string) string {
	if strings.TrimSpace(configured) == "" {
		configured = r.d.store.GetSetting(SettingCodexExecutable)
	}
	return agentdriver.MustGet(string(protocol.SessionAgentCodex)).ResolveExecutable(configured)
}

func (r *codexShared) ensureServer(ctx context.Context, profile, executable string) (*codexServer, error) {
	s := r.server(profile)
	s.ensureMu.Lock()
	defer s.ensureMu.Unlock()
	if s.client() != nil {
		return s, nil
	}
	if !r.serverRunning(ctx, s.terminal) {
		if err := r.startServer(ctx, s, executable); err != nil {
			return nil, err
		}
	}
	if err := r.connect(ctx, s); err != nil {
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
	if err := os.MkdirAll(r.dir(), 0o700); err != nil {
		return err
	}
	if err := os.Remove(s.socket); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	command := []string{r.codexExecutable(executable), "app-server"}
	for _, override := range hooks.GenerateCodexServerConfigOverrides(r.d.wrapperPath(), s.profile) {
		command = append(command, "-c", override)
	}
	command = append(command, "--listen", "unix://"+s.socket)
	opts := ptybackend.SpawnOptions{
		ID: s.terminal, Agent: codexServerAgent, Label: "Codex app-server", CWD: r.dir(), Cols: 80, Rows: 24,
		ExternalCommand: command,
		// The server runs every terminal's conversations, so it carries no terminal's identity.
		ExternalEnv:   []string{"ATTN_SESSION_ID=", "ATTN_AGENT="},
		LoginShellEnv: r.d.cachedLoginShellEnv(),
		DaemonEnv:     r.d.spawnRoutingEnv(),
	}
	if err := r.d.ptyBackend.Spawn(ctx, opts); err != nil {
		return fmt.Errorf("start the shared Codex app-server: %w", err)
	}
	r.d.logf("shared Codex: started the app-server of profile %s in terminal %s", s.profile, s.terminal)
	return nil
}

func (r *codexShared) connect(ctx context.Context, s *codexServer) error {
	deadline := time.Now().Add(codexServerStartLimit)
	for {
		attempt, cancel := context.WithTimeout(ctx, codexServerCallLimit)
		client, err := codexshared.Connect(attempt, s.socket, codexClientName, func(m codexshared.Message) { r.observeServer(s, m) })
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
			r.mu.Lock()
			r.seq++
			epoch := fmt.Sprintf("%s%s:%d", codexLinkEpochPrefix, s.profile, r.seq)
			r.mu.Unlock()
			s.mu.Lock()
			s.control, s.loaded, s.epoch = client, make(map[string]bool, len(list.Data)), epoch
			for _, id := range list.Data {
				s.loaded[id] = true
				delete(s.dropped, id)
			}
			s.mu.Unlock()
			if !r.d.life.Go("codexServerControl", func() { r.watchControl(s, client) }) {
				client.Close()
				return errDaemonStopping
			}
			r.restoreParents(s, client, list.Data)
			s.events.run(r.d, func() { r.restateHiddenStates(s, client, epoch) })
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
		for conversation := range s.loaded {
			if s.dropped == nil {
				s.dropped = make(map[string]bool)
			}
			s.dropped[conversation] = true
		}
		s.control, s.loaded = nil, nil
	}
	s.mu.Unlock()
}

func (r *codexShared) observeServer(s *codexServer, m codexshared.Message) {
	var p struct {
		ThreadID string `json:"threadId"`
		Thread   struct {
			ID        string `json:"id"`
			Ephemeral bool   `json:"ephemeral"`
			Parent    string `json:"parentThreadId"`
		} `json:"thread"`
		Status struct {
			Type string `json:"type"`
		} `json:"status"`
	}
	switch m.Method {
	case "thread/started", "thread/status/changed", "thread/closed":
	case "thread/name/updated":
		r.observeName(s, m)
		return
	default:
		return
	}
	if json.Unmarshal(m.Params, &p) != nil {
		return
	}
	if m.Method == "thread/status/changed" {
		r.observeStatus(s, m)
	}
	s.mu.Lock()
	if s.loaded == nil {
		s.mu.Unlock()
		return
	}
	unloaded := ""
	if p.Thread.Parent != "" && p.Thread.ID != "" {
		if s.parents == nil {
			s.parents = make(map[string]string)
		}
		s.parents[p.Thread.ID] = p.Thread.Parent
	}
	switch {
	case m.Method == "thread/started" && !p.Thread.Ephemeral && p.Thread.ID != "":
		s.loaded[p.Thread.ID] = true
	case m.Method == "thread/status/changed" && p.Status.Type != "notLoaded" && p.ThreadID != "":
		s.loaded[p.ThreadID] = true
	case m.Method == "thread/closed" || p.Status.Type == "notLoaded":
		if s.loaded[p.ThreadID] {
			unloaded = p.ThreadID
		}
		delete(s.loaded, p.ThreadID)
		delete(s.parents, p.ThreadID)
	}
	s.mu.Unlock()
	if unloaded != "" {
		r.d.life.Go("codexConversationUnloaded", func() { r.unloaded(s, unloaded, false) })
	}
}

// A server exit ends the running turn: it fires no Stop hook, and a reconnecting TUI restates nothing.
func (r *codexShared) unloaded(s *codexServer, conversation string, exited bool) {
	sessionID := r.d.store.OpenSessionHolding(s.profile, conversation)
	session := r.d.store.Get(sessionID)
	if session != nil && exited &&
		(session.State == protocol.SessionStateWorking || session.State == protocol.SessionStatePendingApproval) {
		r.report(s, sessionID, harness.TurnEnded, false)
		return
	}
	if session == nil || session.State == protocol.SessionStateRecoverable || session.State == protocol.SessionStateIdle ||
		r.d.sessionLive(context.Background(), sessionID) || r.hidden(sessionID) {
		return
	}
	if !r.d.canReviveSession(session) {
		return
	}
	r.d.applyState(sessionStateChange{
		sessionID: sessionID,
		state:     string(protocol.SessionStateRecoverable),
		cause:     hostExitRecovery{},
		origin:    stateOrigin{source: "codex", detail: "the shared app-server let its conversation go"},
	})
}

func (r *codexShared) serverHolds(session *protocol.Session) bool {
	if session == nil {
		return false
	}
	if intent, ok := r.d.store.LaunchIntent(session.ID); !ok || !intent.CodexShared {
		return false
	}
	native := r.d.store.GetSessionConversation(session.ID).NativeID
	if native == "" {
		return false
	}
	r.mu.Lock()
	s := r.servers[session.ProfileID]
	r.mu.Unlock()
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.control != nil && s.loaded[native]
}

func (r *codexShared) openView(t harness.TerminalID, profile string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if v := r.views[t]; v != nil {
		return "unix://" + v.socket, nil
	}
	if err := os.MkdirAll(r.dir(), 0o700); err != nil {
		return "", err
	}
	v := &codexView{terminal: t, profile: profile, socket: r.viewSocket(t)}
	listener, err := listenUnixAtomically(v.socket)
	if err != nil {
		return "", fmt.Errorf("open the Codex proxy of terminal %s: %w", t, err)
	}
	v.server = &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) { r.serveView(v, w, req) })}
	if err := r.d.store.SaveCodexTerminal(string(t), profile); err != nil {
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

func (r *codexShared) closeView(t harness.TerminalID) {
	r.mu.Lock()
	v := r.views[t]
	delete(r.views, t)
	r.mu.Unlock()
	if v != nil {
		_ = v.server.Close()
		_ = os.Remove(v.socket)
	}
	if err := r.d.store.DeleteCodexTerminal(string(t)); err != nil {
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
	}
}

func (r *codexShared) serveView(v *codexView, w http.ResponseWriter, req *http.Request) {
	release, admitted := r.d.life.Hold("codexViewConnection")
	if !admitted {
		http.Error(w, "attn is stopping", http.StatusServiceUnavailable)
		return
	}
	defer release()
	ctx := r.d.life.Context()
	s, err := r.ensureServer(ctx, v.profile, "")
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

// Codex's remote mode forwards neither instructions nor environment; attn adds them here.
func (r *codexShared) prepare(v *codexView, m *codexshared.Message) (func(*codexshared.Message), error) {
	method := m.Method
	switch method {
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
	sessionID := r.d.callerID(string(v.terminal))
	if conversation, _ := params["threadId"].(string); method == "thread/resume" && conversation != "" {
		if holder := r.holder(v.profile, conversation); holder != "" {
			sessionID = holder
		} else if owner := r.d.store.ConversationOwner(sessionID, conversation); owner != "" {
			sessionID = owner
		}
	}
	config, _ := params["config"].(map[string]any)
	if config == nil {
		config = make(map[string]any)
	}
	for key, value := range map[string]string{
		"ATTN_SESSION_ID":   string(v.terminal),
		"ATTN_AGENT":        string(protocol.SessionAgentCodex),
		"ATTN_SOCKET_PATH":  r.d.socketPath,
		"ATTN_WRAPPER_PATH": r.d.wrapperPath(),
	} {
		config["shell_environment_policy.set."+key] = value
	}
	config["features.hooks"] = true
	// A new conversation in a terminal that shows one becomes a plain successor: no chief or crew role.
	launchAs, chief := sessionID, r.d.isChiefOfStaffSession(sessionID)
	if method != "thread/resume" && r.conversation(sessionID) != "" {
		launchAs, chief = "", false
	}
	if limit := r.d.launchContextWindowCap(launchAs, string(protocol.SessionAgentCodex), chief); limit > 0 {
		config["model_auto_compact_token_limit"] = limit
	}
	// Codex ignores developerInstructions on thread/resume: a conversation keeps the ones it started with.
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
	}
	params["config"] = config
	raw, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	m.Params = raw
	return func(reply *codexshared.Message) {
		var result struct {
			Thread struct {
				ID string `json:"id"`
			} `json:"thread"`
		}
		if len(reply.Error) > 0 || json.Unmarshal(reply.Result, &result) != nil || result.Thread.ID == "" {
			return
		}
		if method != "thread/resume" {
			if err := r.materialize(v.profile, result.Thread.ID); err != nil {
				r.d.logf("shared Codex: refused conversation %s, which could not be written: %v", result.Thread.ID, err)
				reply.Result = nil
				reply.Error, _ = json.Marshal(map[string]any{"code": -32603, "message": "attn could not write the new conversation to disk: " + err.Error()})
				return
			}
		}
		r.show(v, result.Thread.ID)
	}, nil
}

func (r *codexShared) instructions(sessionID, profile string, chief bool) string {
	launch, err := r.d.preparePluginLaunchInstructions(sessionID, profile, chief, false)
	if err != nil {
		r.d.logf("shared Codex: no attn instructions for session %s: %v", sessionID, err)
		return ""
	}
	return launch.Content
}

// Codex resumes only conversations on disk, and a terminal reconnecting after a restart resumes its own.
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

func (r *codexShared) show(v *codexView, conversation string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seq++
	v.thread, v.shownSeq = conversation, r.seq
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

func (r *codexShared) remoteEnv(t harness.TerminalID) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if v := r.views[t]; v != nil {
		return []string{"ATTN_CODEX_REMOTE=unix://" + v.socket}
	}
	return nil
}

// An app-server that exits starts again at the next connection; keep a runtime already restarted under the same id.
func (r *codexShared) terminalExited(t harness.TerminalID) {
	if !isCodexServerTerminal(t) {
		r.mu.Lock()
		v := r.views[t]
		r.mu.Unlock()
		if v != nil {
			r.closeView(t)
			r.idleSoon(v.profile)
		}
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
		exited.ensureMu.Lock()
		defer exited.ensureMu.Unlock()
		exited.mu.Lock()
		held := make([]string, 0, len(exited.loaded)+len(exited.dropped))
		for conversation := range exited.dropped {
			held = append(held, conversation)
		}
		if exited.control != nil {
			for conversation := range exited.loaded {
				held = append(held, conversation)
			}
		}
		exited.dropped = nil
		exited.mu.Unlock()
		if client := exited.client(); client != nil {
			client.Close()
		}
		r.d.logf("shared Codex: the app-server of profile %s exited; the next connection starts it again", exited.profile)
		for _, conversation := range held {
			r.d.life.Go("codexConversationUnloaded", func() { r.unloaded(exited, conversation, true) })
		}
	}
	if !r.serverRunning(context.Background(), t) {
		if err := r.d.removePTYSession(t); err != nil {
			r.d.logf("pty backend remove on exit failed for %s: %v", t, err)
		}
	}
}

// Runs before startup decides which sessions are still live.
func (r *codexShared) recoverServers(ctx context.Context) {
	live := r.d.liveTerminals(ctx)
	profiles, err := r.d.store.ListProfiles(false)
	if err != nil {
		r.d.logf("shared Codex: %v", err)
		return
	}
	for _, profile := range profiles {
		if _, running := live[codexServerTerminal(profile.ID)]; !running {
			continue
		}
		connectCtx, cancel := context.WithTimeout(ctx, codexServerCallLimit)
		if _, err := r.ensureServer(connectCtx, profile.ID, ""); err != nil {
			r.d.logf("shared Codex: reconnect to the app-server of profile %s: %v", profile.ID, err)
		}
		cancel()
	}
}

// Runs once recovery knows which terminals survived.
func (r *codexShared) recoverViews(ctx context.Context) {
	live := r.d.liveTerminals(ctx)
	terminals, err := r.d.store.CodexTerminals()
	if err != nil {
		r.d.logf("shared Codex: %v", err)
		return
	}
	for _, t := range terminals {
		id := harness.TerminalID(t.TerminalID)
		if _, running := live[id]; !running {
			if err := r.d.store.DeleteCodexTerminal(t.TerminalID); err != nil {
				r.d.logf("shared Codex: %v", err)
			}
			continue
		}
		if _, err := r.openView(id, t.ProfileID); err != nil {
			r.d.logf("shared Codex: %v", err)
		}
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

// Subagents announce their parent only in thread/started, which a reconnect does not replay.
func (r *codexShared) restoreParents(s *codexServer, client *codexshared.Client, loaded []string) {
	for _, id := range loaded {
		ctx, cancel := context.WithTimeout(r.d.life.Context(), codexServerCallLimit)
		raw, err := client.Call(ctx, "thread/read", map[string]any{"threadId": id})
		cancel()
		var read struct {
			Thread struct {
				Parent string `json:"parentThreadId"`
			} `json:"thread"`
		}
		if err != nil || json.Unmarshal(raw, &read) != nil || read.Thread.Parent == "" {
			continue
		}
		s.mu.Lock()
		if s.parents == nil {
			s.parents = make(map[string]string)
		}
		s.parents[id] = read.Thread.Parent
		s.mu.Unlock()
	}
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

// A subagent's hooks belong to its root's session.
func (d *Daemon) codexThreadCaller(profile, conversation string) string {
	r, asked := d.codexShared(), conversation
	for range 8 {
		if t, ok := r.terminalShowing(profile, conversation); ok {
			if s, ok := d.terminals().Showing(t); ok {
				return string(s)
			}
		}
		if s := d.store.OpenSessionHolding(profile, conversation); s != "" {
			return s
		}
		if conversation = r.parent(profile, conversation); conversation == "" {
			break
		}
	}
	return hooks.CodexThreadCaller(profile, asked)
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
