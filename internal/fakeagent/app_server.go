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

	"github.com/victorarias/attn/internal/effectrpc"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/nisse"
	"github.com/victorarias/attn/internal/sigma"
)

const (
	Nisse                 = Harness("nisse")
	Sigma                 = Harness("sigma")
	serverIdleGrace       = 300 * time.Millisecond
	methodServerFakeReply = "attn-fake/reply"
	methodAwaitInterrupt  = "await_interrupt"
	methodFailRun         = "fail_run"
	methodServerStarted   = "started"
	methodLoseNextSubmit  = "lose_next_submit"
	methodSubmissions     = "submissions"
)

type appServerFlavor struct {
	harness      Harness
	role         string
	homeVariable string
	homeDir      string
	rootVariable string
	socket       func(home string) string
	connect      func(ctx context.Context, socket, client, version string) (*effectrpc.Client, error)
	hello        map[string]any
	completions  bool
}

var appServerFlavors = map[Harness]appServerFlavor{
	Nisse: {
		harness: Nisse, role: "nisse-server", homeVariable: "NISSE_HOME", homeDir: "nh", rootVariable: "NISSE_ROOT_CONVERSATION",
		socket: nisse.Socket, connect: nisse.Connect,
		hello: map[string]any{"version": "fake", "protocol": nisse.Protocol},
	},
	Sigma: {
		harness: Sigma, role: "sigma-server", homeVariable: "SIGMA_HOME", homeDir: "sg", rootVariable: "SIGMA_ROOT_CONVERSATION",
		socket: sigma.Socket, connect: sigma.Connect,
		hello:       map[string]any{"server": sigma.Server, "version": "fake", "protocol": sigma.Protocol},
		completions: true,
	},
}

var serverViewComposer = composer{prompt: "PROMPT "}

var serverViewFlags = flagSpec{values: map[string]bool{"--conversation": true, "--provider": true, "--model": true, "--effort": true, "--instructions": true}}

func appServerProgram(h Harness) func(config) int {
	flavor := appServerFlavors[h]
	return func(cfg config) int {
		if len(os.Args) > 1 && os.Args[1] == "serve" {
			return runAppServer(cfg, flavor)
		}
		return serve(cfg, serverViewComposer, &serverView{flavor: flavor, args: serverViewFlags.parse(os.Args[1:])})
	}
}

type serverView struct {
	flavor       appServerFlavor
	args         parsedArgs
	client       *effectrpc.Client
	conversation string
}

func (n *serverView) launch() launch {
	return launch{Harness: n.flavor.harness, ConversationID: n.conversation, Yolo: n.args.has("--auto")}
}

func (n *serverView) initialPrompt() string { return strings.Join(n.args.afterDashes, " ") }

