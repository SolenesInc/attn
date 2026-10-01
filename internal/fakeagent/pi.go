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
	piVersion          = "0.80.10"
	piPluginAPIVersion = 7
	piRelaySocketEnv   = "ATTN_PI_SUITE_SOCKET"
	piRelayTokenEnv    = "ATTN_PI_TOKEN"
	piAutoModeEnv      = "ATTN_PI_AUTOMODE_CONFIG"
	piYoloEnv          = "ATTN_PI_YOLO"
	PiWithoutResumeEnv = "ATTN_FAKE_PI_NO_RESUME"
)

var piComposer = composer{prompt: "> "}

var piFlags = flagSpec{
	values: map[string]bool{"--session-id": true, "--model": true, "--thinking": true, "-e": true, "--append-system-prompt": true},
}

type piTerminal struct {
	cfg          config
	term         *terminal
	relay        *rpcPeer
	conversation string
	resumed      bool
	prompt       string
	autoMode     json.RawMessage
	yolo         bool
}

type relayHello struct {
	Token       string `json:"token"`
	PiSessionID string `json:"pi_session_id"`
}

type relayState struct {
	State string `json:"state"`
}

type relayStop struct {
	AssistantText string `json:"assistant_text"`
}

func runPiTerminal(cfg config) int {
	return serve(cfg, piComposer, &piTerminal{cfg: cfg})
}

func (p *piTerminal) begin(term *terminal) error {
	p.term = term
	args := piFlags.parse(os.Args[1:])
	if p.conversation = args.value("--session-id"); p.conversation == "" {
		return errors.New("pi launched without --session-id")
	}
	p.prompt = strings.Join(args.positionals, " ")
	if err := p.openSession(); err != nil {
		return err
	}
	if config := os.Getenv(piAutoModeEnv); config != "" {
		p.autoMode = json.RawMessage(config)
	}
	p.yolo = os.Getenv(piYoloEnv) == "true"
	socket, token := os.Getenv(piRelaySocketEnv), os.Getenv(piRelayTokenEnv)
	if socket == "" || token == "" {
		return fmt.Errorf("pi launched without %s and %s from driver.spawn", piRelaySocketEnv, piRelayTokenEnv)
	}
	conn, err := net.Dial("unix", socket)
	if err != nil {
		return fmt.Errorf("dial pi relay: %w", err)
	}
	p.relay = newRPCPeer(conn, nil).start()
	if err := p.relay.call(context.Background(), "hello", relayHello{Token: token, PiSessionID: p.conversation}, nil); err != nil {
		return fmt.Errorf("pi relay hello: %w", err)
	}
	return nil
}

func (p *piTerminal) openSession() error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	file, err := findPiSession(p.cfg.ToolHome, cwd, p.conversation)
	if err != nil {
		return err
	}
	if file != "" {
		p.resumed = true
		return nil
	}
	p.term.print(fmt.Sprintf("Warning: No project session found with id %s; creating a new session with that id.", p.conversation))
	file = filepath.Join(piSessionDir(p.cfg.ToolHome, cwd), strings.ReplaceAll(now(), ":", "-")+"_"+p.conversation+".jsonl")
	return appendLines(file, map[string]any{"type": "session", "version": 3, "id": p.conversation, "timestamp": now(), "cwd": cwd})
}

func piSessionDir(home, cwd string) string {
	encoded := "--" + strings.NewReplacer("/", "-", "\\", "-", ":", "-").Replace(strings.TrimLeft(cwd, "/\\")) + "--"
	return filepath.Join(home, ".pi", "agent", "sessions", encoded)
}

func findPiSession(home, cwd, id string) (string, error) {
	sessions, err := findPiSessions(home, cwd)
	return sessions[id], err
}

