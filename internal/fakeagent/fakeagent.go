package fakeagent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"
)

type Harness string

const (
	Claude  Harness = "claude"
	Codex   Harness = "codex"
	Copilot Harness = "copilot"
	Pi      Harness = "pi"
)

const (
	configName       = "fakeagent.json"
	piPluginName     = "attn-pi"
	wrapperName      = "attn"
	tripwireExitCode = 97
	roleAgent        = "agent"
	rolePlugin       = "plugin"
	methodBooting    = "booting"
	methodLaunched   = "launched"
	methodUnexpected = "unexpected"
	methodExiting    = "exiting"
	methodPrompted   = "prompted"
	methodReply      = "reply"
	methodReplyLate  = "reply_after_stop"
	methodStream     = "stream"
	methodSubagent   = "subagent"
	methodDropSubs   = "delete_subagent_transcripts"
	methodHalt       = "halt"
	methodDeny       = "deny"
	methodExit       = "exit"
	signalExitBase   = 128
)

type config struct {
	Control   string    `json:"control"`
	Bin       string    `json:"bin"`
	ToolHome  string    `json:"tool_home"`
	CodexHome string    `json:"codex_home"`
	Harnesses []Harness `json:"harnesses"`
}

type launch struct {
	Role           string          `json:"role"`
	Harness        Harness         `json:"harness"`
	Pid            int             `json:"pid"`
	Argv           []string        `json:"argv"`
	Env            []string        `json:"env"`
	AttnSessionID  string          `json:"attn_session_id,omitempty"`
	ConversationID string          `json:"conversation_id,omitempty"`
	Resumed        bool            `json:"resumed,omitempty"`
	AutoMode       json.RawMessage `json:"auto_mode,omitempty"`
	Yolo           bool            `json:"yolo,omitempty"`
	Error          string          `json:"error,omitempty"`
}

type bootingParams struct {
	AttnSessionID string `json:"attn_session_id"`
}

type unexpectedLaunch struct {
	Argv   []string `json:"argv"`
	Reason string   `json:"reason"`
}

type textParams struct {
	Text string `json:"text"`
}

type promptedResult struct {
	Text           string `json:"text"`
	ConversationID string `json:"conversation_id"`
}

type exitParams struct {
	Code int `json:"code"`
}

var programs = map[string]func(config) int{
	string(Claude):  runClaude,
	string(Codex):   runCodex,
	string(Copilot): runCopilot,
	string(Pi):      runPiTerminal,
	piPluginName:    runPiPlugin,
	wrapperName:     runWrapperTripwire,
}

func Main() {
	name := filepath.Base(os.Args[0])
	program, ok := programs[name]
	if !ok {
		return
	}
	cfg, err := loadConfig(programDir())
	if err != nil {
		fmt.Fprintf(os.Stderr, "fake %s: %v\n", name, err)
		os.Exit(1)
	}
	harness := Harness(name)
	if name == piPluginName {
		harness = Pi
	}
	if name != wrapperName && !slices.Contains(cfg.Harnesses, harness) {
		os.Exit(tripwire(cfg, name, fmt.Sprintf("%s spawned in a world that did not install it", name)))
	}
	os.Exit(program(cfg))
}

func programDir() string {
	path := os.Args[0]
	if !strings.ContainsRune(path, filepath.Separator) {
		if resolved, err := exec.LookPath(path); err == nil {
			path = resolved
		}
	}
	return filepath.Dir(path)
}

func loadConfig(dir string) (config, error) {
	data, err := os.ReadFile(filepath.Join(dir, configName))
	if err != nil {
		return config{}, err
	}
	var cfg config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return config{}, fmt.Errorf("decode %s: %w", configName, err)
	}
	return cfg, nil
}

func runWrapperTripwire(cfg config) int {
	return tripwire(cfg, wrapperName, "agent spawned in a world that named no agents")
}

func tripwire(cfg config, program, reason string) int {
	fmt.Fprintf(os.Stderr, "fake %s: %s\n", program, reason)
	if control, err := dialControl(cfg, nil); err == nil {
		_ = control.start().call(context.Background(), methodUnexpected, unexpectedLaunch{Argv: os.Args, Reason: reason}, nil)
	}
	return tripwireExitCode
}

func dialControl(cfg config, handle rpcHandler) (*rpcPeer, error) {
	conn, err := net.Dial("unix", cfg.Control)
	if err != nil {
		return nil, fmt.Errorf("dial control socket: %w", err)
	}
	return newRPCPeer(conn, handle), nil
}

type conversation interface {
	launch() launch
	initialPrompt() string
	begin(term *terminal) error
	submit(prompt string) error
	reply(text string, afterStop bool) error
}

type halter interface {
	halt() error
}

type autoModeGuard interface {
	deny(denial Denial) error
}

type transcriptAuthor interface {
	stream(text string) error
	subagent(text string) error
	deleteSubagentTranscripts() error
}

