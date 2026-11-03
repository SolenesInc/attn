package fakeagent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/google/uuid"
)

const (
	plugVersion          = "0.80.10"
	plugAPIVersion       = 7
	plugRelaySocketEnv   = "ATTN_PLUG_RELAY_SOCKET"
	plugRelayTokenEnv    = "ATTN_PLUG_TOKEN"
	plugYoloEnv          = "ATTN_PLUG_YOLO"
	PlugWithoutResumeEnv = "ATTN_FAKE_PLUG_NO_RESUME"
)

var plugComposer = composer{prompt: "> "}

var plugFlags = flagSpec{
	values: map[string]bool{"--session-id": true, "--model": true, "--effort": true, "-e": true, "--append-system-prompt": true},
}

type plugTerminal struct {
	cfg          config
	term         *terminal
	relay        *rpcPeer
	conversation string
	transcript   string
	resumed      bool
	prompt       string
	yolo         bool
}

type relayHello struct {
	Token          string `json:"token"`
	PlugSessionID  string `json:"plug_session_id"`
	TranscriptPath string `json:"transcript_path"`
}

type relayState struct {
	State string `json:"state"`
}

type relayStop struct {
	AssistantText string `json:"assistant_text"`
}

func runPlugTerminal(cfg config) int {
	return serve(cfg, plugComposer, &plugTerminal{cfg: cfg})
}

func (p *plugTerminal) begin(term *terminal) error {
	p.term = term
	args := plugFlags.parse(os.Args[1:])
	if p.conversation = args.value("--session-id"); p.conversation == "" {
		return errors.New("plug launched without --session-id")
	}
	p.prompt = strings.Join(args.positionals, " ")
	if err := p.openSession(); err != nil {
		return err
	}
	p.yolo = os.Getenv(plugYoloEnv) == "true"
	return p.connectRelay()
}

// connectRelay says hello at startup and again after every switch to another session.
func (p *plugTerminal) connectRelay() error {
	socket, token := os.Getenv(plugRelaySocketEnv), os.Getenv(plugRelayTokenEnv)
	if socket == "" || token == "" {
		return fmt.Errorf("plug launched without %s and %s from driver.spawn", plugRelaySocketEnv, plugRelayTokenEnv)
	}
	conn, err := net.Dial("unix", socket)
	if err != nil {
		return fmt.Errorf("dial plug relay: %w", err)
	}
	if p.relay != nil {
		p.relay.close()
	}
	p.relay = newRPCPeer(conn, nil).start()
	if err := p.relay.call(context.Background(), "hello", relayHello{Token: token, PlugSessionID: p.conversation, TranscriptPath: p.transcript}, nil); err != nil {
		return fmt.Errorf("plug relay hello: %w", err)
	}
	return nil
}

func (p *plugTerminal) openSession() error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	file, err := findPlugSession(p.cfg.ToolHome, cwd, p.conversation)
	if err != nil {
		return err
	}
	if file != "" {
		p.resumed = true
		p.transcript = file
		return nil
	}
	p.term.print(fmt.Sprintf("Warning: No project session found with id %s; creating a new session with that id.", p.conversation))
	file = filepath.Join(plugSessionDir(p.cfg.ToolHome, cwd), strings.ReplaceAll(now(), ":", "-")+"_"+p.conversation+".jsonl")
	p.transcript = file
	return appendLines(file, map[string]any{"type": "session", "version": 3, "id": p.conversation, "timestamp": now(), "cwd": cwd})
}

func plugSessionDir(home, cwd string) string {
	encoded := "--" + strings.NewReplacer("/", "-", "\\", "-", ":", "-").Replace(strings.TrimLeft(cwd, "/\\")) + "--"
	return filepath.Join(home, ".plug", "sessions", encoded)
}

func findPlugSession(home, cwd, id string) (string, error) {
	sessions, err := findPlugSessions(home, cwd)
	return sessions[id], err
}

func findPlugSessions(home, cwd string) (map[string]string, error) {
	matches := make(map[string]string)
	dir := plugSessionDir(home, cwd)
	files, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return matches, nil
	}
	if err != nil {
		return nil, err
	}
	for _, entry := range files {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		file, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		line, err := bufio.NewReader(file).ReadBytes('\n')
		file.Close()
		var header struct {
			Type string `json:"type"`
			ID   string `json:"id"`
		}
		if err == nil && json.Unmarshal(line, &header) == nil && header.Type == "session" {
			matches[header.ID] = path
		}
	}
	return matches, nil
}

