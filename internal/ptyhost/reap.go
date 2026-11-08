package ptyhost

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/procreap"
	"github.com/victorarias/attn/internal/ptyworker"
)

func HostRegistryPaths(dataDir string) []string {
	paths, _ := filepath.Glob(filepath.Join(dataDir, "pty-hosts", "*", "hosts", "*.json"))
	return paths
}

func ReapDataDir(dataDir string) []procreap.ReapResult {
	var results []procreap.ReapResult
	for _, path := range HostRegistryPaths(dataDir) {
		result := procreap.ReapResult{Path: path, ID: filepath.Base(path)}
		entry, err := ReadHostRegistry(path)
		if err != nil {
			result.Outcome, result.Err = procreap.ReapUnreadable, err
		} else {
			result.PID = entry.HostPID
			switch {
			case entry.HostPID <= 0:
				result.Outcome, result.Err = procreap.ReapUnreadable, errors.New("missing host PID")
			case !procreap.ProcessAlive(entry.HostPID):
				result.Outcome = procreap.ReapAlreadyGone
			default:
				result.Err = shutdownHost(dataDir, path, entry)
				if result.Err != nil {
					result.Outcome = procreap.ReapUnidentified
				} else if waitHostExit(entry.HostPID) {
					result.Outcome = procreap.ReapTerminated
				} else {
					result.Outcome, result.Err = procreap.ReapSurvived, errors.New("host accepted shutdown but did not exit")
				}
			}
		}
		results = append(results, result)
	}
	return results
}

func RunningHostPIDs(dataDir string) ([]int, error) {
	out, err := exec.Command("ps", "-axww", "-o", "pid=,command=").Output()
	if err != nil {
		return nil, err
	}
	return hostPIDsUnder(string(out), dataDir), nil
}

func hostPIDsUnder(processTable, dataDir string) []int {
	marker := "--host-registry-path " + filepath.Join(dataDir, "pty-hosts") + string(filepath.Separator)
	var pids []int
	for _, line := range strings.Split(processTable, "\n") {
		if !strings.Contains(line, marker) {
			continue
		}
		fields := strings.Fields(line)
		if pid, err := strconv.Atoi(fields[0]); err == nil {
			pids = append(pids, pid)
		}
	}
	return pids
}

func shutdownHost(dataDir, registryPath string, entry HostRegistry) error {
	if entry.DaemonInstanceID == "" || entry.ControlToken == "" ||
		filepath.Dir(filepath.Clean(registryPath)) != HostRegistryDir(dataDir, entry.DaemonInstanceID) {
		return errors.New("invalid host registry identity")
	}
	if err := ValidateSocketPath(dataDir, entry.DaemonInstanceID, entry.SocketPath); err != nil {
		return err
	}
	conn, err := net.DialTimeout("unix", entry.SocketPath, 2*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(30 * time.Second)); err != nil {
		return err
	}
	enc, dec := json.NewEncoder(conn), json.NewDecoder(conn)
	call := func(method string, params, result any) error {
		raw, err := json.Marshal(params)
		if err != nil {
			return err
		}
		id := "reap-" + method
		if err := enc.Encode(ptyworker.RequestEnvelope{Type: "req", ID: id, Method: method, Params: raw}); err != nil {
			return err
		}
		for {
			var response ptyworker.ResponseEnvelope
			if err := dec.Decode(&response); err != nil {
				return err
			}
			if response.Type != "res" || response.ID != id {
				continue
			}
			if !response.OK {
				return fmt.Errorf("host %s rejected: %+v", method, response.Error)
			}
			if result != nil {
				return json.Unmarshal(response.Result, result)
			}
			return nil
		}
	}
	if err := call(ptyworker.MethodHello, ptyworker.HelloParams{
		RPCMajor: ptyworker.RPCMajor, RPCMinor: ptyworker.RPCMinor,
		DaemonInstanceID: entry.DaemonInstanceID, ControlToken: entry.ControlToken,
	}, nil); err != nil {
		return err
	}
	var info HostInfoResult
	if err := call(MethodHostInfo, nil, &info); err != nil {
		return err
	}
	if info.HostPID != entry.HostPID {
		return errors.New("host PID does not match its registry")
	}
	return call(MethodShutdown, nil, nil)
}

func waitHostExit(pid int) bool {
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for procreap.ProcessAlive(pid) {
		select {
		case <-ticker.C:
		case <-deadline.C:
			return !procreap.ProcessAlive(pid)
		}
	}
	return true
}
