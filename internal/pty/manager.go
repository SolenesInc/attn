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
	"strings"
	"sync"
	"syscall"
	"time"

	creackpty "github.com/creack/pty"
	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/ghosttyvt"
	"github.com/victorarias/attn/internal/launchenv"
	"github.com/victorarias/attn/internal/pausepoint"
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

	Cols uint16
	Rows uint16

	AgentLoginShell bool
	AgentArgv       []string
	AgentEnv        []string
	ExternalCWD     string
	DaemonEnv       []string
	LifecycleID     string

	LoginShellEnv []string

	Theme TerminalTheme
}

func (opts SpawnOptions) WorkingDirectory() string {
	if strings.TrimSpace(opts.ExternalCWD) != "" {
		return opts.ExternalCWD
	}
	return opts.CWD
}

type ViewportSnapshot struct {
	Payload []byte
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
	Screen  *ViewportSnapshot
}

type AttachInfo struct {
	LastSeq                    uint32
	Cols                       uint16
	Rows                       uint16
	PID                        int
	Running                    bool
	ExitCode                   *int
	ExitSignal                 *string
	GhosttySnapshot            []byte
	GhosttySnapshotFormat      string
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
	onTitle       func(sessionID, title string)

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

func (m *Manager) SetTitleHandler(handler func(sessionID, title string)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onTitle = handler
}

func (m *Manager) Title(sessionID string) string {
	s, err := m.getSession(sessionID)
	if err != nil {
		return ""
	}
	s.signalMu.Lock()
	defer s.signalMu.Unlock()
	if s.shellSignals == nil {
		return ""
	}
	s.shellSignals.mu.Lock()
	defer s.shellSignals.mu.Unlock()
	return s.shellSignals.title.Title()
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

	if opts.Cols == 0 {
		opts.Cols = 80
	}
	if opts.Rows == 0 {
		opts.Rows = 24
	}

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

	prepared, err := PrepareLaunch(opts, m.logf)
	if err != nil {
		return err
	}

	var (
		cmd        *exec.Cmd
		ptmx       *os.File
		lastErr    error
		usedShell  string
		overlayDir string
	)
	startedAttempt := -1
	for i, attempt := range prepared.Attempts {
		cmd = commandFromPrepared(attempt)
		overlayDir = attempt.CleanupDir
		ptmx, lastErr = creackpty.StartWithSize(cmd, &creackpty.Winsize{
			Cols: opts.Cols,
			Rows: opts.Rows,
		})
		if lastErr == nil {
			usedShell = attempt.ShellPath
			startedAttempt = i
			break
		}
		removeShellOverlay(overlayDir)
		overlayDir = ""

		if i < len(prepared.Attempts)-1 && shouldFallbackShell(lastErr) {
			m.logf("pty spawn: failed with shell=%s id=%s err=%v; trying fallback shell", attempt.ShellPath, opts.ID, lastErr)
			continue
		}
		prepared.CleanupExcept(-1)
		return fmt.Errorf("spawn session %s: %w", opts.ID, lastErr)
	}
	prepared.CleanupExcept(startedAttempt)
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
	if usedShell != "" && usedShell != prepared.Attempts[0].ShellPath {
		m.logf("pty spawn: using fallback shell=%s (preferred=%s) id=%s", usedShell, prepared.Attempts[0].ShellPath, opts.ID)
	}

	session := &Session{
		id:             opts.ID,
		promptExpected: ShellIntegrationExpected(usedShell, cmd.Env),
		cwd:            opts.CWD,
		agent:          prepared.Agent,
		cols:           opts.Cols,
		rows:           opts.Rows,
		ptmx:           ptmx,
		child:          &childProcess{cmd: cmd, pid: cmd.Process.Pid},
		subscribers:    make(map[string]*sessionSubscriber),
		running:        true,
		exited:         make(chan struct{}),
		quiesced:       make(chan struct{}),
		startedAt:      time.Now(),
		theme:          opts.Theme,
		cleanupDir:     overlayDir,
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
	session.kittyEpoch = mintKittyEpoch()
	session.wireFeed = newWireFeeder(gt, session.kittyEpoch, m.logf, kittyLimit)

	m.logf("pty spawn: id=%s agent=%s cwd=%s pid=%d", opts.ID, prepared.Agent, opts.CWD, cmd.Process.Pid)
	m.start(session, opts.LifecycleID)
	return nil
}

func (m *Manager) start(session *Session, lifecycleID string) {
	session.lifecycleID = lifecycleID
	m.mu.Lock()
	m.sessions[session.id] = session
	onExit := m.onExit
	onState := m.onState
	onTitle := m.onTitle
	m.mu.Unlock()

	session.harnessSignals = newHarnessSignalObserver(agentHarnessSignals(session.agent))
	isShellPane := session.agent == "shell"
	session.programStatus = newProgramStatusObserver(session.lastSignal)
	if onState != nil {
		id := session.id
		session.onState = func(obs Observation) {
			onState(id, obs)
		}
	}
	if isShellPane {
		if session.shellSignals == nil {
			session.shellSignals = newShellSignalArbiter(session.childProcessGroup())
			session.shellSignals.title.Folder = session.cwd
		}
		session.shellSignals.foreground = session.foregroundProcessGroup
		if onTitle != nil {
			session.shellSignals.onTitle = func(title string) { onTitle(session.id, title) }
			onTitle(session.id, session.shellSignals.title.Title())
		}
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

func (m *Manager) Input(ctx context.Context, sessionID string, data []byte) error {
	session, err := m.getSession(sessionID)
	if err != nil {
		return err
	}
	return session.input(ctx, data)
}

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

func (m *Manager) LifecycleID(sessionID string) (string, bool) {
	session, err := m.getSession(sessionID)
	if err != nil {
		return "", false
	}
	return session.lifecycleID, true
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

func buildSpawnCommand(opts SpawnOptions, agent, shellPath string, env []string) *exec.Cmd {
	if agent == "shell" {
		return exec.Command(shellPath, "-l")
	}
	if opts.AgentLoginShell {
		overlay := MergeEnvironment(pathOverlay(env), opts.AgentEnv)
		return loginShellCommand(shellPath, opts.AgentArgv, overlay)
	}
	command := opts.AgentArgv[0]
	if resolved, ok := ResolveCommandPath(command, env); ok {
		command = resolved
	}
	return exec.Command(command, opts.AgentArgv[1:]...)
}

func pathOverlay(env []string) []string {
	for _, entry := range env {
		if strings.HasPrefix(entry, "PATH=") {
			return []string{entry}
		}
	}
	return nil
}

func loginShellCommand(shellPath string, argv, overlay []string) *exec.Cmd {
	args := []string{"-l", "-c"}
	if filepath.Base(shellPath) == "fish" {
		args = append(args, "exec $argv")
	} else {
		args = append(args, `exec "$@"`, "attn-agent")
	}
	args = append(args, "/usr/bin/env")
	args = append(args, overlay...)
	args = append(args, "/bin/sh", "-c", `exec "$@"`, "attn-agent")
	args = append(args, argv...)
	return exec.Command(shellPath, args...)
}

func ResolveCommandPath(command string, env []string) (string, bool) {
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

func buildSpawnEnv(loginShell string, opts SpawnOptions, agent, wrapperPath string, logf LogFunc) []string {
	env := os.Environ()

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
	env = filterEnvKeys(env, "ATTN_DAEMON_MANAGED", "ATTN_AUTO_APPROVE", "ATTN_TRUST_WORKING_DIRECTORY", "ATTN_MODEL", "ATTN_EFFORT", "ATTN_AUTO_COMPACT_WINDOW", "ATTN_CHIEF_AUTO_COMPACT_WINDOW", "ATTN_PTY_WORKER", "ATTN_CACHED_SHELL_ENV", "ATTN_PTY_DAEMON_ENV",
		pausepoint.EnvPoints, pausepoint.EnvSocket)

	env = config.WithoutAgentSessionIdentityEnv(env)

	env = filterEnvKeys(env, "NO_COLOR")

	env = filterEnvKeys(env, "TERM_PROGRAM_VERSION")
	env = MergeEnvironment(env, []string{"TERM=xterm-256color", "TERM_PROGRAM=ghostty"})
	env = launchenv.WithActiveAttnFirst(env, wrapperPath)
	env = filterEnvKeys(env, "ATTN_TERMINAL_ID", "ATTN_SESSION_ID", "ATTN_AGENT")
	if agent != "shell" {
		env = MergeEnvironment(env, []string{
			"ATTN_INSIDE_APP=1",
			"ATTN_TERMINAL_ID=" + opts.ID,
			"ATTN_AGENT=" + agent,
		})
		if wrapperPath != "" {
			env = MergeEnvironment(env, []string{"ATTN_WRAPPER_PATH=" + wrapperPath})
		}
	}
	if len(opts.AgentEnv) > 0 {
		env = MergeEnvironment(env, opts.AgentEnv)
	}
	env = filterEnvKeys(env, "ATTN_DAEMON_MANAGED", "ATTN_AUTO_APPROVE", "ATTN_TRUST_WORKING_DIRECTORY", "ATTN_MODEL", "ATTN_EFFORT", "ATTN_AUTO_COMPACT_WINDOW", "ATTN_CHIEF_AUTO_COMPACT_WINDOW", "ATTN_PTY_WORKER", "ATTN_CACHED_SHELL_ENV", "ATTN_PTY_DAEMON_ENV",
		pausepoint.EnvPoints, pausepoint.EnvSocket)
	env = MergeEnvironment(env, opts.DaemonEnv)
	return env
}

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
