package procwait

import (
	"errors"
	"time"

	"golang.org/x/sys/unix"
)

// Exited reports whether process pid exits within timeout, whoever its parent is.
func Exited(pid int, timeout time.Duration) (bool, error) {
	kq, err := unix.Kqueue()
	if err != nil {
		return false, err
	}
	defer unix.Close(kq)
	watch := []unix.Kevent_t{{Ident: uint64(pid), Filter: unix.EVFILT_PROC, Flags: unix.EV_ADD | unix.EV_ONESHOT, Fflags: unix.NOTE_EXIT}}
	events := make([]unix.Kevent_t, 1)
	wait := unix.NsecToTimespec(timeout.Nanoseconds())
	for {
		n, err := unix.Kevent(kq, watch, events, &wait)
		switch {
		case errors.Is(err, unix.ESRCH):
			return true, nil
		case errors.Is(err, unix.EINTR):
			continue
		case err != nil:
			return false, err
		}
		if n == 1 && events[0].Flags&unix.EV_ERROR != 0 && unix.Errno(events[0].Data) == unix.ESRCH {
			return true, nil
		}
		return n == 1, nil
	}
}