type promptSubmission struct {
	text         string
	conversation string
	err          error
}

type agent struct {
	term    *terminal
	conv    conversation
	control *rpcPeer
	turn    sync.Mutex
	prompts chan promptSubmission
}

func serve(cfg config, style composer, conv conversation) int {
	term, err := openTerminal(style)
	if err != nil {
		fmt.Fprintf(os.Stderr, "fake agent: %v\n", err)
		return 1
	}
	a := &agent{term: term, conv: conv, prompts: make(chan promptSubmission, 16)}
	if a.control, err = dialControl(cfg, a.handle); err != nil {
		fmt.Fprintf(os.Stderr, "fake agent: %v\n", err)
		return 1
	}
	a.control.start()
	if err := a.control.call(context.Background(), methodBooting, bootingParams{AttnSessionID: os.Getenv("ATTN_SESSION_ID")}, nil); err != nil {
		return 1
	}
	began := conv.begin(term)
	report := conv.launch()
	report.Role = roleAgent
	report.Pid = os.Getpid()
	report.Argv = os.Args
	report.Env = os.Environ()
	report.AttnSessionID = os.Getenv("ATTN_SESSION_ID")
	if report.AttnSessionID == "" {
		began = errors.Join(began, errors.New("ATTN_SESSION_ID is not set"))
	}
	if began != nil {
		report.Error = began.Error()
	}
	if err := a.control.call(context.Background(), methodLaunched, report, nil); err != nil || began != nil {
		return 1
	}
	a.exitOnSignal()
	if prompt := conv.initialPrompt(); strings.TrimSpace(prompt) != "" {
		a.term.echo(prompt)
		a.submit(prompt)
	}
	go term.readLines(a.submit)
	<-a.control.done
	return 0
}

func (a *agent) submit(prompt string) {
	a.turn.Lock()
	err := a.conv.submit(prompt)
	conversation := a.conv.launch().ConversationID
	a.turn.Unlock()
	a.prompts <- promptSubmission{text: prompt, conversation: conversation, err: err}
}

func (a *agent) handle(_ *rpcPeer, method string, params json.RawMessage) (any, error) {
	switch method {
	case methodPrompted:
		submitted := <-a.prompts
		return promptedResult{Text: submitted.text, ConversationID: submitted.conversation}, submitted.err
	case methodReply, methodReplyLate:
		var p textParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
		a.turn.Lock()
		defer a.turn.Unlock()
		return struct{}{}, a.conv.reply(p.Text, method == methodReplyLate)
	case methodStream, methodSubagent, methodDropSubs:
		var p textParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
		author, ok := a.conv.(transcriptAuthor)
		if !ok {
			return nil, fmt.Errorf("%T does not script %s", a.conv, method)
		}
		a.turn.Lock()
		defer a.turn.Unlock()
		switch method {
		case methodStream:
			return struct{}{}, author.stream(p.Text)
		case methodSubagent:
			return struct{}{}, author.subagent(p.Text)
		default:
			return struct{}{}, author.deleteSubagentTranscripts()
		}
	case methodHalt:
		halting, ok := a.conv.(halter)
		if !ok {
			return nil, fmt.Errorf("%T does not script a halt", a.conv)
		}
		a.turn.Lock()
		defer a.turn.Unlock()
		return struct{}{}, halting.halt()
	case methodDeny:
		var denial Denial
		if err := json.Unmarshal(params, &denial); err != nil {
			return nil, err
		}
		guard, ok := a.conv.(autoModeGuard)
		if !ok {
			return nil, fmt.Errorf("%T has no auto mode to deny a tool call", a.conv)
		}
		a.turn.Lock()
		defer a.turn.Unlock()
		return struct{}{}, guard.deny(denial)
	case methodExit:
		var p exitParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
		a.exit(p.Code)
		return nil, nil
	default:
		return nil, fmt.Errorf("unknown method %q", method)
	}
}

func (a *agent) exitOnSignal() {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGHUP, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		sig := (<-signals).(syscall.Signal)
		a.exit(signalExitBase + int(sig))
	}()
}

func (a *agent) exit(code int) {
	_ = a.control.call(context.Background(), methodExiting, exitParams{Code: code}, nil)
	os.Exit(code)
}

var errPeerClosed = errors.New("peer closed the connection")

type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcHandler func(p *rpcPeer, method string, params json.RawMessage) (any, error)

type rpcPeer struct {
	conn    net.Conn
	handle  rpcHandler
	writeMu sync.Mutex
	mu      sync.Mutex
	nextID  uint64
	pending map[string]chan rpcMessage
	done    chan struct{}
}

func newRPCPeer(conn net.Conn, handle rpcHandler) *rpcPeer {
	return &rpcPeer{conn: conn, handle: handle, pending: map[string]chan rpcMessage{}, done: make(chan struct{})}
}

