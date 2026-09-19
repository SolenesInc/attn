package daemon

import (
	"testing"
	"time"
)

const agentMailboxDrainReceiptDeadline = 10 * time.Second

type agentMailboxDrainReceipts struct {
	t      *testing.T
	drains chan int
	closed chan struct{}
}

func observeAgentMailboxDrains(t *testing.T, d *Daemon) *agentMailboxDrainReceipts {
	t.Helper()
	return observeAgentMailboxDrainsFor(t, d, "")
}

func observeAgentMailboxDrainsFor(t *testing.T, d *Daemon, sessionID string) *agentMailboxDrainReceipts {
	t.Helper()
	r := &agentMailboxDrainReceipts{t: t, drains: make(chan int), closed: make(chan struct{})}
	d.agentMailboxDrainHook = func(drained string, delivered int) {
		if sessionID != "" && drained != sessionID {
			return
		}
		select {
		case r.drains <- delivered:
		case <-r.closed:
		}
	}
	t.Cleanup(func() {
		close(r.closed)
		d.stopAgentMailboxDoorbells()
		d.sessionInputs().stopRetries()
	})
	return r
}

func (r *agentMailboxDrainReceipts) next() int {
	r.t.Helper()
	deadline := time.NewTimer(agentMailboxDrainReceiptDeadline)
	defer deadline.Stop()
	select {
	case delivered := <-r.drains:
		return delivered
	case <-deadline.C:
		r.t.Fatalf("%s: no agent mailbox drain receipt within %s", r.t.Name(), agentMailboxDrainReceiptDeadline)
		return 0
	}
}
