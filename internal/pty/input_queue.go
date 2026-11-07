package pty

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

const InputQueueMaxBytes = 2 * protocol.MaxSocketFrameBytes

type inputQueue struct {
	mu      sync.Mutex
	changed sync.Cond
	chunks  [][]byte
	waiting int
	failure error
	paused  bool
	writing bool
	stopped bool
}

func newInputQueue(pending []byte) *inputQueue {
	q := &inputQueue{}
	q.changed.L = &q.mu
	if len(pending) > 0 {
		q.chunks = [][]byte{pending}
		q.waiting = len(pending)
	}
	return q
}

func (q *inputQueue) push(data []byte) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.waiting+len(data) > InputQueueMaxBytes {
		return fmt.Errorf("%w: the program in this terminal is not reading its input: the input queue holds at most %d bytes, %d are waiting and this write adds %d", ErrInputQueueFull, InputQueueMaxBytes, q.waiting, len(data))
	}
	q.chunks = append(q.chunks, append([]byte(nil), data...))
	q.waiting += len(data)
	q.changed.Broadcast()
	return nil
}

func (q *inputQueue) takeFailure() error {
	q.mu.Lock()
	defer q.mu.Unlock()
	failure := q.failure
	q.failure = nil
	return failure
}

func (q *inputQueue) next() ([]byte, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.writing = false
	q.changed.Broadcast()
	for !q.stopped && (q.paused || len(q.chunks) == 0) {
		q.changed.Wait()
	}
	if q.stopped {
		return nil, false
	}
	q.writing = true
	return q.chunks[0], true
}

func (q *inputQueue) wrote(written int, err error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.waiting -= written
	if rest := q.chunks[0][written:]; len(rest) > 0 {
		q.chunks[0] = rest
	} else {
		q.chunks = q.chunks[1:]
	}
	if err != nil && !errors.Is(err, os.ErrDeadlineExceeded) {
		q.chunks, q.waiting, q.failure = nil, 0, fmt.Errorf("write PTY: %w", err)
	}
}

func (q *inputQueue) pauseAndCopyPending(interrupt func()) []byte {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.paused = true
	interrupt()
	for q.writing && !q.stopped {
		q.changed.Wait()
	}
	pending := make([]byte, 0, q.waiting)
	for _, chunk := range q.chunks {
		pending = append(pending, chunk...)
	}
	return pending
}

func (q *inputQueue) stop() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.stopped = true
	q.changed.Broadcast()
}

func (s *Session) startInputWriter() {
	go func() {
		for {
			chunk, ok := s.inputs.next()
			if !ok {
				return
			}
			written, err := s.ptmx.Write(chunk)
			s.inputs.wrote(written, err)
		}
	}()
}

func (s *Session) pauseInputForHandoff() []byte {
	return s.inputs.pauseAndCopyPending(func() { _ = s.ptmx.SetWriteDeadline(time.Unix(1, 0)) })
}

func (s *Session) replyToProgram(reply []byte) {
	_ = s.inputs.push(reply)
}
