package bridge

import (
	"context"
	"fmt"
	"net"
	"os"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const bulkChunkBytes = 8 << 10

type bulkPacer struct {
	mu     sync.Mutex
	file   *os.File
	socket syscall.RawConn
}

func newBulkPacer(raw net.Conn) (*bulkPacer, error) {
	tcp, ok := raw.(*net.TCPConn)
	if !ok {
		return nil, nil
	}
	socket, err := tcp.SyscallConn()
	if err != nil {
		return nil, err
	}
	var duplicate int
	var dupErr error
	if err := socket.Control(func(fd uintptr) {
		duplicate, dupErr = unix.FcntlInt(fd, unix.F_DUPFD_CLOEXEC, 0)
	}); err != nil {
		return nil, err
	}
	if dupErr != nil {
		return nil, dupErr
	}
	file := os.NewFile(uintptr(duplicate), "bridge bulk readiness")
	socket, err = file.SyscallConn()
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	return &bulkPacer{file: file, socket: socket}, nil
}

func (p *bulkPacer) close() {
	if p != nil {
		_ = p.file.Close()
	}
}

func (p *bulkPacer) wait(ctx context.Context) error {
	var ended chan struct{}
	var stop func() bool
	var pollErr error
	err := p.socket.Write(func(fd uintptr) bool {
		fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLOUT}}
		for {
			_, pollErr = unix.Poll(fds, 0)
			if pollErr != unix.EINTR {
				break
			}
		}
		ready := pollErr != nil || fds[0].Revents != 0
		if !ready && stop == nil {
			ended = make(chan struct{})
			stop = context.AfterFunc(ctx, func() { _ = p.file.SetWriteDeadline(time.Now()); close(ended) })
		}
		return ready
	})
	if stop != nil {
		if !stop() {
			<-ended
		}
		_ = p.file.SetWriteDeadline(time.Time{})
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		return err
	}
	return pollErr
}

func boundUnsentBytes(raw net.Conn) error {
	tcp, ok := raw.(*net.TCPConn)
	if !ok {
		return nil
	}
	socket, err := tcp.SyscallConn()
	if err != nil {
		return err
	}
	var setErr error
	if err := socket.Control(func(fd uintptr) {
		setErr = unix.SetsockoptInt(int(fd), unix.IPPROTO_TCP, unix.TCP_NOTSENT_LOWAT, bulkChunkBytes)
	}); err != nil {
		return err
	}
	if setErr != nil {
		return fmt.Errorf("bridge TCP_NOTSENT_LOWAT=%d: %w", bulkChunkBytes, setErr)
	}
	return nil
}
