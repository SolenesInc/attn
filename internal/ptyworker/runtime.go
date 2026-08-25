package ptyworker

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/victorarias/attn/internal/buildinfo"
	"github.com/victorarias/attn/internal/launchcontract"
	"github.com/victorarias/attn/internal/pty"
)

func previewWorkerBytesForLog(data []byte) string {
	if len(data) == 0 {
		return ""
	}
	const maxPreview = 32
	preview := string(data)
	if len(preview) > maxPreview {
		preview = preview[:maxPreview]
	}
	preview = strings.ReplaceAll(preview, "\n", "\\n")
	preview = strings.ReplaceAll(preview, "\r", "\\r")
	preview = strings.ReplaceAll(preview, "\t", "\\t")
	return preview
}

func attachBlocksToWire(blocks []pty.AttachBlockData) []AttachBlock {
	if len(blocks) == 0 {
		return nil
	}
	out := make([]AttachBlock, len(blocks))
	for i, b := range blocks {
		out[i] = AttachBlock{
			ID:             b.ID,
			Pending:        b.Pending,
			PromptRow:      b.PromptRow,
			InputRow:       b.InputRow,
			InputCol:       b.InputCol,
			OutputStartRow: b.OutputStartRow,
			EndRow:         b.EndRow,
			Command:        b.Command,
			ExitCode:       b.ExitCode,
		}
	}
	return out
}

var exitedSessionCleanupTTL = 45 * time.Second

// Reaps only workers that are both unowned and idle; PTY output defers it.
// Override with ATTN_WORKER_ORPHAN_TTL (Go duration; "0" disables reaping).
var orphanedWorkerTTL = 12 * time.Hour

const orphanTTLEnvVar = "ATTN_WORKER_ORPHAN_TTL"

const orphanActivitySubscriberID = "worker-orphan-activity"

const (
	connSendQueueSize       = 256
	connWriteTimeout        = 2 * time.Second
	connResponseSendTimeout = 500 * time.Millisecond
	connHelloTimeout        = 5 * time.Second
	connIdleReadTimeout     = 3 * time.Minute
)

type Config struct {
	DaemonInstanceID string
	SessionID        string
	Agent            string
	CWD              string
	Cols             uint16
	Rows             uint16
	Label            string

	ResumeSessionID   string
	ResumePicker      bool
	YoloMode          bool
	ApprovalRoute     launchcontract.ApprovalRoute
	InitialPromptFile string

	ThemeForeground  string
	ThemeBackground  string
	ThemeCursor      string
	ThemeANSIPalette [16]string

	Executable string

	ClaudeExecutable  string
	CodexExecutable   string
	CopilotExecutable string
	ExternalCommand   []string
	ExternalEnv       []string
	ExternalCWD       string
	DaemonEnv         []string
	UnattendedLaunch  launchcontract.UnattendedLaunchSpec

	RegistryPath   string
	SocketPath     string
	ControlToken   string
	OwnerPID       int
	OwnerStartedAt string
	OwnerNonce     string

	Logf func(format string, args ...interface{}) `json:"-"`

	// Set only on the adopt half of an in-place upgrade; see upgrade.go.
	AdoptHandoff    string
	AdoptPtmxFD     int
	AdoptListenerFD int

	Debug bool
}

type Runtime struct {
	cfg     Config
	manager *pty.Manager
	// Released after the manager takes it: it carries the whole screen as VT
	// (measured 590KB on a full 8MB scrollback) and this process lives for days.
	adopt    *pty.HandoffState
	adopted  bool
	listener net.Listener
	logf     func(format string, args ...interface{})
	capture  *debugCapture

	stateMu    sync.RWMutex
	state      string
	exitCode   *int
	exitSignal *string

	stopOnce sync.Once
	stopCh   chan struct{}

	lifecycleMu sync.Mutex
	authedConns int
	exited      bool
	cleanupTTL  *time.Timer

	// orphanTimer is guarded by lifecycleMu; lastOutputNano by the output path.
	orphanTTL      time.Duration
	orphanTimer    *time.Timer
	lastOutputNano atomic.Int64

	connSeq atomic.Uint64

	upgradeMu     sync.Mutex
	upgradeResume chan net.Listener

	watchMu   sync.RWMutex
	watchConn map[*connCtx]struct{}
}

func Run(ctx context.Context, cfg Config) error {
	logf := cfg.Logf
	if logf == nil {
		logf = func(string, ...interface{}) {}
	}
	var adopt *pty.HandoffState
	if cfg.AdoptHandoff != "" {
		hf, err := readHandoff(cfg.AdoptHandoff)
		if err != nil {
			return err
		}
		// The handoff JSON is the entire contract: a Config field that does not
		// serialize reaches an adopted session as its zero value.
		inherited := cfg
		cfg = hf.Config
		cfg.Logf = logf
		cfg.Debug = inherited.Debug
		cfg.AdoptHandoff = inherited.AdoptHandoff
		cfg.AdoptPtmxFD = inherited.AdoptPtmxFD
		cfg.AdoptListenerFD = inherited.AdoptListenerFD

		state := hf.PTY
		state.PtmxFD = inherited.AdoptPtmxFD
		adopt = &state
		logf("worker adopt: session=%s child=%d dump=%dB blocks=%d last_seq=%d blackout=%s",
			cfg.SessionID, state.ChildPID, len(state.VTDump), len(state.Blocks), state.LastSeq,
			time.Since(hf.HandedOverAt).Round(time.Millisecond))
	}
	rt := &Runtime{
		adopt:     adopt,
		adopted:   adopt != nil,
		cfg:       cfg,
		state:     "working",
		stopCh:    make(chan struct{}),
		logf:      logf,
		watchConn: make(map[*connCtx]struct{}),
		orphanTTL: orphanedWorkerTTL,
	}
	if v := strings.TrimSpace(os.Getenv(orphanTTLEnvVar)); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d >= 0 {
			rt.orphanTTL = d
		} else {
			logf("worker orphan ttl: ignoring invalid %s=%q: %v", orphanTTLEnvVar, v, err)
		}
	}
	return rt.run(ctx)
}

