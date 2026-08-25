package ptyworker

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type ReapOutcome string

const (
	ReapRemoved      ReapOutcome = "removed"
	ReapAlreadyGone  ReapOutcome = "already gone"
	ReapSignalled    ReapOutcome = "signalled"
	ReapUnidentified ReapOutcome = "unidentified"
)

type ReapResult struct {
	SessionID string
	WorkerPID int
	Outcome   ReapOutcome
	Err       error
}

// Reap before removing a data dir: deleting it destroys the registry surviving workers are
// found through. Shutdown goes over each worker's control socket, so no PID can be reused.
func ReapDataDir(dataDir string) []ReapResult {
	paths, err := filepath.Glob(filepath.Join(dataDir, "workers", "*", "registry", "*.json"))
	if err != nil {
		return nil
	}
	sort.Strings(paths)

	var results []ReapResult
	for _, path := range paths {
		entry, err := ReadRegistry(path)
		if err != nil {
			continue
		}
		res := reapEntry(entry, path)
		// Only once the worker is provably gone. ReapUnidentified means it may
		// still be running, and a live worker mid-swap needs its handoff.
		if res.Outcome == ReapRemoved || res.Outcome == ReapAlreadyGone || res.Outcome == ReapSignalled {
			RemoveHandoff(path, entry.SessionID)
		}
		results = append(results, res)
	}
	return results
}

func reapEntry(entry RegistryEntry, registryPath string) ReapResult {
	res := ReapResult{SessionID: entry.SessionID, WorkerPID: entry.WorkerPID}

	if entry.WorkerPID <= 0 || !ProcessAlive(entry.WorkerPID) {
		res.Outcome = ReapAlreadyGone
		return res
	}

	if err := requestWorkerRemove(entry); err == nil {
		if waitForExit(entry.WorkerPID, 5*time.Second) {
			res.Outcome = ReapRemoved
			return res
		}
		res.Err = errors.New("worker accepted remove but did not exit")
	} else {
		res.Err = err
	}

	// Signal only a process still positively identifiable as this worker: the registry path is
	// unique per session per data dir, so finding it in the argv rules out a recycled PID.
	if !processHasArg(entry.WorkerPID, registryPath) {
		res.Outcome = ReapUnidentified
		return res
	}
	_ = syscall.Kill(entry.WorkerPID, syscall.SIGTERM)
	waitForExit(entry.WorkerPID, 5*time.Second)
	res.Outcome = ReapSignalled
	return res
}

func requestWorkerRemove(entry RegistryEntry) error {
	if strings.TrimSpace(entry.SocketPath) == "" {
		return errors.New("registry entry has no socket path")
	}
	conn, err := net.DialTimeout("unix", entry.SocketPath, 2*time.Second)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return err
	}

	enc := json.NewEncoder(conn)
	dec := json.NewDecoder(conn)

	hello := HelloParams{
		RPCMajor:         RPCMajor,
		RPCMinor:         RPCMinor,
		DaemonInstanceID: entry.DaemonInstanceID,
		ControlToken:     entry.ControlToken,
	}
	if err := writeReapRequest(enc, "reap-hello", MethodHello, hello); err != nil {
		return err
	}
	if err := awaitOK(dec, "reap-hello"); err != nil {
		return err
	}
	if err := writeReapRequest(enc, "reap-remove", MethodRemove, map[string]any{}); err != nil {
		return err
	}
	return awaitOK(dec, "reap-remove")
}

func writeReapRequest(enc *json.Encoder, id, method string, params any) error {
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	return enc.Encode(RequestEnvelope{Type: "req", ID: id, Method: method, Params: raw})
}

func awaitOK(dec *json.Decoder, id string) error {
	for {
		var res ResponseEnvelope
		if err := dec.Decode(&res); err != nil {
			return err
		}
		if res.Type != "res" || res.ID != id {
			continue
		}
		if !res.OK {
			if res.Error != nil {
				return fmt.Errorf("worker %s: %s", res.Error.Code, res.Error.Message)
			}
			return errors.New("worker rejected request")
		}
		return nil
	}
}

func ProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

func waitForExit(pid int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !ProcessAlive(pid) {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return !ProcessAlive(pid)
}

// An unreadable argv must read as "not identified" rather than "probably fine".
func processHasArg(pid int, want string) bool {
	if want == "" {
		return false
	}
	if raw, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "cmdline")); err == nil {
		for _, arg := range strings.Split(string(raw), "\x00") {
			if arg == want {
				return true
			}
		}
		return false
	}
	out, err := exec.Command("ps", "-o", "command=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), want)
}