func findPiSessions(home, cwd string) (map[string]string, error) {
	matches := make(map[string]string)
	dir := piSessionDir(home, cwd)
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

func (p *piTerminal) launch() launch {
	return launch{Harness: Pi, ConversationID: p.conversation, Resumed: p.resumed, AutoMode: p.autoMode, Yolo: p.yolo}
}

func (p *piTerminal) initialPrompt() string { return p.prompt }

func (p *piTerminal) submit(string) error {
	return p.relay.call(context.Background(), "report_state", relayState{State: "working"}, nil)
}

func (p *piTerminal) reply(text string, afterStop bool) error {
	if afterStop {
		return errors.New("pi has no Stop hook to reply after")
	}
	p.term.print(text)
	return p.relay.call(context.Background(), "report_stop", relayStop{AssistantText: text}, nil)
}

type relayDenial struct {
	Denial
	At string `json:"at"`
}

func (p *piTerminal) deny(denial Denial) error {
	at := denial.At
	if at == "" {
		at = now()
	}
	return p.relay.call(context.Background(), "report_denial", relayDenial{Denial: denial, At: at}, nil)
}

type piPlugin struct {
	cfg       config
	resume    bool
	relayPath string
	daemon    *rpcPeer
	mu        sync.Mutex
	runs      map[string]*piRun
}

type piRun struct {
	sessionID   string
	runID       string
	piSessionID string
	seq         uint64
	relay       *rpcPeer
}

type piSpawn struct {
	SessionID       string          `json:"session_id"`
	RunID           string          `json:"run_id"`
	CWD             string          `json:"cwd"`
	InitialPrompt   string          `json:"initial_prompt"`
	Metadata        json.RawMessage `json:"metadata"`
	ResumeSessionID string          `json:"resume_session_id"`
	AutoMode        json.RawMessage `json:"auto_mode"`
	Yolo            bool            `json:"yolo"`
}

type piMetadata struct {
	Schema      int    `json:"schema"`
	PiSessionID string `json:"pi_session_id"`
	PiVersion   string `json:"pi_version"`
}

type piRunParams struct {
	SessionID string `json:"session_id"`
	RunID     string `json:"run_id"`
}

type okResult struct {
	OK      bool   `json:"ok"`
	Message string `json:"message,omitempty"`
}

func runPiPlugin(cfg config) int {
	control, err := dialControl(cfg, nil)
	if err == nil {
		err = control.start().call(context.Background(), methodLaunched, launch{Role: rolePlugin, Harness: Pi, Pid: os.Getpid(), Argv: os.Args}, nil)
	}
	p := &piPlugin{cfg: cfg, resume: os.Getenv(PiWithoutResumeEnv) == "", runs: map[string]*piRun{}}
	if err == nil {
		err = p.connect()
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "fake attn-pi: %v\n", err)
		return 1
	}
	<-p.daemon.done
	return 0
}

func (p *piPlugin) connect() error {
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
		"version":          piVersion,
		"attn_api_version": piPluginAPIVersion,
		"generation":       generation,
	}, &hello)
	if err != nil || !hello.OK {
		return fmt.Errorf("attn refused hello: %v", err)
	}
	var registered okResult
	err = p.daemon.call(context.Background(), "driver.register", map[string]any{
		"agent": piAgentName(),
		"capabilities": withPiCapabilityOverrides(map[string]bool{
			"resume":              p.resume,
			"resume_availability": true,
			"initial_prompt":      true,
			"state_reporting":     true,
			"message_delivery":    false,
			"auto_mode":           true,
		}),
	}, &registered)
	if err != nil || !registered.OK {
		return fmt.Errorf("attn refused driver.register: %v", err)
	}
	return nil
}

func (p *piPlugin) acceptRelays(listener net.Listener) {
	for {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		newRPCPeer(conn, p.handleRelay).start()
	}
}

func (p *piPlugin) handleDaemon(_ *rpcPeer, method string, params json.RawMessage) (any, error) {
	switch method {
	case "attn.health":
		return okResult{OK: true, Message: "fake pi " + piVersion + " is ready"}, nil
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
				stored, err = findPiSessions(p.cfg.ToolHome, conversation.CWD)
				if err != nil {
					return nil, err
				}
				sessions[conversation.CWD] = stored
			}
			availability = append(availability, map[string]any{"agent": conversation.Agent, "cwd": conversation.CWD, "resume_session_id": conversation.ResumeSessionID, "available": stored[conversation.ResumeSessionID] != "", "reason": fmt.Sprintf("conversation %s is no longer in pi's storage (%s)", conversation.ResumeSessionID, piSessionDir(p.cfg.ToolHome, conversation.CWD))})
		}
		return map[string]any{"availability": availability}, nil
	case "driver.models":
		if catalog := os.Getenv(PiModelsEnv); catalog != "" {
			return json.RawMessage(catalog), nil
		}
		return nil, fmt.Errorf("unknown method %q", method)
	case "driver.session_closed":
		var closed piRunParams
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