func (p *plugTerminal) launch() launch {
	return launch{Harness: Plug, ConversationID: p.conversation, Resumed: p.resumed, Yolo: p.yolo}
}

func (p *plugTerminal) initialPrompt() string { return p.prompt }

func (p *plugTerminal) submit(prompt string) error {
	if strings.TrimSpace(prompt) == "/new" {
		return p.moveTo(uuid.NewString())
	}
	if conversation, ok := strings.CutPrefix(strings.TrimSpace(prompt), "/resume "); ok {
		return p.moveTo(strings.TrimSpace(conversation))
	}
	return p.relay.call(context.Background(), "report_state", relayState{State: "working"}, nil)
}

func (p *plugTerminal) moveTo(conversation string) error {
	p.conversation = conversation
	if err := p.openSession(); err != nil {
		return err
	}
	return p.connectRelay()
}

func (p *plugTerminal) reply(text string, afterStop bool) error {
	if afterStop {
		return errors.New("plug has no Stop hook to reply after")
	}
	p.term.print(text)
	return p.relay.call(context.Background(), "report_stop", relayStop{AssistantText: text}, nil)
}

type plugDriver struct {
	cfg       config
	resume    bool
	relayPath string
	daemon    *rpcPeer
	mu        sync.Mutex
	runs      map[string]*plugRun
}

type plugRun struct {
	sessionID     string
	runID         string
	plugSessionID string
	seq           uint64
	relay         *rpcPeer
}

type plugSpawn struct {
	SessionID       string          `json:"session_id"`
	RunID           string          `json:"run_id"`
	CWD             string          `json:"cwd"`
	InitialPrompt   string          `json:"initial_prompt"`
	Metadata        json.RawMessage `json:"metadata"`
	ResumeSessionID string          `json:"resume_session_id"`
	Yolo            bool            `json:"yolo"`
}

type plugMetadata struct {
	Schema        int    `json:"schema"`
	PlugSessionID string `json:"plug_session_id"`
	PlugVersion   string `json:"plug_version"`
}

type plugRunParams struct {
	SessionID string `json:"session_id"`
	RunID     string `json:"run_id"`
}

type okResult struct {
	OK      bool   `json:"ok"`
	Message string `json:"message,omitempty"`
}

func runPlugDriver(cfg config) int {
	control, err := dialControl(cfg, nil)
	if err == nil {
		err = control.start().call(context.Background(), methodLaunched, launch{Role: rolePlugin, Harness: Plug, Pid: os.Getpid(), Argv: os.Args}, nil)
	}
	p := &plugDriver{cfg: cfg, resume: os.Getenv(PlugWithoutResumeEnv) == "", runs: map[string]*plugRun{}}
	if err == nil {
		err = p.connect()
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "fake attn-plug: %v\n", err)
		return 1
	}
	<-p.daemon.done
	return 0
}

func (p *plugDriver) connect() error {
	dataRoot := os.Getenv("ATTN_PLUGIN_DATA_ROOT")
	generation, err := strconv.ParseUint(os.Getenv("ATTN_PLUGIN_GENERATION"), 10, 64)
	if dataRoot == "" || err != nil {
		return errors.New("attn did not pass ATTN_PLUGIN_DATA_ROOT and ATTN_PLUGIN_GENERATION")
	}
	if err := os.MkdirAll(dataRoot, 0o700); err != nil {
		return err
	}
	p.relayPath = filepath.Join(dataRoot, "relay.sock")
	_ = os.Remove(p.relayPath)
	relay, err := net.Listen("unix", p.relayPath)
	if err != nil {
		return err
	}
	conn, err := net.Dial("unix", os.Getenv("ATTN_SOCKET_PATH"))
	if err != nil {
		return fmt.Errorf("dial attn: %w", err)
	}
	p.daemon = newRPCPeer(conn, p.handleDaemon)
	p.daemon.start()
	go p.acceptRelays(relay)
	var hello okResult
	err = p.daemon.call(context.Background(), "hello", map[string]any{
		"name":             os.Getenv("ATTN_PLUGIN_NAME"),
		"version":          plugVersion,
		"attn_api_version": plugAPIVersion,
		"generation":       generation,
	}, &hello)
	if err != nil || !hello.OK {
		return fmt.Errorf("attn refused hello: %v", err)
	}
	var registered okResult
	err = p.daemon.call(context.Background(), "driver.register", map[string]any{
		"agent": plugAgentName(),
		"capabilities": withPlugCapabilityOverrides(map[string]bool{
			"resume":              p.resume,
			"resume_availability": true,
			"initial_prompt":      true,
			"state_reporting":     true,
			"message_delivery":    false,
		}),
	}, &registered)
	if err != nil || !registered.OK {
		return fmt.Errorf("attn refused driver.register: %v", err)
	}
	return nil
}

