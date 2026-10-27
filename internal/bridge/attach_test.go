package bridge_test

import (
	"bytes"
	"context"
	"fmt"
	"github.com/victorarias/attn/internal/harness"
	"strconv"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/victorarias/attn/internal/bridge"
	"github.com/victorarias/attn/internal/bridge/memnet"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/ptybackend"
	"golang.org/x/crypto/ssh"
)

type terminalOutput struct {
	events chan ptybackend.OutputEvent
	done   chan struct{}
	once   sync.Once
}

func newTerminalOutput() *terminalOutput {
	return &terminalOutput{events: make(chan ptybackend.OutputEvent), done: make(chan struct{})}
}
func (s *terminalOutput) Events() <-chan ptybackend.OutputEvent { return s.events }
func (s *terminalOutput) Close() error                          { s.once.Do(func() { close(s.done) }); return nil }
func (s *terminalOutput) write(e ptybackend.OutputEvent) bool {
	select {
	case s.events <- e:
		return true
	case <-s.done:
		return false
	}
}
func ack(s ptybackend.Stream) { s.(interface{ Acknowledge() }).Acknowledge() }

func TestBridgeAttachWaitsForAppCreditAndEndsOnSubscriberOverflow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := pair(t)
		source := newTerminalOutput()
		w.cfg.Handlers = bridge.Handlers{bridge.AttachChannel: func(conn *bridge.Conn, incoming ssh.NewChannel) {
			go conn.ServeAttach(w.ctx, incoming, func(bridge.AttachRequest) (ptybackend.AttachInfo, ptybackend.Stream, error) {
				return ptybackend.AttachInfo{}, source, nil
			})
		}}
		w.start()
		client, _ := bridged(t, w, nil)
		_, stream, err := client.Attach(w.ctx, bridge.AttachRequest{Terminal: "shell"})
		if err != nil {
			t.Fatal(err)
		}
		defer stream.Close()
		event := ptybackend.OutputEvent{Kind: ptybackend.OutputEventKindOutput, Data: bytes.Repeat([]byte("x"), 256<<10), Seq: 1}
		encodedBytes := len(event.Data) + bridge.MaxOutputFrameBytes - pty.OutputEventMaxBytes
		t.Logf("largest PTY event: terminal_bytes=%d bridge_bytes=%d credit=%d", len(event.Data), encodedBytes, bridge.TerminalOutputCredit)
		go func() {
			defer close(source.events)
			source.write(event)
			event.Seq++
			source.write(event)
			source.write(ptybackend.OutputEvent{Kind: ptybackend.OutputEventKindDesync, Reason: "subscriber_backpressure"})
		}()
		first := <-stream.Events()
		if first.Seq != 1 || !bytes.Equal(first.Data, event.Data) {
			t.Fatal("first output was changed or skipped")
		}
		synctest.Wait()
		select {
		case next := <-stream.Events():
			t.Fatalf("sent before app acknowledged: %+v", next)
		default:
		}
		ack(stream)
		second := <-stream.Events()
		if second.Seq != 2 {
			t.Fatalf("second output lost: %+v", second)
		}
		ack(stream)
		next := <-stream.Events()
		if next.Kind != ptybackend.OutputEventKindDesync || next.Reason != "subscriber_backpressure" {
			t.Fatalf("after draining: %+v", next)
		}
		ack(stream)
		if _, open := <-stream.Events(); open {
			t.Fatal("attach continued after its desync")
		}
	})
}
func TestHeldAttachLeavesCreditForAnotherTerminal(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		data := bytes.Repeat([]byte("x"), pty.OutputEventMaxBytes)
		encodedBytes := bridge.MaxOutputFrameBytes
		if bridge.BridgeOutputCredit <= bridge.TerminalOutputCredit+encodedBytes {
			t.Fatalf("bridge isolation needs shared_threshold > attach_threshold + encoded_event: shared_threshold=%d attach_threshold=%d encoded_event=%d", bridge.BridgeOutputCredit, bridge.TerminalOutputCredit, encodedBytes)
		}
		t.Logf("coalescing target plus read overshoot: raw_bytes=%d encoded_bytes=%d attach_threshold=%d shared_threshold=%d", len(data), encodedBytes, bridge.TerminalOutputCredit, bridge.BridgeOutputCredit)
		w := pair(t)
		w.network = memnet.Listen(memnet.Rate(2_800_000))
		held, echo := newTerminalOutput(), newTerminalOutput()
		w.cfg.Handlers = bridge.Handlers{
			bridge.AttachChannel: func(conn *bridge.Conn, incoming ssh.NewChannel) {
				go conn.ServeAttach(w.ctx, incoming, func(req bridge.AttachRequest) (ptybackend.AttachInfo, ptybackend.Stream, error) {
					if req.Terminal == "held" {
						return ptybackend.AttachInfo{}, held, nil
					}
					return ptybackend.AttachInfo{}, echo, nil
				})
			},
			"input": func(conn *bridge.Conn, incoming ssh.NewChannel) {
				go bridge.Answer(incoming, func(_ context.Context, _ []byte) (any, error) {
					echo.write(ptybackend.OutputEvent{Kind: ptybackend.OutputEventKindOutput, Data: []byte("echo")})
					return nil, nil
				})
			},
		}
		w.start()
		client, _ := bridged(t, w, nil)
		_, flood, err := client.Attach(w.ctx, bridge.AttachRequest{Terminal: "held"})
		if err != nil {
			t.Fatal(err)
		}
		defer flood.Close()
		_, typing, err := client.Attach(w.ctx, bridge.AttachRequest{Terminal: "echo"})
		if err != nil {
			t.Fatal(err)
		}
		defer typing.Close()
		go func() {
			for n := uint32(1); held.write(ptybackend.OutputEvent{Kind: ptybackend.OutputEventKindOutput, Seq: n, Data: data}); n++ {
			}
		}()
		<-flood.Events()
		synctest.Wait()
		ctx, cancel := context.WithTimeout(w.ctx, bridge.StallAfter)
		defer cancel()
		began := time.Now()
		if err := client.Call(ctx, "input", struct{}{}, nil); err != nil {
			t.Fatal(err)
		}
		select {
		case event := <-typing.Events():
			ack(typing)
			if string(event.Data) != "echo" {
				t.Fatalf("echo=%q", event.Data)
			}
			t.Logf("held coalesced attach: echo=%s", time.Since(began))
		case <-ctx.Done():
			t.Fatal("held coalesced attach consumed the shared budget; typing got no output before the heartbeat stall guard")
		}
	})
}

