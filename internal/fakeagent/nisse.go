package fakeagent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/nisse"
)

const (
	Nisse                = Harness("nisse")
	roleNisseServer      = "nisse-server"
	nisseHomeDir         = "nh"
	nisseIdleGrace       = 300 * time.Millisecond
	methodNisseFakeReply = "attn-fake/reply"
	methodAwaitInterrupt = "await_interrupt"
	methodFailRun        = "fail_run"
	methodNisseStarted   = "started"
)

var nisseComposer = composer{prompt: "PROMPT "}

var nisseFlags = flagSpec{values: map[string]bool{"--conversation": true, "--provider": true, "--model": true, "--effort": true, "--instructions": true}}

func runNisse(cfg config) int {
	if len(os.Args) > 1 && os.Args[1] == "serve" {
		return runNisseServer(cfg)
	}
	return serve(cfg, nisseComposer, &nisseTUI{args: nisseFlags.parse(os.Args[1:])})
}

type nisseTUI struct {
	args         parsedArgs
	client       *nisse.Client
	conversation string
}

func (n *nisseTUI) launch() launch {
	return launch{Harness: Nisse, ConversationID: n.conversation, Yolo: n.args.has("--auto")}
}

func (n *nisseTUI) initialPrompt() string { return strings.Join(n.args.afterDashes, " ") }

