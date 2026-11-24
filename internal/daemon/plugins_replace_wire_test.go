package daemon_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func plugSource(t *testing.T, w *world, dir, version, script string) string {
	t.Helper()
	installed := filepath.Join(w.Dir, "plugins", "attn-plug")
	stable := w.Path("plug-bin")
	if _, err := os.Stat(stable); os.IsNotExist(err) {
		binary, err := os.Readlink(filepath.Join(installed, "attn-plug"))
		if err != nil {
			t.Fatal(err)
		}
		config, err := os.ReadFile(filepath.Join(installed, "fakeagent.json"))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(stable, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(binary, filepath.Join(stable, "attn-plug")); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(stable, "fakeagent.json"), config, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if script == "" {
		script = "#!/bin/sh\nexec '" + filepath.Join(stable, "attn-plug") + "' \"$@\"\n"
	}
	return pluginsCheckout(t, dir, "attn-plug", version, script)
}

func plugListed(app *testworld.Peer, want string, match func(protocol.PluginInfo) bool) protocol.PluginInfo {
	app.T.Helper()
	plugins := pluginsListed(app, want, func(plugins []protocol.PluginInfo) bool {
		p, ok := pluginsNamed(plugins, "attn-plug")
		return ok && match(p)
	})
	p, _ := pluginsNamed(plugins, "attn-plug")
	return p
}

func TestReplacingAndReloadingTheDriverOfALiveSessionKeepsTheSessionWorking(t *testing.T) {
	w := newWorld(t, fakeagent.Plug)
	app := w.App()
	awaitHarnessOffered(app, "plug")
	session := w.Spawn(app, fakeagent.Plug, w.Path("shop"))
	run := w.Launched(session)
	converse(t, app, session, run, "price the checkout")

	copied := plugSource(t, w, w.Path("sources", "copied"), "0.2.0", "")
	pluginsAct(t, app, protocol.InstallPluginMessage{Cmd: protocol.CmdInstallPlugin, Source: copied}, "install", "attn-plug")
	installed := plugListed(app, "attn-plug 0.2.0 copied and connected", func(p protocol.PluginInfo) bool {
		return p.Version == "0.2.0" && p.Connected && p.LinkTarget == nil
	})
	if protocol.Deref(installed.Source) != copied || protocol.Deref(installed.SourceKind) != "path" || installed.CanUninstall {
		t.Errorf("the replaced plugin is listed from %q (%q), can uninstall %v; want %s as a path source still driving the session", protocol.Deref(installed.Source), protocol.Deref(installed.SourceKind), installed.CanUninstall, copied)
	}
	converse(t, app, session, run, "after the replacement")

	checkout := plugSource(t, w, w.Path("sources", "checkout"), "0.3.0", "")
	pluginsAct(t, app, protocol.InstallPluginMessage{Cmd: protocol.CmdInstallPlugin, Source: checkout, Link: protocol.Ptr(true)}, "link", "attn-plug")
	plugListed(app, "attn-plug 0.3.0 linked to its checkout", func(p protocol.PluginInfo) bool {
		return p.Version == "0.3.0" && p.Connected && protocol.Deref(p.LinkTarget) == checkout && p.Source == nil
	})
	converse(t, app, session, run, "after linking")

	plugSource(t, w, checkout, "0.4.0", "")
	pluginsAct(t, app, protocol.ReloadPluginMessage{Cmd: protocol.CmdReloadPlugin, Name: "attn-plug", SkipInstall: protocol.Ptr(true)}, "reload", "attn-plug")
	plugListed(app, "attn-plug reloaded at 0.4.0", func(p protocol.PluginInfo) bool { return p.Version == "0.4.0" && p.Connected })
	converse(t, app, session, run, "after the reload")

	pluginsRefused(t, app, protocol.UninstallPluginMessage{Cmd: protocol.CmdUninstallPlugin, Name: "attn-plug"}, "uninstall", "active delegated run")
}

func TestAReplacementThatNeverConnectsRestoresThePreviousCopy(t *testing.T) {
	w := newWorld(t, fakeagent.Plug)
	app := w.App()
	awaitHarnessOffered(app, "plug")
	session := w.Spawn(app, fakeagent.Plug, w.Path("shop"))
	run := w.Launched(session)
	working := plugSource(t, w, w.Path("sources", "working"), "0.2.0", "")
	pluginsAct(t, app, protocol.InstallPluginMessage{Cmd: protocol.CmdInstallPlugin, Source: working}, "install", "attn-plug")

	broken := plugSource(t, w, w.Path("sources", "broken"), "0.3.0", pluginsCrash)
	result := pluginsRequest(app, protocol.InstallPluginMessage{Cmd: protocol.CmdInstallPlugin, Source: broken}, "install")
	if refusal := protocol.Deref(result.Error); result.Success || !strings.Contains(refusal, "exited before it connected") || !strings.Contains(refusal, "restored the previous copy") {
		t.Fatalf("installing a plugin that exits at once answered %+v, want a failure that says it restored the previous copy", result)
	}
	plugListed(app, "attn-plug back at 0.2.0 and connected", func(p protocol.PluginInfo) bool {
		return p.Version == "0.2.0" && p.Connected && protocol.Deref(p.Source) == working
	})
	if leftovers, _ := filepath.Glob(filepath.Join(w.Dir, "plugins", ".attn-plug.*")); len(leftovers) != 0 {
		t.Errorf("the rolled-back replacement left %v behind", leftovers)
	}
	converse(t, app, session, run, "after the rollback")
}

func TestUpdatingAPluginInstalledFromGitFetchesItsNewCommit(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "attn-plug.git")
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "url.file://"+filepath.Dir(repo)+"/.insteadOf")
	t.Setenv("GIT_CONFIG_VALUE_0", "https://plugins.example/")
	w := newWorld(t, fakeagent.Plug)
	app := w.App()
	awaitHarnessOffered(app, "plug")
	plugSource(t, w, repo, "0.2.0", "")
	runGit(t, repo, "init", "-q", "-b", "main")
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-q", "-m", "plugin")
	session := w.Spawn(app, fakeagent.Plug, w.Path("shop"))
	run := w.Launched(session)
	source := "https://plugins.example/" + filepath.Base(repo)

	pluginsAct(t, app, protocol.InstallPluginMessage{Cmd: protocol.CmdInstallPlugin, Source: source}, "install", "attn-plug")
	plugListed(app, "attn-plug 0.2.0 from git", func(p protocol.PluginInfo) bool {
		return p.Version == "0.2.0" && p.Connected && protocol.Deref(p.Source) == source && protocol.Deref(p.SourceKind) == "git"
	})

	plugSource(t, w, repo, "0.3.0", "")
	runGit(t, repo, "commit", "-q", "-am", "release 0.3.0")
	pluginsAct(t, app, protocol.UpdatePluginMessage{Cmd: protocol.CmdUpdatePlugin, Name: "attn-plug"}, "update", "attn-plug")
	plugListed(app, "attn-plug updated to 0.3.0", func(p protocol.PluginInfo) bool { return p.Version == "0.3.0" && p.Connected })
	converse(t, app, session, run, "after the update")
}
