package daemon_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

func TestAFailedPluginCloneNeverShowsTheSourcesCredentials(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	source := "https://user:hunter2@127.0.0.1:1/team/plugin.git?access_token=s3cret#frag"

	result := pluginsRequest(app, protocol.InstallPluginMessage{Cmd: protocol.CmdInstallPlugin, Source: source}, "install")
	refusal := protocol.Deref(result.Error)
	if result.Success || !strings.Contains(refusal, "clone plugin repository") {
		t.Fatalf("installing from an unreachable repository answered %+v, want a clone failure", result)
	}
	for _, secret := range []string{"hunter2", "s3cret"} {
		if strings.Contains(refusal, secret) {
			t.Errorf("the clone failure shows %q: %s", secret, refusal)
		}
	}
}

func TestAPluginInstallsOnceAndNeverUnderANameThatLeavesThePluginDirectory(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	checkout := pluginsCheckout(t, w.Path("checkouts", "worktree-provider"), "worktree-provider", "0.1.0", pluginsIdle)

	pluginsAct(t, app, protocol.InstallPluginMessage{Cmd: protocol.CmdInstallPlugin, Source: checkout}, "install", "worktree-provider")
	pluginsRefused(t, app, protocol.InstallPluginMessage{Cmd: protocol.CmdInstallPlugin, Source: checkout}, "install", `plugin "worktree-provider" is already installed`)
	pluginsListed(app, "worktree-provider installed once", func(plugins []protocol.PluginInfo) bool {
		_, ok := pluginsNamed(plugins, "worktree-provider")
		return ok && len(plugins) == 1
	})

	escaping := pluginsCheckout(t, w.Path("checkouts", "escaping"), "../escaping", "0.1.0", pluginsIdle)
	pluginsRefused(t, app, protocol.InstallPluginMessage{Cmd: protocol.CmdInstallPlugin, Source: escaping}, "install", "cannot be used as an install directory")
	if _, err := os.Stat(filepath.Join(w.Dir, "escaping")); !os.IsNotExist(err) {
		t.Errorf("the refused install left %s behind (%v)", filepath.Join(w.Dir, "escaping"), err)
	}
}
