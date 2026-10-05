package fakeagent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
	"nhooyr.io/websocket"

	"github.com/victorarias/attn/internal/codexshared"
)

const (
	roleCodexServer      = "codex-server"
	methodFakeReply      = "attn-fake/reply"
	methodFakeHalt       = "attn-fake/halt"
	methodFakeApproval   = "attn-fake/ask-approval"
	methodFakeCrash      = "attn-fake/crash"
	codexApprovalRequest = "item/commandExecution/requestApproval"
)

// codexAppServer plays stock 0.160.0's app-server: thread/start writes nothing until a turn or an injected
// item, hooks name the conversation, and an idle unsubscribed conversation unloads at once (stock: ~60s).
type codexAppServer struct {
	cfg     config
	hooks   hookSet
	mu      sync.Mutex
	threads map[string]*codexServerThread
	conns   map[*codexServerConn]bool
	nextID  int
}

type codexServerConn struct {
	ws      *websocket.Conn
	writeMu sync.Mutex
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
}

func runCodexAppServer(cfg config) int {
	args := flagSpec{values: map[string]bool{"-c": true, "--listen": true}}.parse(os.Args[2:])
	path := strings.TrimPrefix(args.value("--listen"), "unix://")
	configured, err := codexHooks(args.values["-c"], "")
	if err != nil {
		fmt.Fprintf(os.Stderr, "fake codex app-server: %v\n", err)
		return 1
	}
	s := &codexAppServer{
		cfg:     cfg,
		hooks:   hookSet{groups: configured.groups, env: os.Environ()},
		threads: map[string]*codexServerThread{},
		conns:   map[*codexServerConn]bool{},
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: app-server control socket is already in use at %s: %v\n", path, err)
		return 1
	}
	control, err := dialControl(cfg, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "fake codex app-server: %v\n", err)
		return 1
	}
	control.start()
	report := launch{Role: roleCodexServer, Harness: Codex, Pid: os.Getpid(), Argv: os.Args, Env: os.Environ()}
	if err := control.call(context.Background(), methodLaunched, report, nil); err != nil {
		return 1
	}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGHUP, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		sig := (<-signals).(syscall.Signal)
		_ = listener.Close()
		os.Exit(signalExitBase + int(sig))
	}()
	_ = http.Serve(listener, http.HandlerFunc(s.serve))
	return 0
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
	CWD            string            `json:"cwd"`
	Model          string            `json:"model"`
	Ephemeral      bool              `json:"ephemeral"`
	Text           string            `json:"text"`
	Items          []json.RawMessage `json:"items"`
	DevInstruction string            `json:"developerInstructions"`
	Input          []struct {
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
	switch m.Method {
	case "initialize":
		return map[string]any{"userAgent": "codex-fake", "codexHome": s.cfg.CodexHome, "platformFamily": "unix", "platformOs": "linux"}, nil, nil
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
		t, err := s.loaded(p.ThreadID)
		if err != nil {
			return nil, nil, err
		}
		return map[string]any{"thread": s.metadata(t)}, nil, nil
	case "thread/loaded/list":
		s.mu.Lock()
		ids := make([]string, 0, len(s.threads))
		for id := range s.threads {
			ids = append(ids, id)
		}
		s.mu.Unlock()
		return map[string]any{"data": ids}, nil, nil
	case "turn/start":
		return s.turn(p)
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
	if t != nil {
		t.subscribers[conn] = true
	}
	s.mu.Unlock()
	if t == nil {
		path, cwd := s.findRollout(p.ThreadID)
		if path == "" {
			return nil, nil, fmt.Errorf("no rollout found for thread id %s", p.ThreadID)
		}
		t = s.newThread(p.ThreadID, cwd, p.Model, path, "resume", time.Now().UTC(), conn)
		t.written = true
		s.mu.Lock()
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

// write puts lines in the conversation's rollout, writing the rollout first when it is not on disk.
func (s *codexAppServer) write(t *codexServerThread, lines ...any) error {
	s.mu.Lock()
	first := !t.written
	t.written = true
	s.mu.Unlock()
	if first {
		lines = append([]any{codexSessionMeta(t.c.conversation, t.c.cwd, "codex-tui", t.started)}, lines...)
	}
	return appendLines(t.c.transcript, lines...)
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
		status = map[string]any{"type": "active", "activeFlags": flags}
	}
	return map[string]any{"id": t.c.conversation, "cwd": t.c.cwd, "path": t.c.transcript, "ephemeral": false, "source": "cli", "status": status}
}

func (s *codexAppServer) status(t *codexServerThread) {
	s.broadcast("thread/status/changed", map[string]any{"threadId": t.c.conversation, "status": s.metadata(t)["status"]})
}

func (s *codexAppServer) turn(p codexServerParams) (any, func(), error) {
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
	t.active, t.approval, t.approvalID = false, nil, ""
	s.mu.Unlock()
	turn := map[string]any{"id": t.c.turnID, "status": "completed"}
	return map[string]any{}, func() {
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

// answered settles a pending approval: whichever connection answers, every subscriber hears it resolved.
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
