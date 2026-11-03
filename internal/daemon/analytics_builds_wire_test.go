package daemon_test

import (
	"encoding/json"
	"testing"

	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestEachUploadBatchCarriesTheBuildThatRecordedItsRecords(t *testing.T) {
	ingest := testworld.StartFakeIngest(t)
	inBubbleUploadingTo(t, ingest.Link(t), func(t *testing.T, w *world) {
		app := appAt(w, "0.1.0")
		setSetting(t, app, "analytics.enabled", "true")
		sendAppAction(app, "settings_section", map[string]any{"section": "under-0.1.0"})
		analyticsStatus(app)
		w.restart()

		app = appAt(w, "0.2.0")
		sendAppAction(app, "settings_section", map[string]any{"section": "under-0.2.0"})
		setSetting(t, app, "analytics.sharing_enabled", "true")
		w.advance(0)

		shippedIn := map[string]string{}
		for shipped := 0; len(shippedIn) < 2; w.advance(uploadInterval) {
			batches := ingest.Batches()
			if len(batches) == shipped {
				t.Fatalf("an upload tick sent no batch while records shipped only in %v", shippedIn)
			}
			for _, batch := range batches[shipped:] {
				for _, record := range batch.Records {
					var props struct {
						Section string `json:"section"`
					}
					_ = json.Unmarshal(record.Props, &props)
					if props.Section != "" {
						shippedIn[props.Section] = batch.Build.AppVersion
					}
				}
			}
			shipped = len(batches)
		}
		if shippedIn["under-0.1.0"] != "0.1.0" || shippedIn["under-0.2.0"] != "0.2.0" {
			t.Fatalf("records shipped in batches with app versions %v, want each under the app version connected when it was recorded", shippedIn)
		}
	})
}

func appAt(w *world, version string) *testworld.Peer {
	w.T.Helper()
	app := w.Connect(protocol.ClientHelloMessage{
		Cmd:          protocol.CmdClientHello,
		ClientKind:   "tauri-app",
		Version:      "protocol-" + protocol.ProtocolVersion,
		Capabilities: []string{},
		ClientToken:  protocol.Ptr(config.ClientToken()),
		AppVersion:   protocol.Ptr(version),
		AppChannel:   protocol.Ptr(testworld.AppChannel),
	}, nil)
	app.Initial = testworld.Await[protocol.InitialStateMessage](app, protocol.EventInitialState, nil)
	return app
}
