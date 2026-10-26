package fakeagent

import (
	"bufio"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
	"nhooyr.io/websocket"

	"github.com/victorarias/attn/internal/codexshared"
	"github.com/victorarias/attn/internal/harness"
)

const (
	roleCodexServer      = "codex-server"
	methodFakeReply      = "attn-fake/reply"
	methodFakeHalt       = "attn-fake/halt"
	methodFakeApproval   = "attn-fake/ask-approval"
	methodFakeCrash      = "attn-fake/crash"
	methodFakeTerminal   = "attn-fake/terminal"
	methodThreadName     = "thread_name"
	methodAwaitArchived  = "await_archived"
	methodAskQuestion    = "ask_question"
	methodWork           = "work"
	methodInstructions   = "instructions"
	methodCompactLimit   = "compact_limit"
	methodMemoryConfig   = "memory_config"
	methodWorkspaceRoots = "workspace_roots"
	methodPermissions    = "permissions"
	methodDropControl    = "drop_control"
	methodRunTool        = "run_tool"
	methodSpawnSubagent  = "spawn_subagent"
	codexApprovalRequest = "item/commandExecution/requestApproval"
	codexDrainRefusal    = "Server is draining; retry after reconnecting"
)

type codexRefusal string

func (r codexRefusal) Error() string { return string(r) }

var codexRefusedWhileDraining = map[string]bool{"turn/start": true, "turn/steer": true, "thread/start": true, "thread/resume": true, "thread/fork": true, "thread/archive": true}

type codexAppServer struct {
	cfg      config
	version  string
	control  *rpcPeer
	listener net.Listener
	draining bool
	hooks    hookSet
	toolEnv  []string
	mu       sync.Mutex
	threads  map[string]*codexServerThread
	conns    map[*codexServerConn]bool
	nextID   int
	prompts  map[string]chan string
	named    chan struct{}
	archived chan struct{}
	listed   chan struct{}
}

type codexServerConn struct {
	ws       *websocket.Conn
	writeMu  sync.Mutex
	terminal bool
}

type codexServerThread struct {
	c              *codex
	written        bool
	started        time.Time
	sessionSource  string
	sessionStarted bool
	active         bool
	subscribers    map[*codexServerConn]bool
	approval       json.RawMessage
	approvalID     string
	question       bool
	instructions   string
	compactLimit   string
	memoryConfig   map[string]any
	workspaceRoots []string
	parent         string
}

type CodexPermissions struct {
	ApprovalPolicy    string `json:"approvalPolicy,omitempty"`
	ApprovalsReviewer string `json:"approvalsReviewer,omitempty"`
	Sandbox           string `json:"sandbox,omitempty"`
}

var codexConfigApprovals = CodexPermissions{ApprovalPolicy: "on-request", ApprovalsReviewer: "user", Sandbox: "workspace-write"}

func (current CodexPermissions) overriddenBy(asked CodexPermissions) bool {
	differs := func(want, have string) bool { return want != "" && want != have }
	return differs(asked.ApprovalPolicy, current.ApprovalPolicy) || differs(asked.ApprovalsReviewer, current.ApprovalsReviewer) || differs(asked.Sandbox, current.Sandbox)
}

var codexServerFlags = flagSpec{values: map[string]bool{"-c": true, "--listen": true}}

func codexListenPath(argv []string) string {
	return strings.TrimPrefix(codexServerFlags.parse(argv[2:]).value("--listen"), "unix://")
}

func codexServerProfile(argv []string) string {
	configured, err := codexHooks(codexServerFlags.parse(argv[2:]).values["-c"], "")
	if err != nil {
		return ""
	}
	for _, env := range configured.env {
		if profile, ok := strings.CutPrefix(env, harness.CodexSharedProfileEnv+"="); ok {
			return profile
		}
	}
	return ""
}

