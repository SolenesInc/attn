package daemon

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/appbuild"
	"github.com/victorarias/attn/internal/apps"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

const appViewCrashEvent = "app.view.crashed"

// appViewCrashErrorLimit sits ~8x past the largest component stack React has
// produced in this app. Over it the text is truncated with a line saying so.
const appViewCrashErrorLimit = 32 * 1024

// Deliberately not appSummary: that joins the bus consumer's cursor and lag,
// costing a bus-head read per push for something a tile ignores.
func (d *Daemon) appRegistryForWire() []protocol.AppRegistryEntry {
	if d.store == nil {
		return nil
	}
	rows, err := d.store.ListApps()
	if err != nil {
		d.logf("apps: listing apps for the UI registry: %v", err)
		return nil
	}
	out := make([]protocol.AppRegistryEntry, 0, len(rows))
	for _, row := range rows {
		entry, err := d.appRegistryEntry(row)
		if err != nil {
			d.logf("apps: describing app %s for the UI registry: %v", row.Name, err)
			continue
		}
		out = append(out, entry)
	}
	return out
}

func (d *Daemon) appRegistryEntry(row store.App) (protocol.AppRegistryEntry, error) {
	entry := protocol.AppRegistryEntry{Name: row.Name, Views: []protocol.AppViewInfo{}}
	if consumer, ok, err := d.store.GetBusConsumer(apps.ConsumerName(row.Name)); err != nil {
		return protocol.AppRegistryEntry{}, fmt.Errorf("reading its bus consumer: %w", err)
	} else if ok {
		entry.Enabled = consumer.Enabled
	}
	if row.CurrentVersionID == 0 {
		return entry, nil
	}
	version, ok, err := d.store.GetAppVersion(row.CurrentVersionID)
	if err != nil {
		return protocol.AppRegistryEntry{}, fmt.Errorf("reading version %d: %w", row.CurrentVersionID, err)
	}
	if !ok {
		return entry, nil
	}
	entry.VersionID = protocol.Ptr(int(version.ID))
	entry.ContentHash = protocol.Ptr(version.ContentHash)
	if description := appDeclarationDescription(version.Declaration); description != "" {
		entry.Description = protocol.Ptr(description)
	}
	entry.Views = appViewsForWire(version.Declaration, d.logf)
	return entry, nil
}

func appViewsForWire(declaration string, logf func(string, ...any)) []protocol.AppViewInfo {
	views, err := appbuild.DeclaredViews(declaration)
	if err != nil {
		if logf != nil {
			logf("apps: reading the views of a stored declaration: %v", err)
		}
		return []protocol.AppViewInfo{}
	}
	out := make([]protocol.AppViewInfo, 0, len(views))
	for _, v := range views {
		info := protocol.AppViewInfo{Name: v.Name, Kind: v.Kind, Title: v.Title}
		if v.Params != nil {
			info.ParamsLabel = protocol.Ptr(v.Params.Label)
			if v.Params.Placeholder != "" {
				info.ParamsPlaceholder = protocol.Ptr(v.Params.Placeholder)
			}
		}
		out = append(out, info)
	}
	return out
}

func appDeclarationDescription(declaration string) string {
	var snapshot struct {
		Description string `json:"description"`
	}
	if err := json.Unmarshal([]byte(declaration), &snapshot); err != nil {
		return ""
	}
	return strings.TrimSpace(snapshot.Description)
}

func (d *Daemon) projectAppsUpdated() {
	if d.store == nil {
		return
	}
	d.projectSnapshot(snapshotApps, func() {
		apps := d.appRegistryForWire()
		// AppsUpdatedMessage is its own top-level type, invisible to the hub's
		// WebSocketEvent-only broadcast listener; tests use this hook.
		if d.appsBroadcastHook != nil {
			d.appsBroadcastHook(apps)
		}
		if d.wsHub == nil {
			return
		}
		d.broadcastMessage(&protocol.AppsUpdatedMessage{
			Event: protocol.EventAppsUpdated,
			Apps:  apps,
		})
	})
}

// Deliberately does NOT advance the app's stall clock: that clock exists because
// a stuck consumer holds the retention floor open, and a crashing tile pins nothing.
func (d *Daemon) handleAppViewCrash(_ *wsClient, msg *protocol.AppViewCrashMessage) {
	name := strings.TrimSpace(msg.App)
	if err := apps.ValidateName(name); err != nil {
		d.logf("apps: a view crash report named app %q: %v", msg.App, err)
		return
	}
	view := strings.TrimSpace(msg.View)
	if err := apps.ValidateViewName(view); err != nil {
		d.logf("apps: a view crash report of app %s named view %q: %v", name, msg.View, err)
		return
	}
	if d.store == nil {
		return
	}
	version, ok, err := d.store.GetAppVersion(int64(msg.VersionID))
	if err != nil {
		d.logf("apps: reading version %d for a view crash report of %s: %v", msg.VersionID, name, err)
		return
	}
	if !ok || version.AppName != name {
		d.logf("apps: dropping a view crash report of %s/%s: version %d is not a version of that app", name, view, msg.VersionID)
		return
	}

	text := strings.TrimSpace(msg.Error)
	if text == "" {
		text = "the view threw while rendering and the boundary caught no message"
	}
	if len(text) > appViewCrashErrorLimit {
		text = text[:appViewCrashErrorLimit] + fmt.Sprintf("\n… truncated at %d bytes", appViewCrashErrorLimit)
	}
	now := time.Now()
	d.logf("apps: view %s/%s crashed while rendering at version %d: %s", name, view, version.ID, firstLine(text))
	d.recordAppInvocation(store.AppInvocation{
		AppName:      name,
		VersionID:    version.ID,
		Kind:         store.AppInvocationKindView,
		EventName:    appViewCrashEvent,
		EventSubject: strings.TrimSpace(msg.TileID),
		Handler:      apps.ViewLabel(view),
		Status:       appInvocationStatusError,
		Error:        text,
		StartedAt:    now,
	})
	if err := appendAppLogLines(AppRuntimeLogPath(d.socketPath), name, fmt.Sprintf(
		"view %s crashed while rendering (version %d)\n%s", view, version.ID, text)); err != nil {
		d.logf("apps: writing the view crash of %s/%s to the app log: %v", name, view, err)
	}
}

// The supervisor's capture holds the same file open with O_APPEND, so the block goes out
// in ONE write and a crash's stack cannot interleave with a handler's output.
func appendAppLogLines(path, app, text string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()

	tag := appRuntimeAppTag(app)
	var block strings.Builder
	for _, line := range strings.Split(text, "\n") {
		block.WriteString(tag)
		block.WriteString(line)
		block.WriteByte('\n')
	}
	_, err = file.WriteString(block.String())
	return err
}
