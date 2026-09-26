package daemon_test

import (
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

const (
	pluginsIdle  = "#!/bin/sh\nexec sleep 3600\n"
	pluginsCrash = "#!/bin/sh\nexit 17\n"
)

func TestALinkedPluginIsListedAndUninstallingKeepsTheCheckout(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	checkout := pluginsCheckout(t, w.Path("checkouts", "worktree-provider"), "worktree-provider", "0.1.0", pluginsIdle)

	pluginsAct(t, app, protocol.InstallPluginMessage{Cmd: protocol.CmdInstallPlugin, Source: checkout, Link: protocol.Ptr(true)}, "link", "worktree-provider")
	pluginsListed(app, "worktree-provider linked to its checkout", func(plugins []protocol.PluginInfo) bool {
		p, ok := pluginsNamed(plugins, "worktree-provider")
		return ok && protocol.Deref(p.LinkTarget) == checkout && p.Availability == "user" && p.InstallationState == "installed" && p.Running
	})

	pluginsAct(t, app, protocol.UninstallPluginMessage{Cmd: protocol.CmdUninstallPlugin, Name: "worktree-provider"}, "uninstall", "worktree-provider")
	pluginsListed(app, "no worktree-provider", func(plugins []protocol.PluginInfo) bool {
		_, ok := pluginsNamed(plugins, "worktree-provider")
		return !ok
	})
	if _, err := os.Lstat(filepath.Join(w.Dir, "plugins", "worktree-provider")); !os.IsNotExist(err) {
		t.Errorf("the link survived the uninstall: %v", err)
	}
	if _, err := os.Stat(filepath.Join(checkout, "attn-plugin.toml")); err != nil {
		t.Errorf("uninstalling touched the checkout: %v", err)
	}
}

func TestAPluginsPriorityOrdersTheListAndSurvivesARestart(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	for _, name := range []string{"alpha-provider", "beta-provider"} {
		checkout := pluginsCheckout(t, w.Path("checkouts", name), name, "0.1.0", pluginsIdle)
		pluginsAct(t, app, protocol.InstallPluginMessage{Cmd: protocol.CmdInstallPlugin, Source: checkout, Link: protocol.Ptr(true)}, "link", name)
	}
	betaFirst := func(plugins []protocol.PluginInfo) bool {
		return len(plugins) == 2 && plugins[0].Name == "beta-provider" && plugins[0].Priority == 50 && plugins[1].Name == "alpha-provider"
	}

	pluginsAct(t, app, protocol.SetPluginPriorityMessage{Cmd: protocol.CmdSetPluginPriority, Name: "beta-provider", Priority: 50}, "set_priority", "beta-provider")
	pluginsListed(app, "beta-provider first at priority 50", betaFirst)

	w.restart()
	pluginsListed(w.App(), "beta-provider still first at priority 50 after a restart", betaFirst)
}

func TestAPluginThatExitsIsShownBackingOffWithItsExit(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	checkout := pluginsCheckout(t, w.Path("checkouts", "flaky-provider"), "flaky-provider", "0.1.0", pluginsCrash)

	pluginsAct(t, app, protocol.InstallPluginMessage{Cmd: protocol.CmdInstallPlugin, Source: checkout, Link: protocol.Ptr(true)}, "link", "flaky-provider")

	backingOff := testworld.Await(app, protocol.EventPluginsUpdated, func(m protocol.PluginsUpdatedMessage) bool {
		p, ok := pluginsNamed(m.Plugins, "flaky-provider")
		return ok && protocol.Deref(p.RuntimePhase) == "backoff"
	})
	p, _ := pluginsNamed(backingOff.Plugins, "flaky-provider")
	if protocol.Deref(p.RestartAttempt) != 1 || protocol.Deref(p.NextRestartAt) == "" || !strings.Contains(protocol.Deref(p.LastExit), "exit status 17") || p.RuntimeState != "degraded" {
		t.Errorf("the exited plugin is listed %s on attempt %d, next restart %q, last exit %q; want it degraded on its first backoff with a next restart time and exit status 17", p.RuntimeState, protocol.Deref(p.RestartAttempt), protocol.Deref(p.NextRestartAt), protocol.Deref(p.LastExit))
	}
}

func TestInstallingAndRemovingAPluginFromGit(t *testing.T) {
	alive := filepath.Join(t.TempDir(), "alive")
	if err := syscall.Mkfifo(alive, 0o600); err != nil {
		t.Fatal(err)
	}
	repo := pluginsGitRepo(t, "#!/bin/sh\nexec sleep 3600 3>'"+alive+"'\n")
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "url.file://"+filepath.Dir(repo)+"/.insteadOf")
	t.Setenv("GIT_CONFIG_VALUE_0", "https://plugins.example/")
	w := newWorld(t)
	app := w.App()

	pluginsAct(t, app, protocol.InstallPluginMessage{Cmd: protocol.CmdInstallPlugin, Source: "https://plugins.example/" + filepath.Base(repo)}, "install", "attn-snipe")
	pluginsListed(app, "attn-snipe installed and running", func(plugins []protocol.PluginInfo) bool {
		p, ok := pluginsNamed(plugins, "attn-snipe")
		return ok && p.InstallationState == "installed" && p.Running && p.LinkTarget == nil
	})
	installed := filepath.Join(w.Dir, "plugins", "attn-snipe")
	if _, err := os.Stat(filepath.Join(installed, "attn-plugin.toml")); err != nil {
		t.Fatalf("the installed plugin has no manifest: %v", err)
	}

	running := pluginsHolding(t, alive)

	pluginsAct(t, app, protocol.RemovePluginMessage{Cmd: protocol.CmdRemovePlugin, Name: "attn-snipe"}, "remove", "attn-snipe")
	pluginsAwaitExit(t, running)
	pluginsListed(app, "no attn-snipe", func(plugins []protocol.PluginInfo) bool {
		_, ok := pluginsNamed(plugins, "attn-snipe")
		return !ok
	})
	if _, err := os.Stat(installed); !os.IsNotExist(err) {
		t.Errorf("the removed plugin's files are still there: %v", err)
	}
}