func (n *serverView) begin(*terminal) error {
	n.conversation = n.args.value("--conversation")
	if n.conversation == "" {
		n.conversation = uuid.NewString()
	}
	socket := n.flavor.socket(os.Getenv(n.flavor.homeVariable))
	deadline := time.Now().Add(HangGuard)
	for {
		client, err := n.flavor.connect(context.Background(), socket, string(n.flavor.harness)+"-tui", "fake")
		if err == nil {
			n.client = client
			break
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("no %s server answered at %s: %w", n.flavor.harness, socket, err)
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

func (n *serverView) submit(prompt string) error {
	return n.call("conversation/submit", map[string]any{"conversation": n.conversation, "requestId": uuid.NewString(), "author": "you", "text": prompt})
}

func (n *serverView) reply(text string, _ bool) error {
	return n.call(methodServerFakeReply, map[string]any{"conversation": n.conversation, "text": text})
}

func (n *serverView) call(method string, params any) error {
	ctx, cancel := context.WithTimeout(context.Background(), HangGuard)
	defer cancel()
	return n.client.Call(ctx, method, params, nil)
}

type serverFrame struct {
	ID     *uint64         `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

type ServerSubmission struct {
	Conversation string `json:"conversation"`
	RequestID    string `json:"requestId"`
	Author       string `json:"author"`
	Text         string `json:"text"`
}

type ServerProviderModels struct {
	Provider string        `json:"provider"`
	Models   []ServerModel `json:"models"`
}

type ServerModel struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Efforts []string `json:"efforts"`
}

var ServerModels = []ServerProviderModels{
	{Provider: "claude", Models: []ServerModel{{ID: "opus", Name: "Opus", Efforts: []string{"low", "medium", "high"}}}},
	{Provider: "openai", Models: []ServerModel{{ID: "gpt-5.5", Name: "GPT-5.5", Efforts: []string{"low", "high"}}, {ID: "gpt-image", Name: "Image", Efforts: []string{}}}},
}

type ServerConversation struct {
	ID           string         `json:"id"`
	Cwd          string         `json:"cwd"`
	Provider     string         `json:"provider,omitempty"`
	Model        string         `json:"model,omitempty"`
	Effort       string         `json:"effort,omitempty"`
	Approvals    string         `json:"approvals,omitempty"`
	Instructions string         `json:"instructions,omitempty"`
	Ended        map[string]any `json:"ended,omitempty"`
	status       string
	requests     map[string]int
	submitted    int
	losesSubmit  bool
}

type appServerConn struct {
	conn    net.Conn
	writeMu sync.Mutex
	watch   *uint64
	client  string
}

type appServer struct {
	flavor      appServerFlavor
	control     *rpcPeer
	listener    net.Listener
	mu          sync.Mutex
	seq         int
	conns       map[*appServerConn]bool
	convs       map[string]*ServerConversation
	submissions map[string]chan ServerSubmission
	interrupts  map[string]chan struct{}
	idle        *time.Timer
	offline     chan struct{}
}

func runAppServer(cfg config, flavor appServerFlavor) int {
	s := &appServer{flavor: flavor, conns: map[*appServerConn]bool{}, convs: map[string]*ServerConversation{}, submissions: map[string]chan ServerSubmission{}, interrupts: map[string]chan struct{}{}}
	control, err := dialControl(cfg, s.handleKit)
	if err != nil {
		fmt.Fprintf(os.Stderr, "fake %s serve: %v\n", flavor.harness, err)
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
	home := os.Getenv(flavor.homeVariable)
	if err := os.MkdirAll(home, 0o700); err != nil {
		fmt.Fprintf(os.Stderr, "fake %s serve: %v\n", flavor.harness, err)
		return 1
	}
	socket := flavor.socket(home)
	if conn, err := net.Dial("unix", socket); err == nil {
		_ = conn.Close()
		fmt.Fprintf(os.Stderr, "another server already serves %s\n", home)
		return 1
	}
	_ = os.Remove(socket)
	listener, err := net.Listen("unix", socket)
	if err != nil {
		fmt.Fprintf(os.Stderr, "fake %s serve: %v\n", flavor.harness, err)
		return 1
	}
	s.listener = listener
	report := launch{Role: flavor.role, Harness: flavor.harness, Pid: os.Getpid(), Argv: os.Args, Env: os.Environ()}
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
		c := &appServerConn{conn: conn}
		s.mu.Lock()
		s.conns[c] = true
		if s.idle != nil {
			s.idle.Stop()
		}
		s.mu.Unlock()
		go s.serveConn(c)
	}
}

func (s *appServer) stop() {
	_ = s.listener.Close()
	_ = os.Remove(s.listener.Addr().String())
}

func (s *appServer) idleSoon() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.conns) == 0 {
		s.idle = time.AfterFunc(serverIdleGrace, s.stop)
	}
}

func (s *appServer) serveConn(c *appServerConn) {
	lines := bufio.NewScanner(c.conn)
	lines.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for lines.Scan() {
		var f serverFrame
		if json.Unmarshal(lines.Bytes(), &f) != nil || f.ID == nil {
			continue
		}
		result, failure := s.handle(c, *f.ID, f.Method, f.Params)
		if failure == replyLost || (f.Method == "status/watch" && failure == "") {
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

const replyLost = "\x00lost"

func (c *appServerConn) send(v any) {
	line, err := json.Marshal(v)
	if err != nil {
		return
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_, _ = c.conn.Write(append(line, '\n'))
}

func (s *appServer) handle(c *appServerConn, id uint64, method string, raw json.RawMessage) (any, string) {
	var p struct {
		ServerConversation
		Conversation string           `json:"conversation"`
		Text         string           `json:"text"`
		RequestID    string           `json:"requestId"`
		Author       string           `json:"author"`
		Client       string           `json:"client"`
		After        map[string]int64 `json:"after"`
	}
	_ = json.Unmarshal(raw, &p)
	switch method {
	case "initialize":
		s.mu.Lock()
		c.client = p.Client
		offline := s.offline
		s.mu.Unlock()
		if offline != nil && c.client == "attn" {
			<-offline
		}
		return s.flavor.hello, ""
	case "models/list":
		return ServerModels, ""
	case "conversation/start", "conversation/open":
		s.mu.Lock()
		defer s.mu.Unlock()
		if p.ID == "" {
			p.ID = uuid.NewString()
		}
		conv := s.convs[p.ID]
		if conv == nil {
			conv = &p.ServerConversation
			conv.status, conv.requests = "idle", map[string]int{}
			s.convs[p.ID] = conv
		}
		return conv, ""
	case "conversation/list":
		s.mu.Lock()
		defer s.mu.Unlock()
		convs := make([]*ServerConversation, 0, len(s.convs))
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
		submission, admitted := conv.requests[p.RequestID]
		fresh := !admitted
		lost := fresh && conv.losesSubmit
		if fresh {
			conv.submitted++
			submission = conv.submitted
			conv.requests[p.RequestID] = submission
		}
		if lost {
			conv.losesSubmit = false
			conv.status = "working"
			s.offline = make(chan struct{})
			for other := range s.conns {
				if other.client == "attn" {
					_ = other.conn.Close()
				}
			}
		} else if fresh && conv.status == "idle" {
			s.statusLocked(conv, "working", nil)
		}
		s.mu.Unlock()
		if fresh {
			s.submissionsOf(p.Conversation) <- ServerSubmission{Conversation: p.Conversation, RequestID: p.RequestID, Author: p.Author, Text: p.Text}
		}
		if lost {
			return nil, replyLost
		}
		return map[string]any{"submission": submission, "requestId": p.RequestID, "status": "placed"}, ""
	case "conversation/interrupt":
		s.mu.Lock()
		conv := s.convs[p.Conversation]
		if conv == nil {
			s.mu.Unlock()
			return nil, "NoSuchConversation"
		}
		if conv.status != "idle" {
			s.statusLocked(conv, "idle", s.endedLocked(conv, map[string]any{"outcome": "interrupted"}))
		}
		s.mu.Unlock()
		s.interruptsOf(p.Conversation) <- struct{}{}
		return nil, ""
	case methodServerFakeReply:
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
		items := []any{map[string]any{"_tag": "trees", "seq": s.seq, "trees": trees}}
		for conversation, applied := range p.After {
			if conv := s.convs[conversation]; s.flavor.completions && conv != nil && conv.status == "idle" && completionOf(conv.Ended) > applied {
				items = append(items, map[string]any{"_tag": "tree", "conversation": conversation, "status": "idle", "ended": conv.Ended})
			}
		}
		c.send(map[string]any{"jsonrpc": "2.0", "chunk": true, "id": id, "result": items})
		return nil, ""
	}
	return nil, "UnknownMethod"
}

func completionOf(ended map[string]any) int64 {
	completion, _ := ended["completion"].(int)
	return int64(completion)
}

func (s *appServer) endedLocked(conv *ServerConversation, ended map[string]any) map[string]any {
	if s.flavor.completions {
		ended["completion"] = conv.submitted
	}
	return ended
}

func (s *appServer) end(conversation string, ended map[string]any) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	conv := s.convs[conversation]
	if conv == nil {
		return "NoSuchConversation"
	}
	s.statusLocked(conv, "idle", s.endedLocked(conv, ended))
	if s.offline != nil {
		close(s.offline)
		s.offline = nil
	}
	return ""
}

func (s *appServer) statusLocked(conv *ServerConversation, status string, ended map[string]any) {
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

func (s *appServer) submissionsOf(conversation string) chan ServerSubmission {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.submissions[conversation] == nil {
		s.submissions[conversation] = make(chan ServerSubmission, 16)
	}
	return s.submissions[conversation]
}

func (s *appServer) interruptsOf(conversation string) chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.interrupts[conversation] == nil {
		s.interrupts[conversation] = make(chan struct{}, 16)
	}
	return s.interrupts[conversation]
}

func (s *appServer) handleKit(_ *rpcPeer, method string, raw json.RawMessage) (any, error) {
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
	case methodAskApproval, methodWork, methodLoseNextSubmit, methodSubmissions, methodServerStarted:
		s.mu.Lock()
		defer s.mu.Unlock()
		conv := s.convs[p.ThreadID]
		if conv == nil {
			return nil, fmt.Errorf("no conversation %s", p.ThreadID)
		}
		switch method {
		case methodLoseNextSubmit:
			conv.losesSubmit = true
		case methodSubmissions:
			return conv.submitted, nil
		case methodServerStarted:
			return conv, nil
		default:
			s.statusLocked(conv, map[string]string{methodAskApproval: "waiting", methodWork: "working"}[method], nil)
		}
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
		cmd.Env = append(os.Environ(), s.flavor.rootVariable+"="+p.ThreadID)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return nil, fmt.Errorf("%v: %s", err, out)
		}
		return promptedResult{Text: string(out), ConversationID: p.ThreadID}, nil
	}
	return nil, fmt.Errorf("unknown method %q", method)
}

func failure(tag string) error {
	if tag == "" {
		return nil
	}
	return errors.New(tag)
}

type AppServer struct {
	t      testing.TB
	flavor appServerFlavor
	fake   *fake
}

func (k *Kit) AppServer(h Harness) *AppServer {
	k.t.Helper()
	flavor := appServerFlavors[h]
	deadline := time.After(HangGuard)
	for {
		k.mu.Lock()
		var found *fake
		for _, f := range k.fakes {
			if f.Role == flavor.role {
				found = f
			}
		}
		k.mu.Unlock()
		if found != nil {
			return &AppServer{t: k.t, flavor: flavor, fake: found}
		}
		select {
		case <-deadline:
			k.t.Fatalf("no %s server was launched within %s", h, HangGuard)
			return nil
		case <-time.After(20 * time.Millisecond):
		}
	}
}

func (k *Kit) AppServers(h Harness) int {
	k.mu.Lock()
	defer k.mu.Unlock()
	count := 0
	for _, f := range k.fakes {
		if f.Role == appServerFlavors[h].role {
			count++
		}
	}
	return count
}

func (s *AppServer) Prompted(conversation string) ServerSubmission {
	s.t.Helper()
	var submitted ServerSubmission
	s.call(methodPrompted, serverThreadParams{ThreadID: conversation}, &submitted)
	return submitted
}

func (s *AppServer) Reply(conversation, text string) {
	s.t.Helper()
	s.call(methodReply, serverThreadParams{ThreadID: conversation, Text: text}, nil)
}

func (s *AppServer) Fail(conversation, message string) {
	s.t.Helper()
	s.call(methodFailRun, serverThreadParams{ThreadID: conversation, Text: message}, nil)
}

func (s *AppServer) AskApproval(conversation string) {
	s.t.Helper()
	s.call(methodAskApproval, serverThreadParams{ThreadID: conversation}, nil)
}

func (s *AppServer) Work(conversation string) {
	s.t.Helper()
	s.call(methodWork, serverThreadParams{ThreadID: conversation}, nil)
}

// LoseNextSubmit makes the server admit the conversation's next new input without replying or reporting it working:
// it drops attn's connections and answers attn's next initialize only once the run ends.
func (s *AppServer) LoseNextSubmit(conversation string) {
	s.t.Helper()
	s.call(methodLoseNextSubmit, serverThreadParams{ThreadID: conversation}, nil)
}

func (s *AppServer) Submissions(conversation string) int {
	s.t.Helper()
	var admitted int
	s.call(methodSubmissions, serverThreadParams{ThreadID: conversation}, &admitted)
	return admitted
}

func (s *AppServer) AwaitInterrupted(conversation string) {
	s.t.Helper()
	s.call(methodAwaitInterrupt, serverThreadParams{ThreadID: conversation}, nil)
}

func (s *AppServer) Started(conversation string) ServerConversation {
	s.t.Helper()
	var started ServerConversation
	s.call(methodServerStarted, serverThreadParams{ThreadID: conversation}, &started)
	return started
}

func (s *AppServer) RunTool(conversation, command string) string {
	s.t.Helper()
	var ran promptedResult
	s.call(methodRunTool, serverThreadParams{ThreadID: conversation, Text: command}, &ran)
	return ran.Text
}

func (s *AppServer) Env() []string { return s.fake.Env }

func (s *AppServer) AwaitStopped() {
	s.t.Helper()
	select {
	case <-s.fake.peer.done:
	case <-time.After(HangGuard):
		s.t.Fatalf("%s server (pid %d) still runs %s after nothing needs it", s.flavor.harness, s.fake.Pid, HangGuard)
	}
}

func (s *AppServer) call(method string, params, result any) {
	s.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), HangGuard)
	defer cancel()
	if err := s.fake.peer.call(ctx, method, params, result); err != nil {
		s.t.Fatalf("%s server %s: %v", s.flavor.harness, method, err)
	}
}

func appServerHome(dir string, h Harness) string {
	return filepath.Join(dir, appServerFlavors[h].homeDir)
}
