package pty

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	creackpty "github.com/creack/pty"
	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/ghosttyvt"
	"github.com/victorarias/attn/internal/launchcontract"
	"github.com/victorarias/attn/internal/launchenv"
)

const (
	defaultKillTimeout = 10 * time.Second
	shellEnvTimeout    = 2 * time.Second
)

var ErrSessionNotFound = errors.New("session not found")

type LogFunc func(format string, args ...interface{})

type SpawnOptions struct {
	ID    string
	CWD   string
	Agent string
	Label string

	Cols uint16
	Rows uint16

	ResumeSessionID   string
	ResumePicker      bool
	YoloMode          bool
	InitialPromptFile string

	Executable string

	ClaudeExecutable  string
	CodexExecutable   string
	CopilotExecutable string
	ExternalCommand   []string
	ExternalEnv       []string
	ExternalCWD       string
	DaemonEnv         []string
	LifecycleID       string

	LoginShellEnv []string

	WorkflowGuidanceEnabled bool
	AutoApprove             bool
	TrustWorkingDirectory   bool
	Model                   string
	Effort                  string
	ContextWindowCap        int
	UnattendedLaunch        launchcontract.UnattendedLaunchSpec

	// Set explicitly, or a spawn under a non-default theme briefly answers with
	// built-in defaults.
	Theme TerminalTheme
}

type ViewportSnapshot struct {
	Payload []byte
	// Excludes scrollback and styles.
	Text    string
	HasText bool
	Cols    uint16
	Rows    uint16
}

type ScreenSnapshotInfo struct {
	LastSeq uint32
	Cols    uint16
	Rows    uint16
	Running bool
	Screen  *ViewportSnapshot // nil when the terminal has produced no frame
}

type AttachInfo struct {
	LastSeq    uint32
	Cols       uint16
	Rows       uint16
	PID        int
	Running    bool
	ExitCode   *int
	ExitSignal *string
	// Snapshot geometry is Cols/Rows.
	GhosttySnapshot []byte
	// A worker outlives an install, so a snapshot can reach a client built
	// against a different libghostty-vt; the format is how it declines.
	GhosttySnapshotFormat string
	// Rows are SCREEN-space, captured atomically with the dump and LastSeq.
	GhosttyBlocks              []AttachBlockData
	GhosttyPlacements          []KittyPlacement
	GhosttyScrollbackTruncated bool
}

type ExitInfo struct {
	ID          string
	ExitCode    int
	Signal      string
	LifecycleID string
}

type SessionInfo struct {
	SessionID string
	Agent     string
	CWD       string

	Running bool

	Cols    uint16
	Rows    uint16
	PID     int
	LastSeq uint32

	ExitCode   *int
	ExitSignal *string
}

type Manager struct {
	mu            sync.RWMutex
	sessions      map[string]*Session
	pendingSpawns map[string]struct{}
	logf          LogFunc
	onExit        func(ExitInfo)
	onState       func(sessionID string, obs Observation)

	// Test-only seam for deterministic overlap; never set in production.
	testHookAfterSpawnReserve func()
}

func NewManager(logf LogFunc) *Manager {
	if logf == nil {
		logf = func(string, ...interface{}) {}
	}
	return &Manager{
		sessions:      make(map[string]*Session),
		pendingSpawns: make(map[string]struct{}),
		logf:          logf,
	}
}

func (m *Manager) SetExitHandler(handler func(ExitInfo)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onExit = handler
}

func (m *Manager) SetStateHandler(handler func(sessionID string, obs Observation)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onState = handler
}

func agentHarnessSignals(agent string) agentdriver.HarnessSignalKind {
	if d := agentdriver.Get(agent); d != nil {
		return agentdriver.EffectiveCapabilities(d).HarnessSignals
	}
	return agentdriver.HarnessSignalsNone
}

