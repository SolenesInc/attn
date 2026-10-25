package bridge

import (
	"context"
)

const TerminalOutputCredit = 1024 << 10
const BridgeOutputCredit = 1408 << 10

type outputFlow struct {
	served               uint64
	outstanding, pending int
}
type outputReservation struct {
	ctx    context.Context
	flow   *outputFlow
	bytes  int
	finish uint64
	ready  chan struct{}
}
type outputReturn struct {
	flow  *outputFlow
	bytes int
}
type outputCredit struct {
	done    <-chan struct{}
	changes chan any
}

func newOutputCredit(done <-chan struct{}) *outputCredit {
	c := &outputCredit{done: done, changes: make(chan any)}
	go c.run()
	return c
}

func (c *outputCredit) reserve(ctx context.Context, flow *outputFlow, n int) error {
	request := &outputReservation{ctx: ctx, flow: flow, bytes: n, ready: make(chan struct{})}
	select {
	case c.changes <- request:
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		return context.Canceled
	}
	select {
	case <-request.ready:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		return context.Canceled
	}
}
func (c *outputCredit) returned(flow *outputFlow, n int) {
	select {
	case c.changes <- outputReturn{flow, n}:
	case <-c.done:
	}
}
func (c *outputCredit) closeFlow(flow *outputFlow) {
	select {
	case c.changes <- flow:
	case <-c.done:
	}
}

func (c *outputCredit) run() {
	available := BridgeOutputCredit
	accounts := map[*outputFlow]bool{}
	var waiting []*outputReservation
	apply := func(change any) {
		switch change := change.(type) {
		case *outputReservation:
			var base uint64
			found := false
			for account := range accounts {
				if account.outstanding > 0 || account.pending > 0 {
					if !found || account.served < base {
						base = account.served
						found = true
					}
				}
			}
			account := change.flow
			accounts[account] = true
			change.finish = max(account.served, base) + uint64(change.bytes)
			account.pending++
			waiting = append(waiting, change)
		case outputReturn:
			if account := change.flow; accounts[account] && change.bytes > 0 && change.bytes <= account.outstanding {
				account.outstanding -= change.bytes
				available += change.bytes
			}
		case *outputFlow:
			if account := change; accounts[account] {
				available += account.outstanding
				delete(accounts, change)
			}
		}
	}
	for {
		select {
		case change := <-c.changes:
			apply(change)
		case <-c.done:
			return
		}
		for {
			select {
			case change := <-c.changes:
				apply(change)
			default:
				goto schedule
			}
		}
	schedule:
		for len(waiting) > 0 {
			selected := -1
			for i := 0; i < len(waiting); {
				request := waiting[i]
				account := request.flow
				if request.ctx.Err() != nil || !accounts[account] {
					if accounts[account] {
						account.pending--
					}
					waiting = append(waiting[:i], waiting[i+1:]...)
					continue
				}
				if account.outstanding >= TerminalOutputCredit && request.bytes > 0 {
					i++
					continue
				}
				if (request.bytes > 0 || account.outstanding == 0) && (selected < 0 || request.finish < waiting[selected].finish) {
					selected = i
				}
				i++
			}
			if selected < 0 {
				break
			}
			request := waiting[selected]
			account := request.flow
			if request.bytes > 0 && available <= 0 {
				break
			}
			waiting = append(waiting[:selected], waiting[selected+1:]...)
			account.pending--
			available -= request.bytes
			account.outstanding += request.bytes
			account.served = request.finish
			select {
			case request.ready <- struct{}{}:
			case <-request.ctx.Done():
				available += request.bytes
				account.outstanding -= request.bytes
			case <-c.done:
				return
			}
		}
	}
}
