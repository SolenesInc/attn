// docs/decisions/2026-08-08-daemon-children-are-reaped-from-files.md.
// Design: docs/decisions/2026-08-08-daemon-children-are-reaped-from-files.md.
package procreap

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"syscall"
	"time"
)

type Entry struct {
	Version int    `json:"version"`
	ID      string `json:"id"`
	PID     int    `json:"pid"`
	// PGID equal to PID means the child leads its own group and may be swept; a
	// shared group is never group-signalled — its members are not this entry's.
	PGID             int      `json:"pgid"`
	Command          []string `json:"command"`
	ProcessStartTime string   `json:"process_start_time"`
	StartedAt        string   `json:"started_at"`
}

const entryVersion = 1

func NewEntry(id string, pid, pgid int, command []string) Entry {
	startTime, err := processStartTime(pid)
	if err != nil {
		// An empty stamp reads as "cannot identify", never as "safe to signal".
		startTime = ""
	}
	return Entry{
		Version:          entryVersion,
		ID:               id,
		PID:              pid,
		PGID:             pgid,
		Command:          append([]string(nil), command...),
		ProcessStartTime: startTime,
		StartedAt:        time.Now().UTC().Format(time.RFC3339),
	}
}

func WriteEntry(path string, entry Entry) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create process registry dir: %w", err)
	}
	encoded, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("encode process registry entry: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, encoded, 0o600); err != nil {
		return fmt.Errorf("write process registry entry: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("commit process registry entry: %w", err)
	}
	return nil
}

func ReadEntry(path string) (Entry, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Entry{}, err
	}
	var entry Entry
	if err := json.Unmarshal(raw, &entry); err != nil {
		return Entry{}, fmt.Errorf("decode process registry entry %s: %w", path, err)
	}
	if entry.Version != entryVersion {
		return Entry{}, fmt.Errorf("process registry entry %s has version %d, want %d", path, entry.Version, entryVersion)
	}
	return entry, nil
}

func RemoveEntry(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

type ReapOutcome string

const (
	ReapTerminated   ReapOutcome = "terminated"
	ReapKilled       ReapOutcome = "killed"
	ReapAlreadyGone  ReapOutcome = "already gone"
	ReapUnidentified ReapOutcome = "unidentified"
	ReapSurvived     ReapOutcome = "survived"
	ReapUnreadable   ReapOutcome = "unreadable"
)

type ReapResult struct {
	ID      string
	PID     int
	Outcome ReapOutcome
	Path    string
	Err     error
}

// A group id is held until its last member leaves, so sweeping the group after
// the leader is gone cannot hit a recycled pid.
func ReapDir(dir string, grace time.Duration) []ReapResult {
	paths, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return nil
	}
	sort.Strings(paths)

	var results []ReapResult
	for _, path := range paths {
		entry, err := ReadEntry(path)
		if err != nil {
			results = append(results, ReapResult{
				ID:      filepath.Base(path),
				Outcome: ReapUnreadable,
				Path:    path,
				Err:     err,
			})
			continue
		}
		result := reapEntry(entry, grace)
		result.Path = path
		results = append(results, result)
	}
	return results
}

func reapEntry(entry Entry, grace time.Duration) ReapResult {
	res := ReapResult{ID: entry.ID, PID: entry.PID}
	leadsGroup := entry.PGID == entry.PID && entry.PID > 0

	if entry.PID <= 0 || !processAlive(entry.PID) {
		res.Outcome = ReapAlreadyGone
		return res
	}

	// Identity gate: signal only a process still carrying the start time recorded at
	// spawn. A recycled pid fails it, and an empty stamp matches nothing.
	current, err := processStartTime(entry.PID)
	if err != nil || entry.ProcessStartTime == "" || current != entry.ProcessStartTime {
		if err == nil {
			err = fmt.Errorf("start time %q does not match recorded %q", current, entry.ProcessStartTime)
		}
		res.Outcome = ReapUnidentified
		res.Err = err
		return res
	}

	if err := syscall.Kill(entry.PID, syscall.SIGTERM); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			res.Outcome = ReapAlreadyGone
			return res
		}
		res.Outcome = ReapUnidentified
		res.Err = fmt.Errorf("SIGTERM pid %d: %w", entry.PID, err)
		return res
	}
	if waitForGone(entry.PID, grace) {
		res.Outcome = ReapTerminated
		if leadsGroup {
			sweepGroup(entry.PGID)
		}
		return res
	}

	if leadsGroup {
		_ = syscall.Kill(-entry.PGID, syscall.SIGKILL)
	}
	_ = syscall.Kill(entry.PID, syscall.SIGKILL)
	if waitForGone(entry.PID, grace) {
		res.Outcome = ReapKilled
		if leadsGroup {
			sweepGroup(entry.PGID)
		}
		return res
	}
	res.Outcome = ReapSurvived
	res.Err = fmt.Errorf("pid %d still alive after SIGKILL", entry.PID)
	return res
}

func sweepGroup(pgid int) {
	_ = syscall.Kill(-pgid, syscall.SIGKILL)
}

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

func waitForGone(pid int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !processAlive(pid) {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return !processAlive(pid)
}

// processStartTime's resolution is the whole safety property: a stamp coarser than the
// interval in which the kernel can reuse a pid passes the gate for a stranger.
