package pty

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"sync"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

const InputQueueMaxBytes = 2 * protocol.MaxSocketFrameBytes

type inputChunk struct {
	data    []byte
	settled chan error
}

func (c *inputChunk) settle(err error) {
	if c.settled != nil {
		c.settled <- err
		c.settled = nil
	}
}

type inputQueue struct {
	mu          sync.Mutex
	changed     sync.Cond
	chunks      []*inputChunk
	waiting     int
	failure     error
	paused      bool
	withdrawing bool
	writing     bool
	stopped     bool
}

func newInputQueue(pending []byte) *inputQueue {
	q := &inputQueue{}
	q.changed.L = &q.mu
	if len(pending) > 0 {
		q.chunks = []*inputChunk{{data: pending}}
		q.waiting = len(pending)
	}
	return q
}

func (q *inputQueue) push(data []byte) error {
	return q.pushChunk(&inputChunk{data: append([]byte(nil), data...)})
}

func (q *inputQueue) pushChunk(chunk *inputChunk) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.stopped {
		return errSessionNotRunning
	}
	if q.waiting+len(chunk.data) > InputQueueMaxBytes {
		return fmt.Errorf("%w: the program in this terminal is not reading its input: the input queue holds at most %d bytes, %d are waiting and this write adds %d", ErrInputQueueFull, InputQueueMaxBytes, q.waiting, len(chunk.data))
	}
	q.chunks = append(q.chunks, chunk)
	q.waiting += len(chunk.data)
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
	for !q.stopped && (q.paused || q.withdrawing || len(q.chunks) == 0) {
		q.changed.Wait()
	}
	if q.stopped {
		return nil, false
	}
	q.writing = true
	return q.chunks[0].data, true
}

func (q *inputQueue) wrote(written int, err error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.waiting -= written
	front := q.chunks[0]
	front.data = front.data[written:]
	if len(front.data) == 0 {
		q.chunks = q.chunks[1:]
		front.settle(nil)
	}
	if err != nil && !errors.Is(err, os.ErrDeadlineExceeded) {
		q.failure = fmt.Errorf("write PTY: %w", err)
		q.settleAll(q.failure)
		q.chunks, q.waiting = nil, 0
	}
}

func (q *inputQueue) settleAll(err error) {
	for _, chunk := range q.chunks {
		chunk.settle(err)
	}
}

func (q *inputQueue) withdraw(chunk *inputChunk, interrupt, resume func()) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	for q.withdrawing && !q.stopped {
		q.changed.Wait()
	}
	if len(q.chunks) > 0 && q.chunks[0] == chunk {
		q.withdrawing = true
		interrupt()
		for q.writing && !q.stopped {
			q.changed.Wait()
		}
		resume()
		q.withdrawing = false
		q.changed.Broadcast()
	}
	at := slices.Index(q.chunks, chunk)
	if q.stopped || at < 0 {
		return false
	}
	q.waiting -= len(chunk.data)
	q.chunks = slices.Delete(q.chunks, at, at+1)
	chunk.settled = nil
	return true
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
		pending = append(pending, chunk.data...)
	}
	return pending
}

func (q *inputQueue) stop() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.stopped = true
	q.settleAll(errSessionNotRunning)
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

func (s *Session) placeInput(data []byte, within time.Duration) error {
	if err := s.takingInput(); err != nil {
		return err
	}
	settled := make(chan error, 1)
	chunk := &inputChunk{data: append([]byte(nil), data...), settled: settled}
	if err := s.inputs.pushChunk(chunk); err != nil {
		return err
	}
	timer := time.NewTimer(within)
	defer timer.Stop()
	select {
	case err := <-settled:
		return err
	case <-timer.C:
	}
	interrupt := func() { _ = s.ptmx.SetWriteDeadline(time.Unix(1, 0)) }
	resume := func() { _ = s.ptmx.SetWriteDeadline(time.Time{}) }
	if s.inputs.withdraw(chunk, interrupt, resume) {
		return fmt.Errorf("the program in this terminal is not reading its input: this write did not finish within %d ms, and attn withdrew what it had not written", within.Milliseconds())
	}
	return <-settled
}

func (s *Session) pauseInputForHandoff() []byte {
	return s.inputs.pauseAndCopyPending(func() { _ = s.ptmx.SetWriteDeadline(time.Unix(1, 0)) })
}

func (s *Session) replyToProgram(reply []byte) {
	_ = s.inputs.push(reply)
}
