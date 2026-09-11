package daemon

import (
	"testing"
	"time"
)

// Receipt (2026-09-11, -race, 200 drains): p99 2.7ms, max 4.8ms; 10s is the tripwire.
const agentMailboxDrainReceiptDeadline = 10 * time.Second

// One receipt per drain. Cleanup stops the doorbell timers and releases any
// drain still waiting on the test, so no re-armed drain outlives it.
type agentMailboxDrainReceipts struct {
	t      *testing.T
	drains chan int
	closed chan struct{}
}

func observeAgentMailboxDrains(t *testing.T, d *Daemon) *agentMailboxDrainReceipts {
	t.Helper()
	return observeAgentMailboxDrainsFor(t, d, "")
}

// An empty sessionID observes every session's drains.
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

// next returns the delivered count of the next drain, or fails the test by
// name so the package timeout is never the first tripwire.
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
