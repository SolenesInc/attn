package hostsession

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/victorarias/attn/internal/procreap"
)

type Event struct {
	SessionID   string
	Seq         int
	Kind        string
	Body        map[string]interface{}
	LifecycleID string
}

type ExitInfo struct {
	SessionID   string
	ExitCode    int
	Signal      string
	LifecycleID string
}

type SpawnOptions struct {
	SessionID    string
	LifecycleID  string
	Command      []string
	Env          []string
	CWD          string
	LogPath      string
	RegistryPath string
}

// terminationGrace: measured 3 ms SIGTERM-to-exit (pi 0.83.0, idle and mid-run,
// 2026-08-05); 3 s is a tripwire — reaching it means wedged, not busy.
const terminationGrace = 3 * time.Second

// envelopeDrainGrace: a tool child that inherited fd 3 can hold the pipe open
// forever; 2 s means something holds it.
const envelopeDrainGrace = 2 * time.Second

type host struct {
	sessionID    string
	lifecycleID  string
	cmd          *exec.Cmd
	pgid         int
	registryPath string
	stdin        *os.File
	envelopes    *os.File
	logFile      *os.File
	reaped       chan struct{}
	exited       chan struct{}
	drained      chan struct{}
	killOnce     sync.Once
}

type Manager struct {
	logf    func(format string, args ...interface{})
	onEvent func(Event)
	onExit  func(ExitInfo)

	mu    sync.Mutex
	hosts map[string]*host
}

func New(logf func(format string, args ...interface{}), onEvent func(Event), onExit func(ExitInfo)) *Manager {
	if logf == nil {
		logf = func(string, ...interface{}) {}
	}
	if onEvent == nil {
		onEvent = func(Event) {}
	}
	if onExit == nil {
		onExit = func(ExitInfo) {}
	}
	return &Manager{logf: logf, onEvent: onEvent, onExit: onExit, hosts: make(map[string]*host)}
}

var ErrNotFound = errors.New("host session not found")

func (m *Manager) Spawn(opts SpawnOptions) error {
	if opts.SessionID == "" {
		return errors.New("host spawn needs a session id")
	}
	if len(opts.Command) == 0 {
		return errors.New("host spawn needs a command")
	}
	m.mu.Lock()
	if _, exists := m.hosts[opts.SessionID]; exists {
		m.mu.Unlock()
		return fmt.Errorf("host for session %s is already running", opts.SessionID)
	}
	m.mu.Unlock()

	logFile, err := openLog(opts.LogPath)
	if err != nil {
		return err
	}

	envelopeR, envelopeW, err := os.Pipe()
	if err != nil {
		logFile.Close()
		return fmt.Errorf("create envelope pipe: %w", err)
	}
	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		logFile.Close()
		envelopeR.Close()
		envelopeW.Close()
		return fmt.Errorf("create verb pipe: %w", err)
	}

	cmd := exec.Command(opts.Command[0], opts.Command[1:]...)
	cmd.Dir = opts.CWD
	cmd.Env = opts.Env
	cmd.Stdin = stdinR
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.ExtraFiles = []*os.File{envelopeW}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		logFile.Close()
		envelopeR.Close()
		envelopeW.Close()
		stdinR.Close()
		stdinW.Close()
		return fmt.Errorf("start host %v: %w", opts.Command, err)
	}
	// Close our copies or the envelope reader never sees EOF.
	envelopeW.Close()
	stdinR.Close()

	h := &host{
		sessionID:    opts.SessionID,
		lifecycleID:  opts.LifecycleID,
		cmd:          cmd,
		pgid:         cmd.Process.Pid,
		registryPath: opts.RegistryPath,
		stdin:        stdinW,
		envelopes:    envelopeR,
		logFile:      logFile,
		reaped:       make(chan struct{}),
		exited:       make(chan struct{}),
		drained:      make(chan struct{}),
	}
	if opts.RegistryPath != "" {
		entry := procreap.NewEntry(opts.SessionID, cmd.Process.Pid, cmd.Process.Pid, opts.Command)
		if err := procreap.WriteEntry(opts.RegistryPath, entry); err != nil {
			m.logf("host session %s: recording host registry entry failed: %v", opts.SessionID, err)
		}
	}
	m.mu.Lock()
	m.hosts[opts.SessionID] = h
	m.mu.Unlock()

	m.logf("host session %s started pid=%d pgid=%d cmd=%v", opts.SessionID, h.pgid, h.pgid, opts.Command)
	go m.readEnvelopes(h, envelopeR)
	go m.monitor(h)
	return nil
}

