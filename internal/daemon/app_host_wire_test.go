package daemon_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/appbuild"
	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func applyRunningApp(t *testing.T, cli *client.Client, m appbuild.Manifest, bundle string) *protocol.AppApplyResult {
	t.Helper()
	return applyAppVersion(t, cli, m.Name, appManifestDeclaration(t, m), bundle)
}

func withCollections(m appbuild.Manifest, names ...string) appbuild.Manifest {
	for _, name := range names {
		m.Collections = append(m.Collections, appbuild.Collection{Name: name})
	}
	return m
}

func subscriber(name string, events ...string) appbuild.Manifest {
	return withCollections(appbuild.Manifest{Name: name, Subscribe: []appbuild.Subscribe{{Events: events}}}, "marks")
}

type appDocs struct {
	t       *testing.T
	windows chan []protocol.StoredDocument
	seen    map[string]protocol.StoredDocument
}

func watchAppDocs(t *testing.T, w *world, app, collection string) *appDocs {
	t.Helper()
	docs := &appDocs{t: t, windows: make(chan []protocol.StoredDocument, 64), seen: map[string]protocol.StoredDocument{}}
	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	subscribed := make(chan struct{})
	go func() {
		first := true
		_ = w.Client().DocSubscribe(protocol.DocumentQuery{Namespace: "app/" + app, Collection: collection}, nil, func(window client.DocWindow) bool {
			if first {
				first = false
				close(subscribed)
			}
			select {
			case docs.windows <- window.Documents:
				return true
			case <-stop:
				return false
			}
		})
	}()
	select {
	case <-subscribed:
	case <-time.After(fakeagent.HangGuard):
		t.Fatalf("subscribing to app/%s/%s got no first window within %s", app, collection, fakeagent.HangGuard)
	}
	return docs
}

func (d *appDocs) await(id string) map[string]any {
	d.t.Helper()
	for {
		if doc, ok := d.seen[id]; ok {
			var body map[string]any
			if err := json.Unmarshal([]byte(doc.Body), &body); err != nil {
				d.t.Fatalf("decode %s: %v", doc.Body, err)
			}
			return body
		}
		select {
		case window := <-d.windows:
			for _, doc := range window {
				d.seen[doc.ID] = doc
			}
		case <-time.After(fakeagent.HangGuard):
			d.t.Fatalf("the app wrote no document %q within %s", id, fakeagent.HangGuard)
		}
	}
}

func awaitAppEnabled(app *testworld.Peer, name string, enabled bool) {
	app.T.Helper()
	testworld.Await(app, protocol.EventAppsUpdated, func(m protocol.AppsUpdatedMessage) bool {
		for _, entry := range m.Apps {
			if entry.Name == name {
				return entry.Enabled == enabled
			}
		}
		return false
	})
}

func awaitInvocationWith(t *testing.T, invocations <-chan protocol.AppInvocationInfo, status string) protocol.AppInvocationInfo {
	t.Helper()
	for {
		if got := awaitAppInvocation(t, invocations); got.Status == status {
			return got
		}
	}
}

func appNotificationsOf(app *testworld.Peer, kind string) []protocol.Notification {
	app.T.Helper()
	var out []protocol.Notification
	for _, n := range listNotifications(app).Notifications {
		if n.Kind == kind {
			out = append(out, n)
		}
	}
	return out
}

func requireMentions(t *testing.T, what, text string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(text, want) {
			t.Errorf("%s does not say %q: %s", what, want, text)
		}
	}
}

func settling(m appbuild.Manifest) appbuild.Manifest {
	m.Commands = append(m.Commands, appbuild.Command{Name: "settle"})
	return m
}

func settleApp(app *testworld.Peer, name string) {
	app.T.Helper()
	requestAppCommand(app, name, "settle", "")
}

func awaitAppNotifications(app *testworld.Peer, kind string) []protocol.Notification {
	app.T.Helper()
	for {
		if notes := appNotificationsOf(app, kind); len(notes) > 0 {
			return notes
		}
		testworld.Await(app, protocol.EventNotificationsUpdated, func(protocol.NotificationsUpdatedMessage) bool { return true })
	}
}
