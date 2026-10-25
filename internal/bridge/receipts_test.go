//go:build bridge_receipts

package bridge_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/victorarias/attn/internal/bridge"
	"github.com/victorarias/attn/internal/bridge/memnet"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/ptybackend"
	"golang.org/x/crypto/ssh"
)

func TestBridgeFloodsLeaveTypingAndHeartbeatsResponsive(t *testing.T) {
	for _, floods := range []int{0, 1, 4, 16} {
		t.Run(fmt.Sprint(floods), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				w := pair(t)
				w.network = memnet.Listen(memnet.Rate(2_800_000))
				echo := newTerminalOutput()
				defer echo.Close()
				var sources sync.WaitGroup
				w.cfg.Handlers = bridge.Handlers{
					bridge.AttachChannel: func(conn *bridge.Conn, incoming ssh.NewChannel) {
						go conn.ServeAttach(w.ctx, incoming, func(req bridge.AttachRequest) (ptybackend.AttachInfo, ptybackend.Stream, error) {
							if req.Terminal == "echo" {
								return ptybackend.AttachInfo{}, echo, nil
							}
							output := newTerminalOutput()
							sources.Go(func() {
								defer close(output.events)
								data := bytes.Repeat([]byte("flood\r\n"), 4096/7)
								for seq := uint32(1); output.write(ptybackend.OutputEvent{Kind: ptybackend.OutputEventKindOutput, Data: data, Seq: seq}); seq++ {
								}
							})
							return ptybackend.AttachInfo{GhosttySnapshot: make([]byte, 28_000)}, output, nil
						})
					},
					"input": func(conn *bridge.Conn, incoming ssh.NewChannel) {
						go bridge.Answer(incoming, func(_ context.Context, raw []byte) (any, error) {
							var n uint32
							if err := json.Unmarshal(raw, &n); err != nil {
								return nil, err
							}
							echo.write(ptybackend.OutputEvent{Kind: ptybackend.OutputEventKindOutput, Seq: n})
							return nil, nil
						})
					},
				}
				w.start()
				client, server := bridged(t, w, nil)
				var states []bridge.State
				var stateMu sync.Mutex
				var observers sync.WaitGroup
				for _, conn := range []*bridge.Conn{client, server} {
					observers.Go(func() {
						for state := range conn.States() {
							stateMu.Lock()
							states = append(states, state)
							stateMu.Unlock()
						}
					})
				}
				_, stream, err := client.Attach(w.ctx, bridge.AttachRequest{Terminal: "echo"})
				if err != nil {
					t.Fatal(err)
				}
				defer stream.Close()
				ctx, cancel := context.WithCancel(w.ctx)
				var readers sync.WaitGroup
				var recoveries sync.Mutex
				recoveryCount := 0
				for i := 0; i < floods; i++ {
					readers.Go(func() {
						for ctx.Err() == nil {
							_, flood, err := client.Attach(ctx, bridge.AttachRequest{Terminal: harness.TerminalID(fmt.Sprintf("flood-%d", i))})
							if err != nil {
								return
							}
							stop := context.AfterFunc(ctx, func() { _ = flood.Close() })
							for event := range flood.Events() {
								ack(flood)
								if event.Kind == ptybackend.OutputEventKindDesync {
									recoveries.Lock()
									recoveryCount++
									recoveries.Unlock()
								}
							}
							stop()
							_ = flood.Close()
						}
					})
				}
				sent := make([]time.Time, 1000)
				sendDone := make(chan error, 1)
				go func() {
					ticker := time.NewTicker(30 * time.Millisecond)
					defer ticker.Stop()
					results := make(chan error, len(sent))
					for i := range sent {
						<-ticker.C
						sent[i] = time.Now()
						go func() { results <- client.Call(w.ctx, "input", uint32(i), nil) }()
					}
					var failure error
					for range sent {
						if err := <-results; err != nil {
							failure = err
						}
					}
					sendDone <- failure
				}()

				elapsed := make([]time.Duration, 0, len(sent))
				for range sent {
					event, ok := <-stream.Events()
					if !ok {
						t.Fatal("echo stream ended")
					}
					elapsed = append(elapsed, time.Since(sent[event.Seq]))
					ack(stream)
				}
				if err := <-sendDone; err != nil {
					t.Fatal(err)
				}
				cancel()
				readers.Wait()
				sources.Wait()
				slices.Sort(elapsed)
				t.Logf("rate=2800000B/s floods=%d samples=%d echo_p50=%s echo_p99=%s max=%s recoveries=%d credit=%d", floods, len(elapsed), elapsed[len(elapsed)/2], elapsed[989], elapsed[len(elapsed)-1], recoveryCount, bridge.TerminalOutputCredit)
				_ = client.Close()
				_ = server.Close()
				observers.Wait()

				for _, state := range states {
					if state == bridge.StateStalled {
						t.Error("flood stalled a heartbeat")
					}
				}
			})
		})
	}
}
