//go:build bridge_receipts

package bridge_test

import (
	"io"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/bridge"
	"golang.org/x/crypto/ssh"
)

func TestKernelBulkThroughputReceipt(t *testing.T) {
	w := pair(t)
	accepted := make(chan *bridge.BulkStream, 1)
	w.cfg.Handlers = bridge.Handlers{"bulk": func(c *bridge.Conn, in ssh.NewChannel) {
		go func() {
			ch, requests, err := in.Accept()
			if err == nil {
				accepted <- c.Bulk(w.ctx, ch, requests)
			}
		}()
	}}
	client, server := tcpPair(t, w, nil)
	defer client.Close()
	defer server.Close()
	channel, requests, err := client.Open(w.ctx, "bulk", nil)
	if err != nil {
		t.Fatal(err)
	}
	a, b := client.Bulk(w.ctx, channel, requests), <-accepted
	defer a.Close()
	defer b.Close()
	const size = 128 << 20
	done := make(chan error, 4)
	start := time.Now()
	for _, s := range []*bridge.BulkStream{a, b} {
		go func() { _, err := io.CopyN(s, zeros{}, size); done <- err }()
		go func() { _, err := io.CopyN(io.Discard, s, size); done <- err }()
	}
	for range 4 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	elapsed := time.Since(start)
	t.Logf("bytes_each_direction=%d credit=%d elapsed=%s MB_s_each_direction=%.3f", size, bridge.BulkCredit, elapsed, float64(size)/1e6/elapsed.Seconds())
}
