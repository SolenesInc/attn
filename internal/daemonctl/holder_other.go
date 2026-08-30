//go:build !linux

package daemonctl

import (
	"bufio"
	"bytes"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// PATH first, then its macOS location: Stop may run with a trimmed PATH.
var lsofPath = resolveLsofPath()

func resolveLsofPath() string {
	if p, err := exec.LookPath("lsof"); err == nil {
		return p
	}
	return "/usr/sbin/lsof"
}

// No /proc here, so lsof is the only fd table available. Fail-closed on
// exec/parse errors. Membership, not exclusivity, so Stop's own fd is harmless.
func pidHoldsPIDFile(pid int, pidPath string) (bool, error) {
	cmd := exec.Command(lsofPath, "-t", "--", pidPath)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	if runErr != nil {
		if exitErr, ok := runErr.(*exec.ExitError); ok && exitErr.ExitCode() == 1 && stdout.Len() == 0 {
			// lsof's documented "nothing matched" exit status.
			return false, nil
		}
		return false, fmt.Errorf("lsof -t %s: %w (stderr: %s)", pidPath, runErr, strings.TrimSpace(stderr.String()))
	}
	scanner := bufio.NewScanner(&stdout)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		holderPID, err := strconv.Atoi(line)
		if err != nil {
			return false, fmt.Errorf("lsof -t %s: unexpected output line %q", pidPath, line)
		}
		if holderPID == pid {
			return true, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return false, fmt.Errorf("lsof -t %s: read output: %w", pidPath, err)
	}
	return false, nil
}
