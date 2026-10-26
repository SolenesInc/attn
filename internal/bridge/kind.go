package bridge

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"syscall"
)

func ErrorKind(err error) string {
	var refusal *Refusal
	var netErr net.Error
	switch {
	case err == nil:
		return ""
	case errors.As(err, &refusal):
		return "refused " + refusal.Reason
	case errors.Is(err, errNoFrames), errors.Is(err, context.DeadlineExceeded), errors.Is(err, os.ErrDeadlineExceeded), errors.As(err, &netErr) && netErr.Timeout():
		return "timeout"
	case errors.Is(err, syscall.ECONNREFUSED):
		return "connection refused"
	case errors.Is(err, syscall.ECONNRESET), errors.Is(err, syscall.ECONNABORTED), errors.Is(err, syscall.EPIPE), errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return "connection closed"
	case errors.Is(err, syscall.EHOSTUNREACH), errors.Is(err, syscall.ENETUNREACH), errors.Is(err, syscall.EHOSTDOWN):
		return "unreachable"
	}
	return err.Error()
}
