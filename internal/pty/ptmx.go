package pty

import (
	"fmt"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

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

func adoptPTMX(fd int) (*os.File, error) {
	if err := syscall.SetNonblock(fd, true); err != nil {
		return nil, fmt.Errorf("set adopted pty master non-blocking: %w", err)
	}
	return os.NewFile(uintptr(fd), "ptmx"), nil
}

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

func setWinsize(fd uintptr, cols, rows, xpixel, ypixel uint16) error {
	return unix.IoctlSetWinsize(int(fd), unix.TIOCSWINSZ, &unix.Winsize{
		Row: rows, Col: cols, Xpixel: xpixel, Ypixel: ypixel,
	})
}
