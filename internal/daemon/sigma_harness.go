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
	facts.Recovery = harness.ByServer
	facts.ModelDiscovery = true
	facts.ServerTerminal = h.d.serverTerminalOf(facts.ID)
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

func (h sigmaHarness) Launch(_ context.Context, req harness.LaunchRequest) (harness.Command, error) {
	return h.d.launchOnServerOrTerminal(req, h.Facts().ID)
}

func (h sigmaHarness) Plan(req harness.LaunchRequest) (harness.Planned, error) {
	return h.d.planOnServer(h.Facts().ID, "", req), nil
}

func (sigmaHarness) Start(context.Context) error { return nil }

func (h sigmaHarness) Recover(ctx context.Context, point harness.RecoverPoint) {
	h.d.recoverServer(ctx, h.Facts().ID, point)
}

func (h sigmaHarness) SettingChanged(key string) {
	if key == setting.AgentToolsEnabled.Key {
		h.d.life.Go("reconcileAgentTools", h.d.reconcileAgentTools)
	}
}

func (h sigmaHarness) Released(string) { h.d.releaseServer(h.Facts().ID) }

func (sigmaHarness) Stop() {}

func (h sigmaHarness) Conversations() harness.ConversationStore { return h }

func (h sigmaHarness) Admit(ref harness.ConversationRef) error {
	return h.d.admitToServer(h.Facts().ID, ref)
}

func (h sigmaHarness) Check(_ context.Context, refs []harness.ConversationRef) map[harness.ConversationRef]harness.Availability {
	if l := h.d.serverLinkFor(h.Facts().ID); l.watchesConversations() {
		return l.conversationsResumable(refs)
	}
	return h.d.checkStoredConversations(h.driver(), refs)
}

func (sigmaHarness) Prepare(_ context.Context, ref harness.ConversationRef) (bool, error) {
	return ref.ID != "", nil
}

func (h sigmaHarness) End(_ context.Context, e harness.Ending) {
	if l := h.d.serverLinkFor(h.Facts().ID); l != nil {
		l.sessionClosed(e.Session, e.Profile, e.Released)
	}
}

func (h sigmaHarness) Models(ctx context.Context) (harness.Models, error) {
	if server := h.d.serverLinkFor(h.Facts().ID); server != nil {
		return server.harnessModels(ctx)
	}
	return noModelDiscovery, nil
}

func (h sigmaHarness) Health(context.Context) harness.Health {
	return h.d.executableHealth(h.Facts(), h.d.on(setting.SigmaEnabled))
}
