package procreap

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

const testGrace = 3 * time.Second

func writeScript(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-child.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatalf("write fake child: %v", err)
	}
	return path
}

func orphan(t *testing.T, dir, id, body string) Entry {
	t.Helper()
	readyFile := filepath.Join(t.TempDir(), "ready")
	script := writeScript(t, "READY_FILE="+readyFile+"\n"+body)
	cmd := exec.Command(script)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start orphan: %v", err)
	}
	pid := cmd.Process.Pid
	go func() { _ = cmd.Wait() }()
	t.Cleanup(func() {
		_ = syscall.Kill(-pid, syscall.SIGKILL)
	})

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(readyFile); err == nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if _, err := os.Stat(readyFile); err != nil {
		t.Fatalf("orphan never reported ready: %v", err)
	}

	entry := NewEntry(id, pid, pid, []string{script})
	if err := WriteEntry(filepath.Join(dir, id+".json"), entry); err != nil {
		t.Fatalf("write registry: %v", err)
	}
	return entry
}

func TestReapLeavesARecycledPIDAlone(t *testing.T) {
	dir := t.TempDir()
	entry := orphan(t, dir, "e1", `
touch "$READY_FILE"
while true; do sleep 0.05; done
`)
	entry.ProcessStartTime = "not-this-process"
	if err := WriteEntry(filepath.Join(dir, "e1.json"), entry); err != nil {
		t.Fatalf("rewrite registry: %v", err)
	}

	results := ReapDir(dir, testGrace)
	if len(results) != 1 || results[0].Outcome != ReapUnidentified {
		t.Fatalf("expected %s, got %+v", ReapUnidentified, results)
	}
	if !ProcessAlive(entry.PID) {
		t.Fatalf("reap signalled a pid it could not identify")
	}
}

func TestReapRefusesAnEntryWithNoStartTime(t *testing.T) {
	dir := t.TempDir()
	entry := orphan(t, dir, "e1", `
touch "$READY_FILE"
while true; do sleep 0.05; done
`)
	entry.ProcessStartTime = ""
	if err := WriteEntry(filepath.Join(dir, "e1.json"), entry); err != nil {
		t.Fatalf("rewrite registry: %v", err)
	}

	results := ReapDir(dir, testGrace)
	if len(results) != 1 || results[0].Outcome != ReapUnidentified {
		t.Fatalf("expected %s, got %+v", ReapUnidentified, results)
	}
	if !ProcessAlive(entry.PID) {
		t.Fatalf("reap signalled a pid it could not identify")
	}
}

func TestReapReportsADeadEntryAsAlreadyGone(t *testing.T) {
	dir := t.TempDir()
	script := writeScript(t, "exit 0\n")
	cmd := exec.Command(script)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	entry := NewEntry("e1", cmd.Process.Pid, cmd.Process.Pid, []string{script})
	_ = cmd.Wait()
	if err := WriteEntry(filepath.Join(dir, "e1.json"), entry); err != nil {
		t.Fatalf("write registry: %v", err)
	}

	results := ReapDir(dir, testGrace)
	if len(results) != 1 || results[0].Outcome != ReapAlreadyGone {
		t.Fatalf("expected %s, got %+v", ReapAlreadyGone, results)
	}
}

func TestReapReportsAnUnreadableRecord(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "e1.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := WriteEntry(filepath.Join(dir, "e2.json"), Entry{Version: entryVersion + 1, ID: "e2", PID: 1}); err != nil {
		t.Fatalf("write: %v", err)
	}

	results := ReapDir(dir, testGrace)
	if len(results) != 2 {
		t.Fatalf("expected both records reported, got %+v", results)
	}
	for _, res := range results {
		if res.Outcome != ReapUnreadable {
			t.Fatalf("expected %s, got %+v", ReapUnreadable, res)
		}
		if res.Err == nil || res.ID == "" {
			t.Fatalf("an unreadable record must name itself and say why: %+v", res)
		}
	}
}

func TestReapCooperativeTeardownReachesTheDetachedChild(t *testing.T) {
	dir := t.TempDir()
	childPIDFile := filepath.Join(t.TempDir(), "child.pid")
	orphan(t, dir, "e1", `
set -m
sleep 300 &
echo $! > `+childPIDFile+`
set +m
trap 'kill $(cat `+childPIDFile+`) 2>/dev/null; exit 0' TERM
touch "$READY_FILE"
while true; do sleep 0.05; done
`)
	deadline := time.Now().Add(5 * time.Second)
	var childPID int
	for time.Now().Before(deadline) {
		if raw, err := os.ReadFile(childPIDFile); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(raw))); err == nil && pid > 0 {
				childPID = pid
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if childPID <= 0 {
		t.Fatal("orphan never reported its detached child pid")
	}
	t.Cleanup(func() { _ = syscall.Kill(childPID, syscall.SIGKILL) })

	results := ReapDir(dir, testGrace)
	if len(results) != 1 || results[0].Outcome != ReapTerminated {
		t.Fatalf("expected %s, got %+v", ReapTerminated, results)
	}
	if !waitForGone(childPID, 5*time.Second) {
		t.Fatalf("detached child %d survived the cooperative reap", childPID)
	}
}

func TestReapNeverGroupSignalsASharedGroupEntry(t *testing.T) {
	dir := t.TempDir()
	readyFile := filepath.Join(t.TempDir(), "ready")
	script := writeScript(t, "READY_FILE="+readyFile+`
trap '' TERM
touch "$READY_FILE"
while true; do sleep 0.05; done
`)
	cmd := exec.Command(script)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	pid := cmd.Process.Pid
	go func() { _ = cmd.Wait() }()
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(readyFile); err == nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	pgid, err := syscall.Getpgid(pid)
	if err != nil {
		t.Fatalf("getpgid: %v", err)
	}
	if pgid == pid {
		t.Fatalf("test setup: child unexpectedly leads its own group")
	}
	entry := NewEntry("e1", pid, pgid, []string{script})
	if err := WriteEntry(filepath.Join(dir, "e1.json"), entry); err != nil {
		t.Fatalf("write registry: %v", err)
	}

	results := ReapDir(dir, testGrace)
	if len(results) != 1 || results[0].Outcome != ReapKilled {
		t.Fatalf("expected %s, got %+v", ReapKilled, results)
	}
	if ProcessAlive(pid) {
		t.Fatalf("pid %d still alive after reap", pid)
	}
}
