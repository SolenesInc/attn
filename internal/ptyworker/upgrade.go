package ptyworker

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/victorarias/attn/internal/pty"
)

type handoffFile struct {
	Config       Config           `json:"config"`
	PTY          pty.HandoffState `json:"pty"`
	VTDumpPath   string           `json:"vt_dump_path"`
	HandedOverAt time.Time        `json:"handed_over_at"`
}

// HandoffPaths names the two files an upgrade leaves for the image that takes over. They sit
// beside the registry, not in it: Recover globs registry/*.json and would delete one mid-swap.
func HandoffPaths(registryPath, sessionID string) (jsonPath, dumpPath string) {
	base := filepath.Join(filepath.Dir(filepath.Dir(registryPath)), "handoff", sessionID)
	return base + ".json", base + ".vt"
}

func RemoveHandoff(registryPath, sessionID string) {
	jsonPath, dumpPath := HandoffPaths(registryPath, sessionID)
	_ = os.Remove(jsonPath)
	_ = os.Remove(dumpPath)
}

// upgrade captures the session, writes the handoff, and replaces this process image. It
// returns only on failure, and the caller must have answered the RPC: the exec ends every conn.
func (r *Runtime) upgrade(executable string, state pty.HandoffState, listenerFD int) error {
	jsonPath, dumpPath := HandoffPaths(r.cfg.RegistryPath, r.cfg.SessionID)
	if err := os.MkdirAll(filepath.Dir(jsonPath), 0700); err != nil {
		return fmt.Errorf("create handoff dir: %w", err)
	}
	if err := os.WriteFile(dumpPath, state.VTDump, 0600); err != nil {
		return fmt.Errorf("write handoff vt dump: %w", err)
	}
	dump := state.VTDump
	state.VTDump = nil

	payload, err := json.Marshal(handoffFile{
		Config:       r.cfg,
		PTY:          state,
		VTDumpPath:   dumpPath,
		HandedOverAt: time.Now(),
	})
	if err != nil {
		_ = os.Remove(dumpPath)
		return fmt.Errorf("marshal handoff: %w", err)
	}
	if err := os.WriteFile(jsonPath, payload, 0600); err != nil {
		_ = os.Remove(dumpPath)
		return fmt.Errorf("write handoff: %w", err)
	}

	argv := []string{
		executable,
		"pty-worker",
		"--adopt-handoff", jsonPath,
		"--adopt-ptmx-fd", fmt.Sprint(state.PtmxFD),
		"--adopt-listener-fd", fmt.Sprint(listenerFD),
	}
	r.logf("worker upgrade: exec session=%s binary=%s child=%d dump=%dB blocks=%d ptmx_fd=%d listener_fd=%d",
		r.cfg.SessionID, executable, state.ChildPID, len(dump), len(state.Blocks), state.PtmxFD, listenerFD)
	if err := syscall.Exec(executable, argv, os.Environ()); err != nil {
		_ = os.Remove(jsonPath)
		_ = os.Remove(dumpPath)
		return fmt.Errorf("exec %s: %w", executable, err)
	}
	return nil // unreachable: a successful Exec never returns.
}

// dupListener returns the listening socket's descriptor with CLOEXEC cleared, so it survives
// the exec. SetUnlinkOnClose(false) keeps the socket file in place.
func dupListener(l net.Listener) (int, error) {
	unixListener, ok := l.(*net.UnixListener)
	if !ok {
		return 0, fmt.Errorf("worker listener is %T, not a unix listener", l)
	}
	unixListener.SetUnlinkOnClose(false)
	file, err := unixListener.File()
	if err != nil {
		return 0, err
	}
	defer file.Close()
	// dup(2) returns a descriptor without CLOEXEC; File() sets it on its own.
	fd, err := syscall.Dup(int(file.Fd()))
	if err != nil {
		return 0, err
	}
	return fd, nil
}

func readHandoff(path string) (handoffFile, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return handoffFile{}, fmt.Errorf("read handoff: %w", err)
	}
	var hf handoffFile
	if err := json.Unmarshal(raw, &hf); err != nil {
		return handoffFile{}, fmt.Errorf("parse handoff %s: %w", path, err)
	}
	if hf.VTDumpPath != "" {
		dump, err := os.ReadFile(hf.VTDumpPath)
		if err != nil {
			return handoffFile{}, fmt.Errorf("read handoff vt dump: %w", err)
		}
		hf.PTY.VTDump = dump
		_ = os.Remove(hf.VTDumpPath)
	}
	_ = os.Remove(path)
	return hf, nil
}

func adoptListener(fd int) (net.Listener, error) {
	file := os.NewFile(uintptr(fd), "worker-listener")
	if file == nil {
		return nil, fmt.Errorf("listener fd %d is not open", fd)
	}
	defer file.Close()
	listener, err := net.FileListener(file)
	if err != nil {
		return nil, fmt.Errorf("adopt listener fd %d: %w", fd, err)
	}
	return listener, nil
}

// handleUpgrade runs the swap for one RPC. The order is what makes it safe: stop accepting,
// capture, flush the result, exec. Past the capture there is no way back — a failed exec dies.
func (r *Runtime) handleUpgrade(c *connCtx, reqID string, params UpgradeParams) {
	executable := strings.TrimSpace(params.Executable)
	if executable == "" {
		c.sendError(reqID, ErrBadRequest, "upgrade requires an executable")
		return
	}
	info, err := os.Stat(executable)
	if err != nil {
		c.sendError(reqID, ErrBadRequest, fmt.Sprintf("upgrade executable %s: %v", executable, err))
		return
	}
	if info.IsDir() || info.Mode()&0111 == 0 {
		c.sendError(reqID, ErrBadRequest, fmt.Sprintf("upgrade executable %s is not executable", executable))
		return
	}

	// The listener crosses as a descriptor rather than being rebound: measured, rebinding leaves
	// a ~12ms hole where a daemon dial fails, and inheriting leaves none (0 in 2483 connects).
	listenerFD, err := r.pauseAccept()
	if err != nil {
		c.sendError(reqID, ErrInternal, fmt.Sprintf("stop accepting for upgrade: %v", err))
		return
	}
	state, err := r.manager.Handoff(r.cfg.SessionID)
	if err != nil {
		r.resumeAccept(listenerFD)
		c.sendError(reqID, ErrInternal, fmt.Sprintf("capture session for upgrade: %v", err))
		return
	}
	c.sendResult(reqID, UpgradeResult{
		ChildPID:   state.ChildPID,
		DumpBytes:  len(state.VTDump),
		BlockCount: len(state.Blocks),
	})
	// The exec ends every connection, so the result has to be on the wire
	// before it: closing the send queue drains it, and sendDone says when.
	c.closeSend()
	<-c.sendDone

	// Past this point there is no session in this image to hand back: the manager
	// gave it away and the PTY file is closed.
	err = r.upgrade(executable, state, listenerFD)
	r.logf("worker upgrade: exec failed session=%s child=%d err=%v; the session cannot be resumed, exiting",
		r.cfg.SessionID, state.ChildPID, err)
	os.Exit(1)
}
