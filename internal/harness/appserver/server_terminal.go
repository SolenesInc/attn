package appserver

import "github.com/victorarias/attn/internal/harness"

func (c *Client) terminal(t harness.TerminalID, e harness.TerminalEvent) {
	switch e := e.(type) {
	case harness.Started:
		if l := c.serverHolding(e.Session); l != nil && !e.Prompted {
			l.launchStarted(e.Session)
		}
	case harness.Exited:
		if c.host.Terminals().Owned(t) {
			if err := c.host.Terminals().Remove(c.host.Life().Context(), t); err != nil {
				c.host.Logf("remove app-server terminal %s: %v", t, err)
			}
			return
		}
		for _, l := range c.Links() {
			l.terminalExited(t)
		}
	case harness.Dropped:
		for _, l := range c.Links() {
			l.terminalDropped(t)
		}
	}
}