func runCodexAppServer(cfg config) int {
	if refusal := readVersionFile(filepath.Join(programDir(), codexStartErrorFile), ""); refusal != "" {
		fmt.Fprintln(os.Stderr, refusal)
		return 1
	}
	args := codexServerFlags.parse(os.Args[2:])
	path := codexListenPath(os.Args)
	configured, err := codexHooks(args.values["-c"], "")
	if err != nil {
		fmt.Fprintf(os.Stderr, "fake codex app-server: %v\n", err)
		return 1
	}
	s := &codexAppServer{
		cfg:      cfg,
		version:  installedCodexVersion(),
		hooks:    hookSet{groups: configured.groups, env: os.Environ()},
		toolEnv:  configured.env,
		threads:  map[string]*codexServerThread{},
		conns:    map[*codexServerConn]bool{},
		prompts:  map[string]chan string{},
		named:    make(chan struct{}),
		archived: make(chan struct{}),
		listed:   make(chan struct{}),
	}
	control, err := dialControl(cfg, s.handleKit)
	if err != nil {
		fmt.Fprintf(os.Stderr, "fake codex app-server: %v\n", err)
		return 1
	}
	s.control = control
	control.start()
	var boot bootingResult
	if err := control.call(context.Background(), methodBooting, bootingParams{AttnSessionID: string(harness.TerminalFromEnv())}, &boot); err != nil {
		return 1
	}
	if boot.Exit {
		return boot.Code
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: app-server control socket is already in use at %s: %v\n", path, err)
		return 1
	}
	report := launch{Role: roleCodexServer, Harness: Codex, Pid: os.Getpid(), Argv: os.Args, Env: os.Environ()}
	if err := control.call(context.Background(), methodLaunched, report, nil); err != nil {
		return 1
	}
	s.listener = listener
	signals := make(chan os.Signal, 2)
	signal.Notify(signals, syscall.SIGHUP, syscall.SIGTERM, syscall.SIGINT)
	go s.drainOn(signals)
	_ = http.Serve(listener, http.HandlerFunc(s.serve))
	s.mu.Lock()
	draining := s.draining
	s.mu.Unlock()
	if draining {
		select {}
	}
	return 0
}

func (s *codexAppServer) drainOn(signals <-chan os.Signal) {
	forcible := false
	for received := range signals {
		sig := received.(syscall.Signal)
		if forcible && sig != syscall.SIGHUP {
			os.Exit(signalExitBase + int(sig))
		}
		forcible = forcible || sig != syscall.SIGHUP
		s.mu.Lock()
		s.draining = true
		s.mu.Unlock()
		s.stopWhenDrained()
	}
}

func (s *codexAppServer) stopWhenDrained() {
	s.mu.Lock()
	drained := s.draining
	for _, t := range s.threads {
		drained = drained && !t.active
	}
	conns := make([]*codexServerConn, 0, len(s.conns))
	for conn := range s.conns {
		conns = append(conns, conn)
	}
	s.mu.Unlock()
	if !drained {
		return
	}
	_ = s.listener.Close()
	for _, conn := range conns {
		_ = conn.ws.CloseNow()
	}
	go func() {
		s.control.answering.Wait()
		os.Exit(0)
	}()
}

func (s *codexAppServer) serve(w http.ResponseWriter, req *http.Request) {
	ws, err := websocket.Accept(w, req, nil)
	if err != nil {
		return
	}
	ws.SetReadLimit(-1)
	conn := &codexServerConn{ws: ws}
	s.mu.Lock()
	s.conns[conn] = true
	s.mu.Unlock()
	defer s.disconnect(conn)
	for {
		_, data, err := ws.Read(context.Background())
		if err != nil {
			return
		}
		var m codexshared.Message
		if json.Unmarshal(data, &m) != nil {
			continue
		}
		if m.Method == "" {
			s.answered(m)
			continue
		}
		if len(m.ID) == 0 {
			continue
		}
		result, after, err := s.handle(conn, m)
		reply := codexshared.Message{ID: m.ID}
		if err != nil {
			reply.Error, _ = json.Marshal(map[string]any{"code": -32600, "message": err.Error()})
		} else if reply.Result, err = json.Marshal(result); err != nil {
			return
		}
		conn.send(reply)
		if after != nil {
			after()
		}
	}
}