func (p *rpcPeer) start() *rpcPeer {
	go p.read()
	return p
}

func (p *rpcPeer) read() {
	defer close(p.done)
	reader := bufio.NewReader(p.conn)
	for {
		line, err := reader.ReadBytes('\n')
		if line = bytes.TrimSpace(line); len(line) > 0 {
			var msg rpcMessage
			if json.Unmarshal(line, &msg) == nil {
				p.dispatch(msg)
			}
		}
		if err != nil {
			return
		}
	}
}

func (p *rpcPeer) dispatch(msg rpcMessage) {
	if msg.Method != "" {
		go p.answer(msg)
		return
	}
	p.mu.Lock()
	waiter := p.pending[string(msg.ID)]
	delete(p.pending, string(msg.ID))
	p.mu.Unlock()
	if waiter != nil {
		waiter <- msg
	}
}

func (p *rpcPeer) answer(msg rpcMessage) {
	var result any
	err := fmt.Errorf("unknown method %q", msg.Method)
	if p.handle != nil {
		result, err = p.handle(p, msg.Method, msg.Params)
	}
	if len(msg.ID) == 0 {
		return
	}
	reply := rpcMessage{JSONRPC: "2.0", ID: msg.ID}
	if err != nil {
		reply.Error = &rpcError{Code: -32603, Message: err.Error()}
	} else if reply.Result, err = json.Marshal(result); err != nil {
		reply.Error = &rpcError{Code: -32603, Message: err.Error()}
	}
	_ = p.write(reply)
}

func (p *rpcPeer) call(ctx context.Context, method string, params, result any) error {
	p.mu.Lock()
	p.nextID++
	id := json.RawMessage(fmt.Sprint(p.nextID))
	waiter := make(chan rpcMessage, 1)
	p.pending[string(id)] = waiter
	p.mu.Unlock()
	forget := func() {
		p.mu.Lock()
		delete(p.pending, string(id))
		p.mu.Unlock()
	}
	if err := p.send(id, method, params); err != nil {
		forget()
		return err
	}
	select {
	case msg := <-waiter:
		return decodeReply(msg, result)
	case <-p.done:
		select {
		case msg := <-waiter:
			return decodeReply(msg, result)
		default:
			return errPeerClosed
		}
	case <-ctx.Done():
		forget()
		return ctx.Err()
	}
}

func (p *rpcPeer) notify(method string, params any) error {
	return p.send(nil, method, params)
}

func (p *rpcPeer) send(id json.RawMessage, method string, params any) error {
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	return p.write(rpcMessage{JSONRPC: "2.0", ID: id, Method: method, Params: raw})
}

func (p *rpcPeer) write(msg rpcMessage) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	_, err = p.conn.Write(append(data, '\n'))
	return err
}

func (p *rpcPeer) close() {
	_ = p.conn.Close()
	<-p.done
}

func decodeReply(msg rpcMessage, result any) error {
	if msg.Error != nil {
		return errors.New(msg.Error.Message)
	}
	if result == nil || len(msg.Result) == 0 {
		return nil
	}
	return json.Unmarshal(msg.Result, result)
}

func appendLines(path string, lines ...any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	var buf bytes.Buffer
	for _, line := range lines {
		data, err := json.Marshal(line)
		if err != nil {
			return err
		}
		buf.Write(append(data, '\n'))
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(buf.Bytes()); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func now() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}

type flagSpec struct {
	values   map[string]bool
	variadic map[string]bool
	optional map[string]bool
}

type parsedArgs struct {
	values      map[string][]string
	positionals []string
	afterDashes []string
}

func (a parsedArgs) value(names ...string) string {
	for _, name := range names {
		if values := a.values[name]; len(values) > 0 {
			return values[len(values)-1]
		}
	}
	return ""
}

func (a parsedArgs) has(names ...string) bool {
	for _, name := range names {
		if _, ok := a.values[name]; ok {
			return true
		}
	}
	return false
}

func (spec flagSpec) parse(args []string) parsedArgs {
	parsed := parsedArgs{values: map[string][]string{}}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			parsed.afterDashes = args[i+1:]
			break
		}
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			parsed.positionals = append(parsed.positionals, arg)
			continue
		}
		name, inline, hasInline := strings.Cut(arg, "=")
		switch {
		case hasInline:
			parsed.values[name] = append(parsed.values[name], inline)
		case spec.values[name] && i+1 < len(args):
			i++
			parsed.values[name] = append(parsed.values[name], args[i])
		case spec.variadic[name]:
			values := parsed.values[name]
			for i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				i++
				values = append(values, args[i])
			}
			parsed.values[name] = values
		case spec.optional[name] && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-"):
			i++
			parsed.values[name] = append(parsed.values[name], args[i])
		default:
			parsed.values[name] = append(parsed.values[name], "")
		}
	}
	return parsed
}
