package daemonctl

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/victorarias/attn/internal/buildinfo"
	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/protocol"
)

const (
	stopTimeout = 5 * time.Second
	readyFDEnv  = "ATTN_DAEMON_READY_FD"
	// Runs 34526737207 and 34537529658 took 13s on github-hosted 4vcpu/16GB; 60s is the startup tripwire.
	startupTimeout = 60 * time.Second
)

var errDaemonAlreadyRunning = errors.New("daemon already running")
var errDaemonLockHeld = errors.New("daemon lock held by another attn process")
var errDaemonStartupTimeout = errors.New("daemon startup tripwire expired")

type StartupSignal struct {
	writer *os.File
}

type daemonProcess struct {
	ready io.ReadCloser
}

type EnsureResult struct {
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

type healthResponse struct {
	Status            string `json:"status"`
	Protocol          string `json:"protocol"`
	Version           string `json:"version"`
	SourceFingerprint string `json:"source_fingerprint"`
	Profile           string `json:"profile"`
	DataDir           string `json:"data_dir"`
	SocketPath        string `json:"socket_path"`
	Port              string `json:"port"`
}

func Ensure(ctx context.Context, binaryPath string) (EnsureResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeoutCause(ctx, startupTimeout, errDaemonStartupTimeout)
	defer cancel()
	return ensureWithTripwire(ctx, binaryPath, ensure, matchingDaemonIsLive)
}

func ensure(ctx context.Context, binaryPath string) (EnsureResult, error) {
	if strings.TrimSpace(binaryPath) == "" {
		return EnsureResult{}, fmt.Errorf("missing binary path")
	}
	if err := config.ValidateDaemonIsolation(config.SocketPath()); err != nil {
		return EnsureResult{}, err
	}
	release, err := acquireEnsureLock(ctx)
	if err != nil {
		return EnsureResult{}, err
	}
	defer release()

	if !isSocketLive(config.SocketPath()) {
		if err := removeStaleSocketFiles(); err != nil {
			return EnsureResult{}, err
		}
		contended, err := startDaemon(ctx, binaryPath)
		if err != nil {
			return EnsureResult{}, err
		}
		if contended {
			return EnsureResult{Status: "already_running"}, nil
		}
		return EnsureResult{Status: "started"}, nil
	}

	health, err := fetchHealth(ctx)
	if err == nil && daemonMatchesCurrentBinary(health) {
		return EnsureResult{Status: "already_running"}, nil
	}

	reason := mismatchReason(err, health)
	if err := stopRunningDaemon(ctx); err != nil {
		return EnsureResult{}, err
	}
	if err := removeStaleSocketFiles(); err != nil {
		return EnsureResult{}, err
	}
	contended, err := startDaemon(ctx, binaryPath)
	if err != nil {
		return EnsureResult{}, err
	}
	if contended {
		return EnsureResult{Status: "already_running"}, nil
	}
	return EnsureResult{Status: "restarted", Reason: reason}, nil
}

func ensureWithTripwire(
	ctx context.Context,
	binaryPath string,
	run func(context.Context, string) (EnsureResult, error),
	reconcile func(context.Context) bool,
) (EnsureResult, error) {
	result, err := run(ctx, binaryPath)
	if err == nil || !errors.Is(context.Cause(ctx), errDaemonStartupTimeout) {
		return result, err
	}
	if reconcile(context.Background()) {
		return EnsureResult{Status: "already_running"}, nil
	}
	return EnsureResult{}, fmt.Errorf("daemon startup wait exceeded %s; inspect %s", startupTimeout, config.LogPath())
}

func acquireEnsureLock(ctx context.Context) (func(), error) {
	lockPath := config.PIDPath() + ".ensure"
	if err := os.MkdirAll(filepath.Dir(lockPath), 0700); err != nil {
		return nil, fmt.Errorf("create daemon data directory: %w", err)
	}
	lockFile, err := os.OpenFile(lockPath, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return nil, fmt.Errorf("open daemon ensure lock: %w", err)
	}
	result := make(chan error, 1)
	go func() {
		result <- syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX)
	}()
	select {
	case err := <-result:
		if err != nil {
			lockFile.Close()
			return nil, fmt.Errorf("acquire daemon ensure lock: %w", err)
		}
		return func() {
			_ = syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)
			_ = lockFile.Close()
		}, nil
	case <-ctx.Done():
		go func() {
			if <-result == nil {
				_ = syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)
			}
			_ = lockFile.Close()
		}()
		return nil, ctx.Err()
	}
}

