package stepclock_test

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/stepclock"
)

type steppedProcess struct {
	t      *testing.T
	hub    *stepclock.Hub
	conn   net.Conn
	reader *bufio.Reader
}

func connectProcess(t *testing.T, hub *stepclock.Hub, socket string) *steppedProcess {
	t.Helper()
	conn, err := net.Dial("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	process := &steppedProcess{t: t, hub: hub, conn: conn, reader: bufio.NewReader(conn)}
	process.send("ready 1")
	process.armAndAwait("booted")
	return process
}

func (p *steppedProcess) send(line string) {
	p.t.Helper()
	if _, err := fmt.Fprintln(p.conn, line); err != nil {
		p.t.Fatalf("send %q: %v", line, err)
	}
}

func (p *steppedProcess) armAndAwait(name stepclock.Name) {
	p.t.Helper()
	p.send(fmt.Sprintf("armed %s %s 1s", name, name))
	ctx, cancel := context.WithTimeout(context.Background(), fakeagent.HangGuard)
	defer cancel()
	if pending, ok := p.hub.AwaitTimer(ctx, name); !ok {
		p.t.Fatalf("the hub never read the process's timer %s; timers pending: %v", name, pending)
	}
}

func (p *steppedProcess) receive(want string) {
	p.t.Helper()
	line, err := p.reader.ReadString('\n')
	if err != nil {
		p.t.Fatalf("read %q: %v", want, err)
	}
	if line != want+"\n" {
		p.t.Fatalf("the process received %q, want %q", line, want)
	}
}

func (p *steppedProcess) advanceCancelledUnanswered() error {
	p.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	returned := make(chan error, 1)
	go func() { returned <- p.hub.Advance(ctx, time.Second) }()
	p.receive("advance 1s")
	cancel()
	return <-returned
}

func TestAnAdvanceItsCallerGaveUpOnNeitherStallsItsProcessNorAnswersTheNextAdvance(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "attn-clock-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	socket := filepath.Join(dir, "clock.sock")
	hub, err := stepclock.Listen(socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		closed := make(chan struct{})
		go func() {
			hub.Close()
			close(closed)
		}()
		select {
		case <-closed:
		case <-time.After(fakeagent.HangGuard):
			t.Errorf("hub.Close did not return within %s", fakeagent.HangGuard)
		}
	})
	process := connectProcess(t, hub, socket)

	for range 2 {
		if err := process.advanceCancelledUnanswered(); err == nil {
			t.Fatal("an Advance cancelled before the process answered reported success")
		}
	}
	process.send("advanced 1s")
	process.send("advanced 1s")
	process.armAndAwait("after-late-answers")

	if err := process.advanceCancelledUnanswered(); err == nil {
		t.Fatal("an Advance returned on the answer to an earlier Advance, before its process answered it")
	}
	process.send("advanced 1s")
	process.armAndAwait("after-third-answer")

	finished := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), fakeagent.HangGuard)
		defer cancel()
		finished <- hub.Advance(ctx, time.Second)
	}()
	process.receive("advance 1s")
	process.send("advanced 1s")
	if err := <-finished; err != nil {
		t.Fatalf("an answered Advance reported %v", err)
	}
}