func (p *piPlugin) launchRun(raw json.RawMessage, resume bool) (any, error) {
	if reason := piLaunchRefusal(); reason != "" {
		return nil, errors.New(reason)
	}
	var spawn piSpawn
	if err := json.Unmarshal(raw, &spawn); err != nil {
		return nil, err
	}
	run := &piRun{sessionID: spawn.SessionID, runID: spawn.RunID, piSessionID: uuid.NewString()}
	if resume {
		piSessionID, err := resumedPiSession(spawn)
		if err != nil {
			return nil, err
		}
		run.piSessionID = piSessionID
	}
	argv := append([]string{filepath.Join(p.cfg.Bin, string(Pi)), "--session-id", run.piSessionID}, piLaunchFlags(raw)...)
	if !resume && strings.TrimSpace(spawn.InitialPrompt) != "" {
		argv = append(argv, spawn.InitialPrompt)
	}
	p.mu.Lock()
	p.runs[run.sessionID] = run
	p.mu.Unlock()
	err := p.daemon.call(context.Background(), "session.report_metadata", map[string]any{
		"session_id":        run.sessionID,
		"run_id":            run.runID,
		"seq":               p.nextSeq(run),
		"metadata":          piMetadata{Schema: 1, PiSessionID: run.piSessionID, PiVersion: piVersion},
		"resume_session_id": run.piSessionID,
	}, nil)
	if err != nil {
		return nil, err
	}
	env := map[string]string{piRelaySocketEnv: p.relayPath, piRelayTokenEnv: run.runID}
	if len(spawn.AutoMode) > 0 && string(spawn.AutoMode) != "null" {
		env[piAutoModeEnv] = string(spawn.AutoMode)
	}
	if spawn.Yolo {
		env[piYoloEnv] = "true"
	}
	return map[string]any{
		"argv": argv,
		"cwd":  spawn.CWD,
		"env":  env,
	}, nil
}

func resumedPiSession(spawn piSpawn) (string, error) {
	requested := strings.TrimSpace(spawn.ResumeSessionID)
	if len(spawn.Metadata) == 0 || string(spawn.Metadata) == "null" {
		if requested == "" {
			return "", errors.New("resume needs the session's pi metadata or a resume_session_id naming the pi session to pick up")
		}
		return requested, nil
	}
	var previous piMetadata
	if err := json.Unmarshal(spawn.Metadata, &previous); err != nil {
		return "", fmt.Errorf("pi session metadata: %w", err)
	}
	if previous.Schema != 1 {
		return "", fmt.Errorf("unsupported pi session metadata schema %d", previous.Schema)
	}
	if strings.TrimSpace(previous.PiSessionID) == "" || strings.TrimSpace(previous.PiVersion) == "" {
		return "", errors.New("pi session metadata is missing pi_session_id or pi_version")
	}
	if requested != "" {
		return requested, nil
	}
	return strings.TrimSpace(previous.PiSessionID), nil
}

func (p *piPlugin) handleRelay(relay *rpcPeer, method string, params json.RawMessage) (any, error) {
	if method == "hello" {
		var hello relayHello
		if err := json.Unmarshal(params, &hello); err != nil {
			return nil, err
		}
		return struct{}{}, p.adopt(relay, hello.Token)
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
	case "report_denial":
		var denial relayDenial
		if err := json.Unmarshal(params, &denial); err != nil {
			return nil, err
		}
		return struct{}{}, p.report("session.report_automode_denial", run, map[string]any{
			"tool": denial.Tool, "action": denial.Action, "reason": denial.Reason, "rule": denial.Rule, "at": denial.At,
		})
	default:
		return nil, fmt.Errorf("unknown method %q", method)
	}
}

func (p *piPlugin) adopt(relay *rpcPeer, token string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, run := range p.runs {
		if run.runID == token {
			run.relay = relay
			return nil
		}
	}
	return errors.New("unknown pi relay token")
}

func (p *piPlugin) runOn(relay *rpcPeer) *piRun {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, run := range p.runs {
		if run.relay == relay {
			return run
		}
	}
	return nil
}

func (p *piPlugin) nextSeq(run *piRun) uint64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	run.seq++
	return run.seq
}

func (p *piPlugin) report(method string, run *piRun, fields map[string]any) error {
	fields["session_id"] = run.sessionID
	fields["run_id"] = run.runID
	fields["seq"] = p.nextSeq(run)
	return p.daemon.call(context.Background(), method, fields, nil)
}

func (p *piPlugin) reportStop(run *piRun, text string) error {
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
