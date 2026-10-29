package bridge_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/victorarias/attn/internal/bridge"
	"golang.org/x/crypto/ssh"
	"golang.org/x/sys/unix"
)

type queueSocket struct {
	*net.TCPConn
	accepted atomic.Int64
	active   atomic.Bool
	peak     atomic.Int64
	queued   chan struct{}
	once     sync.Once
}

func (c *queueSocket) Write(data []byte) (int, error) {
	socket, err := c.SyscallConn()
	if err != nil {
		return 0, err
	}
	written := 0
	var cause error
	err = socket.Write(func(fd uintptr) bool {
		for len(data) > 0 {
			n, e := unix.Write(int(fd), data)
			if n > 0 {
				written += n
				accepted := c.accepted.Add(int64(n))
				data = data[n:]
				queued, err := socketUnsent(fd, accepted)
				if err != nil {
					cause = err
					return true
				}
				if c.active.Load() {
					if queued > c.peak.Load() {
						c.peak.Store(queued)
					}
					fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLOUT}}
					for {
						_, err = unix.Poll(fds, 0)
						if err != unix.EINTR {
							break
						}
					}
					if err != nil {
						cause = err
						return true
					}
					if queued >= queueLowWaterBytes && fds[0].Revents == 0 {
						c.once.Do(func() { close(c.queued) })
					}
				}
			}
			if e == unix.EINTR {
				continue
			}
			if e == unix.EAGAIN {
				return false
			}
			if e != nil {
				cause = e
				return true
			}
		}
		return true
	})
	if err != nil {
		return written, err
	}
	return written, cause
}

type heldSocket struct {
	*net.TCPConn
	active  *atomic.Bool
	held    chan struct{}
	release chan struct{}
	once    sync.Once
}

func (c *heldSocket) Read(data []byte) (int, error) {
	if c.active.Load() {
		c.once.Do(func() { close(c.held) })
		<-c.release
	}
	return c.TCPConn.Read(data)
}

func TestBulkKernelQueueIsBounded(t *testing.T) {
	for _, upload := range []bool{true, false} {
		t.Run(fmt.Sprintf("upload=%v", upload), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				w := pair(t)
				var active atomic.Bool
				var senderSocket *queueSocket
				var receiverSocket *heldSocket
				wrap := func(c net.Conn, homeSide bool) (net.Conn, error) {
					if err := smallSocket(c); err != nil {
						return nil, err
					}
					if homeSide == upload {
						senderSocket = &queueSocket{TCPConn: c.(*net.TCPConn), queued: make(chan struct{})}
						return senderSocket, nil
					}
					receiverSocket = &heldSocket{TCPConn: c.(*net.TCPConn), active: &active, held: make(chan struct{}), release: make(chan struct{})}
					return receiverSocket, nil
				}
				accepted := make(chan *bridge.BulkStream, 1)
				barrierReceived := make(chan struct{})
				w.cfg.Handlers = bridge.Handlers{
					"queue-barrier": func(_ *bridge.Conn, in ssh.NewChannel) {
						senderSocket.active.Store(false)
						close(barrierReceived)
						go func() { _ = in.Reject(ssh.Prohibited, "barrier reached") }()
					},
					"bulk": func(c *bridge.Conn, in ssh.NewChannel) {
						go func() {
							channel, requests, err := in.Accept()
							if err == nil {
								accepted <- c.Bulk(w.ctx, channel, requests)
							}
						}()
					},
					"input": func(_ *bridge.Conn, in ssh.NewChannel) {
						go bridge.Answer(in, func(_ context.Context, raw []byte) (any, error) { return string(raw), nil })
					},
				}
				client, server := tcpPair(t, w, wrap)
				defer client.Close()
				defer server.Close()
				channel, requests, err := client.Open(w.ctx, "bulk", nil)
				if err != nil {
					t.Fatal(err)
				}
				a, b := client.Bulk(w.ctx, channel, requests), <-accepted
				defer a.Close()
				defer b.Close()
				sender, receiver := a, b
				if !upload {
					sender, receiver = b, a
				}
				var release sync.Once
				resume := func() { release.Do(func() { close(receiverSocket.release) }) }
				defer resume()

				warm := make([]byte, 1<<20)
				warmWritten := make(chan error, 1)
				go func() { _, err := sender.Write(warm); warmWritten <- err }()
				if _, err := io.ReadFull(receiver, make([]byte, len(warm))); err != nil {
					t.Fatal(err)
				}
				if err := <-warmWritten; err != nil {
					t.Fatal(err)
				}
				calibration := bytes.Repeat([]byte("c"), queueLowWaterBytes)
				calibrationDone := make(chan error, 1)
				before := senderSocket.accepted.Load()
				go func() { _, err := sender.Write(calibration); calibrationDone <- err }()
				gotCalibration := make([]byte, len(calibration))
				if _, err := io.ReadFull(receiver, gotCalibration); err != nil {
					t.Fatal(err)
				}
				if err := <-calibrationDone; err != nil {
					t.Fatal(err)
				}
				framingBytes := senderSocket.accepted.Load() - before - int64(len(calibration))
				maxUnsentBytes := 2*queueLowWaterBytes + framingBytes
				payload := bytes.Repeat([]byte("kernel queue"), 128<<10)
				read := make(chan error, 1)
				write := make(chan error, 1)
				go func() {
					got := make([]byte, len(payload))
					_, err := io.ReadFull(receiver, got)
					if err == nil && !bytes.Equal(got, payload) {
						err = io.ErrUnexpectedEOF
					}
					read <- err
				}()
				senderSocket.active.Store(true)
				active.Store(true)
				go func() { _, err := sender.Write(payload); write <- err }()
				select {
				case <-receiverSocket.held:
				case <-client.Done():
					t.Fatal(client.Err())
				}
				select {
				case <-senderSocket.queued:
				case <-client.Done():
					t.Fatal(client.Err())
				}
				barrierDone := make(chan error, 1)
				peer := server
				if !upload {
					peer = client
				}
				go func() {
					_, _, err := peer.Open(w.ctx, "queue-barrier", make([]byte, bridge.MaxCallRequest/2))
					barrierDone <- err
				}()
				select {
				case <-barrierReceived:
				case <-client.Done():
					t.Fatal(client.Err())
				}
				resume()
				if err := <-barrierDone; err == nil {
					t.Fatal("barrier channel was accepted")
				}
				called := make(chan error, 1)
				go func() {
					var echo string
					err := client.Call(w.ctx, "input", "key", &echo)
					if err == nil && echo != `"key"` {
						err = fmt.Errorf("echo=%q", echo)
					}
					called <- err
				}()
				if err := <-called; err != nil {
					t.Fatal(err)
				}
				if err := <-write; err != nil {
					t.Fatal(err)
				}
				if err := <-read; err != nil {
					t.Fatal(err)
				}
				senderSocket.active.Store(false)
				peak := senderSocket.peak.Load()
				t.Logf("upload=%v peak_unsent=%d", upload, peak)
				if peak > maxUnsentBytes {
					t.Errorf("max_unsent_bytes=%d, asked=%d", maxUnsentBytes, peak)
				}
			})
		})
	}
}
