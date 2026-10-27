package pty

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func sessionProcesses(sid int) ([]int, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	var processes []int
	sessionID := strconv.Itoa(sid)
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		raw, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "stat"))
		if err != nil {
			continue
		}
		end := strings.LastIndexByte(string(raw), ')')
		if end < 0 {
			continue
		}
		fields := strings.Fields(string(raw[end+1:]))
		if len(fields) < 4 || fields[3] != sessionID {
			continue
		}
		processes = append(processes, pid)
	}
	return processes, nil
}