func (c *codexServerConn) send(v any) {
	data, err := json.Marshal(v)
	if err != nil {
		return
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_ = c.ws.Write(context.Background(), websocket.MessageText, data)
}

func notification(method string, params any) map[string]any {
	return map[string]any{"method": method, "params": params}
}

func (s *codexAppServer) broadcast(method string, params any) {
	s.mu.Lock()
	conns := make([]*codexServerConn, 0, len(s.conns))
	for conn := range s.conns {
		conns = append(conns, conn)
	}
	s.mu.Unlock()
	for _, conn := range conns {
		conn.send(notification(method, params))
	}
}

func (s *codexAppServer) tell(t *codexServerThread, frame any) {
	s.mu.Lock()
	conns := make([]*codexServerConn, 0, len(t.subscribers))
	for conn := range t.subscribers {
		conns = append(conns, conn)
	}
	s.mu.Unlock()
	for _, conn := range conns {
		conn.send(frame)
	}
}

func (s *codexAppServer) disconnect(conn *codexServerConn) {
	s.mu.Lock()
	delete(s.conns, conn)
	var idle []string
	for id, t := range s.threads {
		delete(t.subscribers, conn)
		if len(t.subscribers) == 0 && !t.active {
			idle = append(idle, id)
		}
	}
	s.mu.Unlock()
	for _, id := range idle {
		s.unload(id)
	}
}

func (s *codexAppServer) unload(id string) {
	s.mu.Lock()
	t := s.threads[id]
	if t == nil || len(t.subscribers) > 0 || t.active {
		s.mu.Unlock()
		return
	}
	delete(s.threads, id)
	s.mu.Unlock()
	s.broadcast("thread/status/changed", map[string]any{"threadId": id, "status": map[string]any{"type": "notLoaded"}})
	s.broadcast("thread/closed", map[string]any{"threadId": id})
}

type codexServerParams struct {
	ThreadID       string            `json:"threadId"`
	Name           string            `json:"name"`
	ExpectedTurnID string            `json:"expectedTurnId"`
	CWD            string            `json:"cwd"`
	Model          string            `json:"model"`
	Ephemeral      bool              `json:"ephemeral"`
	Text           string            `json:"text"`
	Items          []json.RawMessage `json:"items"`
	DevInstruction string            `json:"developerInstructions"`
	WorkspaceRoots []string          `json:"runtimeWorkspaceRoots"`
	Config         map[string]any    `json:"config"`
	CodexPermissions
	Input []struct {
		Text string `json:"text"`
	} `json:"input"`
}

func (s *codexAppServer) handle(conn *codexServerConn, m codexshared.Message) (any, func(), error) {
	var p codexServerParams
	if len(m.Params) > 0 {
		if err := json.Unmarshal(m.Params, &p); err != nil {
			return nil, nil, err
		}
	}
	s.mu.Lock()
	refused := s.draining && codexRefusedWhileDraining[m.Method]
	s.mu.Unlock()
	if refused {
		return nil, nil, codexRefusal(codexDrainRefusal)
	}
	switch m.Method {
	case "initialize":
		return map[string]any{"userAgent": "codex-tui/" + s.version + " (fake; fake) fakeagent", "codexHome": s.cfg.CodexHome, "platformFamily": "unix", "platformOs": "linux"}, nil, nil
	case "config/read":
		config, err := readCodexConfig(s.cfg.CodexHome)
		return map[string]any{"config": config}, nil, err
	case "config/batchWrite":
		return map[string]any{}, nil, s.writeConfig(m.Params)
	case "thread/start":
		return s.start(conn, p)
	case "thread/resume":
		return s.resume(conn, p)
	case "thread/unsubscribe":
		s.mu.Lock()
		t := s.threads[p.ThreadID]
		if t != nil {
			delete(t.subscribers, conn)
		}
		s.mu.Unlock()
		return map[string]any{"status": "unsubscribed"}, func() { s.unload(p.ThreadID) }, nil
	case "thread/inject_items":
		t, err := s.loaded(p.ThreadID)
		if err != nil {
			return nil, nil, err
		}
		if len(p.Items) == 0 {
			return nil, nil, fmt.Errorf("items must not be empty")
		}
		lines := make([]any, 0, len(p.Items))
		for _, item := range p.Items {
			lines = append(lines, map[string]any{"timestamp": now(), "type": "response_item", "payload": item})
		}
		return map[string]any{}, nil, s.write(t, lines...)
	case "thread/read":
		if t, err := s.loaded(p.ThreadID); err == nil {
			return map[string]any{"thread": s.metadata(t)}, nil, nil
		}
		if path, _ := s.findRollout(p.ThreadID); path == "" {
			return nil, nil, fmt.Errorf("thread not loaded: %s", p.ThreadID)
		}
		return map[string]any{"thread": map[string]any{"id": p.ThreadID, "status": map[string]any{"type": "notLoaded"}}}, nil, nil
	case "thread/name/set":
		if path, _ := s.findRollout(p.ThreadID); path == "" {
			if _, err := s.loaded(p.ThreadID); err != nil {
				return nil, nil, err
			}
		}
		if err := s.nameThread(p.ThreadID, p.Name); err != nil {
			return nil, nil, err
		}
		s.mu.Lock()
		close(s.named)
		s.named = make(chan struct{})
		s.mu.Unlock()
		return map[string]any{}, func() {
			s.broadcast("thread/name/updated", map[string]any{"threadId": p.ThreadID, "threadName": p.Name})
		}, nil
	case "thread/archive":
		return s.archive(p.ThreadID)
	case "thread/unarchive":
		return s.unarchive(p.ThreadID)
	case "thread/turns/list":
		t, err := s.loaded(p.ThreadID)
		if err != nil {
			return nil, nil, err
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		turns := []any{}
		if t.c.turnID != "" {
			status := "completed"
			if t.active {
				status = "inProgress"
			}
			turns = append(turns, map[string]any{"id": t.c.turnID, "status": status, "items": []any{}})
		}
		return map[string]any{"data": turns, "nextCursor": nil, "backwardsCursor": nil}, nil, nil
	case "turn/steer":
		return s.steer(conn, p)
	case "thread/loaded/list":
		s.mu.Lock()
		close(s.listed)
		s.listed = make(chan struct{})
		ids := make([]string, 0, len(s.threads))
		for id := range s.threads {
			ids = append(ids, id)
		}
		s.mu.Unlock()
		return map[string]any{"data": ids}, nil, nil
	case "turn/start":
		return s.turn(conn, p)
	case "turn/interrupt":
		return s.endTurn(methodFakeHalt, p)
	case methodFakeTerminal:
		s.mu.Lock()
		conn.terminal = true
		s.mu.Unlock()
		return map[string]any{}, nil, nil
	case methodFakeReply, methodFakeHalt:
		return s.endTurn(m.Method, p)
	case methodFakeApproval:
		return s.askApproval(p)
	case methodFakeCrash:
		return map[string]any{}, func() { os.Exit(1) }, nil
	default:
		return nil, nil, fmt.Errorf("the fake app-server does not serve %s", m.Method)
	}
}

func (s *codexAppServer) writeConfig(params json.RawMessage) error {
	var batch struct {
		Edits []struct {
			KeyPath string `json:"keyPath"`
			Value   string `json:"value"`
		} `json:"edits"`
	}
	if err := json.Unmarshal(params, &batch); err != nil {
		return err
	}
	for _, edit := range batch.Edits {
		quoted, ok := strings.CutPrefix(edit.KeyPath, "projects.")
		quoted, trust := strings.CutSuffix(quoted, ".trust_level")
		folder, err := strconv.Unquote(quoted)
		if !ok || !trust || err != nil {
			return fmt.Errorf("the fake app-server writes only project trust levels, not %s", edit.KeyPath)
		}
		if err := setCodexFolderTrust(s.cfg.CodexHome, folder, edit.Value); err != nil {
			return err
		}
	}
	return nil
}

func (s *codexAppServer) loaded(id string) (*codexServerThread, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t := s.threads[id]; t != nil {
		return t, nil
	}
	return nil, fmt.Errorf("thread not loaded: %s", id)
}

func (s *codexAppServer) newThread(id, cwd, model, path, source string, started time.Time, conn *codexServerConn) *codexServerThread {
	hooks := s.hooks
	hooks.cwd = cwd
	c := &codex{cfg: s.cfg, term: &terminal{style: codexComposer, answers: make(chan string, 4)}, hooks: hooks, cwd: cwd, conversation: id, transcript: path, model: model}
	return &codexServerThread{c: c, started: started, sessionSource: source, subscribers: map[*codexServerConn]bool{conn: true}}
}

func (s *codexAppServer) start(conn *codexServerConn, p codexServerParams) (any, func(), error) {
	id, err := uuid.NewV7()
	if err != nil {
		return nil, nil, err
	}
	started := time.Now().UTC()
	t := s.newThread(id.String(), p.CWD, p.Model, codexRolloutPath(filepath.Join(s.cfg.CodexHome, "sessions"), id.String(), started), "startup", started, conn)
	if p.Ephemeral {
		return map[string]any{"thread": map[string]any{"id": id.String(), "ephemeral": true}}, nil, nil
	}
	t.instructions, t.compactLimit, t.workspaceRoots, t.c.approvals = p.DevInstruction, p.compactLimit(), p.WorkspaceRoots, p.CodexPermissions
	t.memoryConfig = p.Config
	s.mu.Lock()
	s.threads[t.c.conversation] = t
	s.mu.Unlock()
	return map[string]any{"thread": s.metadata(t)}, func() {
		s.broadcast("thread/started", map[string]any{"thread": s.metadata(t)})
	}, nil
}

func (s *codexAppServer) resume(conn *codexServerConn, p codexServerParams) (any, func(), error) {
	s.mu.Lock()
	t := s.threads[p.ThreadID]
	if t != nil && !t.written {
		s.mu.Unlock()
		return nil, nil, fmt.Errorf("no rollout found for thread id %s", p.ThreadID)
	}
	loaded := t != nil
	reloads := loaded && len(t.subscribers) == 0 && !t.active && t.c.approvals.overriddenBy(p.CodexPermissions)
	if loaded {
		t.subscribers[conn] = true
	}
	s.mu.Unlock()
	if !loaded {
		path, cwd := s.findRollout(p.ThreadID)
		if path == "" {
			return nil, nil, fmt.Errorf("no rollout found for thread id %s", p.ThreadID)
		}
		t = s.newThread(p.ThreadID, cwd, p.Model, path, "resume", time.Now().UTC(), conn)
		t.written = true
	}
	if !loaded || reloads {
		saved := codexSavedApprovals(t.c.transcript)
		approvals := CodexPermissions{
			ApprovalPolicy:    cmp.Or(p.ApprovalPolicy, saved.ApprovalPolicy),
			ApprovalsReviewer: cmp.Or(p.ApprovalsReviewer, saved.ApprovalsReviewer),
			Sandbox:           cmp.Or(p.Sandbox, codexConfigApprovals.Sandbox),
		}
		if err := appendLines(t.c.transcript, codexTurnContext(approvals)); err != nil {
			return nil, nil, err
		}
		s.mu.Lock()
		t.compactLimit, t.c.approvals = p.compactLimit(), approvals
		t.memoryConfig = p.Config
		s.threads[p.ThreadID] = t
		s.mu.Unlock()
	}
	return map[string]any{"thread": s.metadata(t)}, func() {
		s.status(t)
		s.mu.Lock()
		pending := t.approval
		s.mu.Unlock()
		if pending != nil {
			conn.send(pending)
		}
	}, nil
}

func (p codexServerParams) compactLimit() string {
	if limit, ok := p.Config["model_auto_compact_token_limit"]; ok {
		return fmt.Sprint(limit)
	}
	return ""
}

func (s *codexAppServer) findRollout(id string) (path, cwd string) {
	_ = filepath.WalkDir(filepath.Join(s.cfg.CodexHome, "sessions"), func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(d.Name(), "-"+id+".jsonl") {
			path = p
			return fs.SkipAll
		}
		return nil
	})
	if path == "" {
		return "", ""
	}
	f, err := os.Open(path)
	if err != nil {
		return path, ""
	}
	defer f.Close()
	var meta struct {
		Payload struct {
			CWD string `json:"cwd"`
		} `json:"payload"`
	}
	if line, err := bufio.NewReader(f).ReadBytes('\n'); err == nil {
		_ = json.Unmarshal(line, &meta)
	}
	return path, meta.Payload.CWD
}

func (s *codexAppServer) write(t *codexServerThread, lines ...any) error {
	s.mu.Lock()
	first := !t.written
	t.written = true
	s.mu.Unlock()
	if first {
		lines = append([]any{codexSessionMeta(t.c.conversation, t.c.cwd, "codex-tui", t.started), codexTurnContext(t.c.approvals)}, lines...)
	}
	return appendLines(t.c.transcript, lines...)
}

func codexTurnContext(p CodexPermissions) map[string]any {
	return map[string]any{"timestamp": now(), "type": "turn_context", "payload": p}
}

func codexSavedApprovals(path string) CodexPermissions {
	var saved CodexPermissions
	f, err := os.Open(path)
	if err != nil {
		return saved
	}
	defer f.Close()
	lines := json.NewDecoder(f)
	for {
		var line struct {
			Type    string           `json:"type"`
			Payload CodexPermissions `json:"payload"`
		}
		if lines.Decode(&line) != nil {
			return saved
		}
		if line.Type == "turn_context" {
			saved = CodexPermissions{ApprovalPolicy: line.Payload.ApprovalPolicy, ApprovalsReviewer: line.Payload.ApprovalsReviewer}
		}
	}
}

func (s *codexAppServer) metadata(t *codexServerThread) map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	status := map[string]any{"type": "idle"}
	if t.active {
		flags := []string{}
		if t.approval != nil {
			flags = append(flags, "waitingOnApproval")
		}
		if t.question {
			flags = append(flags, "waitingOnUserInput")
		}
		status = map[string]any{"type": "active", "activeFlags": flags}
	}
	thread := map[string]any{"id": t.c.conversation, "cwd": t.c.cwd, "path": t.c.transcript, "ephemeral": false, "source": "cli", "status": status}
	if t.parent != "" {
		thread["parentThreadId"] = t.parent
	}
	return thread
}