func TestConcurrentMaximumFramesCanAllFinishBeforeAcknowledgment(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := pair(t)
		w.network = memnet.Listen(memnet.Rate(2_800_000))
		count := bridge.BridgeOutputCredit/bridge.MaxOutputFrameBytes + 1
		sources := make([]*terminalOutput, count)
		for i := range sources {
			sources[i] = newTerminalOutput()
		}
		w.cfg.Handlers = bridge.Handlers{bridge.AttachChannel: func(c *bridge.Conn, in ssh.NewChannel) {
			go c.ServeAttach(w.ctx, in, func(r bridge.AttachRequest) (ptybackend.AttachInfo, ptybackend.Stream, error) {
				index, err := strconv.Atoi(string(r.Terminal))
				if err != nil {
					return ptybackend.AttachInfo{}, nil, err
				}
				return ptybackend.AttachInfo{}, sources[index], nil
			})
		}}
		w.start()
		client, _ := bridged(t, w, nil)
		streams := make([]ptybackend.Stream, count)
		for i := range streams {
			_, stream, err := client.Attach(w.ctx, bridge.AttachRequest{Terminal: harness.TerminalID(strconv.Itoa(i))})
			if err != nil {
				t.Fatal(err)
			}
			streams[i] = stream
			defer stream.Close()
		}
		start := make(chan struct{})
		data := bytes.Repeat([]byte("x"), pty.OutputEventMaxBytes)
		for i, source := range sources {
			go func() {
				<-start
				source.write(ptybackend.OutputEvent{Kind: ptybackend.OutputEventKindOutput, Seq: uint32(i + 1), Data: data})
			}()
		}
		close(start)
		completed := make(chan error, count)
		for i, stream := range streams {
			go func() {
				event, open := <-stream.Events()
				if !open || event.Seq != uint32(i+1) || !bytes.Equal(event.Data, data) {
					completed <- fmt.Errorf("frame %d did not arrive intact", i)
					return
				}
				ack(stream)
				completed <- nil
			}()
		}
		for range streams {
			select {
			case err := <-completed:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(bridge.StallAfter):
				t.Fatal("shared credit filled with partial frames before any whole-frame ACK")
			}
		}
	})
}
