package diag

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

func BootTime() (time.Time, error) {
	stat, err := os.Open("/proc/stat")
	if err != nil {
		return time.Time{}, fmt.Errorf("read boot time: %w", err)
	}
	defer stat.Close()
	lines := bufio.NewScanner(stat)
	for lines.Scan() {
		value, found := strings.CutPrefix(lines.Text(), "btime ")
		if !found {
			continue
		}
		seconds, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		if err != nil {
			return time.Time{}, fmt.Errorf("read boot time: /proc/stat btime %q: %w", value, err)
		}
		return time.Unix(seconds, 0), nil
	}
	if err := lines.Err(); err != nil {
		return time.Time{}, fmt.Errorf("read boot time: %w", err)
	}
	return time.Time{}, fmt.Errorf("read boot time: /proc/stat has no btime line")
}
