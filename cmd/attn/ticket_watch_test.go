package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

func TestWatchTicketInboxDedupesErrorsAndPrintsBundles(t *testing.T) {
	type step struct {
		result *protocol.TicketInboxResult
		err    error
	}
	down := errors.New("daemon down")
	steps := []step{
		{nil, nil},
		{&protocol.TicketInboxResult{Bundles: []protocol.TicketEventBundle{{TicketID: "tkt-1"}}}, nil},
		{nil, down},
		{nil, down},
		{nil, nil},
		{nil, down},
	}

	// Buffered so the fetch closure never blocks: the loop pulls exactly one per poll.
	fetchCh := make(chan step, len(steps))
	for _, s := range steps {
		fetchCh <- s
	}
	fetch := func() (*protocol.TicketInboxResult, error) {
		s := <-fetchCh
		return s.result, s.err
	}

	ctx, cancel := context.WithCancel(context.Background())
	tick := make(chan time.Time) // unbuffered: a send blocks until the loop is back at its select
	var out, errOut bytes.Buffer
	done := make(chan struct{})
	go func() {
		watchTicketInbox(ctx, tick, fetch, &out, &errOut, false)
		close(done)
	}()

	for k := 1; k < len(steps); k++ {
		tick <- time.Time{}
	}
	cancel()
	<-done

	if got := strings.Count(out.String(), "tkt-1"); got != 1 {
		t.Fatalf("bundle printed %d times, want exactly 1\nout:\n%s", got, out.String())
	}
	if got := strings.Count(errOut.String(), "ticket inbox --watch: daemon down"); got != 2 {
		t.Fatalf("outage reported %d times, want exactly 2 (poll 3 deduped)\nerr:\n%s", got, errOut.String())
	}
}
