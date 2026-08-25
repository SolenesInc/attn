package procreap

import (
	"fmt"
	"time"

	"golang.org/x/sys/unix"
)

// Must stay well under the pid-reuse interval: on this machine the first reuse took 2m37s
// of nothing but forking — 92043 spawns at 588/s.
const stampResolution = time.Microsecond

// Reads kinfo_proc's p_starttime via sysctl, not `ps -o lstart=`: Darwin's ps(1) formats
// whole seconds and nothing below, leaving too little margin over the reuse floor above.
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