func openLog(path string) (*os.File, error) {
	if path == "" {
		return os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create host log dir: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open host log %s: %w", path, err)
	}
	return file, nil
}

// maxEnvelopeBytes receipt: pi 0.83.0's largest catalog maxTokens is 2,000,000 ≈ 8 MB at
// 4 bytes/token, so 64 MB is 8x past it.
const maxEnvelopeBytes = 64 << 20

func (m *Manager) readEnvelopes(h *host, r *os.File) {
	defer close(h.drained)
	defer r.Close()
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), maxEnvelopeBytes)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var envelope struct {
			SessionID string                 `json:"session_id"`
			Seq       int                    `json:"seq"`
			Kind      string                 `json:"kind"`
			Body      map[string]interface{} `json:"body"`
		}
		if err := json.Unmarshal(line, &envelope); err != nil {
			m.logf("host session %s: undecodable envelope (%d bytes): %v", h.sessionID, len(line), err)
			continue
		}
		if envelope.Kind == "" {
			m.logf("host session %s: envelope seq=%d has no kind; dropped", h.sessionID, envelope.Seq)
			continue
		}
		if envelope.Body == nil {
			envelope.Body = map[string]interface{}{}
		}
		if envelope.SessionID != "" && envelope.SessionID != h.sessionID {
			m.logf("host session %s: envelope claims session %s; using the spawned one", h.sessionID, envelope.SessionID)
		}
		m.onEvent(Event{
			SessionID:   h.sessionID,
			Seq:         envelope.Seq,
			Kind:        envelope.Kind,
			Body:        envelope.Body,
			LifecycleID: h.lifecycleID,
		})
	}
	if err := scanner.Err(); err != nil {
		if errors.Is(err, bufio.ErrTooLong) {
			m.logf("host session %s: envelope exceeded maxEnvelopeBytes=%d; tearing the host down", h.sessionID, maxEnvelopeBytes)
			go m.Kill(h.sessionID)
			return
		}
		m.logf("host session %s: envelope stream failed: %v", h.sessionID, err)
	}
}

// The group sweep on every exit path is what catches pi orphaning its tool
// subprocesses. Post-reap is safe: an empty group is a harmless ESRCH.
func (m *Manager) monitor(h *host) {
	waitErr := h.cmd.Wait()
	if err := syscall.Kill(-h.pgid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		m.logf("host session %s: sweeping process group %d failed: %v", h.sessionID, h.pgid, err)
	}
	close(h.reaped)
	h.stdin.Close()
	h.logFile.Close()

	select {
	case <-h.drained:
	case <-time.After(envelopeDrainGrace):
		m.logf("host session %s: envelope stream still open %s after exit; something inherited fd 3", h.sessionID, envelopeDrainGrace)
		h.envelopes.Close()
		<-h.drained
	}

	exitCode, signal := exitStatus(h.cmd, waitErr)
	m.mu.Lock()
	if current, ok := m.hosts[h.sessionID]; ok && current == h {
		delete(m.hosts, h.sessionID)
	}
	m.mu.Unlock()

	if h.registryPath != "" {
		if err := procreap.RemoveEntry(h.registryPath); err != nil {
			m.logf("host session %s: removing host registry entry failed: %v", h.sessionID, err)
		}
	}

	close(h.exited)

	m.logf("host session %s exited code=%d signal=%q pgid=%d", h.sessionID, exitCode, signal, h.pgid)
	m.onExit(ExitInfo{SessionID: h.sessionID, ExitCode: exitCode, Signal: signal, LifecycleID: h.lifecycleID})
}