func (m *Manager) Spawn(opts SpawnOptions) error {
	if opts.ID == "" {
		return errors.New("missing session id")
	}
	if opts.CWD == "" {
		return errors.New("missing cwd")
	}
	if !opts.UnattendedLaunch.IsZero() {
		if err := opts.UnattendedLaunch.Validate(); err != nil {
			return err
		}
		if strings.TrimSpace(strings.ToLower(opts.Agent)) != strings.TrimSpace(strings.ToLower(opts.UnattendedLaunch.Agent)) {
			return fmt.Errorf("unattended launch agent %q does not match spawn agent %q", opts.UnattendedLaunch.Agent, opts.Agent)
		}
		if opts.AutoApprove || opts.TrustWorkingDirectory || strings.TrimSpace(opts.Model) != "" ||
			strings.TrimSpace(opts.Effort) != "" || strings.TrimSpace(opts.Executable) != "" {
			return errors.New("unattended launch policy must not be duplicated in spawn options")
		}
	}
	if opts.Cols == 0 {
		opts.Cols = 80
	}
	if opts.Rows == 0 {
		opts.Rows = 24
	}

	agent := normalizeAgent(opts.Agent, len(opts.ExternalCommand) > 0)
	// Every PTY resolves bare `attn` to the installation that launched it;
	// managed-agent identity stays conditional in buildSpawnEnv.
	attnPath := launchenv.ActiveAttnExecutable()

	m.mu.Lock()
	if _, exists := m.sessions[opts.ID]; exists {
		m.mu.Unlock()
		return fmt.Errorf("session %s already exists", opts.ID)
	}
	if _, pending := m.pendingSpawns[opts.ID]; pending {
		m.mu.Unlock()
		return fmt.Errorf("session %s spawn already in progress", opts.ID)
	}
	m.pendingSpawns[opts.ID] = struct{}{}
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		delete(m.pendingSpawns, opts.ID)
		m.mu.Unlock()
	}()
	if m.testHookAfterSpawnReserve != nil {
		m.testHookAfterSpawnReserve()
	}

	loginShell := GetUserLoginShell()
	shellCandidates := preferredShellCandidates(loginShell)
	cmdEnv := buildSpawnEnv(loginShell, opts, agent, attnPath, m.logf)

	var (
		cmd        *exec.Cmd
		ptmx       *os.File
		lastErr    error
		usedShell  string
		overlayDir string
	)
	for i, shellPath := range shellCandidates {
		attemptEnv := cmdEnv
		if agent == "shell" {
			launch, err := prepareShellPaneLaunch(shellPath, cmdEnv)
			if err != nil {
				lastErr = err
				return fmt.Errorf("prepare terminal shell %s: %w", shellPath, err)
			}
			cmd = launch.command
			attemptEnv = launch.env
			overlayDir = launch.overlayDir
		} else {
			cmd = buildSpawnCommand(opts, agent, shellPath, attnPath, cmdEnv)
		}
		cmd.Dir = opts.CWD
		if strings.TrimSpace(opts.ExternalCWD) != "" {
			cmd.Dir = opts.ExternalCWD
		}
		cmd.Env = attemptEnv

		ptmx, lastErr = creackpty.StartWithSize(cmd, &creackpty.Winsize{
			Cols: opts.Cols,
			Rows: opts.Rows,
		})
		if lastErr == nil {
			usedShell = shellPath
			break
		}
		removeShellOverlay(overlayDir)
		overlayDir = ""

		if i < len(shellCandidates)-1 && shouldFallbackShell(lastErr) {
			m.logf("pty spawn: failed with shell=%s id=%s err=%v; trying fallback shell", shellPath, opts.ID, lastErr)
			continue
		}
		return fmt.Errorf("spawn session %s: %w", opts.ID, lastErr)
	}
	pollable, pollErr := pollablePTMX(ptmx)
	if pollErr != nil {
		if cmd != nil && cmd.Process != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
		removeShellOverlay(overlayDir)
		return fmt.Errorf("spawn session %s: %w", opts.ID, pollErr)
	}
	ptmx = pollable
	if usedShell != "" && usedShell != loginShell {
		m.logf("pty spawn: using fallback shell=%s (preferred=%s) id=%s", usedShell, loginShell, opts.ID)
	}

	session := &Session{
		id:          opts.ID,
		cwd:         opts.CWD,
		agent:       agent,
		cols:        opts.Cols,
		rows:        opts.Rows,
		ptmx:        ptmx,
		child:       &childProcess{cmd: cmd, pid: cmd.Process.Pid},
		subscribers: make(map[string]*sessionSubscriber),
		running:     true,
		exited:      make(chan struct{}),
		quiesced:    make(chan struct{}),
		startedAt:   time.Now(),
		theme:       opts.Theme,
		cleanupDir:  overlayDir,
	}
	kittyLimit := kittyStorageLimit(m.logf)
	gt, err := ghosttyvt.New(int(opts.Cols), int(opts.Rows), ghosttyvt.Options{
		KittyImageStorageLimit: kittyLimit,
	})
	if err != nil {
		if ptmx != nil {
			_ = ptmx.Close()
		}
		if cmd != nil && cmd.Process != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
		removeShellOverlay(overlayDir)
		return fmt.Errorf("ghostty terminal construction failed: %w", err)
	}
	session.ghostty = gt
	if err := session.SetTheme(opts.Theme); err != nil {
		gt.Close()
		if ptmx != nil {
			_ = ptmx.Close()
		}
		if cmd != nil && cmd.Process != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
		removeShellOverlay(overlayDir)
		return fmt.Errorf("ghostty terminal theme failed: %w", err)
	}
	// A replacement worker under the same session id gets a different epoch,
	// which stops a client redrawing the dead worker's pixels.
	session.kittyEpoch = mintKittyEpoch()
	session.wireFeed = newWireFeeder(gt, session.kittyEpoch, m.logf, kittyLimit)

	m.logf("pty spawn: id=%s agent=%s cwd=%s pid=%d", opts.ID, agent, opts.CWD, cmd.Process.Pid)
	m.start(session, opts.LifecycleID)
	return nil
}

