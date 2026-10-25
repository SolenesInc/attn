package bridge_test

import (
	"bytes"
	"encoding/json"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/victorarias/attn/internal/bridge"
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
		raw, _ := json.Marshal(event)
		t.Logf("largest PTY event: terminal_bytes=%d bridge_bytes=%d credit=%d", len(event.Data), len(raw), bridge.TerminalOutputCredit)
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
		if next.Kind != ptybackend.OutputEventKindDesync || next.Reason != "bridge_backpressure" {
			t.Fatalf("after draining: %+v", next)
		}
		ack(stream)
		if _, open := <-stream.Events(); open {
			t.Fatal("attach continued after its desync")
		}
	})
}
