package bridge

import (
	"fmt"
	"net"

	"golang.org/x/sys/unix"
)

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
		setErr = unix.SetsockoptInt(int(fd), unix.IPPROTO_TCP, unix.TCP_NOTSENT_LOWAT, outputChunkBytes)
	}); err != nil {
		return err
	}
	if setErr != nil {
		return fmt.Errorf("bridge TCP_NOTSENT_LOWAT=%d: %w", outputChunkBytes, setErr)
	}
	return nil
}