func (s *codexAppServer) status(t *codexServerThread) {
	s.broadcast("thread/status/changed", map[string]any{"threadId": t.c.conversation, "status": s.metadata(t)["status"]})
}

func (s *codexAppServer) turn(conn *codexServerConn, p codexServerParams) (any, func(), error) {
	t, err := s.loaded(p.ThreadID)
	if err != nil {
		return nil, nil, err
	}
	texts := make([]string, 0, len(p.Input))
	for _, input := range p.Input {
		texts = append(texts, input.Text)
	}
	text := strings.Join(texts, "\n")
	s.mu.Lock()
	if t.active {
		s.mu.Unlock()
		return nil, nil, fmt.Errorf("turn already active on thread %s", p.ThreadID)
	}
	t.active = true
	firstTurn := !t.sessionStarted
	t.sessionStarted = true
	s.mu.Unlock()
	t.c.turnID = uuid.NewString()
	if firstTurn {
		if err := s.write(t); err != nil {
			return nil, nil, err
		}
		if err := t.c.hooks.run("SessionStart", t.sessionSource, t.c.hookInput("SessionStart", map[string]any{"source": t.sessionSource})); err != nil {
			return nil, nil, err
		}
	}
	if err := t.c.hooks.run("UserPromptSubmit", "", t.c.hookInput("UserPromptSubmit", map[string]any{"prompt": text})); err != nil {
		return nil, nil, err
	}
	if err := s.write(t, codexEvent("user_message", text)); err != nil {
		return nil, nil, err
	}
	s.prompted(conn, p.ThreadID, text)
	turn := map[string]any{"id": t.c.turnID, "status": "inProgress"}
	return map[string]any{"turn": turn}, func() {
		s.tell(t, notification("turn/started", map[string]any{"threadId": t.c.conversation, "turn": turn}))
		s.status(t)
	}, nil
}

