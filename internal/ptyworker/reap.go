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
	"time"

	"github.com/victorarias/attn/internal/procreap"
)

type ReapOutcome string

const (
	ReapRemoved      ReapOutcome = "removed"
	ReapAlreadyGone  ReapOutcome = "already gone"
	ReapFailed       ReapOutcome = "failed"
	ReapUnidentified ReapOutcome = "unidentified"
)

type ReapResult struct {
	SessionID string
	WorkerPID int
	Outcome   ReapOutcome
	Err       error
}

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
			results = append(results, ReapResult{SessionID: strings.TrimSuffix(filepath.Base(path), ".json"), Outcome: ReapFailed, Err: fmt.Errorf("unreadable registry %s: %w", path, err)})
			continue
		}
		res := reapEntry(entry, path)
		if res.Outcome == ReapRemoved || res.Outcome == ReapAlreadyGone {
			RemoveHandoff(path, entry.SessionID)
		}
		results = append(results, res)
	}
	return results
}

const workerExitGrace = 500 * time.Millisecond

func reapEntry(entry RegistryEntry, registryPath string) ReapResult {
	res := ReapResult{SessionID: entry.SessionID, WorkerPID: entry.WorkerPID}

	if entry.WorkerPID <= 0 || !procreap.ProcessAlive(entry.WorkerPID) {
		return workerGone(res, entry)
	}

	if err := requestWorkerRemove(entry); err == nil {
		if waitForExit(entry.WorkerPID, TeardownRPCTimeout) {
			res.Outcome = ReapRemoved
			return res
		}
		res.Err = errors.New("worker accepted remove but did not exit")
	} else {
		res.Err = err
	}

	if waitForExit(entry.WorkerPID, workerExitGrace) {
		res.Err = nil
		return workerGone(res, entry)
	}
	if !processHasArg(entry.WorkerPID, registryPath) {
		res.Outcome = ReapUnidentified
		return res
	}
	res.Outcome = ReapFailed
	return res
}

func workerGone(res ReapResult, entry RegistryEntry) ReapResult {
	if entry.ChildPID > 0 && procreap.ProcessAlive(entry.ChildPID) {
		res.Outcome = ReapFailed
		res.Err = fmt.Errorf("worker exited but its child pid %d is still running", entry.ChildPID)
		return res
	}
	res.Outcome = ReapAlreadyGone
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
	if err := conn.SetDeadline(time.Now().Add(TeardownRPCTimeout)); err != nil {
		return err
	}
	if err := writeReapRequest(enc, "reap-remove", MethodRemove, map[string]any{}); err != nil {
		return err
	}
	if err := awaitOK(dec, "reap-remove"); err != nil {
		return fmt.Errorf("worker removal response (timeout %s): %w", TeardownRPCTimeout, err)
	}
	return nil
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

func waitForExit(pid int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !procreap.ProcessAlive(pid) {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return !procreap.ProcessAlive(pid)
}

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
