package daemon_test

import (
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func catalogEntry(catalog protocol.HarnessCatalogMessage, id string) (protocol.Harness, bool) {
	for _, h := range catalog.Harnesses {
		if h.ID == id {
			return h, true
		}
	}
	return protocol.Harness{}, false
}

func requestLogo(app *testworld.Peer, hash string) protocol.HarnessLogoResultMessage {
	return testworld.Request(app, protocol.HarnessLogoMessage{Cmd: protocol.CmdHarnessLogo, RequestID: "logo-" + hash, Hash: hash},
		protocol.EventHarnessLogoResult, func(m protocol.HarnessLogoResultMessage) bool { return m.RequestID == "logo-"+hash })
}

func TestTheAppGetsEveryHarnessAtConnectIncludingOneThatIsInstalledButNotOffered(t *testing.T) {
	w := newWorld(t, fakeagent.Sigma)
	app := w.App()

	catalog := testworld.Await(app, protocol.EventHarnessCatalog, func(m protocol.HarnessCatalogMessage) bool {
		h, ok := catalogEntry(m, "sigma")
		return ok && h.Installation != protocol.HarnessInstallationChecking
	})
	sigma, _ := catalogEntry(catalog, "sigma")
	if sigma.Offered || sigma.Installation != protocol.HarnessInstallationInstalled ||
		!sigma.Options.AutoApproveUnreviewed || !sigma.Options.AutomationExecutable || sigma.Options.KeepsConversations ||
		protocol.Deref(sigma.ActivityEffort) != "" {
		t.Fatalf("Sigma at connect = %+v; want installed, unoffered, unreviewed auto-approve, automation executable, no default efforts", sigma)
	}
	claude, _ := catalogEntry(catalog, "claude")
	if !claude.Offered || !claude.Options.ExecutableSetting || !claude.Options.Resume || !claude.Options.InitialPrompt ||
		!claude.Options.Discovery || !claude.Options.AutomationExecutable || !claude.Options.KeepsConversations || claude.Options.AutoApproveUnreviewed ||
		protocol.Deref(claude.ActivityEffort) != "" {
		t.Fatalf("Claude Code at connect = %+v, want offered, executable setting, resume, initial prompts, discovery, automation executable, keeps conversations", claude)
	}
	codex, _ := catalogEntry(catalog, "codex")
	if !codex.Offered || !codex.Options.Discovery || codex.Options.AutomationExecutable || codex.Options.KeepsConversations || codex.Options.AutoApproveUnreviewed ||
		protocol.Deref(codex.ActivityEffort) != "low" {
		t.Fatalf("Codex at connect = %+v, want offered, discovery, no automation executable, activity effort low", codex)
	}

	mark := len(app.Log())
	setSetting(t, app, "sigma_enabled", "true")
	changed := testworld.AwaitSince(app, mark, protocol.EventHarnessCatalog, func(m protocol.HarnessCatalogMessage) bool {
		h, _ := catalogEntry(m, "sigma")
		return h.Offered
	})
	if _, listed := catalogEntry(changed, "claude"); !listed {
		t.Fatalf("the catalog sent on the change lists %+v, want every harness again", changed.Harnesses)
	}
	var ids []string
	for _, h := range changed.Harnesses {
		ids = append(ids, h.ID)
	}
	if strings.Join(ids, ",") != "claude,codex,sigma" {
		t.Fatalf("catalog order with Sigma enabled = %v, want [claude codex sigma]", ids)
	}
}

func TestTheAppDrawsABuiltInHarnessLogoItAsksForByHash(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	catalog := testworld.Await(app, protocol.EventHarnessCatalog, func(protocol.HarnessCatalogMessage) bool { return true })
	claude, _ := catalogEntry(catalog, "claude")
	codex, _ := catalogEntry(catalog, "codex")
	if protocol.Deref(claude.Logo) == "" || protocol.Deref(codex.Logo) == "" || *claude.Logo == *codex.Logo {
		t.Fatalf("the catalog's logos are Claude Code %v and Codex %v, want two different hashes", protocol.Deref(claude.Logo), protocol.Deref(codex.Logo))
	}
	logo := requestLogo(app, *claude.Logo)
	if !logo.Success || logo.Logo == nil || logo.Logo.View != 1200 || !strings.Contains(logo.Logo.Body, "currentColor") {
		t.Fatalf("harness_logo for Claude Code's hash = %+v, want its 1200-unit mark drawn in currentColor", logo)
	}
	if unknown := requestLogo(app, "0123"); unknown.Success || !strings.Contains(protocol.Deref(unknown.Error), "0123") {
		t.Fatalf("harness_logo for an unknown hash = %+v, want a refusal naming the hash", unknown)
	}
}

func TestAPluginHarnessReachesTheCatalogWithTheLogoItRegistered(t *testing.T) {
	t.Setenv("ATTN_FAKE_PLUG_DISPLAY_NAME", "Example Agent")
	t.Setenv("ATTN_FAKE_PLUG_LOGO", `{"view": 16, "body": "<circle cx=\"8\" cy=\"8\" r=\"6\" fill=\"currentColor\"/>"}`)
	w := newWorld(t, fakeagent.Plug)
	app := w.App()
	catalog := testworld.Await(app, protocol.EventHarnessCatalog, func(m protocol.HarnessCatalogMessage) bool {
		_, listed := catalogEntry(m, "plug")
		return listed
	})
	plug, _ := catalogEntry(catalog, "plug")
	if plug.Name != "Example Agent" || plug.Options.ExecutableSetting || protocol.Deref(plug.Logo) == "" {
		t.Fatalf("the plugin's harness in the catalog = %+v, want Example Agent with its logo and no executable setting", plug)
	}
	logo := requestLogo(app, *plug.Logo)
	if !logo.Success || logo.Logo.View != 16 || !strings.HasPrefix(logo.Logo.Body, "<circle") {
		t.Fatalf("harness_logo for the plugin's hash = %+v, want the circle it registered", logo)
	}
}

func TestAPluginLogoIsRefusedOnlyWhenItWouldBreakAttn(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	register := func(agent string, view any, body string) error {
		driver := dialDriver(t, w, "attn-"+agent)
		return driver.call("driver.register", map[string]any{"agent": agent, "capabilities": map[string]bool{"initial_prompt": true}, "logo": map[string]any{"view": view, "body": body}}, &driver.registered)
	}
	for _, refused := range []struct {
		agent string
		view  any
		body  string
		names string
	}{
		{"flat", 0, "<path/>", "view"},
		{"inside-out", -24, "<path/>", "view"},
		{"vast", 24, strings.Repeat("x", 64<<10+1), "64"},
	} {
		if err := register(refused.agent, refused.view, refused.body); err == nil || !strings.Contains(err.Error(), refused.names) {
			t.Errorf("registering %s's logo = %v, want it refused naming the %s limit", refused.agent, err, refused.names)
		}
	}
	if err := register("loud", 24, strings.Repeat("x", 64<<10)); err != nil {
		t.Fatalf("a 64 KiB logo in any colors was refused: %v", err)
	}
}

func TestGetSettingsRefreshesTheHarnessCatalog(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	testworld.Await(app, protocol.EventHarnessCatalog, func(protocol.HarnessCatalogMessage) bool { return true })

	mark := len(app.Log())
	app.Send(protocol.GetSettingsMessage{Cmd: protocol.CmdGetSettings})
	refreshed := testworld.AwaitSince(app, mark, protocol.EventHarnessCatalog, func(protocol.HarnessCatalogMessage) bool { return true })
	claude, listed := catalogEntry(refreshed, "claude")
	if !listed || claude.Installation != protocol.HarnessInstallationInstalled {
		t.Fatalf("get_settings catalog = %+v, want claude installed", refreshed.Harnesses)
	}
}
