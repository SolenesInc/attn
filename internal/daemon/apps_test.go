package daemon

import (
	"net"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/apps"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

func seedApp(t *testing.T, d *Daemon, name string, enabled bool) store.AppVersion {
	t.Helper()
	now := time.Now().UTC()
	version, _, err := d.store.CommitAppVersion(store.AppVersion{
		AppName:      name,
		ContentHash:  "sha256:" + name,
		Declaration:  `{"name":"` + name + `","subscribe":[{"events":["ticket.*"]}]}`,
		ArtifactPath: "apps/" + name + "/bundle.js",
	}, now)
	if err != nil {
		t.Fatalf("seed version for %s: %v", name, err)
	}
	seedAppConsumer(t, d, name, enabled, 0)
	return version
}

func seedAppConsumer(t *testing.T, d *Daemon, name string, enabled bool, cursor int64) {
	t.Helper()
	if err := d.store.SaveBusConsumer(store.BusConsumer{
		Name:    apps.ConsumerName(name),
		Cursor:  cursor,
		Filter:  "ticket.*",
		Enabled: enabled,
	}, time.Now()); err != nil {
		t.Fatalf("seed consumer for %s: %v", name, err)
	}
	if _, err := d.store.SetBusConsumerEnabled(apps.ConsumerName(name), enabled, time.Now()); err != nil {
		t.Fatalf("seed consumer bit for %s: %v", name, err)
	}
	if err := d.store.SetBusConsumerCursor(apps.ConsumerName(name), cursor, time.Now()); err != nil {
		t.Fatalf("seed consumer cursor for %s: %v", name, err)
	}
}

func appStatus(t *testing.T, d *Daemon, name string) protocol.Response {
	t.Helper()
	return docCall(t, func(c net.Conn) {
		d.handleAppStatus(c, &protocol.AppStatusMessage{Cmd: protocol.CmdAppStatus, Name: name})
	})
}