func (p *plugDriver) acceptRelays(listener net.Listener) {
	for {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		newRPCPeer(conn, p.handleRelay).start()
	}
}

func (p *plugDriver) handleDaemon(_ *rpcPeer, method string, params json.RawMessage) (any, error) {
	switch method {
	case "attn.health":
		return okResult{OK: true, Message: "fake plug " + plugVersion + " is ready"}, nil
	case "driver.spawn":
		return p.launchRun(params, false)
	case "driver.resume":
		if !p.resume {
			return nil, fmt.Errorf("unknown method %q", method)
		}
		return p.launchRun(params, true)
	case "driver.resume_available":
		var request struct {
			Conversations []struct {
				Agent           string `json:"agent"`
				CWD             string `json:"cwd"`
				ResumeSessionID string `json:"resume_session_id"`
			} `json:"conversations"`
		}
		if err := json.Unmarshal(params, &request); err != nil {
			return nil, err
		}
		sessions := make(map[string]map[string]string)
		availability := make([]map[string]any, 0, len(request.Conversations))
		for _, conversation := range request.Conversations {
			stored, ok := sessions[conversation.CWD]
			if !ok {
				var err error
				stored, err = findPlugSessions(p.cfg.ToolHome, conversation.CWD)
				if err != nil {
					return nil, err
				}
				sessions[conversation.CWD] = stored
			}
			availability = append(availability, map[string]any{"agent": conversation.Agent, "cwd": conversation.CWD, "resume_session_id": conversation.ResumeSessionID, "available": stored[conversation.ResumeSessionID] != "", "reason": fmt.Sprintf("conversation %s is no longer in plug's storage (%s)", conversation.ResumeSessionID, plugSessionDir(p.cfg.ToolHome, conversation.CWD))})
		}
		return map[string]any{"availability": availability}, nil
	case "driver.models":
		if catalog := os.Getenv(PlugModelsEnv); catalog != "" {
			return json.RawMessage(catalog), nil
		}
		return nil, fmt.Errorf("unknown method %q", method)
	case "driver.session_closed":
		var closed plugRunParams
		if err := json.Unmarshal(params, &closed); err != nil {
			return nil, err
		}
		p.mu.Lock()
		if run := p.runs[closed.SessionID]; run != nil && run.runID == closed.RunID {
			delete(p.runs, closed.SessionID)
		}
		p.mu.Unlock()
		return okResult{OK: true}, nil
	default:
		return nil, fmt.Errorf("unknown method %q", method)
	}
}

func (p *plugDriver) launchRun(raw json.RawMessage, resume bool) (any, error) {
	if reason := plugLaunchRefusal(); reason != "" {
		return nil, errors.New(reason)
	}
	var spawn plugSpawn
	if err := json.Unmarshal(raw, &spawn); err != nil {
		return nil, err
	}
	run := &plugRun{sessionID: spawn.SessionID, runID: spawn.RunID, plugSessionID: uuid.NewString()}
	if resume {
		plugSessionID, err := resumedPlugSession(spawn)
		if err != nil {
			return nil, err
		}
		run.plugSessionID = plugSessionID
	}
	argv := append([]string{filepath.Join(p.cfg.Bin, string(Plug)), "--session-id", run.plugSessionID}, plugLaunchFlags(raw)...)
	if !resume && strings.TrimSpace(spawn.InitialPrompt) != "" {
		argv = append(argv, spawn.InitialPrompt)
	}
	p.mu.Lock()
	p.runs[run.sessionID] = run
	p.mu.Unlock()
	if err := p.reportMetadata(run); err != nil {
		return nil, err
	}
	env := map[string]string{plugRelaySocketEnv: p.relayPath, plugRelayTokenEnv: run.runID}
	if spawn.Yolo {
		env[plugYoloEnv] = "true"
	}
	return map[string]any{
		"argv": argv,
		"cwd":  spawn.CWD,
		"env":  env,
	}, nil
}

