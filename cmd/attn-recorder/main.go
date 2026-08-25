package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	recorderBundleID = "com.attn.recorder"
	manifestName     = "evidence-recording.json"
	finalizeTripwire = 10 * time.Second
	maxRequestBytes  = 64 * 1024
	maxStderrBytes   = 4096
)

type recordingManifest struct {
	Port      int    `json:"port"`
	Token     string `json:"token"`
	PID       int    `json:"pid"`
	StartedAt int64  `json:"started_at"`
}

type startRequest struct {
	Token          string `json:"token"`
	Action         string `json:"action"`
	WindowID       uint32 `json:"window_id"`
	TargetBundleID string `json:"target_bundle_id"`
	OutputPath     string `json:"output_path"`
}

type controlRequest struct {
	Action string `json:"action"`
}

type recordingResult struct {
	Bytes    int64   `json:"bytes"`
	ExitCode *int    `json:"exitCode"`
	Failure  *string `json:"failure"`
}

type recorderSession interface {
	PID() int
	Done() <-chan struct{}
	Result() recordingResult
	Stop() recordingResult
}

type startRecorderFunc func(path string, windowID uint32, bundleID, outputPath string) (recorderSession, error)

type broker struct {
	token        string
	recorderPath string
	start        startRecorderFunc
}

func main() {
	if err := run(); err != nil {
		log.Printf("attn-recorder: %v", err)
		os.Exit(1)
	}
}

func run() error {
	if runtime.GOOS != "darwin" {
		return errors.New("screen recording is only available on macOS")
	}
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve recorder executable: %w", err)
	}
	recorderPath := filepath.Clean(filepath.Join(filepath.Dir(executable), "..", "Helpers", "AttnRecorderCapture"))
	if info, err := os.Stat(recorderPath); err != nil || info.IsDir() {
		return fmt.Errorf("window recorder helper is unavailable at %s", recorderPath)
	}
	manifestPath, err := defaultManifestPath()
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	defer listener.Close()

	token, err := generateToken()
	if err != nil {
		return err
	}
	port := listener.Addr().(*net.TCPAddr).Port
	manifest := recordingManifest{
		Port:      port,
		Token:     token,
		PID:       os.Getpid(),
		StartedAt: time.Now().Unix(),
	}
	if err := writeManifest(manifestPath, manifest); err != nil {
		return err
	}
	defer removeManifestIfCurrent(manifestPath, token)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(stop)
	go func() {
		<-stop
		_ = listener.Close()
	}()

	server := broker{token: token, recorderPath: recorderPath, start: startRecorderProcess}
	for {
		conn, err := listener.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			log.Printf("attn-recorder: accept: %v", err)
			continue
		}
		go server.serveConnection(conn)
	}
}

func defaultManifestPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, "Library", "Application Support", recorderBundleID, manifestName), nil
}

func generateToken() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generate broker token: %w", err)
	}
	return hex.EncodeToString(bytes), nil
}

func writeManifest(path string, manifest recordingManifest) error {
	parent := filepath.Dir(path)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return fmt.Errorf("create manifest directory: %w", err)
	}
	if err := os.Chmod(parent, 0o700); err != nil {
		return fmt.Errorf("protect manifest directory: %w", err)
	}
	temporary, err := os.CreateTemp(parent, ".evidence-recording-*")
	if err != nil {
		return fmt.Errorf("create manifest: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return fmt.Errorf("protect manifest: %w", err)
	}
	encoder := json.NewEncoder(temporary)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(manifest); err != nil {
		temporary.Close()
		return fmt.Errorf("encode manifest: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close manifest: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("publish manifest: %w", err)
	}
	return nil
}

func removeManifestIfCurrent(path, token string) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var manifest recordingManifest
	if json.Unmarshal(contents, &manifest) == nil && manifest.Token == token {
		_ = os.Remove(path)
	}
}

func (b broker) serveConnection(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	decoder := json.NewDecoder(io.LimitReader(conn, maxRequestBytes))
	encoder := json.NewEncoder(conn)

	var request startRequest
	if err := decoder.Decode(&request); err != nil {
		sendError(encoder, fmt.Sprintf("invalid start request: %v", err))
		return
	}
	_ = conn.SetReadDeadline(time.Time{})
	if request.Token != b.token {
		sendError(encoder, "invalid token")
		return
	}
	if request.Action != "start" {
		sendError(encoder, "first action must be start")
		return
	}
	if err := validateBundleID(request.TargetBundleID); err != nil {
		sendError(encoder, err.Error())
		return
	}
	outputPath, err := validateOutputPath(request.OutputPath)
	if err != nil {
		sendError(encoder, err.Error())
		return
	}

	recorder, err := b.start(b.recorderPath, request.WindowID, request.TargetBundleID, outputPath)
	if err != nil {
		sendError(encoder, err.Error())
		return
	}
	_ = encoder.Encode(map[string]any{"event": "started", "pid": recorder.PID()})

	control := make(chan error, 1)
	go func() {
		var request controlRequest
		if err := decoder.Decode(&request); err != nil {
			control <- err
			return
		}
		if request.Action != "stop" {
			control <- fmt.Errorf("unsupported control action %q", request.Action)
			return
		}
		control <- nil
	}()

	var result recordingResult
	select {
	case <-recorder.Done():
		result = recorder.Result()
	case <-control:
		result = recorder.Stop()
	}
	_ = encoder.Encode(map[string]any{
		"event":    "finished",
		"bytes":    result.Bytes,
		"exitCode": result.ExitCode,
		"failure":  result.Failure,
	})
}