func TestBundledPluginsInstallPerInstance(t *testing.T) {
	bundled := t.TempDir()
	pluginsCheckout(t, filepath.Join(bundled, "attn-example"), "attn-example", "0.1.0", pluginsIdle)
	t.Setenv("ATTN_BUNDLED_PLUGIN_DIR", bundled)
	w := newWorld(t)
	app := w.App()
	available := func(plugins []protocol.PluginInfo) bool {
		p, ok := pluginsNamed(plugins, "attn-example")
		return ok && p.Availability == "bundled" && p.InstallationState == "available" && p.RuntimeState == "stopped" && !p.Running && p.CanInstall && !p.CanUninstall
	}
	pluginsListed(app, "attn-example available and inert", available)

	override := pluginsCheckout(t, w.Path("checkouts", "attn-example"), "attn-example", "9.0.0", pluginsIdle)
	pluginsAct(t, app, protocol.InstallPluginMessage{Cmd: protocol.CmdInstallPlugin, Source: override, Link: protocol.Ptr(true)}, "link", "attn-example")
	pluginsRefused(t, app, protocol.InstallBundledPluginMessage{Cmd: protocol.CmdInstallBundledPlugin, Name: "attn-example"}, "install_bundled", "user plugin")
	pluginsListed(app, "only the user's attn-example", func(plugins []protocol.PluginInfo) bool {
		p, ok := pluginsNamed(plugins, "attn-example")
		return ok && len(plugins) == 1 && p.Availability == "user"
	})
	pluginsAct(t, app, protocol.UninstallPluginMessage{Cmd: protocol.CmdUninstallPlugin, Name: "attn-example"}, "uninstall", "attn-example")

	pluginsAct(t, app, protocol.InstallBundledPluginMessage{Cmd: protocol.CmdInstallBundledPlugin, Name: "attn-example"}, "install_bundled", "attn-example")
	pluginsListed(app, "attn-example installed and starting", func(plugins []protocol.PluginInfo) bool {
		p, ok := pluginsNamed(plugins, "attn-example")
		return ok && p.InstallationState == "installed" && p.RuntimeState == "starting" && p.Running && !p.CanInstall && p.CanUninstall
	})
	shadow := pluginsCheckout(t, w.Path("checkouts", "shadow", "attn-example"), "attn-example", "9.0.0", pluginsIdle)
	pluginsRefused(t, app, protocol.InstallPluginMessage{Cmd: protocol.CmdInstallPlugin, Source: shadow, Link: protocol.Ptr(true)}, "link", "uninstall bundled plugin")
	if _, err := os.Lstat(filepath.Join(w.Dir, "plugins", "attn-example")); !os.IsNotExist(err) {
		t.Errorf("the refused link left an entry in the plugins directory: %v", err)
	}
	if _, err := os.Stat(filepath.Join(shadow, "node_modules")); !os.IsNotExist(err) {
		t.Errorf("the refused link installed the checkout's dependencies: %v", err)
	}

	pluginsCheckout(t, filepath.Join(bundled, "attn-example"), "attn-example", "0.2.0", pluginsIdle)
	w.restart()
	app = w.App()
	pluginsListed(app, "attn-example still installed at the updated 0.2.0", func(plugins []protocol.PluginInfo) bool {
		p, ok := pluginsNamed(plugins, "attn-example")
		return ok && p.Version == "0.2.0" && p.InstallationState == "installed" && p.Running
	})

	pluginsAct(t, app, protocol.UninstallPluginMessage{Cmd: protocol.CmdUninstallPlugin, Name: "attn-example"}, "uninstall", "attn-example")
	pluginsListed(app, "attn-example available again", available)
	if _, err := os.Stat(filepath.Join(bundled, "attn-example", "attn-plugin.toml")); err != nil {
		t.Errorf("uninstalling deleted the bundled plugin: %v", err)
	}
}