func (r *Runtime) run(ctx context.Context) error {
	if err := r.validate(); err != nil {
		return err
	}

	r.manager = pty.NewManager(r.logf)
	r.manager.SetStateHandler(func(_ string, obs pty.Observation) {
		// Evidence-only: it must not touch the cached state or be deduped against
		// it, or a heartbeat is dropped whenever it equals the last state string.
		if !obs.Source.ClaimsProtocolState() {
			r.broadcastLifecycle(stateChangedEvent(r.cfg.SessionID, obs))
			return
		}
		state := obs.Claim
		r.stateMu.Lock()
		previousState := r.state
		changed := previousState != state
		r.state = state
		r.stateMu.Unlock()
		if r.capture != nil {
			r.capture.recordState(state)
			if isWorkingToStopTransition(previousState, state) {
				path, err := r.capture.dump("working_to_" + state)
				if err != nil {
					r.logf("worker debug capture dump failed: session=%s reason=%s err=%v", r.cfg.SessionID, state, err)
				} else if path != "" {
					r.logf("worker debug capture dump: session=%s reason=working_to_%s path=%s", r.cfg.SessionID, state, path)
				}
			}
		}
		if changed {
			r.broadcastLifecycle(stateChangedEvent(r.cfg.SessionID, obs))
		}
	})
	r.manager.SetExitHandler(func(info pty.ExitInfo) {
		r.stateMu.Lock()
		code := info.ExitCode
		r.exitCode = &code
		if info.Signal != "" {
			sig := info.Signal
			r.exitSignal = &sig
		}
		r.stateMu.Unlock()
		if r.capture != nil {
			r.capture.recordNote(fmt.Sprintf("exit code=%d signal=%s", info.ExitCode, info.Signal))
			path, err := r.capture.dump("exit")
			if err != nil {
				r.logf("worker debug capture dump failed: session=%s reason=exit err=%v", r.cfg.SessionID, err)
			} else if path != "" {
				r.logf("worker debug capture dump: session=%s reason=exit path=%s", r.cfg.SessionID, path)
			}
		}
		r.noteSessionExited()
		r.broadcastLifecycle(EventEnvelope{
			Type:       "evt",
			Event:      EventExit,
			SessionID:  r.cfg.SessionID,
			ExitCode:   &code,
			ExitSignal: r.exitSignal,
		})
	})

	if err := os.MkdirAll(filepath.Dir(r.cfg.SocketPath), 0700); err != nil {
		return fmt.Errorf("create socket dir: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(r.cfg.RegistryPath), 0700); err != nil {
		return fmt.Errorf("create registry dir: %w", err)
	}

	r.logf("worker startup: session=%s socket=%s registry=%s", r.cfg.SessionID, r.cfg.SocketPath, r.cfg.RegistryPath)
	var listener net.Listener
	if r.adopted {
		// Inherited, not rebound: rebinding leaves a measured ~12ms hole where a
		// daemon dial fails.
		var err error
		if listener, err = adoptListener(r.cfg.AdoptListenerFD); err != nil {
			return err
		}
	} else {
		_ = os.Remove(r.cfg.SocketPath)
		var err error
		if listener, err = net.Listen("unix", r.cfg.SocketPath); err != nil {
			return fmt.Errorf("listen unix socket: %w", err)
		}
		_ = os.Chmod(r.cfg.SocketPath, 0600)
	}
	r.listener = listener
	r.logf("worker startup: listener ready session=%s socket=%s adopted=%v", r.cfg.SessionID, r.cfg.SocketPath, r.adopted)

	defer func() {
		r.requestStop()
		if r.manager != nil {
			r.manager.Detach(r.cfg.SessionID, debugCaptureSubscriberID)
			r.manager.Detach(r.cfg.SessionID, orphanActivitySubscriberID)
		}
		if r.capture != nil {
			path, err := r.capture.dump("runtime_shutdown")
			if err != nil {
				r.logf("worker debug capture dump failed: session=%s reason=runtime_shutdown err=%v", r.cfg.SessionID, err)
			} else if path != "" {
				r.logf("worker debug capture dump: session=%s reason=runtime_shutdown path=%s", r.cfg.SessionID, path)
			}
		}
		if r.manager != nil {
			r.manager.Shutdown()
		}
		r.cleanup()
	}()

	if r.adopted {
		state := *r.adopt
		r.adopt = nil
		if err := r.manager.Adopt(state); err != nil {
			return fmt.Errorf("adopt PTY session: %w", err)
		}
	} else if err := r.manager.Spawn(pty.SpawnOptions{
		ID:                r.cfg.SessionID,
		CWD:               r.cfg.CWD,
		Agent:             r.cfg.Agent,
		Label:             r.cfg.Label,
		Cols:              r.cfg.Cols,
		Rows:              r.cfg.Rows,
		ResumeSessionID:   r.cfg.ResumeSessionID,
		ResumePicker:      r.cfg.ResumePicker,
		YoloMode:          r.cfg.YoloMode,
		InitialPromptFile: r.cfg.InitialPromptFile,
		Theme: pty.TerminalTheme{
			Foreground:  r.cfg.ThemeForeground,
			Background:  r.cfg.ThemeBackground,
			Cursor:      r.cfg.ThemeCursor,
			ANSIPalette: r.cfg.ThemeANSIPalette,
		},
		Executable:        r.cfg.Executable,
		ClaudeExecutable:  r.cfg.ClaudeExecutable,
		CodexExecutable:   r.cfg.CodexExecutable,
		CopilotExecutable: r.cfg.CopilotExecutable,
		ExternalCommand:   r.cfg.ExternalCommand,
		ExternalEnv:       r.cfg.ExternalEnv,
		ExternalCWD:       r.cfg.ExternalCWD,
		DaemonEnv:         r.cfg.DaemonEnv,
		UnattendedLaunch:  r.cfg.UnattendedLaunch,
	}); err != nil {
		return fmt.Errorf("spawn PTY session: %w", err)
	}
	r.logf("worker startup: pty session ready session=%s adopted=%v", r.cfg.SessionID, r.adopted)
	r.capture = newDebugCapture(r.cfg, r.logf)
	if r.capture != nil {
		r.capture.recordNote("capture enabled")
		r.capture.recordState("working")
		_, err := r.manager.Subscribe(
			r.cfg.SessionID,
			debugCaptureSubscriberID,
			func(data []byte, seq uint32) bool {
				if r.capture != nil {
					r.capture.recordOutput(seq, data)
				}
				return true
			},
			func(reason string) {
				if r.capture != nil {
					r.capture.recordNote("capture subscriber drop: " + reason)
				}
			},
		)
		if err != nil {
			r.logf("worker debug capture attach failed: session=%s err=%v", r.cfg.SessionID, err)
			r.capture = nil
		} else {
			r.logf("worker debug capture enabled: session=%s", r.cfg.SessionID)
		}
	}

	info, err := r.sessionInfo()
	if err != nil {
		return fmt.Errorf("load initial session info: %w", err)
	}
	entry := NewRegistryEntry(
		r.cfg.DaemonInstanceID,
		r.cfg.SessionID,
		os.Getpid(),
		info.PID,
		r.cfg.SocketPath,
		r.cfg.Agent,
		r.cfg.CWD,
		r.cfg.ControlToken,
	)
	entry.OwnerPID = r.cfg.OwnerPID
	entry.OwnerStartedAt = r.cfg.OwnerStartedAt
	entry.OwnerNonce = r.cfg.OwnerNonce
	// The daemon cannot source these otherwise, and a reload would re-spawn with
	// defaults instead of the same yolo/executable.
	entry.LaunchParamsRecorded = true
	entry.YoloMode = r.cfg.YoloMode
	entry.ApprovalRoute = r.cfg.ApprovalRoute
	entry.Executable = r.cfg.Executable
	entry.ClaudeExecutable = r.cfg.ClaudeExecutable
	entry.CodexExecutable = r.cfg.CodexExecutable
	entry.CopilotExecutable = r.cfg.CopilotExecutable
	if r.cfg.UnattendedLaunch.IsZero() {
		entry.Model = strings.TrimSpace(os.Getenv("ATTN_MODEL"))
		entry.Effort = strings.TrimSpace(os.Getenv("ATTN_EFFORT"))
	} else {
		entry.UnattendedLaunch = r.cfg.UnattendedLaunch
	}
	if err := WriteRegistryAtomic(r.cfg.RegistryPath, entry); err != nil {
		return err
	}
	r.logf("worker startup: registry ready session=%s pid=%d child_pid=%d", r.cfg.SessionID, os.Getpid(), info.PID)

	r.noteOutputActivity()
	if _, err := r.manager.Subscribe(
		r.cfg.SessionID,
		orphanActivitySubscriberID,
		func(_ []byte, _ uint32) bool {
			r.noteOutputActivity()
			return true
		},
		func(reason string) {
			r.logf("worker orphan activity subscriber dropped: session=%s reason=%s", r.cfg.SessionID, reason)
		},
	); err != nil {
		r.logf("worker orphan activity attach failed: session=%s err=%v", r.cfg.SessionID, err)
	}
	r.armOrphanWatch()

	go func() {
		<-ctx.Done()
		r.requestStop()
	}()

	for {
		conn, err := listener.Accept()
		if err != nil {
			// An upgrade closes the listener on purpose so new connections queue
			// in the kernel for the image taking over.
			if resumed, ok := r.awaitUpgradeListener(); ok {
				listener = resumed
				continue
			}
			select {
			case <-r.stopCh:
				return nil
			default:
			}
			if isTemporary(err) {
				continue
			}
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return fmt.Errorf("accept worker connection: %w", err)
		}
		go r.handleConn(conn)
	}
}

func (r *Runtime) validate() error {
	if strings.TrimSpace(r.cfg.DaemonInstanceID) == "" {
		return errors.New("missing --daemon-instance-id")
	}
	if strings.TrimSpace(r.cfg.SessionID) == "" {
		return errors.New("missing --session-id")
	}
	if strings.TrimSpace(r.cfg.Agent) == "" {
		return errors.New("missing --agent")
	}
	if strings.TrimSpace(r.cfg.CWD) == "" {
		return errors.New("missing --cwd")
	}
	if !r.cfg.UnattendedLaunch.IsZero() {
		if err := r.cfg.UnattendedLaunch.Validate(); err != nil {
			return err
		}
		if !strings.EqualFold(strings.TrimSpace(r.cfg.Agent), strings.TrimSpace(r.cfg.UnattendedLaunch.Agent)) {
			return fmt.Errorf("unattended launch agent %q does not match worker agent %q", r.cfg.UnattendedLaunch.Agent, r.cfg.Agent)
		}
	}
	if strings.TrimSpace(r.cfg.RegistryPath) == "" {
		return errors.New("missing --registry-path")
	}
	if strings.TrimSpace(r.cfg.SocketPath) == "" {
		return errors.New("missing --socket-path")
	}
	if strings.TrimSpace(r.cfg.ControlToken) == "" {
		return errors.New("missing --control-token")
	}
	if r.cfg.OwnerPID <= 0 {
		return errors.New("missing --owner-pid")
	}
	if strings.TrimSpace(r.cfg.OwnerStartedAt) == "" {
		return errors.New("missing --owner-started-at")
	}
	if strings.TrimSpace(r.cfg.OwnerNonce) == "" {
		return errors.New("missing --owner-nonce")
	}
	if r.cfg.Cols == 0 {
		r.cfg.Cols = 80
	}
	if r.cfg.Rows == 0 {
		r.cfg.Rows = 24
	}
	return nil
}

func (r *Runtime) cleanup() {
	r.lifecycleMu.Lock()
	if r.cleanupTTL != nil {
		r.cleanupTTL.Stop()
		r.cleanupTTL = nil
	}
	r.stopOrphanTimerLocked()
	r.lifecycleMu.Unlock()

	_ = os.Remove(r.cfg.RegistryPath)
	_ = os.Remove(r.cfg.SocketPath)
}

func (r *Runtime) requestStop() {
	r.stopOnce.Do(func() {
		close(r.stopCh)
		if r.listener != nil {
			_ = r.listener.Close()
		}
	})
}

func (r *Runtime) noteSessionExited() {
	r.lifecycleMu.Lock()
	r.exited = true
	r.stopOrphanTimerLocked()
	r.maybeScheduleCleanupLocked()
	r.lifecycleMu.Unlock()
}

func (r *Runtime) noteConnAuthed() {
	r.lifecycleMu.Lock()
	r.authedConns++
	if r.cleanupTTL != nil {
		r.cleanupTTL.Stop()
		r.cleanupTTL = nil
	}
	r.stopOrphanTimerLocked()
	r.lifecycleMu.Unlock()
}

func (r *Runtime) noteConnClosed() {
	r.lifecycleMu.Lock()
	if r.authedConns > 0 {
		r.authedConns--
	}
	r.maybeScheduleCleanupLocked()
	r.maybeScheduleOrphanLocked()
	r.lifecycleMu.Unlock()
}

func (r *Runtime) noteOutputActivity() {
	r.lastOutputNano.Store(time.Now().UnixNano())
}

func (r *Runtime) armOrphanWatch() {
	r.lifecycleMu.Lock()
	r.maybeScheduleOrphanLocked()
	r.lifecycleMu.Unlock()
}

func (r *Runtime) stopOrphanTimerLocked() {
	if r.orphanTimer != nil {
		r.orphanTimer.Stop()
		r.orphanTimer = nil
	}
}

func (r *Runtime) maybeScheduleOrphanLocked() {
	if r.orphanTTL <= 0 || r.exited || r.authedConns != 0 || r.orphanTimer != nil {
		return
	}
	r.orphanTimer = time.AfterFunc(r.orphanTTL, r.orphanDeadlineFired)
}

func (r *Runtime) orphanDeadlineFired() {
	r.lifecycleMu.Lock()
	r.orphanTimer = nil
	if r.exited || r.authedConns != 0 {
		r.lifecycleMu.Unlock()
		return
	}
	idle := time.Since(time.Unix(0, r.lastOutputNano.Load()))
	if idle < r.orphanTTL {
		r.orphanTimer = time.AfterFunc(r.orphanTTL-idle, r.orphanDeadlineFired)
		r.lifecycleMu.Unlock()
		return
	}
	r.lifecycleMu.Unlock()
	if r.logf != nil {
		r.logf("worker orphaned: session=%s no daemon connection and no output for %s; stopping", r.cfg.SessionID, r.orphanTTL)
	}
	r.requestStop()
}

func (r *Runtime) maybeScheduleCleanupLocked() {
	if !r.exited || r.authedConns != 0 || r.cleanupTTL != nil {
		return
	}
	r.cleanupTTL = time.AfterFunc(exitedSessionCleanupTTL, func() {
		r.lifecycleMu.Lock()
		r.cleanupTTL = nil
		shouldStop := r.exited && r.authedConns == 0
		r.lifecycleMu.Unlock()
		if shouldStop {
			r.requestStop()
		}
	})
}

func (r *Runtime) addWatcher(conn *connCtx) {
	r.watchMu.Lock()
	r.watchConn[conn] = struct{}{}
	r.watchMu.Unlock()
}

func (r *Runtime) removeWatcher(conn *connCtx) {
	r.watchMu.Lock()
	delete(r.watchConn, conn)
	r.watchMu.Unlock()
}

func (r *Runtime) broadcastLifecycle(evt EventEnvelope) {
	r.watchMu.RLock()
	targets := make([]*connCtx, 0, len(r.watchConn))
	for watcher := range r.watchConn {
		targets = append(targets, watcher)
	}
	r.watchMu.RUnlock()

	for _, watcher := range targets {
		_ = watcher.sendEvent(evt)
	}
}

type connCtx struct {
	runtime  *Runtime
	conn     net.Conn
	enc      *json.Encoder
	dec      *json.Decoder
	sendMu   sync.RWMutex
	sendQ    chan any
	sendDone chan struct{}
	sendOnce sync.Once
	closed   bool
	connID   string
	authed   bool
	watching bool
	subID    string
	shutdown bool
}

func (r *Runtime) handleConn(conn net.Conn) {
	ctx := &connCtx{
		runtime:  r,
		conn:     conn,
		enc:      json.NewEncoder(conn),
		dec:      json.NewDecoder(conn),
		sendQ:    make(chan any, connSendQueueSize),
		sendDone: make(chan struct{}),
		connID:   strconv.FormatUint(r.connSeq.Add(1), 10),
	}
	go ctx.writeLoop()
	defer func() {
		if ctx.subID != "" {
			r.manager.Detach(r.cfg.SessionID, ctx.subID)
		}
		if ctx.watching {
			r.removeWatcher(ctx)
		}
		if ctx.authed {
			r.noteConnClosed()
		}
		ctx.closeSend()
		<-ctx.sendDone
		_ = conn.Close()
	}()

	for {
		readTimeout, useDeadline := ctx.nextReadTimeout()
		if useDeadline {
			_ = conn.SetReadDeadline(time.Now().Add(readTimeout))
		} else {
			// Attach/watch are server-push and idle for long periods: no deadline.
			_ = conn.SetReadDeadline(time.Time{})
		}

		var req RequestEnvelope
		if err := ctx.dec.Decode(&req); err != nil {
			var netErr net.Error
			if errors.As(err, &netErr) && netErr.Timeout() {
				ctx.runtime.logf("worker conn read timeout: conn=%s authed=%v", ctx.connID, ctx.authed)
			}
			ctx.runtime.logf("worker conn decode error: conn=%s err=%v", ctx.connID, err)
			return
		}
		if req.Type != "req" {
			ctx.sendError(req.ID, ErrBadRequest, "request type must be req")
			continue
		}
		ctx.handleRequest(req)
		if ctx.shutdown {
			return
		}
	}
}

func (c *connCtx) nextReadTimeout() (time.Duration, bool) {
	if !c.authed {
		return connHelloTimeout, true
	}
	if c.subID != "" || c.watching {
		return 0, false
	}
	return connIdleReadTimeout, true
}

func (c *connCtx) writeLoop() {
	defer close(c.sendDone)
	for msg := range c.sendQ {
		_ = c.conn.SetWriteDeadline(time.Now().Add(connWriteTimeout))
		if err := c.enc.Encode(msg); err != nil {
			c.runtime.logf("worker conn write error: conn=%s err=%v", c.connID, err)
			c.closeSend()
			return
		}
	}
}

func (c *connCtx) closeSend() {
	c.sendOnce.Do(func() {
		c.sendMu.Lock()
		c.closed = true
		close(c.sendQ)
		c.sendMu.Unlock()
	})
}

func (c *connCtx) enqueue(v any, wait time.Duration) bool {
	c.sendMu.RLock()
	defer c.sendMu.RUnlock()
	if c.closed {
		c.runtime.logf("worker conn enqueue rejected: conn=%s closed=true type=%T", c.connID, v)
		return false
	}
	if wait <= 0 {
		select {
		case c.sendQ <- v:
			return true
		default:
			c.runtime.logf("worker conn enqueue dropped: conn=%s wait=0 type=%T", c.connID, v)
			return false
		}
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case c.sendQ <- v:
		return true
	case <-timer.C:
		c.runtime.logf("worker conn enqueue timeout: conn=%s wait=%s type=%T", c.connID, wait, v)
		return false
	}
}

func (c *connCtx) handleRequest(req RequestEnvelope) {
	switch req.Method {
	case MethodHello:
		var params HelloParams
		if err := json.Unmarshal(req.Params, &params); err != nil {
			c.sendError(req.ID, ErrBadRequest, "invalid hello params")
			return
		}
		if !IsCompatibleVersion(params.RPCMajor, params.RPCMinor) {
			c.runtime.logf("worker conn hello version mismatch: conn=%s got=%d.%d", c.connID, params.RPCMajor, params.RPCMinor)
			c.sendError(
				req.ID,
				ErrUnsupportedVersion,
				fmt.Sprintf(
					"rpc version incompatible: got=%d.%d supported=%d.%d..%d.%d",
					params.RPCMajor, params.RPCMinor,
					RPCMajor, MinCompatibleRPCMinor,
					RPCMajor, RPCMinor,
				),
			)
			c.shutdown = true
			return
		}
		if params.DaemonInstanceID != c.runtime.cfg.DaemonInstanceID || params.ControlToken != c.runtime.cfg.ControlToken {
			c.runtime.logf("worker conn hello unauthorized: conn=%s", c.connID)
			c.sendError(req.ID, ErrUnauthorized, "daemon identity or control token mismatch")
			c.shutdown = true
			return
		}
		if !c.authed {
			c.authed = true
			c.runtime.noteConnAuthed()
			c.runtime.logf("worker conn authed: conn=%s", c.connID)
		}
		c.sendResult(req.ID, HelloResult{
			WorkerVersion:    "attn",
			RPCMajor:         RPCMajor,
			RPCMinor:         RPCMinor,
			DaemonInstanceID: c.runtime.cfg.DaemonInstanceID,
			SessionID:        c.runtime.cfg.SessionID,
			SnapshotFormat:   buildinfo.SnapshotFormat,
		})
		return
	}

	if !c.authed {
		c.runtime.logf("worker conn request before hello: conn=%s method=%s", c.connID, req.Method)
		c.sendError(req.ID, ErrUnauthorized, "hello required before method calls")
		c.shutdown = true
		return
	}

	switch req.Method {
	case MethodInfo:
		info, err := c.runtime.infoResult()
		if err != nil {
			c.sendError(req.ID, ErrInternal, err.Error())
			return
		}
		c.sendResult(req.ID, info)
	case MethodScreenSnapshot:
		info, err := c.runtime.manager.ScreenSnapshot(c.runtime.cfg.SessionID)
		if err != nil {
			if errors.Is(err, pty.ErrSessionNotFound) {
				c.sendError(req.ID, ErrSessionNotFound, err.Error())
				return
			}
			c.sendError(req.ID, ErrInternal, err.Error())
			return
		}
		result := ScreenSnapshotResult{
			LastSeq: info.LastSeq,
			Cols:    info.Cols,
			Rows:    info.Rows,
			Running: info.Running,
		}
		if info.Screen != nil {
			result.ScreenSnapshot = info.Screen.Payload
			if info.Screen.HasText {
				text := info.Screen.Text
				result.ScreenText = &text
			}
			result.ScreenCols = info.Screen.Cols
			result.ScreenRows = info.Screen.Rows
		}
		c.sendResult(req.ID, result)
	case MethodAttach:
		var params AttachParams
		if len(req.Params) > 0 {
			if err := json.Unmarshal(req.Params, &params); err != nil {
				c.sendError(req.ID, ErrBadRequest, "invalid attach params")
				return
			}
		}
		if c.subID != "" {
			c.runtime.manager.Detach(c.runtime.cfg.SessionID, c.subID)
		}
		subID := strings.TrimSpace(params.SubscriberID)
		if subID == "" {
			subID = "conn-" + c.connID
		}
		send := func(data []byte, seq uint32) bool {
			// One call per output chunk: an ungated log line here grows the
			// per-session .log without bound.
			if c.runtime.cfg.Debug {
				c.runtime.logf(
					"worker output event: session=%s conn=%s sub=%s seq=%d bytes=%d preview=%q",
					c.runtime.cfg.SessionID,
					c.connID,
					subID,
					seq,
					len(data),
					previewWorkerBytesForLog(data),
				)
			}
			encoded := base64.StdEncoding.EncodeToString(data)
			ok := c.sendEvent(EventEnvelope{
				Type:      "evt",
				Event:     EventOutput,
				SessionID: c.runtime.cfg.SessionID,
				Seq:       &seq,
				Data:      &encoded,
			})
			if !ok {
				c.runtime.logf(
					"worker output forward failed: session=%s conn=%s sub=%s seq=%d",
					c.runtime.cfg.SessionID,
					c.connID,
					subID,
					seq,
				)
			}
			return ok
		}
		onDrop := func(reason string) {
			c.runtime.logf(
				"worker output desync: session=%s conn=%s sub=%s reason=%s",
				c.runtime.cfg.SessionID,
				c.connID,
				subID,
				reason,
			)
			if !c.sendEvent(EventEnvelope{
				Type:      "evt",
				Event:     EventDesync,
				SessionID: c.runtime.cfg.SessionID,
				Reason:    &reason,
			}) {
				c.runtime.logf(
					"worker output desync forward failed: session=%s conn=%s sub=%s reason=%s",
					c.runtime.cfg.SessionID,
					c.connID,
					subID,
					reason,
				)
			}
		}
		// Placements ride this connection's own queue, which keeps a set ordered
		// behind the output event carrying the same seq. Success must not log.
		onPlacements := pty.OnPlacements(func(update pty.PlacementUpdate) {
			seq := update.Seq
			if !c.sendEvent(EventEnvelope{
				Type:       "evt",
				Event:      EventKittyPlacements,
				SessionID:  c.runtime.cfg.SessionID,
				Seq:        &seq,
				Placements: placementsToWire(update.Placements),
			}) {
				c.runtime.logf(
					"worker kitty placements forward failed: session=%s conn=%s sub=%s seq=%d",
					c.runtime.cfg.SessionID,
					c.connID,
					subID,
					update.Seq,
				)
			}
		})
		var info pty.AttachInfo
		var err error
		if params.OmitReplay {
			info, err = c.runtime.manager.Subscribe(
				c.runtime.cfg.SessionID, subID, send, onDrop, onPlacements,
			)
		} else {
			info, err = c.runtime.manager.Attach(
				c.runtime.cfg.SessionID, subID, send, onDrop, onPlacements,
			)
		}
		if err != nil {
			if errors.Is(err, pty.ErrSessionNotFound) {
				c.sendError(req.ID, ErrSessionNotFound, err.Error())
				return
			}
			c.sendError(req.ID, ErrInternal, err.Error())
			return
		}
		c.subID = subID
		c.runtime.logf("worker conn attached: conn=%s sub=%s", c.connID, subID)
		c.sendResult(req.ID, AttachResult{
			LastSeq:                    info.LastSeq,
			Cols:                       info.Cols,
			Rows:                       info.Rows,
			PID:                        info.PID,
			Running:                    info.Running,
			ExitCode:                   info.ExitCode,
			ExitSignal:                 info.ExitSignal,
			GhosttySnapshot:            info.GhosttySnapshot,
			GhosttySnapshotFormat:      info.GhosttySnapshotFormat,
			GhosttyBlocks:              attachBlocksToWire(info.GhosttyBlocks),
			GhosttyPlacements:          placementsToWire(info.GhosttyPlacements),
			GhosttyScrollbackTruncated: info.GhosttyScrollbackTruncated,
		})
	case MethodDetach:
		if c.subID != "" {
			c.runtime.manager.Detach(c.runtime.cfg.SessionID, c.subID)
			c.runtime.logf("worker conn detached: conn=%s sub=%s", c.connID, c.subID)
			c.subID = ""
		}
		c.sendResult(req.ID, map[string]any{"ok": true})
	case MethodInput:
		var params InputParams
		if err := json.Unmarshal(req.Params, &params); err != nil {
			c.sendError(req.ID, ErrBadRequest, "invalid input params")
			return
		}
		data, err := base64.StdEncoding.DecodeString(params.Data)
		if err != nil {
			c.sendError(req.ID, ErrBadRequest, "invalid base64 input payload")
			return
		}
		if c.runtime.capture != nil {
			c.runtime.capture.recordInput(data)
		}
		if err := c.runtime.manager.Input(c.runtime.cfg.SessionID, data); err != nil {
			if errors.Is(err, pty.ErrSessionNotFound) {
				c.sendError(req.ID, ErrSessionNotFound, err.Error())
				return
			}
			if strings.Contains(strings.ToLower(err.Error()), "not running") {
				c.sendError(req.ID, ErrSessionNotRunning, err.Error())
				return
			}
			c.sendError(req.ID, ErrIO, err.Error())
			return
		}
		c.sendResult(req.ID, map[string]any{"ok": true})
	case MethodResize:
		var params ResizeParams
		if err := json.Unmarshal(req.Params, &params); err != nil {
			c.sendError(req.ID, ErrBadRequest, "invalid resize params")
			return
		}
		if params.Cols == 0 || params.Rows == 0 {
			c.sendError(req.ID, ErrBadRequest, "cols and rows must be > 0")
			return
		}
		changed, err := c.runtime.manager.Resize(c.runtime.cfg.SessionID, params.Cols, params.Rows, params.XPixel, params.YPixel)
		if err != nil {
			if errors.Is(err, pty.ErrSessionNotFound) {
				c.sendError(req.ID, ErrSessionNotFound, err.Error())
				return
			}
			c.sendError(req.ID, ErrIO, err.Error())
			return
		}
		c.sendResult(req.ID, ResizeResult{OK: true, Changed: &changed})
	case MethodSetTheme:
		var params SetThemeParams
		if err := json.Unmarshal(req.Params, &params); err != nil {
			c.sendError(req.ID, ErrBadRequest, "invalid set_theme params")
			return
		}
		if err := c.runtime.manager.SetTheme(c.runtime.cfg.SessionID, pty.TerminalTheme{
			Foreground:  params.Foreground,
			Background:  params.Background,
			Cursor:      params.Cursor,
			ANSIPalette: params.ANSIPalette,
		}); err != nil {
			if errors.Is(err, pty.ErrSessionNotFound) {
				c.sendError(req.ID, ErrSessionNotFound, err.Error())
				return
			}
			c.sendError(req.ID, ErrIO, err.Error())
			return
		}
		c.sendResult(req.ID, map[string]any{"ok": true})
	case MethodKittyImage:
		var params KittyImageParams
		if err := json.Unmarshal(req.Params, &params); err != nil {
			c.sendError(req.ID, ErrBadRequest, "invalid kitty_image params")
			return
		}
		img, err := c.runtime.manager.KittyImage(c.runtime.cfg.SessionID, params.ImageID)
		if err != nil {
			switch {
			case errors.Is(err, pty.ErrSessionNotFound):
				c.sendError(req.ID, ErrSessionNotFound, err.Error())
			case errors.Is(err, pty.ErrKittyImageNotFound):
				c.sendError(req.ID, ErrImageNotFound, err.Error())
			default:
				c.sendError(req.ID, ErrInternal, err.Error())
			}
			return
		}
		result, err := kittyImageToWire(img)
		if err != nil {
			c.sendError(req.ID, ErrInternal, err.Error())
			return
		}
		c.runtime.logf(
			"worker kitty image: session=%s conn=%s image=%d %dx%d format=%s bytes=%d",
			c.runtime.cfg.SessionID,
			c.connID,
			result.ImageID,
			result.Width,
			result.Height,
			result.Format,
			len(img.Data),
		)
		c.sendResult(req.ID, result)
	case MethodUpgrade:
		var params UpgradeParams
		if err := json.Unmarshal(req.Params, &params); err != nil {
			c.sendError(req.ID, ErrBadRequest, "invalid upgrade params")
			return
		}
		c.runtime.handleUpgrade(c, req.ID, params)
	case MethodSignal:
		var params SignalParams
		if err := json.Unmarshal(req.Params, &params); err != nil {
			c.sendError(req.ID, ErrBadRequest, "invalid signal params")
			return
		}
		sig := parseSignal(params.Signal)
		if err := c.runtime.manager.Kill(c.runtime.cfg.SessionID, sig); err != nil {
			if errors.Is(err, pty.ErrSessionNotFound) {
				c.sendError(req.ID, ErrSessionNotFound, err.Error())
				return
			}
			c.sendError(req.ID, ErrIO, err.Error())
			return
		}
		c.sendResult(req.ID, map[string]any{"ok": true})
	case MethodRemove:
		// Respond before killing: Kill waits up to defaultKillTimeout (10s),
		// past the daemon's 5s RPC timeout, and probes then read "i/o timeout".
		c.sendResult(req.ID, map[string]any{"ok": true})
		c.shutdown = true
		_ = c.runtime.manager.Kill(c.runtime.cfg.SessionID, syscall.SIGTERM)
		c.runtime.manager.Remove(c.runtime.cfg.SessionID)
		c.runtime.requestStop()
	case MethodWatch:
		if !c.watching {
			c.watching = true
			c.runtime.addWatcher(c)
			c.runtime.logf("worker conn lifecycle watch enabled: conn=%s", c.connID)
		}
		c.sendResult(req.ID, map[string]any{"ok": true})

		c.runtime.stateMu.RLock()
		state := c.runtime.state
		exitCode := c.runtime.exitCode
		exitSignal := c.runtime.exitSignal
		c.runtime.stateMu.RUnlock()
		if state == "" {
			state = "working"
		}
		// Replays the cached state; not a fresh terminal observation.
		_ = c.sendEvent(stateChangedEvent(c.runtime.cfg.SessionID, pty.Observation{
			Source: pty.SourceWorkerInfo,
			Claim:  state,
			Detail: "watch subscribe replay",
			At:     time.Now(),
		}))
		if exitCode != nil || exitSignal != nil {
			_ = c.sendEvent(EventEnvelope{
				Type:       "evt",
				Event:      EventExit,
				SessionID:  c.runtime.cfg.SessionID,
				ExitCode:   exitCode,
				ExitSignal: exitSignal,
			})
		}
	case MethodHealth:
		info, err := c.runtime.infoResult()
		if err != nil {
			c.sendError(req.ID, ErrInternal, err.Error())
			return
		}
		c.sendResult(req.ID, map[string]any{
			"ok":      true,
			"running": info.Running,
		})
	default:
		c.sendError(req.ID, ErrBadRequest, "unknown method")
	}
}

func (c *connCtx) sendResult(reqID string, result any) bool {
	payload, err := json.Marshal(result)
	if err != nil {
		return c.sendError(reqID, ErrInternal, err.Error())
	}
	ok := c.send(ResponseEnvelope{Type: "res", ID: reqID, OK: true, Result: payload})
	if !ok {
		c.shutdown = true
	}
	return ok
}

func (c *connCtx) sendError(reqID, code, msg string) bool {
	ok := c.send(ResponseEnvelope{
		Type:  "res",
		ID:    reqID,
		OK:    false,
		Error: &RPCError{Code: code, Message: msg},
	})
	if !ok {
		c.shutdown = true
	}
	return ok
}

func (c *connCtx) sendEvent(evt EventEnvelope) bool {
	return c.enqueue(evt, connWriteTimeout)
}

func (c *connCtx) send(v any) bool {
	return c.enqueue(v, connResponseSendTimeout)
}

func (r *Runtime) sessionInfo() (pty.SessionInfo, error) {
	return r.manager.SessionInfo(r.cfg.SessionID)
}

func (r *Runtime) infoResult() (InfoResult, error) {
	info, err := r.sessionInfo()
	if err != nil {
		return InfoResult{}, err
	}
	r.stateMu.RLock()
	state := r.state
	exitCode := r.exitCode
	exitSignal := r.exitSignal
	r.stateMu.RUnlock()

	// State stays empty when nothing set it: a default here reads downstream as
	// a claim, and stamped every recovered session `working` on a daemon restart.
	result := InfoResult{
		Running:   info.Running,
		Agent:     r.cfg.Agent,
		CWD:       r.cfg.CWD,
		Cols:      info.Cols,
		Rows:      info.Rows,
		WorkerPID: os.Getpid(),
		ChildPID:  info.PID,
		LastSeq:   info.LastSeq,
		State:     state,
	}
	if signal, ok := r.manager.LastSignal(r.cfg.SessionID); ok {
		result.LastSignalClaim = signal.Claim
		result.LastSignalDetail = signal.Detail
		result.LastSignalSource = string(signal.Source)
		result.LastSignalAt = signal.At.Format(time.RFC3339Nano)
	}
	if exitCode != nil {
		code := *exitCode
		result.ExitCode = &code
	}
	if exitSignal != nil {
		sig := *exitSignal
		result.ExitSignal = &sig
	}
	if info.ExitCode != nil {
		result.ExitCode = info.ExitCode
	}
	if info.ExitSignal != nil {
		result.ExitSignal = info.ExitSignal
	}
	return result, nil
}

func parseSignal(name string) syscall.Signal {
	switch strings.ToUpper(strings.TrimSpace(name)) {
	case "", "SIGTERM", "TERM":
		return syscall.SIGTERM
	case "SIGINT", "INT":
		return syscall.SIGINT
	case "SIGHUP", "HUP":
		return syscall.SIGHUP
	case "SIGKILL", "KILL":
		return syscall.SIGKILL
	default:
		return syscall.SIGTERM
	}
}

func isTemporary(err error) bool {
	var netErr net.Error
	if errors.As(err, &netErr) {
		return netErr.Temporary()
	}
	return false
}

// CLOEXEC is cleared so the socket crosses the execve still bound and queueing.
// Must run before the session is captured, or dials get "session not found".
func (r *Runtime) pauseAccept() (int, error) {
	r.upgradeMu.Lock()
	defer r.upgradeMu.Unlock()
	if r.upgradeResume != nil {
		return 0, fmt.Errorf("an upgrade is already in progress")
	}
	fd, err := dupListener(r.listener)
	if err != nil {
		return 0, err
	}
	r.upgradeResume = make(chan net.Listener, 1)
	// The dup above keeps the socket open, backlog and all.
	_ = r.listener.Close()
	return fd, nil
}

func (r *Runtime) resumeAccept(fd int) {
	listener, err := adoptListener(fd)
	if err != nil {
		r.logf("worker upgrade: cannot resume accepting session=%s err=%v", r.cfg.SessionID, err)
		_ = syscall.Close(fd)
		r.requestStop()
		return
	}
	r.upgradeMu.Lock()
	resume := r.upgradeResume
	r.upgradeResume = nil
	r.listener = listener
	r.upgradeMu.Unlock()
	if resume != nil {
		resume <- listener
	}
}

// Reports false when no upgrade is pausing accepts, which is every other reason
// Accept can fail.
func (r *Runtime) awaitUpgradeListener() (net.Listener, bool) {
	r.upgradeMu.Lock()
	resume := r.upgradeResume
	r.upgradeMu.Unlock()
	if resume == nil {
		return nil, false
	}
	select {
	case listener := <-resume:
		return listener, true
	case <-r.stopCh:
		return nil, false
	}
}
