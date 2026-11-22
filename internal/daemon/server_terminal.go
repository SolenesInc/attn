package daemon

import "github.com/victorarias/attn/internal/harness"

func (c appServer) terminal(t harness.TerminalID, e harness.TerminalEvent) {
	switch e := e.(type) {
	case harness.Started:
		if l := c.d.serverLinkFor(c.d.agentOf(e.Session)); l != nil && !e.Prompted {
			l.launchStarted(e.Session)
		}
	case harness.Exited:
		if _, server := c.d.appServerProcess(c.d.life.Context(), t); server {
			if err := c.d.removePTYSession(t); err != nil {
				c.d.logf("remove app-server terminal %s: %v", t, err)
			}
			return
		}
		for _, l := range c.d.appServerLinks() {
			l.terminalExited(t)
		}
	case harness.Dropped:
		for _, l := range c.d.appServerLinks() {
			l.terminalDropped(t)
		}
	}
}