func daemonMatchesCurrentBinary(health healthResponse) bool {
	// Profile identity is stronger than binary identity: a daemon running under
	// another profile must be restarted even when the fingerprint matches.
	if !profileMatchesCurrent(health) {
		return false
	}
	currentFingerprint := normalizedFingerprint(buildinfo.SourceFingerprint)
	if currentFingerprint != "" {
		return normalizedFingerprint(health.SourceFingerprint) == currentFingerprint
	}
	return strings.TrimSpace(health.Protocol) == protocol.ProtocolVersion
}

func profileMatchesCurrent(health healthResponse) bool {
	expected := config.ProfileLabel()
	// Older daemons predate the profile field; treat an empty profile as "default".
	reported := strings.TrimSpace(health.Profile)
	if reported == "" {
		reported = "default"
	}
	return reported == expected
}

func mismatchReason(healthErr error, health healthResponse) string {
	if healthErr != nil {
		return "health_unavailable"
	}
	if !profileMatchesCurrent(health) {
		return "profile_mismatch"
	}
	currentFingerprint := normalizedFingerprint(buildinfo.SourceFingerprint)
	runningFingerprint := normalizedFingerprint(health.SourceFingerprint)
	if currentFingerprint == "" {
		return "protocol_mismatch"
	}
	if runningFingerprint == "" {
		return "source_fingerprint_missing"
	}
	return "source_fingerprint_mismatch"
}

func normalizedFingerprint(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || trimmed == "unknown" {
		return ""
	}
	return trimmed
}

func spawnDaemon(binaryPath string) (daemonProcess, error) {
	reader, writer, err := os.Pipe()
	if err != nil {
		return daemonProcess{}, fmt.Errorf("create daemon readiness pipe: %w", err)
	}
	cmd := exec.Command(binaryPath, "daemon")
	cmd.Env = append(os.Environ(),
		"ATTN_WRAPPER_PATH="+binaryPath,
		fmt.Sprintf("%s=%d", readyFDEnv, 3),
	)
	cmd.ExtraFiles = []*os.File{writer}
	if err := cmd.Start(); err != nil {
		reader.Close()
		writer.Close()
		return daemonProcess{}, fmt.Errorf("start daemon: %w", err)
	}
	writer.Close()
	go cmd.Wait()
	return daemonProcess{ready: reader}, nil
}

func TakeStartupSignal() (StartupSignal, error) {
	raw := strings.TrimSpace(os.Getenv(readyFDEnv))
	if raw == "" {
		return StartupSignal{}, nil
	}
	os.Unsetenv(readyFDEnv)
	fd, err := strconvAtoi(raw)
	if err != nil || fd < 3 {
		return StartupSignal{}, fmt.Errorf("invalid daemon readiness fd %q", raw)
	}
	syscall.CloseOnExec(fd)
	return StartupSignal{writer: os.NewFile(uintptr(fd), "daemon-ready")}, nil
}

func (s StartupSignal) Ready() error {
	return s.finish("ready\n")
}

func (s StartupSignal) Failed(err error) {
	if err != nil {
		_ = s.finish("error:" + err.Error() + "\n")
	}
}

func (s StartupSignal) AlreadyRunning() {
	_ = s.finish("already-running\n")
}

func (s StartupSignal) LockHeld() {
	_ = s.finish("lock-held\n")
}

func (s StartupSignal) finish(message string) error {
	if s.writer == nil {
		return nil
	}
	defer s.writer.Close()
	_, err := io.WriteString(s.writer, message)
	return err
}

func (p daemonProcess) waitForReady(ctx context.Context) error {
	result := make(chan error, 1)
	go func() {
		message, err := bufio.NewReader(p.ready).ReadString('\n')
		if err == nil {
			switch text := strings.TrimSuffix(message, "\n"); {
			case text == "ready":
			case text == "already-running":
				err = errDaemonAlreadyRunning
			case text == "lock-held":
				err = errDaemonLockHeld
			case strings.HasPrefix(text, "error:"):
				err = fmt.Errorf("daemon startup failed: %s", strings.TrimPrefix(text, "error:"))
			default:
				err = fmt.Errorf("daemon exited before signaling readiness")
			}
		}
		result <- err
	}()
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		p.ready.Close()
		return fmt.Errorf("wait for daemon readiness: %w", ctx.Err())
	}
}

func waitForSpawnedDaemon(ctx context.Context, process daemonProcess) (bool, error) {
	err := process.waitForReady(ctx)
	if !errors.Is(err, errDaemonAlreadyRunning) {
		return false, err
	}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	if err := waitForMatchingDaemon(ctx, ticker.C, fetchHealth, func() bool {
		return isSocketLive(config.SocketPath())
	}); err != nil {
		return false, err
	}
	return true, nil
}

