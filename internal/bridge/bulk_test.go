package bridge_test

import (
	"bytes"
	"context"
	"io"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/victorarias/attn/internal/bridge"
	"github.com/victorarias/attn/internal/bridge/memnet"
	"github.com/victorarias/attn/internal/ptybackend"
	"golang.org/x/crypto/ssh"
)

func TestDuplexBulkPreservesTerminalEchoAndHeartbeats(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := pair(t)
		w.network = memnet.Listen(memnet.Rate(2_800_000))
		accepted := make(chan *bridge.BulkStream, 1)
		echo := newTerminalOutput()
		w.cfg.Handlers = bridge.Handlers{
			"bulk": func(c *bridge.Conn, incoming ssh.NewChannel) {
				go func() {
					channel, requests, err := incoming.Accept()
					if err == nil {
						accepted <- c.Bulk(w.ctx, channel, requests)
					}
				}()
			},
			bridge.AttachChannel: func(c *bridge.Conn, incoming ssh.NewChannel) {
				go c.ServeAttach(w.ctx, incoming, func(bridge.AttachRequest) (ptybackend.AttachInfo, ptybackend.Stream, error) {
					return ptybackend.AttachInfo{}, echo, nil
				})
			},
			"input": func(_ *bridge.Conn, incoming ssh.NewChannel) {
				go bridge.Answer(incoming, func(context.Context, []byte) (any, error) {
					echo.write(ptybackend.OutputEvent{Kind: ptybackend.OutputEventKindOutput, Data: []byte("echo")})
					return nil, nil
				})
			},
		}
		w.start()
		client, server := bridged(t, w, nil)
		channel, requests, err := client.Open(w.ctx, "bulk", nil)
		if err != nil {
			t.Fatal(err)
		}
		a, b := client.Bulk(w.ctx, channel, requests), <-accepted
		defer a.Close()
		defer b.Close()
		_, terminal, err := client.Attach(w.ctx, bridge.AttachRequest{Terminal: "echo"})
		if err != nil {
			t.Fatal(err)
		}
		defer terminal.Close()
		var workers sync.WaitGroup
		stalled := make(chan bridge.State, 4)
		for _, c := range []*bridge.Conn{client, server} {
			workers.Go(func() {
				for state := range c.States() {
					if state == bridge.StateStalled {
						stalled <- state
					}
				}
			})
		}
		echoInput := func() {
			if err := client.Call(w.ctx, "input", struct{}{}, nil); err != nil {
				t.Fatal(err)
			}
			if event := <-terminal.Events(); string(event.Data) != "echo" {
				t.Fatalf("terminal output=%q", event.Data)
			}
			ack(terminal)
		}
		for range 100 {
			echoInput()
		}
		payload := bytes.Repeat([]byte("duplex-bulk"), 1<<20)
		results := make(chan error, 4)
		for _, s := range []*bridge.BulkStream{a, b} {
			go func() {
				_, err := s.Write(payload)
				if err == nil {
					err = s.CloseWrite()
				}
				results <- err
			}()
			go func() {
				got, err := io.ReadAll(s)
				if err == nil && !bytes.Equal(got, payload) {
					err = io.ErrUnexpectedEOF
				}
				results <- err
			}()
		}
		for range 100 {
			echoInput()
		}
		for range 4 {
			if err := <-results; err != nil {
				t.Fatal(err)
			}
		}
		_ = client.Close()
		_ = server.Close()
		workers.Wait()
		close(stalled)
		for state := range stalled {
			t.Errorf("bulk caused heartbeat state=%s", state)
		}
	})
}

func TestBulkDrainsBufferedBytesAfterPeerCloses(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := pair(t)
		payload := bytes.Repeat([]byte("buffered"), 32<<10)
		w.cfg.Handlers = bridge.Handlers{
			"bulk": func(c *bridge.Conn, incoming ssh.NewChannel) {
				go func() {
					channel, requests, err := incoming.Accept()
					if err != nil {
						return
					}
					stream := c.Bulk(w.ctx, channel, requests)
					defer stream.Close()
					_, _ = stream.Write(payload)
				}()
			},
		}
		w.start()
		client, _ := bridged(t, w, nil)
		channel, requests, err := client.Open(w.ctx, "bulk", nil)
		if err != nil {
			t.Fatal(err)
		}
		stream := client.Bulk(w.ctx, channel, requests)
		defer stream.Close()
		synctest.Wait()
		var got bytes.Buffer
		_, err = io.CopyBuffer(&got, struct{ io.Reader }{stream}, make([]byte, 4096))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got.Bytes(), payload) {
			t.Fatalf("received %d bytes after peer closed, wanted %d", got.Len(), len(payload))
		}
	})
}