func (m *Manager) start(session *Session, lifecycleID string) {
	m.mu.Lock()
	m.sessions[session.id] = session
	onExit := m.onExit
	onState := m.onState
	m.mu.Unlock()

	session.harnessSignals = newHarnessSignalObserver(agentHarnessSignals(session.agent))
	isShellPane := session.agent == "shell"
	if (session.harnessSignals != nil || isShellPane) && onState != nil {
		id := session.id
		session.onState = func(obs Observation) {
			onState(id, obs)
		}
	}
	if isShellPane && session.onState != nil {
		session.shellSignals = newShellSignalArbiter(session.childProcessGroup())
		go session.runShellForegroundPoller(shellForegroundPollInterval)
	}

	go session.readLoop(func(exitCode int, signal string) {
		m.logf("pty exited: id=%s code=%d signal=%s", session.id, exitCode, signal)
		if onExit != nil {
			onExit(ExitInfo{ID: session.id, ExitCode: exitCode, Signal: signal, LifecycleID: lifecycleID})
		}
	}, m.logf)
}

func (m *Manager) subscribe(
	sessionID, subscriberID string,
	send func([]byte, uint32) bool,
	onDrop func(reason string),
	opts ...SubscriberOption,
) (*Session, error) {
	session, err := m.getSession(sessionID)
	if err != nil {
		return nil, err
	}
	if send == nil {
		return nil, errors.New("subscriber send callback is required")
	}
	session.addSubscriber(subscriberID, send, onDrop, opts...)
	return session, nil
}

func (m *Manager) Subscribe(
	sessionID, subscriberID string,
	send func([]byte, uint32) bool,
	onDrop func(reason string),
	opts ...SubscriberOption,
) (AttachInfo, error) {
	session, err := m.subscribe(sessionID, subscriberID, send, onDrop, opts...)
	if err != nil {
		return AttachInfo{}, err
	}
	return session.subscriptionInfo(), nil
}

func (m *Manager) Attach(
	sessionID, subscriberID string,
	send func([]byte, uint32) bool,
	onDrop func(reason string),
	opts ...SubscriberOption,
) (AttachInfo, error) {
	session, err := m.subscribe(sessionID, subscriberID, send, onDrop, opts...)
	if err != nil {
		return AttachInfo{}, err
	}
	return session.info(), nil
}

func (m *Manager) KittyImage(sessionID string, imageID uint32) (KittyImage, error) {
	session, err := m.getSession(sessionID)
	if err != nil {
		return KittyImage{}, err
	}
	return session.kittyImage(imageID)
}

func (m *Manager) SetTheme(sessionID string, theme TerminalTheme) error {
	session, err := m.getSession(sessionID)
	if err != nil {
		return err
	}
	return session.SetTheme(theme)
}

func (m *Manager) ScreenSnapshot(sessionID string) (ScreenSnapshotInfo, error) {
	session, err := m.getSession(sessionID)
	if err != nil {
		return ScreenSnapshotInfo{}, err
	}
	return session.screenSnapshot(), nil
}

func (m *Manager) Detach(sessionID, subscriberID string) {
	session, err := m.getSession(sessionID)
	if err != nil {
		return
	}
	session.removeSubscriber(subscriberID)
}

