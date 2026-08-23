package pty

import (
	"fmt"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

// A session's PTY master is held as a POLLABLE file: O_NONBLOCK on the file
// description and registered with the Go runtime's poller. creackpty hands
// back a blocking master, on which SetReadDeadline fails outright ("file type
// does not support deadline") and a read can only be ended by closing the fd —
// which kills the PTY.
//
// A pollable master is what lets the read loop stop at a chunk boundary with
// bytes still in the kernel, so an in-place upgrade hands the rest to the next
// image instead of dropping it. Measured: swapping without that quiesce lost
// ~1KB of output in two runs out of three; with it, eight runs of a 15MB
// stream crossed with no gap and no repeat. Receipts:
// docs/plans/2026-08-22-worker-inplace-upgrade.md.
//
// O_NONBLOCK sits on the master's file description; the child writing to the
// slave never sees it.

// pollablePTMX returns a pollable *os.File over the same PTY master and closes
// the original. The fd is duplicated first, so the master stays open and the
// child notices nothing.
func pollablePTMX(f *os.File) (*os.File, error) {
	fd, err := syscall.Dup(int(f.Fd()))
	if err != nil {
		return nil, fmt.Errorf("dup pty master: %w", err)
	}
	if err := syscall.SetNonblock(fd, true); err != nil {
		_ = syscall.Close(fd)
		return nil, fmt.Errorf("set pty master non-blocking: %w", err)
	}
	out := os.NewFile(uintptr(fd), "ptmx")
	_ = f.Close()
	return out, nil
}

// adoptPTMX wraps an fd inherited across an in-place upgrade. It arrives
// already non-blocking (O_NONBLOCK lives on the file description, which
// survives execve), so this only re-asserts it and hands it to the poller.
func adoptPTMX(fd int) (*os.File, error) {
	if err := syscall.SetNonblock(fd, true); err != nil {
		return nil, fmt.Errorf("set adopted pty master non-blocking: %w", err)
	}
	return os.NewFile(uintptr(fd), "ptmx"), nil
}

// withPTMXFd runs fn on the master's raw descriptor without disturbing the
// poller registration, which File.Fd() is entitled to drop. Callers hold
// writeMu: the fd must not be closed underneath fn.
func (s *Session) withPTMXFd(fn func(fd uintptr) error) error {
	conn, err := s.ptmx.SyscallConn()
	if err != nil {
		return err
	}
	var inner error
	if err := conn.Control(func(fd uintptr) { inner = fn(fd) }); err != nil {
		return err
	}
	return inner
}

// setWinsize is creackpty.Setsize without the File.Fd() call.
func setWinsize(fd uintptr, cols, rows, xpixel, ypixel uint16) error {
	return unix.IoctlSetWinsize(int(fd), unix.TIOCSWINSZ, &unix.Winsize{
		Row: rows, Col: cols, Xpixel: xpixel, Ypixel: ypixel,
	})
}
