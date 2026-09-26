package procreap

import (
	"bufio"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

func TestReapSweepsTheGroupOfALeaderThatExitedUncollected(t *testing.T) {
	dir := t.TempDir()
	script := writeScript(t, "sleep 300 </dev/null >/dev/null 2>&1 &\necho $!\n")
	cmd := exec.Command(script)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start leader: %v", err)
	}
	leader := cmd.Process.Pid
	t.Cleanup(func() {
		_ = syscall.Kill(-leader, syscall.SIGKILL)
		_ = cmd.Wait()
	})

	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil {
		t.Fatalf("leader never reported its child: %v", err)
	}
	child, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil || child <= 0 {
		t.Fatalf("leader reported child %q", line)
	}
	t.Cleanup(func() { _ = syscall.Kill(child, syscall.SIGKILL) })

	var exited unix.Siginfo
	if err := unix.Waitid(unix.P_PID, leader, &exited, unix.WEXITED|unix.WNOWAIT, nil); err != nil {
		t.Fatalf("wait for the leader to exit uncollected: %v", err)
	}
	if err := WriteEntry(filepath.Join(dir, "e1.json"), NewEntry("e1", leader, leader, []string{script})); err != nil {
		t.Fatalf("write registry: %v", err)
	}

	results := ReapDir(dir, testGrace)
	if len(results) != 1 || results[0].Outcome != ReapAlreadyGone {
		t.Fatalf("expected %s, got %+v", ReapAlreadyGone, results)
	}
	if !waitForGone(child, testGrace) {
		t.Fatalf("child %d of the exited leader survived the reap", child)
	}
}