func (m *Manager) Input(sessionID string, data []byte) error {
	session, err := m.getSession(sessionID)
	if err != nil {
		return err
	}
	return session.input(data)
}

// xpixel/ypixel are the pane's total device pixels, 0 when unavailable.
func (m *Manager) Resize(sessionID string, cols, rows, xpixel, ypixel uint16) (bool, error) {
	session, err := m.getSession(sessionID)
	if err != nil {
		return false, err
	}
	session.metaMu.RLock()
	prevCols, prevRows := session.cols, session.rows
	session.metaMu.RUnlock()
	pid := session.child.processID()
	changed, resizeErr := session.resize(cols, rows, xpixel, ypixel)
	if changed || resizeErr != nil {
		m.logf("pty resize: id=%s prev=%dx%d new=%dx%d px=%dx%d pid=%d changed=%v err=%v", sessionID, prevCols, prevRows, cols, rows, xpixel, ypixel, pid, changed, resizeErr)
	}
	return changed, resizeErr
}

func (m *Manager) Kill(sessionID string, sig syscall.Signal) error {
	return m.KillWithEscalation(sessionID, sig, nil)
}

func (m *Manager) KillWithEscalation(sessionID string, sig syscall.Signal, onEscalate func(syscall.Signal)) error {
	session, err := m.getSession(sessionID)
	if err != nil {
		return err
	}
	if session.agent == "shell" && sig == syscall.SIGTERM {
		sig = syscall.SIGHUP
	}
	return session.killWithEscalation(sig, defaultKillTimeout, onEscalate)
}

func (m *Manager) SessionInfo(sessionID string) (SessionInfo, error) {
	session, err := m.getSession(sessionID)
	if err != nil {
		return SessionInfo{}, err
	}

	return session.sessionInfo(), nil
}

func (m *Manager) LastSignal(sessionID string) (Observation, bool) {
	session, err := m.getSession(sessionID)
	if err != nil {
		return Observation{}, false
	}
	return session.LastSignal()
}

func (m *Manager) Remove(sessionID string) {
	m.mu.Lock()
	session, ok := m.sessions[sessionID]
	if ok {
		delete(m.sessions, sessionID)
	}
	m.mu.Unlock()

	if ok {
		session.closePTY()
	}
}

func (m *Manager) Shutdown() {
	m.mu.RLock()
	sessions := make([]*Session, 0, len(m.sessions))
	for _, session := range m.sessions {
		sessions = append(sessions, session)
	}
	m.mu.RUnlock()

	for _, session := range sessions {
		_ = session.kill(syscall.SIGTERM, defaultKillTimeout)
	}
}

func (m *Manager) SessionIDs() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	ids := make([]string, 0, len(m.sessions))
	for id := range m.sessions {
		ids = append(ids, id)
	}
	return ids
}

func (m *Manager) getSession(id string) (*Session, error) {
	m.mu.RLock()
	session, ok := m.sessions[id]
	m.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrSessionNotFound, id)
	}
	return session, nil
}

func normalizeAgent(agent string, external bool) string {
	a := strings.TrimSpace(strings.ToLower(agent))
	if a == "" {
		return "codex"
	}
	if a == "shell" {
		return "shell"
	}
	if agentdriver.Get(a) != nil {
		return a
	}
	if external {
		return a
	}
	return "codex"
}

func buildSpawnCommand(opts SpawnOptions, agent, shellPath, attnPath string, env []string) *exec.Cmd {
	if agent == "shell" {
		return exec.Command(shellPath, "-l")
	}
	if len(opts.ExternalCommand) > 0 {
		command := opts.ExternalCommand[0]
		if resolved, ok := resolveExternalCommandPath(command, env); ok {
			command = resolved
		}
		return exec.Command(command, opts.ExternalCommand[1:]...)
	}

	args := []string{attnPath}
	if opts.Label != "" {
		args = append(args, "-s", opts.Label)
	}
	if opts.ResumeSessionID != "" {
		args = append(args, "--resume", opts.ResumeSessionID)
	} else if opts.ResumePicker {
		args = append(args, "--resume")
	}
	if opts.YoloMode {
		args = append(args, "--yolo")
	}
	if opts.InitialPromptFile != "" {
		args = append(args, "--initial-prompt-file", opts.InitialPromptFile)
	}

	return exec.Command(shellPath, "-l", "-c", postLoginExecCommand(env, args))
}

