package main

import (
	"fmt"
	"os"
	"strings"
)

func processExecutable(pid int) (string, error) {
	exe, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid))
	if err != nil {
		return "", fmt.Errorf("read /proc/%d/exe: %w", pid, err)
	}
	// An app still running out of a replaced install tree is exactly what we must
	// stop; Linux marks its unlinked image, the path is still ours.
	return strings.TrimSuffix(exe, " (deleted)"), nil
}

// SIGTERM is the whole quit request here; there is no bundle broker to ask.
func requestAppQuit(string) bool { return false }

func stopAppWithoutPIDFile(_ profileResolved, pidPath string) (string, error) {
	return "not running (no " + pidPath + ")", nil
}
