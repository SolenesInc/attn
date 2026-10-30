package bridge_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/victorarias/attn/internal/bridge"
	"github.com/victorarias/attn/internal/bridge/memnet"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/ptybackend"
	"golang.org/x/crypto/ssh"
)

func TestBridgeOutputFollowsThePath(t *testing.T) {
	for _, path := range []struct {
		name         string
		rate         float64
		rtt, changed time.Duration
	}{
		{"slow", 2_000_000, 30 * time.Millisecond, 0},
		{"short", 10_000_000, 27 * time.Millisecond, 0},
		{"long", 10_000_000, 54 * time.Millisecond, 0},
		{"halves", 10_000_000, 54 * time.Millisecond, 27 * time.Millisecond},
		{"doubles", 10_000_000, 27 * time.Millisecond, 54 * time.Millisecond},
	} {
		for _, workload := range []string{"bulk", "terminal", "maximum-terminal"} {
			t.Run(fmt.Sprintf("%s/%s", path.name, workload), func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					w := pair(t)
					w.network = memnet.Listen(memnet.Rate(path.rate), memnet.Delay(path.rtt/2))
					accepted := make(chan *bridge.BulkStream, 1)
					source := newTerminalOutput()
					w.cfg.Handlers = bridge.Handlers{
						"input": func(_ *bridge.Conn, incoming ssh.NewChannel) {
							go bridge.Answer(incoming, func(context.Context, []byte) (any, error) { return nil, nil })
						},
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
								return ptybackend.AttachInfo{}, source, nil
							})
						},
					}
					w.start()
					client, server := bridged(t, w, nil)
					defer client.Close()
					defer server.Close()
					var delivered atomic.Int64
					var completed atomic.Int64
					var completedRate float64
					measure := func() (time.Duration, float64) {
						start, before := time.Now(), delivered.Load()
						completedBefore := completed.Load()
						ticker := time.NewTicker(30 * time.Millisecond)
						defer ticker.Stop()
						results := make(chan time.Duration, 100)
						for range 100 {
							<-ticker.C
							go func() {
								began := time.Now()
								if err := client.Call(w.ctx, "input", struct{}{}, nil); err != nil {
									t.Error(err)
								}
								results <- time.Since(began)
							}()
						}
						rate := float64(delivered.Load()-before) / time.Since(start).Seconds()
						completedRate = float64(completed.Load()-completedBefore) / time.Since(start).Seconds()
						samples := make([]time.Duration, 100)
						for i := range samples {
							samples[i] = <-results
						}
						slices.Sort(samples)
						return samples[len(samples)/2], rate
					}
					idle, _ := measure()
					var newIdle time.Duration
					if path.changed != 0 {
						w.network.SetDelay(path.changed / 2)
						newIdle, _ = measure()
						w.network.SetDelay(path.rtt / 2)
					}
					loadCtx, stop := context.WithCancel(w.ctx)
					defer stop()
					if workload != "bulk" {
						request, _ := json.Marshal(bridge.AttachRequest{Terminal: "flood"})
						stream, requests, err := client.Open(w.ctx, bridge.AttachChannel, request)
						if err != nil {
							t.Fatal(err)
						}
						defer stream.Close()
						go ssh.DiscardRequests(requests)
						reader := bufio.NewReader(stream)
						if _, err := reader.ReadBytes('\n'); err != nil {
							t.Fatal(err)
						}
						go func() {
							data := make([]byte, 32<<10)
							for {
								var header [9]byte
								if _, err := io.ReadFull(reader, header[:]); err != nil {
									return
								}
								frameBytes := int(binary.BigEndian.Uint32(header[:4])) + 4
								if header[4] != 1 || frameBytes < len(header) {
									t.Error("expected a terminal output frame")
									return
								}
								for remaining := frameBytes - len(header); remaining > 0; {
									n, err := reader.Read(data[:min(len(data), remaining)])
									delivered.Add(int64(n))
									remaining -= n
									if err != nil {
										return
									}
								}
								completed.Add(int64(frameBytes - len(header)))
								if err := json.NewEncoder(stream).Encode(frameBytes); err != nil {
									return
								}
							}
						}()
						go func() {
							size := 32 << 10
							if workload == "maximum-terminal" {
								size = pty.OutputEventMaxBytes
							}
							data := bytes.Repeat([]byte("x"), size)
							for seq := uint32(1); source.write(ptybackend.OutputEvent{Kind: ptybackend.OutputEventKindOutput, Seq: seq, Data: data}); seq++ {
							}
						}()
					} else {
						channel, requests, err := client.Open(w.ctx, "bulk", nil)
						if err != nil {
							t.Fatal(err)
						}
						sender, receiver := client.Bulk(loadCtx, channel, requests), <-accepted
						defer sender.Close()
						defer receiver.Close()
						go func() {
							data := make([]byte, 32<<10)
							for {
								n, err := receiver.Read(data)
								delivered.Add(int64(n))
								completed.Add(int64(n))
								if err != nil {
									return
								}
							}
						}()
						go func() { _, _ = io.Copy(sender, endlessOutput{}) }()
					}
					check := func(label string, base, idle time.Duration) {
						p50, rate := measure()
						allowance := max(10*time.Millisecond, base*3/10) + time.Duration(float64(32<<10)/path.rate*float64(time.Second))
						t.Logf("%s rate=%.0fB/s base=%s idle_p50=%s loaded_p50=%s excess=%s bound=%s transfer=%.0fB/s completed=%.0fB/s utilization=%.4f", label, path.rate, base, idle, p50, p50-idle, allowance, rate, completedRate, rate/path.rate)
						if p50-idle > allowance {
							t.Errorf("call excess %s exceeds %s", p50-idle, allowance)
						}
						if rate < path.rate*0.9 {
							t.Errorf("transfer %.0fB/s is below 90%% of %.0fB/s", rate, path.rate)
						}
					}
					check("initial", path.rtt, idle)
					if path.changed != 0 {
						w.network.SetDelay(path.changed / 2)
						<-time.After(10 * time.Second)
						check("changed", path.changed, newIdle)
					}
				})
			})
		}
	}
}