func TestAPluginDrivingASessionCannotBeUninstalled(t *testing.T) {
	w := newWorld(t, fakeagent.Pi)
	app := w.App()
	awaitAgentAvailable(app, string(fakeagent.Pi))
	session := w.Spawn(app, fakeagent.Pi, w.Path("shop"))
	w.Launched(session)

	pluginsRefused(t, app, protocol.UninstallPluginMessage{Cmd: protocol.CmdUninstallPlugin, Name: "attn-pi"}, "uninstall", "active delegated run")
	pluginsListed(app, "attn-pi that cannot be uninstalled", func(plugins []protocol.PluginInfo) bool {
		p, ok := pluginsNamed(plugins, "attn-pi")
		return ok && !p.CanUninstall
	})
}

func pluginsCheckout(t *testing.T, dir, name, version, script string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := "name = \"" + name + "\"\nversion = \"" + version + "\"\nattn_api_version = 6\n\n[plugin]\nkind = \"executable\"\npath = \"run\"\n"
	if err := os.WriteFile(filepath.Join(dir, "attn-plugin.toml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "run"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"name":"`+name+`","private":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func pluginsGitRepo(t *testing.T, script string) string {
	t.Helper()
	repo := filepath.Join(t.TempDir(), "attn-snipe.git")
	pluginsCheckout(t, repo, "attn-snipe", "0.1.0", script)
	runGit(t, repo, "init", "-q", "-b", "main")
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-q", "-m", "plugin")
	return repo
}

func pluginsHolding(t *testing.T, fifo string) *os.File {
	t.Helper()
	opened := make(chan *os.File, 1)
	go func() {
		f, err := os.Open(fifo)
		if err != nil {
			t.Error(err)
		}
		opened <- f
	}()
	select {
	case f := <-opened:
		if f == nil {
			t.FailNow()
		}
		t.Cleanup(func() { _ = f.Close() })
		return f
	case <-time.After(fakeagent.HangGuard):
		t.Fatalf("the plugin never opened %s after %s", fifo, fakeagent.HangGuard)
		return nil
	}
}

func pluginsAwaitExit(t *testing.T, held *os.File) {
	t.Helper()
	closed := make(chan error, 1)
	go func() {
		_, err := io.ReadAll(held)
		closed <- err
	}()
	select {
	case err := <-closed:
		if err != nil {
			t.Errorf("reading the plugin's pipe: %v", err)
		}
	case <-time.After(fakeagent.HangGuard):
		t.Fatalf("the removed plugin still holds its pipe after %s", fakeagent.HangGuard)
	}
}

func pluginsAct(t *testing.T, app *testworld.Peer, cmd any, action, name string) {
	t.Helper()
	result := pluginsRequest(app, cmd, action)
	if !result.Success || protocol.Deref(result.Name) != name {
		t.Fatalf("%s answered %q, want success for %s", action, protocol.Deref(result.Error), name)
	}
}

func pluginsRefused(t *testing.T, app *testworld.Peer, cmd any, action, refusal string) {
	t.Helper()
	if result := pluginsRequest(app, cmd, action); result.Success || !strings.Contains(protocol.Deref(result.Error), refusal) {
		t.Errorf("%s answered %+v, want a refusal mentioning %q", action, result, refusal)
	}
}

func pluginsRequest(app *testworld.Peer, cmd any, action string) protocol.PluginActionResultMessage {
	app.T.Helper()
	return testworld.Request(app, cmd, protocol.EventPluginActionResult, func(r protocol.PluginActionResultMessage) bool { return r.Action == action })
}

func pluginsListed(app *testworld.Peer, want string, match func([]protocol.PluginInfo) bool) []protocol.PluginInfo {
	app.T.Helper()
	app.Send(protocol.ListPluginsMessage{Cmd: protocol.CmdListPlugins})
	return testworld.Await(app, protocol.EventPluginsUpdated, func(m protocol.PluginsUpdatedMessage) bool { return match(m.Plugins) }).Plugins
}

func pluginsNamed(plugins []protocol.PluginInfo, name string) (protocol.PluginInfo, bool) {
	i := slices.IndexFunc(plugins, func(p protocol.PluginInfo) bool { return p.Name == name })
	if i < 0 {
		return protocol.PluginInfo{}, false
	}
	return plugins[i], true
}