func (n *nisseTUI) begin(*terminal) error {
	n.conversation = n.args.value("--conversation")
	if n.conversation == "" {
		n.conversation = uuid.NewString()
	}
	socket := nisse.Socket(os.Getenv("NISSE_HOME"))
	deadline := time.Now().Add(HangGuard)
	for {
		client, err := nisse.Connect(context.Background(), socket, "nisse-tui", "fake")
		if err == nil {
			n.client = client
			break
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("no Nisse server answered at %s: %w", socket, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	start := map[string]any{"id": n.conversation, "cwd": cwd}
	if n.args.has("--auto") {
		start["approvals"] = "auto"
	}
	for flag, key := range map[string]string{"--provider": "provider", "--model": "model", "--effort": "effort", "--instructions": "instructions"} {
		if value := n.args.value(flag); value != "" {
			start[key] = value
		}
	}
	return n.call("conversation/start", start)
}

func (n *nisseTUI) submit(prompt string) error {
	return n.call("conversation/submit", map[string]any{"conversation": n.conversation, "requestId": uuid.NewString(), "author": "you", "text": prompt})
}

func (n *nisseTUI) reply(text string, _ bool) error {
	return n.call(methodNisseFakeReply, map[string]any{"conversation": n.conversation, "text": text})
}

func (n *nisseTUI) call(method string, params any) error {
	ctx, cancel := context.WithTimeout(context.Background(), HangGuard)
	defer cancel()
	return n.client.Call(ctx, method, params, nil)
}

type nisseFrame struct {
	ID     *uint64         `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

type nisseSubmission struct {
	Conversation string `json:"conversation"`
	RequestID    string `json:"requestId"`
	Author       string `json:"author"`
	Text         string `json:"text"`
}

type NisseProviderModels struct {
	Provider string       `json:"provider"`
	Models   []NisseModel `json:"models"`
}

type NisseModel struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Efforts []string `json:"efforts"`
}

var NisseModels = []NisseProviderModels{
	{Provider: "claude", Models: []NisseModel{{ID: "opus", Name: "Opus", Efforts: []string{"low", "medium", "high"}}}},
	{Provider: "openai", Models: []NisseModel{{ID: "gpt-5.5", Name: "GPT-5.5", Efforts: []string{"low", "high"}}, {ID: "gpt-image", Name: "Image", Efforts: []string{}}}},
}

type NisseConversation struct {
	ID           string         `json:"id"`
	Cwd          string         `json:"cwd"`
	Provider     string         `json:"provider,omitempty"`
	Model        string         `json:"model,omitempty"`
	Effort       string         `json:"effort,omitempty"`
	Approvals    string         `json:"approvals,omitempty"`
	Instructions string         `json:"instructions,omitempty"`
	Ended        map[string]any `json:"ended,omitempty"`
	status       string
	requests     map[string]bool
}

type nisseServerConn struct {
	conn    net.Conn
	writeMu sync.Mutex
	watch   *uint64
}

type nisseServer struct {
	control     *rpcPeer
	listener    net.Listener
	mu          sync.Mutex
	seq         int
	conns       map[*nisseServerConn]bool
	convs       map[string]*NisseConversation
	submissions map[string]chan nisseSubmission
	interrupts  map[string]chan struct{}
	idle        *time.Timer
}

func runNisseServer(cfg config) int {
	s := &nisseServer{conns: map[*nisseServerConn]bool{}, convs: map[string]*NisseConversation{}, submissions: map[string]chan nisseSubmission{}, interrupts: map[string]chan struct{}{}}
	control, err := dialControl(cfg, s.handleKit)
	if err != nil {
		fmt.Fprintf(os.Stderr, "fake nisse serve: %v\n", err)
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
	home := os.Getenv("NISSE_HOME")
	if err := os.MkdirAll(home, 0o700); err != nil {
		fmt.Fprintf(os.Stderr, "fake nisse serve: %v\n", err)
		return 1
	}
	socket := nisse.Socket(home)
	if conn, err := net.Dial("unix", socket); err == nil {
		_ = conn.Close()
		fmt.Fprintf(os.Stderr, "another server already serves %s\n", home)
		return 1
	}
	_ = os.Remove(socket)
	listener, err := net.Listen("unix", socket)
	if err != nil {
		fmt.Fprintf(os.Stderr, "fake nisse serve: %v\n", err)
		return 1
	}
	s.listener = listener
	report := launch{Role: roleNisseServer, Harness: Nisse, Pid: os.Getpid(), Argv: os.Args, Env: os.Environ()}
	if err := control.call(context.Background(), methodLaunched, report, nil); err != nil {
		return 1
	}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGHUP, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-signals
		s.stop()
	}()
	s.idleSoon()
	for {
		conn, err := listener.Accept()
		if err != nil {
			s.control.answering.Wait()
			return 0
		}
		c := &nisseServerConn{conn: conn}
		s.mu.Lock()
		s.conns[c] = true
		if s.idle != nil {
			s.idle.Stop()
		}
		s.mu.Unlock()
		go s.serveConn(c)
	}
}

func (s *nisseServer) stop() {
	_ = s.listener.Close()
	_ = os.Remove(s.listener.Addr().String())
}

func (s *nisseServer) idleSoon() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.conns) == 0 {
		s.idle = time.AfterFunc(nisseIdleGrace, s.stop)
	}
}

func (s *nisseServer) serveConn(c *nisseServerConn) {
	lines := bufio.NewScanner(c.conn)
	lines.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for lines.Scan() {
		var f nisseFrame
		if json.Unmarshal(lines.Bytes(), &f) != nil || f.ID == nil {
			continue
		}
		result, failure := s.handle(c, *f.ID, f.Method, f.Params)
		if f.Method == "status/watch" && failure == "" {
			continue
		}
		reply := map[string]any{"jsonrpc": "2.0", "id": *f.ID, "result": result}
		if failure != "" {
			reply = map[string]any{"jsonrpc": "2.0", "id": *f.ID, "error": map[string]any{
				"_tag": "Cause", "code": 0, "message": failure,
				"data": []any{map[string]any{"_tag": "Fail", "error": map[string]any{"_tag": failure}}},
			}}
		}
		c.send(reply)
	}
	_ = c.conn.Close()
	s.mu.Lock()
	delete(s.conns, c)
	s.mu.Unlock()
	s.idleSoon()
}

func (c *nisseServerConn) send(v any) {
	line, err := json.Marshal(v)
	if err != nil {
		return
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_, _ = c.conn.Write(append(line, '\n'))
}

func (s *nisseServer) handle(c *nisseServerConn, id uint64, method string, raw json.RawMessage) (any, string) {
	var p struct {
		NisseConversation
		Conversation string `json:"conversation"`
		Text         string `json:"text"`
		RequestID    string `json:"requestId"`
		Author       string `json:"author"`
	}
	_ = json.Unmarshal(raw, &p)
	switch method {
	case "initialize":
		return map[string]any{"version": "fake", "protocol": nisse.Protocol}, ""
	case "models/list":
		return NisseModels, ""
	case "conversation/start":
		s.mu.Lock()
		defer s.mu.Unlock()
		if p.ID == "" {
			p.ID = uuid.NewString()
		}
		conv := s.convs[p.ID]
		if conv == nil {
			conv = &p.NisseConversation
			conv.status, conv.requests = "idle", map[string]bool{}
			s.convs[p.ID] = conv
		}
		return conv, ""
	case "conversation/list":
		s.mu.Lock()
		defer s.mu.Unlock()
		convs := make([]*NisseConversation, 0, len(s.convs))
		for _, conv := range s.convs {
			convs = append(convs, conv)
		}
		return convs, ""
	case "conversation/submit":
		s.mu.Lock()
		conv := s.convs[p.Conversation]
		if conv == nil {
			s.mu.Unlock()
			return nil, "NoSuchConversation"
		}
		fresh := !conv.requests[p.RequestID]
		conv.requests[p.RequestID] = true
		if fresh && conv.status == "idle" {
			s.statusLocked(conv, "working", nil)
		}
		s.mu.Unlock()
		if fresh {
			s.submissionsOf(p.Conversation) <- nisseSubmission{Conversation: p.Conversation, RequestID: p.RequestID, Author: p.Author, Text: p.Text}
		}
		return map[string]any{"submission": p.RequestID}, ""
	case "conversation/interrupt":
		s.mu.Lock()
		conv := s.convs[p.Conversation]
		if conv == nil {
			s.mu.Unlock()
			return nil, "NoSuchConversation"
		}
		if conv.status != "idle" {
			s.statusLocked(conv, "idle", map[string]any{"outcome": "interrupted"})
		}
		s.mu.Unlock()
		s.interruptsOf(p.Conversation) <- struct{}{}
		return nil, ""
	case methodNisseFakeReply:
		return nil, s.end(p.Conversation, map[string]any{"outcome": "answered", "said": p.Text})
	case "status/watch":
		s.mu.Lock()
		defer s.mu.Unlock()
		c.watch = &id
		trees := []any{}
		for _, conv := range s.convs {
			if conv.status != "idle" {
				trees = append(trees, map[string]any{"_tag": "tree", "conversation": conv.ID, "seq": s.seq, "status": conv.status})
			}
		}
		c.send(map[string]any{"jsonrpc": "2.0", "chunk": true, "id": id, "result": []any{map[string]any{"_tag": "trees", "seq": s.seq, "trees": trees}}})
		return nil, ""
	}
	return nil, "UnknownMethod"
}

func (s *nisseServer) end(conversation string, ended map[string]any) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	conv := s.convs[conversation]
	if conv == nil {
		return "NoSuchConversation"
	}
	s.statusLocked(conv, "idle", ended)
	return ""
}

func (s *nisseServer) statusLocked(conv *NisseConversation, status string, ended map[string]any) {
	conv.status = status
	if ended != nil {
		conv.Ended = ended
	}
	s.seq++
	item := map[string]any{"_tag": "tree", "conversation": conv.ID, "seq": s.seq, "status": status}
	if ended != nil {
		item["ended"] = ended
	}
	for c := range s.conns {
		if c.watch != nil {
			c.send(map[string]any{"jsonrpc": "2.0", "chunk": true, "id": *c.watch, "result": []any{item}})
		}
	}
}

func (s *nisseServer) submissionsOf(conversation string) chan nisseSubmission {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.submissions[conversation] == nil {
		s.submissions[conversation] = make(chan nisseSubmission, 16)
	}
	return s.submissions[conversation]
}

func (s *nisseServer) interruptsOf(conversation string) chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.interrupts[conversation] == nil {
		s.interrupts[conversation] = make(chan struct{}, 16)
	}
	return s.interrupts[conversation]
}

func (s *nisseServer) handleKit(_ *rpcPeer, method string, raw json.RawMessage) (any, error) {
	var p serverThreadParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, err
	}
	switch method {
	case methodPrompted:
		select {
		case submitted := <-s.submissionsOf(p.ThreadID):
			return submitted, nil
		case <-time.After(HangGuard):
			return nil, fmt.Errorf("conversation %s took no prompt within %s", p.ThreadID, HangGuard)
		}
	case methodReply:
		return struct{}{}, failure(s.end(p.ThreadID, map[string]any{"outcome": "answered", "said": p.Text}))
	case methodFailRun:
		return struct{}{}, failure(s.end(p.ThreadID, map[string]any{"outcome": "failed", "message": p.Text}))
	case methodAskApproval, methodWork:
		status := map[string]string{methodAskApproval: "waiting", methodWork: "working"}[method]
		s.mu.Lock()
		defer s.mu.Unlock()
		conv := s.convs[p.ThreadID]
		if conv == nil {
			return nil, fmt.Errorf("no conversation %s", p.ThreadID)
		}
		s.statusLocked(conv, status, nil)
		return struct{}{}, nil
	case methodAwaitInterrupt:
		select {
		case <-s.interruptsOf(p.ThreadID):
			return struct{}{}, nil
		case <-time.After(HangGuard):
			return nil, fmt.Errorf("conversation %s was not interrupted within %s", p.ThreadID, HangGuard)
		}
	case methodRunTool:
		cmd := exec.Command("/bin/sh", "-c", p.Text)
		cmd.Env = append(os.Environ(), "NISSE_ROOT_CONVERSATION="+p.ThreadID)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return nil, fmt.Errorf("%v: %s", err, out)
		}
		return promptedResult{Text: string(out), ConversationID: p.ThreadID}, nil
	case methodNisseStarted:
		s.mu.Lock()
		defer s.mu.Unlock()
		conv := s.convs[p.ThreadID]
		if conv == nil {
			return nil, fmt.Errorf("no conversation %s", p.ThreadID)
		}
		return conv, nil
	}
	return nil, fmt.Errorf("unknown method %q", method)
}

func failure(tag string) error {
	if tag == "" {
		return nil
	}
	return errors.New(tag)
}

type NisseServer struct {
	t    testing.TB
	fake *fake
}

type NisseSubmission = nisseSubmission

func (k *Kit) NisseServer() *NisseServer {
	k.t.Helper()
	deadline := time.After(HangGuard)
	for {
		k.mu.Lock()
		var found *fake
		for _, f := range k.fakes {
			if f.Role == roleNisseServer {
				found = f
			}
		}
		k.mu.Unlock()
		if found != nil {
			return &NisseServer{t: k.t, fake: found}
		}
		select {
		case <-deadline:
			k.t.Fatalf("no Nisse server was launched within %s", HangGuard)
			return nil
		case <-time.After(20 * time.Millisecond):
		}
	}
}

func (k *Kit) NisseServers() int {
	k.mu.Lock()
	defer k.mu.Unlock()
	count := 0
	for _, f := range k.fakes {
		if f.Role == roleNisseServer {
			count++
		}
	}
	return count
}

func (s *NisseServer) Prompted(conversation string) NisseSubmission {
	s.t.Helper()
	var submitted NisseSubmission
	s.call(methodPrompted, serverThreadParams{ThreadID: conversation}, &submitted)
	return submitted
}

func (s *NisseServer) Reply(conversation, text string) {
	s.t.Helper()
	s.call(methodReply, serverThreadParams{ThreadID: conversation, Text: text}, nil)
}

func (s *NisseServer) Fail(conversation, message string) {
	s.t.Helper()
	s.call(methodFailRun, serverThreadParams{ThreadID: conversation, Text: message}, nil)
}

func (s *NisseServer) AskApproval(conversation string) {
	s.t.Helper()
	s.call(methodAskApproval, serverThreadParams{ThreadID: conversation}, nil)
}

func (s *NisseServer) Work(conversation string) {
	s.t.Helper()
	s.call(methodWork, serverThreadParams{ThreadID: conversation}, nil)
}

func (s *NisseServer) AwaitInterrupted(conversation string) {
	s.t.Helper()
	s.call(methodAwaitInterrupt, serverThreadParams{ThreadID: conversation}, nil)
}

func (s *NisseServer) Started(conversation string) NisseConversation {
	s.t.Helper()
	var started NisseConversation
	s.call(methodNisseStarted, serverThreadParams{ThreadID: conversation}, &started)
	return started
}

func (s *NisseServer) RunTool(conversation, command string) string {
	s.t.Helper()
	var ran promptedResult
	s.call(methodRunTool, serverThreadParams{ThreadID: conversation, Text: command}, &ran)
	return ran.Text
}

func (s *NisseServer) Env() []string { return s.fake.Env }

func (s *NisseServer) AwaitStopped() {
	s.t.Helper()
	select {
	case <-s.fake.peer.done:
	case <-time.After(HangGuard):
		s.t.Fatalf("Nisse server (pid %d) still runs %s after nothing needs it", s.fake.Pid, HangGuard)
	}
}

func (s *NisseServer) call(method string, params, result any) {
	s.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), HangGuard)
	defer cancel()
	if err := s.fake.peer.call(ctx, method, params, result); err != nil {
		s.t.Fatalf("Nisse server %s: %v", method, err)
	}
}

func nisseHome(dir string) string { return filepath.Join(dir, nisseHomeDir) }
