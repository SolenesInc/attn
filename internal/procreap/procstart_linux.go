package procreap

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// One USER_HZ tick; two processes born inside it share a stamp. Receipt: 1949409
// forks at 8123/s on the Linux target produced no pid reuse (a wrap needs ~8.6 min).
const stampResolution = 10 * time.Millisecond

func processStartTime(pid int) (string, error) {
	raw, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return "", fmt.Errorf("read start time of pid %d: %w", pid, err)
	}
	// comm (field 2) is an arbitrary string in parentheses; starttime is field 22
	// overall, so the 20th after the state field that follows the closing paren.
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
