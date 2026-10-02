package fakeagent

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/victorarias/attn/internal/codexshared"
	"nhooyr.io/websocket"
)

type sharedFakeCodex struct {
	cfg                config
	mu                 sync.Mutex
	roots              map[string]*sharedFakeRoot
	peers              map[*websocket.Conn]bool
	control            *websocket.Conn
	archiveError       bool
	archiveErrors      map[string]bool
	rejectedStart      atomic.Bool
	rejectedInput      atomic.Bool
	controlUnavailable atomic.Bool
}
type sharedFakeRoot struct {
	c               *codex
	a               *agent
	owner           string
	archived        bool
	hasTurn         atomic.Bool
	active          atomic.Bool
	approval        atomic.Bool
	failed          atomic.Bool
	snapshotOnly    atomic.Bool
	archiveUsage    string
	name            string
	nameError       bool
	nameOnResume    string
	holdNameReplies bool
	nameHeld        chan struct{}
	nameReplies     []heldCodexNameReply
	livePath        string
}

type heldCodexNameReply struct {
	conn *websocket.Conn
	data []byte
}

func runSharedCodexServer(cfg config) int {
	args := flagSpec{values: map[string]bool{"--listen": true}}.parse(os.Args[2:])
	path := strings.TrimPrefix(args.value("--listen"), "unix://")
	listener, err := net.Listen("unix", path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	s := &sharedFakeCodex{cfg: cfg, roots: make(map[string]*sharedFakeRoot), peers: make(map[*websocket.Conn]bool)}
	server := &http.Server{Handler: http.HandlerFunc(s.serve)}
	returnCode := 0
	if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
		fmt.Fprintln(os.Stderr, err)
		returnCode = 1
	}
	return returnCode
}

func (s *sharedFakeCodex) serve(w http.ResponseWriter, req *http.Request) {
	conn, err := websocket.Accept(w, req, nil)
	if err != nil {
		return
	}
	conn.SetReadLimit(-1)
	defer conn.CloseNow()
	s.mu.Lock()
	s.peers[conn] = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.peers, conn)
		if s.control == conn {
			s.control = nil
		}
		s.mu.Unlock()
	}()
	for {
		_, raw, err := conn.Read(context.Background())
		if err != nil {
			return
		}
		var m codexshared.Message
		if json.Unmarshal(raw, &m) != nil {
			continue
		}
		if m.Method == "initialized" {
			continue
		}
		if m.Method == "" && len(m.Result) > 0 {
			var rootID string
			_ = json.Unmarshal(m.ID, &rootID)
			s.mu.Lock()
			root := s.roots[strings.TrimPrefix(rootID, "approval:")]
			s.mu.Unlock()
			if root != nil && root.approval.Swap(false) {
				s.broadcast("serverRequest/resolved", map[string]any{"threadId": root.c.conversation, "requestId": rootID})
				s.broadcastStatus(root)
				root.a.term.answers <- "accepted"
			}
			continue
		}
		result, err := s.handle(conn, m)
		if err == nil && (m.Method == "turn/start" || m.Method == "turn/steer") && os.Getenv("ATTN_FAKE_CODEX_DROP_TURN_REPLY") == "1" {
			continue
		}
		reply := codexshared.Message{ID: m.ID}
		if err != nil {
			reply.Error, _ = json.Marshal(map[string]any{"code": -32603, "message": err.Error()})
		} else {
			reply.Result, _ = json.Marshal(result)
		}
		if len(m.ID) > 0 {
			data, _ := json.Marshal(reply)
			if m.Method == "thread/name/set" && err == nil {
				var p struct {
					ThreadID string `json:"threadId"`
					Name     string `json:"name"`
				}
				_ = json.Unmarshal(m.Params, &p)
				s.mu.Lock()
				root := s.roots[p.ThreadID]
				if root != nil && (root.holdNameReplies || p.Name == "Held initial name") {
					root.nameReplies = append(root.nameReplies, heldCodexNameReply{conn, data})
					if len(root.nameReplies) == 1 {
						close(root.nameHeld)
					}
					s.mu.Unlock()
					continue
				}
				s.mu.Unlock()
			}
			if conn.Write(context.Background(), websocket.MessageText, data) != nil {
				return
			}
			if m.Method == "thread/resume" {
				var p struct {
					ThreadID string `json:"threadId"`
				}
				_ = json.Unmarshal(m.Params, &p)
				s.mu.Lock()
				root := s.roots[p.ThreadID]
				s.mu.Unlock()
				if root != nil && root.approval.Load() {
					_ = conn.Write(context.Background(), websocket.MessageText, s.approvalRequest(root))
				}
			}
		}
	}
}