// Restores the launch PATH after login startup, in case a profile prepends a
// stale attn. Exec'd, so the PTY stays attached to the real child.
func postLoginExecCommand(env []string, args []string) string {
	cmdline := "exec " + shellJoin(args)
	for _, entry := range env {
		if strings.HasPrefix(entry, "PATH=") {
			return "export PATH=" + shellQuote(strings.TrimPrefix(entry, "PATH=")) + "; " + cmdline
		}
	}
	return cmdline
}

func resolveExternalCommandPath(command string, env []string) (string, bool) {
	command = strings.TrimSpace(command)
	if command == "" || strings.ContainsRune(command, filepath.Separator) {
		return "", false
	}
	for _, entry := range env {
		if !strings.HasPrefix(entry, "PATH=") {
			continue
		}
		candidates := make([]string, 0)
		for _, dir := range filepath.SplitList(strings.TrimPrefix(entry, "PATH=")) {
			if dir == "" {
				candidates = append(candidates, "."+string(filepath.Separator)+command)
				continue
			}
			candidates = append(candidates, filepath.Join(dir, command))
		}
		return launchenv.FirstExecutablePath(candidates)
	}
	return "", false
}

func readCachedShellEnvFromProcess() []string {
	raw := os.Getenv("ATTN_CACHED_SHELL_ENV")
	if raw == "" {
		return nil
	}
	var env []string
	if err := json.Unmarshal([]byte(raw), &env); err != nil {
		return nil
	}
	return env
}

