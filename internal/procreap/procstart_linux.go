package procreap

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const stampResolution = 10 * time.Millisecond

func processStartTime(pid int) (string, error) {
	raw, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return "", fmt.Errorf("read start time of pid %d: %w", pid, err)
	}
	rest := string(raw)
	i := strings.LastIndexByte(rest, ')')
	if i < 0 {
		return "", fmt.Errorf("unparseable /proc/%d/stat", pid)
	}
	fields := strings.Fields(rest[i+1:])
	if len(fields) < 20 {
		return "", fmt.Errorf("unparseable /proc/%d/stat: %d fields after comm", pid, len(fields))
	}
	return fields[19], nil
}