func (s *sharedFakeCodex) handle(conn *websocket.Conn, m codexshared.Message) (any, error) {
	var p struct {
		Ephemeral      bool            `json:"ephemeral"`
		Name           string          `json:"name"`
		ThreadID       string          `json:"threadId"`
		ExpectedTurnID string          `json:"expectedTurnId"`
		ViewArgv       []string        `json:"fixture_view_argv"`
		ArchiveError   bool            `json:"archiveError"`
		CWD            string          `json:"cwd"`
		Config         json.RawMessage `json:"config"`
		Input          []struct {
			Text string `json:"text"`
		} `json:"input"`
	}
	_ = json.Unmarshal(m.Params, &p)
	switch m.Method {
	case "attn-fixture/archive-error":
		s.mu.Lock()
		if p.ThreadID == "" {
			s.archiveError = p.ArchiveError
		} else {
			if s.archiveErrors == nil {
				s.archiveErrors = make(map[string]bool)
			}
			s.archiveErrors[p.ThreadID] = p.ArchiveError
		}
		s.mu.Unlock()
		return map[string]any{}, nil
	case "initialize":
		s.mu.Lock()
		if s.controlUnavailable.Load() {
			s.mu.Unlock()
			return nil, fmt.Errorf("fixture native control unavailable")
		}
		if s.control == nil {
			s.control = conn
		}
		s.mu.Unlock()
		return map[string]any{}, nil
	case "thread/start", "thread/fork":
		if p.Ephemeral {
			return map[string]any{"thread": map[string]any{"id": "utility-thread", "ephemeral": true, "source": "cli"}}, nil
		}
		if m.Method == "thread/start" && os.Getenv("ATTN_FAKE_CODEX_REJECT_INITIAL_START_ONCE") == "1" && !s.rejectedStart.Swap(true) {
			return nil, fmt.Errorf("fixture initial creation rejected")
		}
		if p.CWD == "" {
			p.CWD, _ = os.Getwd()
		}
		c := &codex{cfg: s.cfg, cwd: p.CWD, term: &terminal{style: codexComposer, answers: make(chan string, 4)}}
		if err := c.startRollout(); err != nil {
			return nil, err
		}
		var cfg codexConfig
		if err := json.Unmarshal(p.Config, &cfg); err != nil {
			return nil, err
		}
		c.hooks = hookSet{groups: make(map[string][]hookGroup), env: os.Environ(), cwd: p.CWD}
		for event, raw := range cfg.Hooks {
			var groups []hookGroup
			if json.Unmarshal(raw, &groups) == nil {
				c.hooks.groups[event] = groups
			}
		}
		for key, value := range cfg.ShellEnvironmentPolicy.Set {
			c.hooks.env = withEnv(c.hooks.env, key, value)
		}
		root := &sharedFakeRoot{c: c, owner: cfg.ShellEnvironmentPolicy.Set["ATTN_SESSION_ID"], livePath: c.transcript, nameHeld: make(chan struct{})}
		if root.owner == "" {
			return nil, fmt.Errorf("shared root missing owner config")
		}
		a := &agent{term: c.term, conv: c, prompts: make(chan promptSubmission, 16)}
		root.a = a
		control, err := dialControl(s.cfg, func(peer *rpcPeer, method string, params json.RawMessage) (any, error) {
			if method == "hold_name_replies" {
				s.mu.Lock()
				root.holdNameReplies = true
				s.mu.Unlock()
				return struct{}{}, nil
			}
			if method == "name_reply_held" {
				s.mu.Lock()
				held := root.nameHeld
				s.mu.Unlock()
				<-held
				return struct{}{}, nil
			}
			if method == "release_name_replies" {
				s.mu.Lock()
				replies := root.nameReplies
				root.nameReplies = nil
				root.holdNameReplies = false
				root.nameHeld = make(chan struct{})
				s.mu.Unlock()
				for _, reply := range replies {
					if err := reply.conn.Write(context.Background(), websocket.MessageText, reply.data); err != nil {
						return nil, err
					}
				}
				return struct{}{}, nil
			}
			if method == "usage_on_archive" {
				var input textParams
				if err := json.Unmarshal(params, &input); err != nil {
					return nil, err
				}
				root.a.turn.Lock()
				root.archiveUsage = input.Text
				root.a.turn.Unlock()
				return struct{}{}, nil
			}
			if method == "name_on_resume" || method == "native_name" || method == "generate_name" || method == "name_error" || method == "read_name" {
				var input textParams
				_ = json.Unmarshal(params, &input)
				s.mu.Lock()
				if method == "name_on_resume" {
					root.nameOnResume = input.Text
				}
				if method == "name_error" {
					root.nameError = input.Text == "on"
				}
				if method == "native_name" || method == "generate_name" && root.name == "" {
					root.name = input.Text
				}
				name := root.name
				s.mu.Unlock()
				if method == "native_name" || method == "generate_name" {
					s.broadcast("thread/name/updated", map[string]any{"threadId": c.conversation, "threadName": name})
				}
				return map[string]any{"name": name}, nil
			}
			if method == "broadcast_usage" {
				s.broadcast("thread/tokenUsage/updated", map[string]any{"threadId": c.conversation, "turnId": c.turnID, "tokenUsage": map[string]any{"total": map[string]any{"totalTokens": 999}, "last": map[string]any{"totalTokens": 999}}})
				return struct{}{}, nil
			}
			if method == "native_snapshots_only" {
				root.snapshotOnly.Store(true)
				return struct{}{}, nil
			}
			if method == "control_available" {
				var input struct {
					Available bool `json:"available"`
				}
				if err := json.Unmarshal(params, &input); err != nil {
					return nil, err
				}
				s.controlUnavailable.Store(!input.Available)
				if input.Available {
					return struct{}{}, nil
				}
				method = "disconnect_control"
			}
			if method == "disconnect_control" {
				s.mu.Lock()
				conn := s.control
				s.control = nil
				s.mu.Unlock()
				if conn != nil {
					_ = conn.CloseNow()
				}
				return struct{}{}, nil
			}
			if method == "system_error" {
				root.failed.Store(true)
				s.broadcastStatus(root)
				return struct{}{}, nil
			}
			if method == "tool_shell" {
				var input textParams
				if err := json.Unmarshal(params, &input); err != nil {
					return nil, err
				}
				command := exec.Command("/bin/sh", "-c", input.Text)
				command.Env, command.Dir = c.hooks.env, c.cwd
				output, err := command.CombinedOutput()
				if err != nil {
					return nil, fmt.Errorf("tool shell: %w: %s", err, output)
				}
				return map[string]any{"stdout": string(output)}, nil
			}
			if method == methodAskApproval {
				root.approval.Store(true)
				s.broadcastStatus(root)
				s.mu.Lock()
				var peers []*websocket.Conn
				for peer := range s.peers {
					peers = append(peers, peer)
				}
				s.mu.Unlock()
				for _, peer := range peers {
					_ = peer.Write(context.Background(), websocket.MessageText, s.approvalRequest(root))
				}
				return struct{}{}, nil
			}
			result, err := a.handle(peer, method, params)
			if method == methodReply || method == methodReplyLate {
				var text textParams
				_ = json.Unmarshal(params, &text)
				s.broadcast("attn-fixture/reply", map[string]any{"threadId": c.conversation, "text": text.Text})
				root.active.Store(false)
				s.broadcast("turn/completed", map[string]any{"threadId": c.conversation, "turn": map[string]any{"id": c.turnID}})
				s.broadcastStatus(root)
			}
			return result, err
		})
		if err != nil {
			return nil, err
		}
		a.control = control
		control.start()
		s.mu.Lock()
		s.roots[c.conversation] = root
		s.mu.Unlock()
		var beforeBinding any
		if os.Getenv("ATTN_FAKE_CODEX_NAME_BEFORE_BIND") != "" {
			beforeBinding = s.metadata(root)
			root.name = os.Getenv("ATTN_FAKE_CODEX_NAME_BEFORE_BIND")
			s.broadcast("thread/name/updated", map[string]any{"threadId": c.conversation, "threadName": root.name})
		}
		if err := c.hooks.run("SessionStart", "startup", c.hookInput("SessionStart", map[string]any{"source": "startup"})); err != nil {
			return nil, err
		}
		report := c.launch()
		report.Role = roleAgent
		report.Pid = os.Getpid()
		report.Argv = os.Args
		if len(p.ViewArgv) > 0 {
			report.Argv = p.ViewArgv
		}
		report.Env = c.hooks.env
		report.AttnSessionID = root.owner
		if err := control.call(context.Background(), methodLaunched, report, nil); err != nil {
			return nil, err
		}
		s.broadcast("thread/started", map[string]any{"thread": s.metadata(root)})
		s.broadcastStatus(root)
		if beforeBinding != nil {
			return map[string]any{"thread": beforeBinding}, nil
		}
		return map[string]any{"thread": s.metadata(root)}, nil
	case "thread/resume", "thread/read", "thread/unarchive", "thread/archive":
		s.mu.Lock()
		root := s.roots[p.ThreadID]
		s.mu.Unlock()
		if root == nil {
			return nil, fmt.Errorf("no rollout found for %s", p.ThreadID)
		}
		s.mu.Lock()
		isControl := conn == s.control
		s.mu.Unlock()
		if m.Method == "thread/resume" && isControl && root.snapshotOnly.Load() {
			return nil, fmt.Errorf("fixture control resume rejected")
		}
		if m.Method == "thread/resume" && os.Getenv("ATTN_FAKE_CODEX_REJECT_BLANK_ATTACH") == "1" && !root.hasTurn.Load() {
			return nil, fmt.Errorf("cannot resume a blank thread %s", p.ThreadID)
		}
		if m.Method == "thread/archive" {
			s.mu.Lock()
			if s.archiveError || s.archiveErrors[p.ThreadID] {
				s.mu.Unlock()
				return nil, fmt.Errorf("fixture archive unavailable for %s", p.ThreadID)
			}
			root.archived = true
			s.mu.Unlock()
			root.a.turn.Lock()
			if root.archiveUsage != "" {
				if err := appendLines(root.c.transcript, root.c.usageLines(root.archiveUsage)...); err != nil {
					root.a.turn.Unlock()
					return nil, err
				}
				root.archiveUsage = ""
			}
			_ = root.c.halt()
			archivedPath := filepath.Join(filepath.Dir(root.c.sessionsDir()), "archived_sessions", filepath.Base(root.c.transcript))
			if err := os.MkdirAll(filepath.Dir(archivedPath), 0o755); err != nil {
				root.a.turn.Unlock()
				return nil, err
			}
			if root.c.transcript != archivedPath {
				if err := os.Rename(root.c.transcript, archivedPath); err != nil {
					root.a.turn.Unlock()
					return nil, err
				}
				root.c.transcript = archivedPath
			}
			root.a.turn.Unlock()
			root.active.Store(false)
			s.broadcast("thread/archived", map[string]any{"threadId": p.ThreadID})
			return map[string]any{}, nil
		}
		if m.Method == "thread/unarchive" {
			root.a.turn.Lock()
			if root.c.transcript != root.livePath {
				if err := os.Rename(root.c.transcript, root.livePath); err != nil {
					root.a.turn.Unlock()
					return nil, err
				}
				root.c.transcript = root.livePath
			}
			root.a.turn.Unlock()
			root.archived = false
			return map[string]any{}, nil
		}
		if m.Method == "thread/resume" && len(p.ViewArgv) > 0 && os.Getenv("ATTN_FAKE_CODEX_REPORT_VIEW_LAUNCH") == "1" {
			report := root.c.launch()
			report.Role, report.AttnSessionID, report.Argv = roleAgent, root.owner, p.ViewArgv
			if err := root.a.control.call(context.Background(), methodLaunched, report, nil); err != nil {
				return nil, err
			}
		}
		snapshot := s.metadata(root)
		s.mu.Lock()
		name := root.nameOnResume
		if m.Method == "thread/resume" {
			root.nameOnResume = ""
		}
		if m.Method == "thread/resume" && name != "" {
			root.name = name
		}
		s.mu.Unlock()
		if m.Method == "thread/resume" && name != "" {
			s.broadcast("thread/name/updated", map[string]any{"threadId": p.ThreadID, "threadName": name})
		}
		return map[string]any{"thread": snapshot}, nil
	case "thread/name/set":
		s.mu.Lock()
		root := s.roots[p.ThreadID]
		if root == nil || root.nameError || p.Name == "fixture rejected name" {
			s.mu.Unlock()
			return nil, fmt.Errorf("fixture name write rejected for %s", p.ThreadID)
		}
		root.name = p.Name
		s.mu.Unlock()
		if os.Getenv("ATTN_FAKE_CODEX_DROP_NAME_EVENTS") != "1" {
			s.broadcast("thread/name/updated", map[string]any{"threadId": p.ThreadID, "threadName": p.Name})
		}
		return map[string]any{}, nil
	case "thread/loaded/list":
		s.mu.Lock()
		var ids []string
		for id, root := range s.roots {
			if !root.archived {
				ids = append(ids, id)
			}
		}
		s.mu.Unlock()
		return map[string]any{"data": ids}, nil
	case "turn/start", "turn/steer":
		if os.Getenv("ATTN_FAKE_CODEX_REJECT_INPUT_ONCE") == "1" && !s.rejectedInput.Swap(true) {
			return nil, fmt.Errorf("fixture input rejected")
		}
		if _, err := os.Getwd(); err != nil {
			return nil, fmt.Errorf("invalid cwd: %w", err)
		}
		s.mu.Lock()
		root := s.roots[p.ThreadID]
		s.mu.Unlock()
		if root == nil {
			return nil, fmt.Errorf("unknown root %s", p.ThreadID)
		}
		var texts []string
		for _, input := range p.Input {
			texts = append(texts, input.Text)
		}
		text := strings.Join(texts, "\n")
		if m.Method == "turn/start" {
			if root.active.Load() {
				return nil, fmt.Errorf("turn already active")
			}
			root.a.turn.Lock()
			err := root.c.submit(text)
			root.a.turn.Unlock()
			if err != nil {
				return nil, err
			}
			root.hasTurn.Store(true)
			root.active.Store(true)
		} else {
			root.a.turn.Lock()
			if !root.active.Load() || p.ExpectedTurnID != root.c.turnID {
				root.a.turn.Unlock()
				return nil, fmt.Errorf("steer expected an active matching turn")
			}
			err := appendLines(root.c.transcript, codexEvent("user_message", text))
			root.a.turn.Unlock()
			if err != nil {
				return nil, err
			}
		}
		s.broadcast("turn/started", map[string]any{"threadId": p.ThreadID, "turn": map[string]any{"id": root.c.turnID}})
		s.broadcastStatus(root)
		// Prompted acknowledges native acceptance after its state has reached every peer.
		root.a.prompts <- promptSubmission{text: text, conversation: root.c.conversation}
		return map[string]any{"turn": map[string]any{"id": root.c.turnID}}, nil
	default:
		return map[string]any{}, nil
	}
}