func (s *codexAppServer) endTurn(method string, p codexServerParams) (any, func(), error) {
	t, err := s.loaded(p.ThreadID)
	if err != nil {
		return nil, nil, err
	}
	s.mu.Lock()
	active := t.active
	s.mu.Unlock()
	if !active {
		return nil, nil, fmt.Errorf("no active turn on thread %s", p.ThreadID)
	}
	if method == methodFakeHalt {
		err = t.c.halt()
	} else {
		err = t.c.reply(p.Text, false)
	}
	if err != nil {
		return nil, nil, err
	}
	s.mu.Lock()
	t.active, t.approval, t.approvalID, t.question = false, nil, "", false
	s.mu.Unlock()
	turn := map[string]any{"id": t.c.turnID, "status": "completed"}
	if method == methodFakeHalt {
		turn["status"] = "interrupted"
	}
	return map[string]any{}, func() {
		defer s.stopWhenDrained()
		if method == methodFakeReply {
			s.tell(t, notification("item/completed", map[string]any{"threadId": t.c.conversation, "turnId": t.c.turnID, "item": map[string]any{"type": "agentMessage", "text": p.Text}}))
		}
		s.tell(t, notification("turn/completed", map[string]any{"threadId": t.c.conversation, "turn": turn}))
		s.status(t)
	}, nil
}