func sendError(encoder *json.Encoder, message string) {
	_ = encoder.Encode(map[string]any{"event": "error", "error": message})
}

func validateBundleID(bundleID string) error {
	if bundleID == "" || len(bundleID) > 255 {
		return errors.New("target bundle id is invalid")
	}
	for _, char := range bundleID {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') || char == '.' || char == '-' {
			continue
		}
		return errors.New("target bundle id is invalid")
	}
	return nil
}

func validateOutputPath(raw string) (string, error) {
	if filepath.Ext(raw) != ".mp4" {
		return "", errors.New("recording output must have an .mp4 extension")
	}
	if _, err := os.Lstat(raw); err == nil {
		return "", errors.New("recording output already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("inspect recording output: %w", err)
	}
	parent, err := filepath.Abs(filepath.Dir(raw))
	if err != nil {
		return "", fmt.Errorf("resolve recording output parent: %w", err)
	}
	parent, err = filepath.EvalSymlinks(parent)
	if err != nil {
		return "", fmt.Errorf("recording output parent is unavailable: %w", err)
	}
	info, err := os.Stat(parent)
	if err != nil {
		return "", fmt.Errorf("inspect recording output parent: %w", err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Geteuid()) {
		return "", errors.New("recording output parent is not owned by the current user")
	}
	return filepath.Join(parent, filepath.Base(raw)), nil
}

type processExit struct {
	state  *os.ProcessState
	err    error
	stderr string
}

type recordingProcess struct {
	command    *exec.Cmd
	stdin      io.Closer
	outputPath string
	finished   chan struct{}
	exit       processExit
	stopOnce   sync.Once
}

func startRecorderProcess(path string, windowID uint32, bundleID, outputPath string) (recorderSession, error) {
	command := exec.Command(path, fmt.Sprint(windowID), bundleID, outputPath)
	command.Stdout = io.Discard
	stderr, err := command.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("capture recorder stderr: %w", err)
	}
	stdin, err := command.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("open recorder lifetime pipe: %w", err)
	}
	if err := command.Start(); err != nil {
		return nil, fmt.Errorf("launch window recorder: %w", err)
	}

	process := &recordingProcess{
		command:    command,
		stdin:      stdin,
		outputPath: outputPath,
		finished:   make(chan struct{}),
	}
	go func() {
		var captured cappedBuffer
		captured.max = maxStderrBytes
		stderrDone := make(chan struct{})
		go func() {
			_, _ = io.Copy(&captured, stderr)
			close(stderrDone)
		}()
		waitErr := command.Wait()
		<-stderrDone
		process.exit = processExit{state: command.ProcessState, err: waitErr, stderr: strings.TrimSpace(captured.String())}
		close(process.finished)
	}()
	return process, nil
}

func (p *recordingProcess) PID() int {
	return p.command.Process.Pid
}

func (p *recordingProcess) Done() <-chan struct{} {
	return p.finished
}

func (p *recordingProcess) Result() recordingResult {
	<-p.finished
	return p.result(p.exit, false)
}

func (p *recordingProcess) Stop() recordingResult {
	p.stopOnce.Do(func() {
		_ = p.stdin.Close()
		_ = p.command.Process.Signal(os.Interrupt)
	})
	timer := time.NewTimer(finalizeTripwire)
	defer timer.Stop()
	select {
	case <-p.finished:
		return p.result(p.exit, false)
	case <-timer.C:
		_ = p.command.Process.Kill()
		<-p.finished
		return p.result(p.exit, true)
	}
}

func (p *recordingProcess) result(exit processExit, forced bool) recordingResult {
	var bytes int64
	if info, err := os.Stat(p.outputPath); err == nil {
		bytes = info.Size()
	}
	var exitCode *int
	if exit.state != nil {
		code := exit.state.ExitCode()
		exitCode = &code
	}
	var failure string
	switch {
	case forced:
		failure = fmt.Sprintf("recorder ignored finalization for %s and was killed; the file is likely unplayable", finalizeTripwire)
	case bytes == 0:
		failure = "recorder produced no output"
	case exitCode != nil && *exitCode != 0:
		failure = fmt.Sprintf("recorder exited %d leaving a possibly unplayable file", *exitCode)
	case exit.err != nil:
		failure = fmt.Sprintf("recorder failed: %v", exit.err)
	}
	if failure != "" && exit.stderr != "" {
		failure += ": " + exit.stderr
	}
	var failurePointer *string
	if failure != "" {
		failurePointer = &failure
	}
	return recordingResult{Bytes: bytes, ExitCode: exitCode, Failure: failurePointer}
}

type cappedBuffer struct {
	bytes.Buffer
	max int
}

func (b *cappedBuffer) Write(contents []byte) (int, error) {
	written := len(contents)
	remaining := b.max - b.Len()
	if remaining > 0 {
		if len(contents) > remaining {
			contents = contents[:remaining]
		}
		_, _ = b.Buffer.Write(contents)
	}
	return written, nil
}