func (s *sharedFakeCodex) metadata(root *sharedFakeRoot) any {
	root.a.turn.Lock()
	defer root.a.turn.Unlock()
	turns := []any{}
	if root.active.Load() {
		turns = append(turns, map[string]any{"id": root.c.turnID, "status": "inProgress"})
	}
	s.mu.Lock()
	name := root.name
	s.mu.Unlock()
	return map[string]any{"name": name, "id": root.c.conversation, "cwd": root.c.cwd, "path": root.c.transcript, "source": "cli", "ephemeral": false, "turns": turns, "status": s.status(root)}
}

func (s *sharedFakeCodex) status(root *sharedFakeRoot) any {
	if root.failed.Load() {
		return map[string]any{"type": "systemError"}
	}
	if root.active.Load() {
		flags := []string{}
		if root.approval.Load() {
			flags = append(flags, "waitingOnApproval")
		}
		return map[string]any{"type": "active", "activeFlags": flags}
	}
	return map[string]any{"type": "idle"}
}

func (s *sharedFakeCodex) approvalRequest(root *sharedFakeRoot) []byte {
	data, _ := json.Marshal(map[string]any{"id": "approval:" + root.c.conversation, "method": "item/commandExecution/requestApproval", "params": map[string]any{"threadId": root.c.conversation, "turnId": root.c.turnID, "itemId": "command-1"}})
	return data
}

