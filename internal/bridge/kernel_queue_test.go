//go:build bridge_receipts

package bridge_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/bridge"
	"golang.org/x/crypto/ssh"
	"golang.org/x/time/rate"
)

type slowSocket struct {
	net.Conn
	ctx     context.Context
	active  *atomic.Bool
	limiter *rate.Limiter
}

func (c *slowSocket) Read(p []byte) (int, error) {
	if c.active.Load() {
		p = p[:min(len(p), c.limiter.Burst())]
		if err := c.limiter.WaitN(c.ctx, len(p)); err != nil {
			return 0, err
		}
	}
	return c.Conn.Read(p)
}

func kernelPair(t *testing.T, w *pairWorld, upload bool, active *atomic.Bool) (*bridge.Conn, *bridge.Conn) {
	return tcpPair(t, w, func(c net.Conn, home bool) (net.Conn, error) {
		if err := smallSocket(c); err != nil {
			return nil, err
		}
		if home != upload {
			return &slowSocket{Conn: c, ctx: w.ctx, active: active, limiter: rate.NewLimiter(1_140_000, 4096)}, nil
		}
		return c, nil
	})
}

func TestBulkKernelQueueLeavesCallsResponsive(t *testing.T) {
	for _, upload := range []bool{true, false} {
		t.Run(fmt.Sprintf("upload=%v", upload), func(t *testing.T) {
			w := pair(t)
			var active atomic.Bool
			accepted := make(chan *bridge.BulkStream, 1)
			w.cfg.Handlers = bridge.Handlers{
				"bulk": func(c *bridge.Conn, in ssh.NewChannel) {
					go func() {
						channel, requests, err := in.Accept()
						if err == nil {
							accepted <- c.Bulk(w.ctx, channel, requests)
						}
					}()
				},
				"input": func(_ *bridge.Conn, in ssh.NewChannel) {
					go bridge.Answer(in, func(_ context.Context, data []byte) (any, error) { return string(data), nil })
				},
			}
			client, server := kernelPair(t, w, upload, &active)
			defer client.Close()
			defer server.Close()
			open := func() (*bridge.BulkStream, *bridge.BulkStream) {
				channel, requests, err := client.Open(w.ctx, "bulk", nil)
				if err != nil {
					t.Fatal(err)
				}
				a, b := client.Bulk(w.ctx, channel, requests), <-accepted
				if upload {
					return a, b
				}
				return b, a
			}
			sender, receiver := open()
			defer sender.Close()
			defer receiver.Close()
			flowing := make(chan struct{})
			drained := make(chan struct{})
			go func() {
				defer close(drained)
				_, err := io.ReadFull(receiver, make([]byte, 512<<10))
				close(flowing)
				if err == nil {
					_, _ = io.Copy(io.Discard, receiver)
				}
			}()
			written := make(chan error, 1)
			active.Store(true)
			go func() { _, err := io.Copy(sender, zeros{}); written <- err }()
			<-flowing
			var worst time.Duration
			for range 4 {
				start := time.Now()
				var echo string
				if err := client.Call(w.ctx, "input", "key", &echo); err != nil {
					t.Fatal(err)
				}
				if echo != `"key"` {
					t.Fatalf("echo=%q", echo)
				}
				worst = max(worst, time.Since(start))
			}
			active.Store(false)
			_ = sender.Close()
			_ = receiver.Close()
			if err := <-written; err == nil {
				t.Fatal("closed bulk writer did not stop with an error")
			}
			<-drained
			sender, receiver = open()
			defer sender.Close()
			defer receiver.Close()
			payload := bytes.Repeat([]byte("after cancellation"), 1024)
			go func() { _, err := sender.Write(payload); written <- err }()
			got := make([]byte, len(payload))
			if _, err := io.ReadFull(receiver, got); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, payload) {
				t.Fatal("bulk payload changed")
			}
			if err := <-written; err != nil {
				t.Fatal(err)
			}
			t.Logf("upload=%v rate=1140000B/s echo_max=%s", upload, worst)
		})
	}
}