func (s *codexAppServer) askApproval(p codexServerParams) (any, func(), error) {
	t, err := s.loaded(p.ThreadID)
	if err != nil {
		return nil, nil, err
	}
	if err := t.c.hooks.run("PermissionRequest", "Bash", t.c.hookInput("PermissionRequest", map[string]any{"tool_name": "Bash"})); err != nil {
		return nil, nil, err
	}
	s.mu.Lock()
	s.nextID++
	id := fmt.Sprintf("approval-%d", s.nextID)
	request, err := json.Marshal(map[string]any{"id": id, "method": codexApprovalRequest, "params": map[string]any{
		"threadId": t.c.conversation, "turnId": t.c.turnID, "itemId": "command-1", "command": "make migrate", "reason": "run the migration",
	}})
	if err == nil {
		t.approval, t.approvalID = request, id
	}
	s.mu.Unlock()
	if err != nil {
		return nil, nil, err
	}
	return map[string]any{}, func() {
		s.tell(t, json.RawMessage(request))
		s.status(t)
	}, nil
}

func (s *codexAppServer) answered(m codexshared.Message) {
	var id string
	if json.Unmarshal(m.ID, &id) != nil {
		return
	}
	s.mu.Lock()
	var settled *codexServerThread
	for _, t := range s.threads {
		if t.approvalID == id {
			settled = t
			t.approval, t.approvalID = nil, ""
		}
	}
	s.mu.Unlock()
	if settled == nil {
		return
	}
	s.tell(settled, notification("serverRequest/resolved", map[string]any{"threadId": settled.c.conversation, "requestId": id}))
	s.status(settled)
}

func (s *codexAppServer) steer(conn *codexServerConn, p codexServerParams) (any, func(), error) {
	t, err := s.loaded(p.ThreadID)
	if err != nil {
		return nil, nil, err
	}
	s.mu.Lock()
	active, turnID := t.active, t.c.turnID
	s.mu.Unlock()
	if !active {
		return nil, nil, fmt.Errorf("no active turn to steer")
	}
	if p.ExpectedTurnID != turnID {
		return nil, nil, fmt.Errorf("expected turn %s, but turn %s is active", p.ExpectedTurnID, turnID)
	}
	texts := make([]string, 0, len(p.Input))
	for _, input := range p.Input {
		texts = append(texts, input.Text)
	}
	text := strings.Join(texts, "\n")
	if err := s.write(t, codexEvent("user_message", text)); err != nil {
		return nil, nil, err
	}
	s.prompted(conn, p.ThreadID, text)
	return map[string]any{"turnId": turnID}, nil, nil
}

func (s *codexAppServer) promptsOf(conversation string) chan string {
	s.mu.Lock()
	defer s.mu.Unlock()
	prompts := s.prompts[conversation]
	if prompts == nil {
		prompts = make(chan string, 16)
		s.prompts[conversation] = prompts
	}
	return prompts
}

func (s *codexAppServer) prompted(conn *codexServerConn, conversation, text string) {
	s.mu.Lock()
	typed := conn.terminal
	s.mu.Unlock()
	if typed {
		return
	}
	select {
	case s.promptsOf(conversation) <- text:
	default:
	}
}

