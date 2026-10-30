package procwait

import (
	"errors"
	"time"

	"golang.org/x/sys/unix"
)

// Exited reports whether process pid exits within timeout, whoever its parent is.
func Exited(pid int, timeout time.Duration) (bool, error) {
	fd, err := unix.PidfdOpen(pid, 0)
	if errors.Is(err, unix.ESRCH) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	defer unix.Close(fd)
	fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
	for {
		n, err := unix.Poll(fds, int(timeout/time.Millisecond))
		if errors.Is(err, unix.EINTR) {
			continue
		}
		return n == 1, err
	}
}
