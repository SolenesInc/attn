package procreap

import (
	"fmt"
	"time"

	"golang.org/x/sys/unix"
)

const stampResolution = time.Microsecond

func processStartTime(pid int) (string, error) {
	proc, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return "", fmt.Errorf("read start time of pid %d: %w", pid, err)
	}
	start := proc.Proc.P_starttime
	if start.Sec == 0 && start.Usec == 0 {
		return "", fmt.Errorf("pid %d reports no start time", pid)
	}
	return fmt.Sprintf("%d.%06d", start.Sec, start.Usec), nil
}
