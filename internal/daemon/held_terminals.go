package daemon

import (
	"slices"

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

// terminalHolder is what holds a terminal in place of a pane: an open ask, or a failed check of a home move to the
// terminal's machine.
type terminalHolder struct {
	ask       string
	moveCheck string
}

func (h terminalHolder) holds() bool {
	return h.ask != "" || h.moveCheck != ""
}

func (h terminalHolder) String() string {
	if h.ask != "" {
		return "in ask " + h.ask
	}
	return "held by the " + h.moveCheck + " check of a home move"
}

func (d *Daemon) heldInPlaceOfPanes(sessionID protocol.SessionID) bool {
	terminals := d.terminalsOf(sessionID)
	for _, t := range terminals {
		if !d.terminals().holder(t).holds() {
			return false
		}
	}
	return len(terminals) > 0
}

// settleMoveCheckTerminals ends the shells that a check of a move to machine holds once a check run of that machine
// finds the check passing.
func (d *Daemon) settleMoveCheckTerminals(machine string, checks []protocol.HomeMoveCheck) {
	held, err := d.store.TerminalsOfMoveChecks(machine)
	if err != nil {
		d.logf("home move: listing the shells the checks of %s hold: %v", machine, err)
		return
	}
	for _, binding := range held {
		if !slices.ContainsFunc(checks, func(c protocol.HomeMoveCheck) bool { return c.Ok && c.Name == binding.MoveCheck }) {
			continue
		}
		if !d.closeTerminal(binding.SessionID, binding.TerminalID) {
			continue
		}
		closing, err := d.beginSessionClose(binding.SessionID, store.SessionClose{By: who.Attn(), Reason: "the " + binding.MoveCheck + " check of a home move to " + machine + " that held its terminal passed"}, nil)
		if err != nil {
			d.logf("home move: closing shell %s, whose %s check passed: %v", binding.SessionID, binding.MoveCheck, err)
			continue
		}
		d.finishSessionClose(binding.SessionID, closing)
	}
}
