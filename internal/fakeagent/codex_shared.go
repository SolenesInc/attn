package fakeagent

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/victorarias/attn/internal/codexshared"
	"nhooyr.io/websocket"
)

type sharedFakeCodex struct {
	cfg          config
	mu           sync.Mutex
	roots        map[string]*sharedFakeRoot
	peers        map[*websocket.Conn]bool
	archiveError bool
}
type sharedFakeRoot struct {
	c        *codex
	a        *agent
	owner    string
	archived bool
	hasTurn  atomic.Bool
	active   atomic.Bool
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
	defer func() { s.mu.Lock(); delete(s.peers, conn); s.mu.Unlock() }()
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
		result, err := s.handle(m)
		reply := codexshared.Message{ID: m.ID}
		if err != nil {
			reply.Error, _ = json.Marshal(map[string]any{"code": -32603, "message": err.Error()})
		} else {
			reply.Result, _ = json.Marshal(result)
		}
		if len(m.ID) > 0 {
			data, _ := json.Marshal(reply)
			if conn.Write(context.Background(), websocket.MessageText, data) != nil {
				return
			}
		}
	}
}

func (s *sharedFakeCodex) handle(m codexshared.Message) (any, error) {
	var p struct {
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
		s.archiveError = p.ArchiveError
		s.mu.Unlock()
		return map[string]any{}, nil
	case "initialize":
		return map[string]any{}, nil
	case "thread/start", "thread/fork":
		if p.CWD == "" {
			p.CWD, _ = os.Getwd()
		}
		c := &codex{cfg: s.cfg, cwd: p.CWD, term: &terminal{style: codexComposer}}
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
		root := &sharedFakeRoot{c: c, owner: cfg.ShellEnvironmentPolicy.Set["ATTN_SESSION_ID"]}
		if root.owner == "" {
			return nil, fmt.Errorf("shared root missing owner config")
		}
		a := &agent{term: c.term, conv: c, prompts: make(chan promptSubmission, 16)}
		root.a = a
		control, err := dialControl(s.cfg, func(peer *rpcPeer, method string, params json.RawMessage) (any, error) {
			result, err := a.handle(peer, method, params)
			if method == methodReply || method == methodReplyLate {
				var text textParams
				_ = json.Unmarshal(params, &text)
				s.broadcast("attn-fixture/reply", map[string]any{"threadId": c.conversation, "text": text.Text})
				root.active.Store(false)
				s.broadcast("turn/completed", map[string]any{"threadId": c.conversation, "turn": map[string]any{"id": c.turnID}})
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
		return map[string]any{"thread": s.metadata(root)}, nil
	case "thread/resume", "thread/read", "thread/unarchive", "thread/archive":
		s.mu.Lock()
		root := s.roots[p.ThreadID]
		s.mu.Unlock()
		if root == nil {
			return nil, fmt.Errorf("no rollout found for %s", p.ThreadID)
		}
		if m.Method == "thread/resume" && os.Getenv("ATTN_FAKE_CODEX_REJECT_BLANK_ATTACH") == "1" && !root.hasTurn.Load() {
			return nil, fmt.Errorf("cannot resume a blank thread %s", p.ThreadID)
		}
		if m.Method == "thread/archive" {
			s.mu.Lock()
			if s.archiveError {
				s.mu.Unlock()
				return nil, fmt.Errorf("fixture archive unavailable for %s", p.ThreadID)
			}
			root.archived = true
			s.mu.Unlock()
			_ = root.c.halt()
			s.broadcast("thread/archived", map[string]any{"threadId": p.ThreadID})
			return map[string]any{}, nil
		}
		if m.Method == "thread/unarchive" {
			root.archived = false
			return map[string]any{}, nil
		}
		return map[string]any{"thread": s.metadata(root)}, nil
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
		if m.Method == "turn/start" {
			if root.active.Load() {
				return nil, fmt.Errorf("turn already active")
			}
			root.a.submit(strings.Join(texts, "\n"))
			root.hasTurn.Store(true)
			root.active.Store(true)
		} else {
			root.a.turn.Lock()
			if !root.active.Load() || p.ExpectedTurnID != root.c.turnID {
				root.a.turn.Unlock()
				return nil, fmt.Errorf("steer expected an active matching turn")
			}
			text := strings.Join(texts, "\n")
			err := appendLines(root.c.transcript, codexEvent("user_message", text))
			root.a.turn.Unlock()
			if err != nil {
				return nil, err
			}
			root.a.prompts <- promptSubmission{text: text, conversation: root.c.conversation}
		}
		s.broadcast("turn/started", map[string]any{"threadId": p.ThreadID, "turn": map[string]any{"id": root.c.turnID}})
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
	return map[string]any{"id": root.c.conversation, "cwd": root.c.cwd, "path": root.c.transcript, "source": "cli", "ephemeral": false, "turns": turns}
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
	client, err := codexshared.Connect(context.Background(), strings.TrimPrefix(args.value("--remote"), "unix://"), func(m codexshared.Message) {
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
	if err != nil {
		return 1
	}
	submit := func(text string) {
		switch {
		case text == "/loaded":
			result, err := client.Call(context.Background(), "thread/loaded/list", map[string]any{})
			if err != nil {
				term.print(err.Error())
			} else {
				term.print("Loaded " + string(result))
			}
		case strings.HasPrefix(text, "/archive-error "):
			_, err := client.Call(context.Background(), "attn-fixture/archive-error", map[string]any{"archiveError": strings.TrimPrefix(text, "/archive-error ") == "on"})
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