func resumedPlugSession(spawn plugSpawn) (string, error) {
	requested := strings.TrimSpace(spawn.ResumeSessionID)
	if len(spawn.Metadata) == 0 || string(spawn.Metadata) == "null" {
		if requested == "" {
			return "", errors.New("resume needs the session's plug metadata or a resume_session_id naming the plug session to pick up")
		}
		return requested, nil
	}
	var previous plugMetadata
	if err := json.Unmarshal(spawn.Metadata, &previous); err != nil {
		return "", fmt.Errorf("plug session metadata: %w", err)
	}
	if previous.Schema != 1 {
		return "", fmt.Errorf("unsupported plug session metadata schema %d", previous.Schema)
	}
	if strings.TrimSpace(previous.PlugSessionID) == "" || strings.TrimSpace(previous.PlugVersion) == "" {
		return "", errors.New("plug session metadata is missing plug_session_id or plug_version")
	}
	if requested != "" {
		return requested, nil
	}
	return strings.TrimSpace(previous.PlugSessionID), nil
}

func (p *plugDriver) handleRelay(relay *rpcPeer, method string, params json.RawMessage) (any, error) {
	if method == "hello" {
		var hello relayHello
		if err := json.Unmarshal(params, &hello); err != nil {
			return nil, err
		}
		run, err := p.adopt(relay, hello)
		if err != nil {
			return nil, err
		}
		if err := p.reportMetadata(run); err != nil {
			return nil, err
		}
		return struct{}{}, p.report("session.report_transcript_path", run, map[string]any{"path": hello.TranscriptPath})
	}
	run := p.runOn(relay)
	if run == nil {
		return nil, errors.New("relay call before hello")
	}
	switch method {
	case "report_state":
		var state relayState
		if err := json.Unmarshal(params, &state); err != nil {
			return nil, err
		}
		return struct{}{}, p.report("session.report_state", run, map[string]any{"state": state.State})
	case "report_stop":
		var stop relayStop
		if err := json.Unmarshal(params, &stop); err != nil {
			return nil, err
		}
		return struct{}{}, p.reportStop(run, stop.AssistantText)
	default:
		return nil, fmt.Errorf("unknown method %q", method)
	}
}

func (p *plugDriver) adopt(relay *rpcPeer, hello relayHello) (*plugRun, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, run := range p.runs {
		if run.runID == hello.Token {
			run.relay = relay
			run.plugSessionID = hello.PlugSessionID
			return run, nil
		}
	}
	return nil, errors.New("unknown plug relay token")
}

func (p *plugDriver) reportMetadata(run *plugRun) error {
	p.mu.Lock()
	plugSessionID := run.plugSessionID
	p.mu.Unlock()
	return p.report("session.report_metadata", run, map[string]any{
		"metadata":          plugMetadata{Schema: 1, PlugSessionID: plugSessionID, PlugVersion: plugVersion},
		"resume_session_id": plugSessionID,
	})
}

func (p *plugDriver) runOn(relay *rpcPeer) *plugRun {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, run := range p.runs {
		if run.relay == relay {
			return run
		}
	}
	return nil
}

func (p *plugDriver) nextSeq(run *plugRun) uint64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	run.seq++
	return run.seq
}

func (p *plugDriver) report(method string, run *plugRun, fields map[string]any) error {
	fields["session_id"] = run.sessionID
	fields["run_id"] = run.runID
	fields["seq"] = p.nextSeq(run)
	return p.daemon.call(context.Background(), method, fields, nil)
}

func (p *plugDriver) reportStop(run *plugRun, text string) error {
	seq := p.nextSeq(run)
	var classified struct {
		Verdict string `json:"verdict"`
	}
	err := p.daemon.call(context.Background(), "attn.classify_stop", map[string]any{
		"session_id":     run.sessionID,
		"run_id":         run.runID,
		"assistant_text": text,
	}, &classified)
	if err != nil {
		return err
	}
	return p.daemon.call(context.Background(), "session.report_stop", map[string]any{
		"session_id": run.sessionID,
		"run_id":     run.runID,
		"seq":        seq,
		"verdict":    classified.Verdict,
	}, nil)
}
