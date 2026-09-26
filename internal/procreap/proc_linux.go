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
	fields, err := procStatFields(pid)
	if err != nil {
		return "", fmt.Errorf("read start time of pid %d: %w", pid, err)
	}
	if len(fields) < 20 {
		return "", fmt.Errorf("unparseable /proc/%d/stat: %d fields after comm", pid, len(fields))
	}
	return fields[19], nil
}

func isZombie(pid int) bool {
	fields, err := procStatFields(pid)
	return err == nil && len(fields) > 0 && fields[0] == "Z"
}

func procStatFields(pid int) ([]string, error) {
	raw, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return nil, err
	}
	rest := string(raw)
	i := strings.LastIndexByte(rest, ')')
	if i < 0 {
		return nil, fmt.Errorf("unparseable /proc/%d/stat", pid)
	}
	return strings.Fields(rest[i+1:]), nil
}