type endlessOutput struct{}

func (endlessOutput) Read(data []byte) (int, error) { clear(data); return len(data), nil }

func TestReturningHeldOutputDoesNotPauseTyping(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := pair(t)
		w.network = memnet.Listen(memnet.Rate(10_000_000), memnet.Delay(27*time.Millisecond/2))
		held, flood, echo := newTerminalOutput(), newTerminalOutput(), newTerminalOutput()
		w.cfg.Handlers = bridge.Handlers{
			bridge.AttachChannel: func(c *bridge.Conn, incoming ssh.NewChannel) {
				go c.ServeAttach(w.ctx, incoming, func(r bridge.AttachRequest) (ptybackend.AttachInfo, ptybackend.Stream, error) {
					source := echo
					if r.Terminal == "held" {
						source = held
					}
					if r.Terminal == "flood" {
						source = flood
					}
					return ptybackend.AttachInfo{}, source, nil
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
		defer client.Close()
		defer server.Close()
		streams := make(map[string]ptybackend.Stream)
		for _, name := range []string{"held", "flood", "echo"} {
			_, stream, err := client.Attach(w.ctx, bridge.AttachRequest{Terminal: harness.TerminalID(name)})
			if err != nil {
				t.Fatal(err)
			}
			defer stream.Close()
			streams[name] = stream
		}
		go held.write(ptybackend.OutputEvent{Kind: ptybackend.OutputEventKindOutput, Data: make([]byte, 32<<10)})
		<-streams["held"].Events()
		probe := func() time.Duration {
			ctx, cancel := context.WithTimeout(w.ctx, bridge.StallAfter)
			defer cancel()
			began := time.Now()
			if err := client.Call(ctx, "input", struct{}{}, nil); err != nil {
				t.Fatal(err)
			}
			select {
			case event := <-streams["echo"].Events():
				if string(event.Data) != "echo" {
					t.Fatalf("echo=%q", event.Data)
				}
				ack(streams["echo"])
				return time.Since(began)
			case <-ctx.Done():
				t.Fatal("typing stopped while another subscriber returned old output")
				return 0
			}
		}
		probe()
		synctest.Wait()
		go func() {
			data := make([]byte, 32<<10)
			for flood.write(ptybackend.OutputEvent{Kind: ptybackend.OutputEventKindOutput, Data: data}) {
			}
		}()
		go func() {
			for range streams["flood"].Events() {
				ack(streams["flood"])
			}
		}()
		ticker := time.NewTicker(30 * time.Millisecond)
		defer ticker.Stop()
		for range 100 {
			<-ticker.C
			probe()
		}
		before := probe()
		ack(streams["held"])
		synctest.Wait()
		after := probe()
		bound := 10*time.Millisecond + time.Duration(float64(32<<10)/10_000_000*float64(time.Second))
		t.Logf("held return: before=%s after=%s excess=%s bound=%s", before, after, after-before, bound)
		if after-before > bound {
			t.Fatalf("held subscriber return added %s to typing, bound %s", after-before, bound)
		}
	})
}