// The identity block has a twin in spawnHostSession (internal/daemon); change
// one and change the other, each test-pinned separately.
func buildSpawnEnv(loginShell string, opts SpawnOptions, agent, wrapperPath string, logf LogFunc) []string {
	env := os.Environ()
	launchEnv := []string(nil)
	launchKeys := []string{
		"ATTN_WORKFLOW_GUIDANCE_ENABLED",
		"ATTN_AUTO_APPROVE",
		"ATTN_TRUST_WORKING_DIRECTORY",
		"ATTN_MODEL",
		"ATTN_EFFORT",
		"ATTN_AUTO_COMPACT_WINDOW",
	}
	if os.Getenv("ATTN_PTY_WORKER") == "1" {
		inheritedKeys := launchKeys
		if !opts.UnattendedLaunch.IsZero() {
			inheritedKeys = []string{"ATTN_WORKFLOW_GUIDANCE_ENABLED", "ATTN_AUTO_COMPACT_WINDOW"}
		}
		for _, key := range inheritedKeys {
			if value, ok := os.LookupEnv(key); ok {
				launchEnv = append(launchEnv, key+"="+value)
			}
		}
	}
	if opts.WorkflowGuidanceEnabled {
		launchEnv = append(launchEnv, "ATTN_WORKFLOW_GUIDANCE_ENABLED=1")
	}
	if opts.AutoApprove {
		launchEnv = append(launchEnv, "ATTN_AUTO_APPROVE=1")
	}
	if opts.TrustWorkingDirectory {
		launchEnv = append(launchEnv, "ATTN_TRUST_WORKING_DIRECTORY=1")
	}
	if model := strings.TrimSpace(opts.Model); model != "" {
		launchEnv = append(launchEnv, "ATTN_MODEL="+model)
	}
	if effort := strings.TrimSpace(opts.Effort); effort != "" {
		launchEnv = append(launchEnv, "ATTN_EFFORT="+effort)
	}
	if opts.ContextWindowCap > 0 {
		launchEnv = append(launchEnv, "ATTN_AUTO_COMPACT_WINDOW="+strconv.Itoa(opts.ContextWindowCap))
	}
	if launch := opts.UnattendedLaunch; !launch.IsZero() {
		launchEnv = append(launchEnv,
			"ATTN_AUTO_APPROVE=1",
			"ATTN_TRUST_WORKING_DIRECTORY=1",
		)
		if model := strings.TrimSpace(launch.Model); model != "" {
			launchEnv = append(launchEnv, "ATTN_MODEL="+model)
		}
		if effort := strings.TrimSpace(launch.Effort); effort != "" {
			launchEnv = append(launchEnv, "ATTN_EFFORT="+effort)
		}
	}

	shellEnv := opts.LoginShellEnv
	if len(shellEnv) == 0 {
		shellEnv = readCachedShellEnvFromProcess()
	}
	if len(shellEnv) > 0 {
		env = MergeEnvironment(env, shellEnv)
	} else if loginShell != "" {
		if captured, err := ReadLoginShellEnv(loginShell); err == nil {
			env = MergeEnvironment(env, captured)
		} else if logf != nil {
			logf("pty spawn: failed to capture login shell env from %s: %v", loginShell, err)
		}
	}
	// Cached login-shell data can carry a parent agent's one-shot launch pins.
	env = filterEnvKeys(env, launchKeys...)
	env = MergeEnvironment(env, launchEnv)
	env = filterEnvKeys(env, "ATTN_PTY_WORKER", "ATTN_CACHED_SHELL_ENV", "ATTN_PTY_EXTERNAL_ENV", "ATTN_PTY_DAEMON_ENV")

	// Strip CLAUDECODE after all merges: ReadLoginShellEnv re-captures it from
	// the inherited environment, and spawned sessions then think they're nested.
	env = filterEnvKeys(env, "CLAUDECODE")

	// An agent runner's NO_COLOR would otherwise disable colors in every PTY.
	env = filterEnvKeys(env, "NO_COLOR")

	// TUIs gate OSC 8 hyperlink emission on TERM_PROGRAM.
	env = filterEnvKeys(env, "TERM_PROGRAM_VERSION")
	env = MergeEnvironment(env, []string{"TERM=xterm-256color", "TERM_PROGRAM=ghostty"})
	env = launchenv.WithActiveAttnFirst(env, wrapperPath)
	if agent == "shell" {
		// An inherited managed-session identity would make ordinary shell
		// commands report against another session.
		env = filterEnvKeys(env, "ATTN_SESSION_ID", "ATTN_AGENT")
	}
	if agent != "shell" {
		env = MergeEnvironment(env, []string{
			"ATTN_INSIDE_APP=1",
			"ATTN_DAEMON_MANAGED=1",
			"ATTN_SESSION_ID=" + opts.ID,
			"ATTN_AGENT=" + agent,
		})
		if wrapperPath != "" {
			env = MergeEnvironment(env, []string{"ATTN_WRAPPER_PATH=" + wrapperPath})
		}

		executable := configuredExecutableForAgent(opts, agent)
		if d := agentdriver.Get(agent); d != nil {
			envKey := strings.TrimSpace(d.ExecutableEnvVar())
			if envKey != "" && executable != "" && executable != d.DefaultExecutable() {
				env = MergeEnvironment(env, []string{envKey + "=" + executable})
			}
		} else {
			if opts.ClaudeExecutable != "" && opts.ClaudeExecutable != "claude" {
				env = MergeEnvironment(env, []string{"ATTN_CLAUDE_EXECUTABLE=" + opts.ClaudeExecutable})
			}
			if opts.CodexExecutable != "" && opts.CodexExecutable != "codex" {
				env = MergeEnvironment(env, []string{"ATTN_CODEX_EXECUTABLE=" + opts.CodexExecutable})
			}
			if opts.CopilotExecutable != "" && opts.CopilotExecutable != "copilot" {
				env = MergeEnvironment(env, []string{"ATTN_COPILOT_EXECUTABLE=" + opts.CopilotExecutable})
			}
		}
	}
	if len(opts.ExternalEnv) > 0 {
		env = MergeEnvironment(env, opts.ExternalEnv)
	}
	env = filterEnvKeys(env, "ATTN_PTY_WORKER", "ATTN_CACHED_SHELL_ENV", "ATTN_PTY_EXTERNAL_ENV", "ATTN_PTY_DAEMON_ENV")
	// Routing is the final overlay: no login-shell or plugin variable may
	// redirect the session to another attn daemon.
	env = MergeEnvironment(env, opts.DaemonEnv)
	return env
}

func configuredExecutableForAgent(opts SpawnOptions, agent string) string {
	if !opts.UnattendedLaunch.IsZero() {
		return strings.TrimSpace(opts.UnattendedLaunch.Executable)
	}
	if strings.TrimSpace(opts.Executable) != "" {
		return strings.TrimSpace(opts.Executable)
	}
	switch agent {
	case "claude":
		return strings.TrimSpace(opts.ClaudeExecutable)
	case "codex":
		return strings.TrimSpace(opts.CodexExecutable)
	case "copilot":
		return strings.TrimSpace(opts.CopilotExecutable)
	default:
		return ""
	}
}