func (s *codexAppServer) archive(id string) (any, func(), error) {
	s.mu.Lock()
	t := s.threads[id]
	s.mu.Unlock()
	if t != nil {
		if err := s.write(t); err != nil {
			return nil, nil, err
		}
	}
	path, _ := s.findRollout(id)
	if path == "" {
		return nil, nil, fmt.Errorf("no rollout found for thread id %s", id)
	}
	archived := filepath.Join(s.cfg.CodexHome, "archived_sessions")
	if err := os.MkdirAll(archived, 0o755); err != nil {
		return nil, nil, err
	}
	if err := os.Rename(path, filepath.Join(archived, filepath.Base(path))); err != nil {
		return nil, nil, err
	}
	s.mu.Lock()
	delete(s.threads, id)
	close(s.archived)
	s.archived = make(chan struct{})
	s.mu.Unlock()
	return map[string]any{}, func() {
		s.broadcast("thread/status/changed", map[string]any{"threadId": id, "status": map[string]any{"type": "notLoaded"}})
		s.broadcast("thread/archived", map[string]any{"threadId": id})
	}, nil
}

func (s *codexAppServer) archivedRollout(id string) string {
	return codexArchivedRollout(s.cfg.CodexHome, id)
}

func codexArchivedRollout(codexHome, id string) string {
	entries, _ := os.ReadDir(filepath.Join(codexHome, "archived_sessions"))
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), "-"+id+".jsonl") {
			return entry.Name()
		}
	}
	return ""
}

type codexSessionIndexEntry struct {
	ID         string `json:"id"`
	ThreadName string `json:"thread_name"`
	UpdatedAt  string `json:"updated_at"`
}

func (s *codexAppServer) sessionIndexPath() string {
	return filepath.Join(s.cfg.CodexHome, "session_index.jsonl")
}

func (s *codexAppServer) nameThread(id, name string) error {
	line, err := json.Marshal(codexSessionIndexEntry{ID: id, ThreadName: name, UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano)})
	if err != nil {
		return err
	}
	f, err := os.OpenFile(s.sessionIndexPath(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(line, '\n'))
	return err
}

func (s *codexAppServer) threadName(id string) string {
	f, err := os.Open(s.sessionIndexPath())
	if err != nil {
		return ""
	}
	defer f.Close()
	var name string
	lines := bufio.NewScanner(f)
	for lines.Scan() {
		var entry codexSessionIndexEntry
		if json.Unmarshal(lines.Bytes(), &entry) == nil && entry.ID == id {
			name = entry.ThreadName
		}
	}
	return name
}