func startDaemon(ctx context.Context, binaryPath string) (bool, error) {
	for {
		process, err := spawnDaemon(binaryPath)
		if err != nil {
			return false, err
		}
		contended, err := waitForSpawnedDaemon(ctx, process)
		if !errors.Is(err, errDaemonLockHeld) {
			return contended, err
		}
		if err := waitForPIDLockRelease(ctx); err != nil {
			return false, err
		}
		if matchingDaemonIsLive(ctx) {
			return true, nil
		}
	}
}

func waitForPIDLockRelease(ctx context.Context) error {
	lockFile, err := os.OpenFile(config.PIDPath(), os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return fmt.Errorf("open daemon pid lock: %w", err)
	}
	result := make(chan error, 1)
	go func() {
		result <- syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX)
	}()
	select {
	case err := <-result:
		if err == nil {
			_ = syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)
		}
		lockFile.Close()
		if err != nil {
			return fmt.Errorf("wait for daemon pid lock: %w", err)
		}
		return nil
	case <-ctx.Done():
		go func() {
			if <-result == nil {
				_ = syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)
			}
			_ = lockFile.Close()
		}()
		return ctx.Err()
	}
}

func waitForMatchingDaemon(
	ctx context.Context,
	retry <-chan time.Time,
	fetch func(context.Context) (healthResponse, error),
	socketLive func() bool,
) error {
	for {
		health, err := fetch(ctx)
		if err == nil {
			if daemonMatchesCurrentBinary(health) && socketLive() {
				return nil
			}
			if daemonMatchesCurrentBinary(health) {
				return fmt.Errorf("competing daemon is missing its Unix listener")
			}
			return fmt.Errorf("competing daemon does not match current binary")
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for competing daemon readiness: %w", ctx.Err())
		case <-retry:
		}
	}
}

func matchingDaemonIsLive(ctx context.Context) bool {
	if !isSocketLive(config.SocketPath()) {
		return false
	}
	health, err := fetchHealth(ctx)
	return err == nil && daemonMatchesCurrentBinary(health) && isSocketLive(config.SocketPath())
}

// removeStaleSocketFiles unlinks the listening socket only. The PID file must
// survive: its exclusive flock is the lock `attn db restore` contends on.
func removeStaleSocketFiles() error {
	socketPath := config.SocketPath()
	if err := os.Remove(socketPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove stale socket %s: %w", socketPath, err)
	}
	return nil
}

func stopRunningDaemon(ctx context.Context) error {
	pidBytes, err := os.ReadFile(config.PIDPath())
	if err != nil {
		return fmt.Errorf("read daemon pid file: %w", err)
	}
	pidText := strings.TrimSpace(string(pidBytes))
	if pidText == "" {
		return fmt.Errorf("daemon pid file is empty")
	}
	pid, err := strconvAtoi(pidText)
	if err != nil || pid <= 0 {
		return fmt.Errorf("parse daemon pid %q: %w", pidText, err)
	}
	if pid == os.Getpid() || pid == os.Getppid() {
		return fmt.Errorf("refusing to stop daemon pid %d because it matches the current process tree", pid)
	}
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil && err != syscall.ESRCH {
		return fmt.Errorf("stop daemon pid %d: %w", pid, err)
	}
	waitCtx, cancel := context.WithTimeout(ctx, stopTimeout)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		if !isSocketLive(config.SocketPath()) {
			return nil
		}
		select {
		case <-waitCtx.Done():
			return fmt.Errorf("timed out waiting for daemon to stop")
		case <-ticker.C:
		}
	}
}

func fetchHealth(ctx context.Context) (healthResponse, error) {
	host := strings.TrimSpace(config.WSBindAddress())
	if host == "" || host == "0.0.0.0" {
		host = "127.0.0.1"
	}
	client := &http.Client{Timeout: 1 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+net.JoinHostPort(host, config.WSPort())+"/health", nil)
	if err != nil {
		return healthResponse{}, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return healthResponse{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return healthResponse{}, fmt.Errorf("health status %d", resp.StatusCode)
	}
	var health healthResponse
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		return healthResponse{}, err
	}
	return health, nil
}

func isSocketLive(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	if _, err := os.Stat(path); err != nil {
		return false
	}
	conn, err := net.DialTimeout("unix", path, 250*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func ResolveAppOwnedBinary() (string, error) {
	if override := strings.TrimSpace(os.Getenv("ATTN_DAEMON_BINARY")); override != "" {
		return override, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve executable: %w", err)
	}
	return filepath.Join(filepath.Dir(exe), "attn"), nil
}

func strconvAtoi(value string) (int, error) {
	sign := 1
	if strings.HasPrefix(value, "-") {
		sign = -1
		value = strings.TrimPrefix(value, "-")
	}
	if value == "" {
		return 0, fmt.Errorf("empty integer")
	}
	total := 0
	for _, r := range value {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("invalid integer")
		}
		total = total*10 + int(r-'0')
	}
	return sign * total, nil
}