func (s *sharedFakeCodex) broadcastStatus(root *sharedFakeRoot) {
	if root.snapshotOnly.Load() {
		return
	}
	s.broadcast("thread/status/changed", map[string]any{"threadId": root.c.conversation, "status": s.status(root)})
}
func (s *sharedFakeCodex) broadcast(method string, params any) {
	data, _ := json.Marshal(map[string]any{"method": method, "params": params})
	s.mu.Lock()
	var peers []*websocket.Conn
	for peer := range s.peers {
		peers = append(peers, peer)
	}
	s.mu.Unlock()
	for _, peer := range peers {
		_ = peer.Write(context.Background(), websocket.MessageText, data)
	}
}

func runSharedCodexView(cfg config) int {
	if os.Getenv("ATTN_FAKE_CODEX_VIEW_FAIL_BEFORE_INITIALIZE") == "1" {
		fmt.Fprintln(os.Stderr, "native view failed before initialization")
		return 1
	}
	args := codexFlags.parse(os.Args[1:])
	term, err := openTerminal(codexComposer)
	if err != nil {
		return 1
	}
	var current string
	var mu sync.Mutex
	approvals := make(map[string]json.RawMessage)
	var showApproval func(string)
	client, err := codexshared.Connect(context.Background(), strings.TrimPrefix(args.value("--remote"), "unix://"), func(m codexshared.Message) {
		if m.Method == "item/commandExecution/requestApproval" || m.Method == "serverRequest/resolved" {
			var p struct {
				ThreadID string `json:"threadId"`
			}
			_ = json.Unmarshal(m.Params, &p)
			mu.Lock()
			if m.Method == "serverRequest/resolved" {
				delete(approvals, p.ThreadID)
			} else {
				approvals[p.ThreadID] = m.ID
			}
			visible := current == p.ThreadID
			show := showApproval
			mu.Unlock()
			if visible && show != nil {
				show(p.ThreadID)
			}
			return
		}
		if m.Method != "attn-fixture/reply" {
			return
		}
		var p struct {
			ThreadID string `json:"threadId"`
			Text     string `json:"text"`
		}
		_ = json.Unmarshal(m.Params, &p)
		mu.Lock()
		visible := current == p.ThreadID
		mu.Unlock()
		if visible {
			term.print(p.Text)
		}
	})
	if err != nil {
		term.print(err.Error())
		return 1
	}
	defer client.Close()
	mu.Lock()
	showApproval = func(root string) {
		mu.Lock()
		id := approvals[root]
		mu.Unlock()
		term.mu.Lock()
		open := term.modal != nil
		term.mu.Unlock()
		if len(id) == 0 {
			if open {
				_, _ = term.closeModal()
			}
			return
		}
		if !open {
			_ = term.openModal(&modal{lines: []string{"Allow the command to run?", "Press enter to confirm"}, answering: true, resting: func() { _ = client.Respond(context.Background(), id, map[string]any{"decision": "accept"}) }})
		}
	}
	mu.Unlock()
	cwd := args.value("-C")
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	selectRoot := func(method, id string, display bool) error {
		params := map[string]any{"cwd": cwd, "fixture_view_argv": os.Args}
		if id != "" {
			params["threadId"] = id
		}
		result, err := client.Call(context.Background(), method, params)
		if err != nil {
			term.print(err.Error())
			return err
		}
		var p struct {
			Thread struct {
				ID string `json:"id"`
			} `json:"thread"`
		}
		_ = json.Unmarshal(result, &p)
		if display {
			mu.Lock()
			current = p.Thread.ID
			mu.Unlock()
			term.title(p.Thread.ID)
			term.print("Showing " + p.Thread.ID)
			showApproval(p.Thread.ID)
		} else {
			term.print("Background " + p.Thread.ID)
		}
		return nil
	}
	if len(args.positionals) > 1 && args.positionals[0] == "resume" {
		err = selectRoot("thread/resume", args.positionals[1], true)
	} else {
		err = selectRoot("thread/start", "", true)
	}
	if err != nil && os.Getenv("ATTN_FAKE_CODEX_REJECT_INITIAL_START_ONCE") != "1" {
		return 1
	}
	submit := func(text string) {
		switch {
		case strings.HasPrefix(text, "/rename "):
			mu.Lock()
			id := current
			mu.Unlock()
			_, err := client.Call(context.Background(), "thread/name/set", map[string]any{"threadId": id, "name": strings.TrimPrefix(text, "/rename ")})
			if err != nil {
				term.print(err.Error())
			}
		case text == "/utility":
			_, _ = client.Call(context.Background(), "thread/start", map[string]any{"ephemeral": true})
			term.print("Utility complete")
		case text == "/loaded":
			result, err := client.Call(context.Background(), "thread/loaded/list", map[string]any{})
			if err != nil {
				term.print(err.Error())
			} else {
				term.print("Loaded " + string(result))
			}
		case strings.HasPrefix(text, "/archive-error "):
			fields := strings.Fields(text)
			params := map[string]any{"archiveError": len(fields) > 1 && fields[1] == "on"}
			if len(fields) > 2 {
				params["threadId"] = fields[2]
			}
			_, err := client.Call(context.Background(), "attn-fixture/archive-error", params)
			if err != nil {
				term.print(err.Error())
			} else {
				term.print("Archive error " + strings.TrimPrefix(text, "/archive-error "))
			}
		case text == "/new":
			_ = selectRoot("thread/start", "", true)
		case text == "/fork":
			mu.Lock()
			id := current
			mu.Unlock()
			_ = selectRoot("thread/fork", id, true)
		case strings.HasPrefix(text, "/agents "):
			_ = selectRoot("thread/resume", strings.TrimPrefix(text, "/agents "), true)
		case strings.HasPrefix(text, "/background "):
			_ = selectRoot("thread/resume", strings.TrimPrefix(text, "/background "), false)
		case strings.HasPrefix(text, "/cached "):
			id := strings.TrimPrefix(text, "/cached ")
			mu.Lock()
			current = id
			mu.Unlock()
			term.title(id)
		case strings.HasPrefix(text, "/title "):
			term.title(strings.TrimPrefix(text, "/title "))
		default:
			mu.Lock()
			id := current
			mu.Unlock()
			_, err := client.Call(context.Background(), "turn/start", map[string]any{"threadId": id, "input": []any{map[string]any{"type": "text", "text": text}}})
			if err != nil {
				term.print(err.Error())
			}
		}
	}
	if len(args.afterDashes) > 0 {
		submit(strings.Join(args.afterDashes, " "))
	}
	term.readLines(submit)
	return 0
}
