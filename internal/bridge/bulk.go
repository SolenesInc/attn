package bridge

import (
	"context"
	"encoding/binary"
	"errors"
	"net"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

const BulkCredit = 256 << 10
const bulkAcknowledgment = "attn-consumed"

type BulkStream struct {
	ssh.Channel
	ctx           context.Context
	cancel        context.CancelFunc
	credit        *outputCredit
	flow          *outputFlow
	writeMu       sync.Mutex
	closeOnce     sync.Once
	stop          func() bool
	local, remote net.Addr
}

func (c *Conn) sender() *outputCredit {
	c.outputOnce.Do(func() { c.output = newOutputCredit(c.done) })
	return c.output
}

func (c *Conn) Bulk(ctx context.Context, channel ssh.Channel, requests <-chan *ssh.Request) *BulkStream {
	ctx, cancel := context.WithCancel(ctx)
	s := &BulkStream{Channel: channel, ctx: ctx, cancel: cancel, credit: c.sender(), flow: &outputFlow{window: BulkCredit, bulk: true}, local: c.transport.LocalAddr(), remote: c.transport.RemoteAddr()}
	s.stop = context.AfterFunc(ctx, func() { _ = channel.Close() })
	go func() {
		defer s.Close()
		for request := range requests {
			if request.Type == bulkAcknowledgment && len(request.Payload) == 4 {
				s.credit.returned(s.flow, int(binary.BigEndian.Uint32(request.Payload)))
			}
			if request.WantReply {
				_ = request.Reply(false, nil)
			}
		}
	}()
	return s
}

func (s *BulkStream) Read(data []byte) (int, error) {
	n, err := s.Channel.Read(data)
	if n > 0 {
		var receipt [4]byte
		binary.BigEndian.PutUint32(receipt[:], uint32(n))
		_, _ = s.Channel.SendRequest(bulkAcknowledgment, false, receipt[:])
	}
	return n, err
}

func (s *BulkStream) Write(data []byte) (int, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	written := 0
	for len(data) > 0 {
		chunk := data[:min(len(data), outputChunkBytes)]
		if err := s.credit.reserve(s.ctx, s.flow, len(chunk)); err != nil {
			return written, err
		}
		if err := s.credit.write(s.ctx, s.flow, chunk, s.Channel); err != nil {
			_ = s.Close()
			return written, err
		}
		written += len(chunk)
		data = data[len(chunk):]
	}
	return written, nil
}

func (s *BulkStream) Close() error {
	s.closeOnce.Do(func() {
		s.cancel()
		s.stop()
		_ = s.Channel.Close()
		s.credit.closeFlow(s.flow)
	})
	return nil
}

func (s *BulkStream) LocalAddr() net.Addr              { return s.local }
func (s *BulkStream) RemoteAddr() net.Addr             { return s.remote }
func (s *BulkStream) SetDeadline(time.Time) error      { return errors.ErrUnsupported }
func (s *BulkStream) SetReadDeadline(time.Time) error  { return errors.ErrUnsupported }
func (s *BulkStream) SetWriteDeadline(time.Time) error { return errors.ErrUnsupported }
