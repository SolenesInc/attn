package daemon

import (
	"github.com/victorarias/attn/internal/bus"
	"github.com/victorarias/attn/internal/crashpoint"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/who"
)

func (d *Daemon) subscribeAskTerminalSettling() {
	d.askTerminalsUnsubscribe = d.eventBus.Subscribe(bus.Filter{FactAskClosed}, func(bus.Event) { d.kickAskTerminalSettling() })
}

func (d *Daemon) unsubscribeAskTerminalSettling() {
	if d.askTerminalsUnsubscribe != nil {
		d.askTerminalsUnsubscribe()
		d.askTerminalsUnsubscribe = nil
	}
}

func (d *Daemon) kickAskTerminalSettling() {
	if !d.askTerminalsUnsettled.CompareAndSwap(false, true) {
		return
	}
	d.life.Go("settleAskTerminals", func() {
		d.askTerminalsUnsettled.Store(false)
		crashpoint.At(pausepoint.AskTerminalsSettling)
		d.settleAskTerminals()
	})
}

func (d *Daemon) settleAskTerminals() {
	held, err := d.store.TerminalsOfClosedAsks()
	defer func() { pausepoint.Done(pausepoint.AskTerminalsSettled, err) }()
	if err != nil {
		d.logf("asks: listing the terminals of asks no longer open: %v", err)
		return
	}
	for _, binding := range held {
		if !d.closeTerminal(binding.SessionID, binding.TerminalID) {
			continue
		}
		closing, err := d.beginSessionClose(binding.SessionID, store.SessionClose{By: who.Attn(), Reason: "ask " + binding.AskID + " that held its terminal is no longer open"}, nil)
		if err != nil {
			d.logf("asks: closing shell %s, whose ask %s is no longer open: %v", binding.SessionID, binding.AskID, err)
			continue
		}
		d.finishSessionClose(binding.SessionID, closing)
	}
}

func (d *Daemon) heldByAsks(sessionID protocol.SessionID) bool {
	terminals := d.terminalsOf(sessionID)
	for _, t := range terminals {
		if d.terminals().askHolding(t) == "" {
			return false
		}
	}
	return len(terminals) > 0
}
