package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/setting"
)

type sigmaHarness struct{ d *Daemon }

func init() {
	harnessSources = append(harnessSources, func(d *Daemon) []harness.Harness { return []harness.Harness{sigmaHarness{d: d}} })
}

func (sigmaHarness) driver() agentdriver.Driver { return agentdriver.MustGet("sigma") }

func (h sigmaHarness) Facts() harness.Facts {
	facts := builtinFacts(h.driver())
	facts.SkillDirs = h.driver().(*agentdriver.Sigma).SkillDirs
	facts.CacheTTL = time.Hour
	facts.Signals.Heartbeat = 500 * time.Millisecond
	return facts
}

func (h sigmaHarness) OwnsCredential(subject string) bool {
	return h.d.appServer().OwnsCredential(subject)
}

func (h sigmaHarness) Authenticate(subject string, header http.Header) (harness.SessionID, string, bool, error) {
	return h.d.appServer().Authenticate(subject, header)
}

func (h sigmaHarness) IdentifyCall(subject string, meta json.RawMessage) (harness.SessionID, string, *harness.Refusal) {
	return h.d.appServer().IdentifyCall(subject, meta)
}

func (h sigmaHarness) ResolveCaller(caller string) (harness.SessionID, harness.TerminalID, string, bool) {
	return h.d.appServer().ResolveCaller(caller)
}

func (h sigmaHarness) Terminal(t harness.TerminalID, e harness.TerminalEvent) {
	h.d.appServer().terminal(t, e)
}

func (h sigmaHarness) End(_ context.Context, e harness.Ending) {
	if l := h.d.serverLinkFor(h.Facts().ID); l != nil {
		l.sessionClosed(e.Session, e.Profile, e.Released)
	}
}

func (h sigmaHarness) Health(context.Context) harness.Health {
	return h.d.executableHealth(h.Facts(), h.d.on(setting.SigmaEnabled))
}
