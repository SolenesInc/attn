package bridge

import (
	"context"
	"io"
	"time"
)

const TerminalOutputCredit = 1024 << 10
const BridgeOutputCredit = 1408 << 10
const outputChunkBytes = 32 << 10

type outputFlow struct {
	window               int
	bulk                 bool
	served               uint64
	outstanding, pending int
	sent                 []outputSend
}
type outputReservation struct {
	ctx    context.Context
	flow   *outputFlow
	bytes  int
	finish uint64
	ready  chan struct{}
	charge bool
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
	return c.submit(ctx, flow, n, true)
}

func (c *outputCredit) write(ctx context.Context, flow *outputFlow, data []byte, writer io.Writer) error {
	for len(data) > 0 {
		chunk := data[:min(len(data), outputChunkBytes)]
		if err := c.submit(ctx, flow, len(chunk), false); err != nil {
			return err
		}
		if _, err := writer.Write(chunk); err != nil {
			return err
		}
		data = data[len(chunk):]
	}
	return nil
}

func (c *outputCredit) submit(ctx context.Context, flow *outputFlow, n int, charge bool) error {
	request := &outputReservation{ctx: ctx, flow: flow, bytes: n, charge: charge, ready: make(chan struct{})}
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
	var path outputPath
	timer := time.NewTimer(0)
	timer.Stop()
	defer timer.Stop()
	var paced <-chan time.Time
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
				path.returned(account, change.bytes, time.Now())
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
		case <-paced:
			paced = nil
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
			for {
				select {
				case change := <-c.changes:
					apply(change)
				default:
					goto choose
				}
			}
		choose:
			selected := -1
			var resume time.Duration
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
				window := account.window
				if window == 0 {
					window = TerminalOutputCredit
				}
				if request.charge && request.bytes > 0 && (account.outstanding >= window || available <= 0) {
					i++
					continue
				}
				if !request.charge && path.rate > 0 {
					if delay := time.Until(path.next); delay > 0 {
						resume = delay
						i++
						continue
					}
				}
				if (!request.charge || request.bytes > 0 || account.outstanding == 0) && (selected < 0 || (waiting[selected].flow.bulk && !account.bulk) || (waiting[selected].flow.bulk == account.bulk && request.finish < waiting[selected].finish)) {
					selected = i
				}
				i++
			}
			if selected < 0 {
				if resume > 0 {
					timer.Reset(resume)
					paced = timer.C
				}
				break
			}
			request := waiting[selected]
			account := request.flow
			waiting = append(waiting[:selected], waiting[selected+1:]...)
			account.pending--
			charge := 0
			if request.charge {
				charge = request.bytes
			}
			available -= charge
			account.outstanding += charge
			account.served = request.finish
			if !request.charge {
				path.sent(account, request.bytes, time.Now())
			}
			select {
			case request.ready <- struct{}{}:
			case <-request.ctx.Done():
				available += charge
				account.outstanding -= charge
			case <-c.done:
				return
			}
		}
	}
}