func (s *codexAppServer) unarchive(id string) (any, func(), error) {
	name := s.archivedRollout(id)
	if name == "" {
		return nil, nil, fmt.Errorf("no archived rollout found for thread id %s", id)
	}
	stamp, _, _ := strings.Cut(strings.TrimPrefix(name, "rollout-"), "T")
	date, err := time.Parse("2006-01-02", stamp)
	if err != nil {
		return nil, nil, err
	}
	dir := filepath.Join(s.cfg.CodexHome, "sessions", date.Format("2006/01/02"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, nil, err
	}
	if err := os.Rename(filepath.Join(s.cfg.CodexHome, "archived_sessions", name), filepath.Join(dir, name)); err != nil {
		return nil, nil, err
	}
	return map[string]any{"thread": map[string]any{"id": id, "status": map[string]any{"type": "notLoaded"}}}, func() {
		s.broadcast("thread/unarchived", map[string]any{"threadId": id})
	}, nil
}

type serverThreadParams struct {
	ThreadID string `json:"threadId"`
	Text     string `json:"text"`
}

func (s *codexAppServer) handleKit(_ *rpcPeer, method string, raw json.RawMessage) (any, error) {
	var p serverThreadParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, err
	}
	switch method {
	case methodPrompted:
		select {
		case text := <-s.promptsOf(p.ThreadID):
			return promptedResult{Text: text, ConversationID: p.ThreadID}, nil
		case <-time.After(HangGuard):
			return nil, fmt.Errorf("conversation %s took no prompt within %s", p.ThreadID, HangGuard)
		}
	case methodReply, methodHalt:
		kind := methodFakeReply
		if method == methodHalt {
			kind = methodFakeHalt
		}
		_, after, err := s.endTurn(kind, codexServerParams{ThreadID: p.ThreadID, Text: p.Text})
		if err == nil && after != nil {
			after()
		}
		return struct{}{}, err
	case methodThreadName:
		deadline := time.After(HangGuard)
		for {
			s.mu.Lock()
			named := s.named
			s.mu.Unlock()
			name := s.threadName(p.ThreadID)
			if name == p.Text {
				return struct{}{}, nil
			}
			select {
			case <-named:
			case <-deadline:
				return nil, fmt.Errorf("conversation %s is named %q, not %q, after %s", p.ThreadID, name, p.Text, HangGuard)
			}
		}
	case methodAwaitArchived:
		deadline := time.After(HangGuard)
		for {
			s.mu.Lock()
			archived := s.archived
			s.mu.Unlock()
			if s.archivedRollout(p.ThreadID) != "" {
				return struct{}{}, nil
			}
			select {
			case <-archived:
			case <-deadline:
				return nil, fmt.Errorf("conversation %s is not archived after %s", p.ThreadID, HangGuard)
			}
		}
	case methodAskApproval:
		_, after, err := s.askApproval(codexServerParams{ThreadID: p.ThreadID})
		if err == nil && after != nil {
			after()
		}
		return struct{}{}, err
	case methodWork:
		t, err := s.loaded(p.ThreadID)
		if err != nil {
			return nil, err
		}
		s.mu.Lock()
		t.active = true
		s.mu.Unlock()
		s.status(t)
		return struct{}{}, nil
	case methodAskQuestion:
		t, err := s.loaded(p.ThreadID)
		if err != nil {
			return nil, err
		}
		s.mu.Lock()
		t.question = true
		s.mu.Unlock()
		s.status(t)
		return struct{}{}, nil
	case methodRunTool:
		t, err := s.loaded(p.ThreadID)
		if err != nil {
			return nil, err
		}
		cmd := exec.Command("/bin/sh", "-c", p.Text)
		cmd.Dir = t.c.cwd
		cmd.Env = withEnv(s.toolEnv, "CODEX_THREAD_ID", p.ThreadID)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return nil, fmt.Errorf("%v: %s", err, out)
		}
		return promptedResult{Text: string(out), ConversationID: p.ThreadID}, nil
	case methodInstructions:
		s.mu.Lock()
		t := s.threads[p.ThreadID]
		s.mu.Unlock()
		if t == nil {
			return nil, fmt.Errorf("conversation %s was not started here", p.ThreadID)
		}
		return promptedResult{Text: t.instructions, ConversationID: p.ThreadID}, nil
	case methodDropControl:
		s.mu.Lock()
		listed := s.listed
		var dropped []*codexServerConn
		for conn := range s.conns {
			if !conn.terminal {
				dropped = append(dropped, conn)
			}
		}
		s.mu.Unlock()
		for _, conn := range dropped {
			_ = conn.ws.CloseNow()
		}
		select {
		case <-listed:
			return promptedResult{}, nil
		case <-time.After(HangGuard):
			return nil, fmt.Errorf("no control connection came back within %s", HangGuard)
		}
	case methodWorkspaceRoots:
		s.mu.Lock()
		defer s.mu.Unlock()
		if t := s.threads[p.ThreadID]; t != nil {
			return workspaceRootsResult{Roots: t.workspaceRoots}, nil
		}
		return nil, fmt.Errorf("conversation %s is not loaded here", p.ThreadID)
	case methodMemoryConfig:
		s.mu.Lock()
		defer s.mu.Unlock()
		if t := s.threads[p.ThreadID]; t != nil {
			return t.memoryConfig, nil
		}
		return nil, fmt.Errorf("conversation %s is not loaded here", p.ThreadID)
	case methodCompactLimit:
		s.mu.Lock()
		defer s.mu.Unlock()
		if t := s.threads[p.ThreadID]; t != nil {
			return promptedResult{Text: t.compactLimit, ConversationID: p.ThreadID}, nil
		}
		return nil, fmt.Errorf("conversation %s is not loaded here", p.ThreadID)
	case methodPermissions:
		s.mu.Lock()
		defer s.mu.Unlock()
		if t := s.threads[p.ThreadID]; t != nil {
			return t.c.approvals, nil
		}
		return nil, fmt.Errorf("conversation %s is not loaded here", p.ThreadID)
	case methodSubagent:
		path, cwd := s.findRollout(p.ThreadID)
		if path == "" {
			return nil, fmt.Errorf("no rollout for parent %s", p.ThreadID)
		}
		child := &codex{cfg: s.cfg, cwd: cwd, conversation: p.ThreadID}
		if err := child.subagent(p.Text); err != nil {
			return nil, err
		}
		return promptedResult{ConversationID: child.lastThread}, nil
	case methodSpawnSubagent:
		parent, err := s.loaded(p.ThreadID)
		if err != nil {
			return nil, err
		}
		id, err := uuid.NewV7()
		if err != nil {
			return nil, err
		}
		started := time.Now().UTC()
		t := s.newThread(id.String(), parent.c.cwd, parent.c.model, codexRolloutPath(filepath.Join(s.cfg.CodexHome, "sessions"), id.String(), started), "subagent", started, nil)
		t.parent, t.subscribers = p.ThreadID, map[*codexServerConn]bool{}
		if err := s.write(t); err != nil {
			return nil, err
		}
		s.mu.Lock()
		s.threads[t.c.conversation] = t
		s.mu.Unlock()
		s.broadcast("thread/started", map[string]any{"thread": s.metadata(t)})
		return promptedResult{ConversationID: t.c.conversation}, nil
	}
	return nil, fmt.Errorf("unknown method %q", method)
}
