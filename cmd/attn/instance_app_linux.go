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
	return strings.TrimSuffix(exe, " (deleted)"), nil
}

func requestAppQuit(string) bool { return false }

func stopAppWithoutPIDFile(_ instanceResolved, pidPath string) (string, error) {
	return "not running (no " + pidPath + ")", nil
}