// Typically ~130ms; callers should cache the result.
func ReadLoginShellEnv(shellPath string) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), shellEnvTimeout)
	defer cancel()

	args := []string{"-l", "-c", "env -0"}
	if strings.HasSuffix(shellPath, "zsh") {
		args = []string{"-l", "-i", "-c", "env -0"}
	}
	cmd := exec.CommandContext(ctx, shellPath, args...)
	output, err := cmd.Output()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("timeout after %s", shellEnvTimeout)
		}
		return nil, err
	}
	return parseNullSeparatedEnv(output), nil
}

func parseNullSeparatedEnv(output []byte) []string {
	if len(output) == 0 {
		return nil
	}
	parts := strings.Split(string(output), "\x00")
	env := make([]string, 0, len(parts))
	for _, part := range parts {
		if part == "" || !strings.Contains(part, "=") {
			continue
		}
		env = append(env, part)
	}
	return env
}

func MergeEnvironment(base, overlay []string) []string {
	if len(overlay) == 0 {
		return append([]string(nil), base...)
	}
	merged := make([]string, 0, len(base)+len(overlay))
	index := make(map[string]int, len(base)+len(overlay))
	add := func(entry string) {
		key := entry
		if idx := strings.Index(entry, "="); idx >= 0 {
			key = entry[:idx]
		}
		if pos, ok := index[key]; ok {
			merged[pos] = entry
			return
		}
		index[key] = len(merged)
		merged = append(merged, entry)
	}
	for _, entry := range base {
		add(entry)
	}
	for _, entry := range overlay {
		add(entry)
	}
	return merged
}

func filterEnvKeys(env []string, keys ...string) []string {
	drop := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		drop[k] = struct{}{}
	}
	out := make([]string, 0, len(env))
	for _, entry := range env {
		key := entry
		if idx := strings.Index(entry, "="); idx >= 0 {
			key = entry[:idx]
		}
		if _, ok := drop[key]; ok {
			continue
		}
		out = append(out, entry)
	}
	return out
}

func preferredShellCandidates(primary string) []string {
	candidates := make([]string, 0, 4)
	seen := map[string]struct{}{}
	add := func(shell string) {
		shell = strings.TrimSpace(shell)
		if shell == "" {
			return
		}
		if _, ok := seen[shell]; ok {
			return
		}
		seen[shell] = struct{}{}
		candidates = append(candidates, shell)
	}

	add(primary)
	if runtime.GOOS == "darwin" {
		add("/bin/zsh")
		add("/bin/bash")
	} else {
		add("/bin/bash")
	}
	add("/bin/sh")
	return candidates
}

func shouldFallbackShell(err error) bool {
	return errors.Is(err, syscall.EPERM) ||
		errors.Is(err, syscall.EACCES) ||
		errors.Is(err, syscall.ENOENT) ||
		errors.Is(err, exec.ErrNotFound)
}

func shellJoin(args []string) string {
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		quoted = append(quoted, shellQuote(arg))
	}
	return strings.Join(quoted, " ")
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func GetUserLoginShell() string {
	if runtime.GOOS == "darwin" {
		if usr, err := user.Current(); err == nil {
			out, dsclErr := exec.Command("dscl", ".", "-read", "/Users/"+usr.Username, "UserShell").Output()
			if dsclErr == nil {
				for _, line := range strings.Split(string(out), "\n") {
					line = strings.TrimSpace(line)
					if strings.HasPrefix(line, "UserShell:") {
						shell := strings.TrimSpace(strings.TrimPrefix(line, "UserShell:"))
						if shell != "" {
							return shell
						}
					}
				}
			}
		}
	}

	if shell := strings.TrimSpace(os.Getenv("SHELL")); shell != "" {
		return shell
	}

	if usr, err := user.Current(); err == nil {
		if passwd, readErr := os.ReadFile("/etc/passwd"); readErr == nil {
			prefix := usr.Username + ":"
			for _, line := range strings.Split(string(passwd), "\n") {
				if !strings.HasPrefix(line, prefix) {
					continue
				}
				parts := strings.Split(line, ":")
				if len(parts) >= 7 && parts[6] != "" {
					return strings.TrimSpace(parts[6])
				}
			}
		}
	}

	return "/bin/bash"
}