func exitStatus(cmd *exec.Cmd, waitErr error) (int, string) {
	state := cmd.ProcessState
	if state == nil {
		if waitErr != nil {
			return -1, ""
		}
		return 0, ""
	}
	if status, ok := state.Sys().(syscall.WaitStatus); ok && status.Signaled() {
		return 128 + int(status.Signal()), status.Signal().String()
	}
	return state.ExitCode(), ""
}

type Delivery string

const (
	DeliveryPrompt   Delivery = "prompt"
	DeliverySteer    Delivery = "steer"
	DeliveryFollowUp Delivery = "follow_up"
)

func (m *Manager) Deliver(sessionID string, how Delivery, text string) error {
	return m.DeliverWithInput(sessionID, how, text, "")
}

func (m *Manager) DeliverWithInput(sessionID string, how Delivery, text, inputID string) error {
	switch how {
	case DeliveryPrompt, DeliverySteer, DeliveryFollowUp:
	default:
		return fmt.Errorf("unsupported host delivery %q", how)
	}
	verb := map[string]interface{}{"verb": string(how), "text": text}
	if inputID != "" {
		verb["input_id"] = inputID
	}
	return m.send(sessionID, verb)
}

func (m *Manager) ToolDetail(sessionID, callID string, full bool) error {
	if callID == "" {
		return errors.New("tool detail needs a call id")
	}
	return m.send(sessionID, map[string]interface{}{"verb": "tool_detail", "call_id": callID, "full": full})
}

func (m *Manager) Snapshot(sessionID string) error {
	return m.send(sessionID, map[string]interface{}{"verb": "snapshot"})
}

func (m *Manager) History(sessionID, before string) error {
	if before == "" {
		return errors.New("history needs a before cursor")
	}
	return m.send(sessionID, map[string]interface{}{"verb": "history", "before": before})
}

func (m *Manager) SetModel(sessionID, model string) error {
	if model == "" {
		return errors.New("set model needs a model")
	}
	return m.send(sessionID, map[string]interface{}{"verb": "set_model", "model": model})
}

func (m *Manager) ClearQueue(sessionID string) error {
	return m.send(sessionID, map[string]interface{}{"verb": "clear_queue"})
}

func (m *Manager) send(sessionID string, verb map[string]interface{}) error {
	m.mu.Lock()
	h, ok := m.hosts[sessionID]
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("%w: %s", ErrNotFound, sessionID)
	}
	encoded, err := json.Marshal(verb)
	if err != nil {
		return fmt.Errorf("encode host verb: %w", err)
	}
	if _, err := h.stdin.Write(append(encoded, '\n')); err != nil {
		return fmt.Errorf("write host verb for session %s: %w", sessionID, err)
	}
	return nil
}

func (m *Manager) Has(sessionID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.hosts[sessionID]
	return ok
}

func (m *Manager) SessionIDs() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := make([]string, 0, len(m.hosts))
	for id := range m.hosts {
		ids = append(ids, id)
	}
	return ids
}

// Kill returns nil only when the group is gone. The SIGTERM is load-bearing: pi's tool
// subprocesses lead their OWN process groups (measured 2026-08-05).
func (m *Manager) Kill(sessionID string) error {
	m.mu.Lock()
	h, ok := m.hosts[sessionID]
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("%w: %s", ErrNotFound, sessionID)
	}

	h.killOnce.Do(func() {
		if err := syscall.Kill(-h.pgid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
			m.logf("host session %s: SIGTERM to group %d failed: %v", sessionID, h.pgid, err)
		}
	})

	// Escalate on reaped, return on exited — escalating on exited would count
	// the envelope drain against the host's grace.
	select {
	case <-h.reaped:
		<-h.exited
		return nil
	case <-time.After(terminationGrace):
	}

	m.logf("host session %s did not exit within %s of SIGTERM; killing group %d", sessionID, terminationGrace, h.pgid)
	if err := syscall.Kill(-h.pgid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("kill host group %d for session %s: %w", h.pgid, sessionID, err)
	}
	<-h.exited
	return nil
}

func (m *Manager) Shutdown() {
	for _, id := range m.SessionIDs() {
		if err := m.Kill(id); err != nil && !errors.Is(err, ErrNotFound) {
			m.logf("host session %s: shutdown kill failed: %v", id, err)
		}
	}
}
