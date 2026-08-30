package daemon

import (
	"fmt"
	"net"
	"sort"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/apps"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

const recentInvocationLimit = 10

// Rollback names ids in its refusals and nothing else lists them, so status has
// to; an AppVersionInfo is ~200 bytes on the wire, so ten is ~2KB.
const recentVersionLimit = 10

// servingHistoryLimit is on the same wire budget as recentVersionLimit. The list
// says when it was cut, so a longer chain is visible rather than silently ending.
const servingHistoryLimit = 10

type appEnabledChanged struct {
	Name     string `json:"name"`
	Consumer string `json:"consumer"`
	Enabled  bool   `json:"enabled"`
}

// appRemoved is FactAppRemoved's payload. It names the consumer and namespace
// because a consumer of this fact cannot look them up: the registry row is gone.
type appRemoved struct {
	Name      string `json:"name"`
	Consumer  string `json:"consumer"`
	Namespace string `json:"namespace"`
}

func (d *Daemon) handleAppList(conn net.Conn, _ *protocol.AppListMessage) {
	if d.store == nil {
		d.sendError(conn, "no database")
		return
	}
	rows, err := d.store.ListApps()
	if err != nil {
		d.sendError(conn, fmt.Sprintf("listing apps: %v", err))
		return
	}
	head, err := d.busHead()
	if err != nil {
		d.sendError(conn, fmt.Sprintf("reading the event log: %v", err))
		return
	}
	summaries := make([]protocol.AppSummary, 0, len(rows))
	for _, row := range rows {
		summary, err := d.appSummary(row, head)
		if err != nil {
			d.sendError(conn, err.Error())
			return
		}
		summaries = append(summaries, summary)
	}
	d.sendDocResponse(conn, protocol.Response{
		Ok:            true,
		AppListResult: &protocol.AppListResult{Apps: summaries},
	})
}

func (d *Daemon) handleAppStatus(conn net.Conn, msg *protocol.AppStatusMessage) {
	name := strings.TrimSpace(msg.Name)
	if err := apps.ValidateName(name); err != nil {
		d.sendError(conn, err.Error())
		return
	}
	if d.store == nil {
		d.sendError(conn, "no database")
		return
	}
	row, ok, err := d.store.GetApp(name)
	if err != nil {
		d.sendError(conn, fmt.Sprintf("reading app %q: %v", name, err))
		return
	}
	if !ok {
		d.sendError(conn, d.unknownAppError("status", name))
		return
	}
	head, err := d.busHead()
	if err != nil {
		d.sendError(conn, fmt.Sprintf("reading the event log: %v", err))
		return
	}
	summary, err := d.appSummary(row, head)
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	versions, err := d.store.CountAppVersions(name)
	if err != nil {
		d.sendError(conn, fmt.Sprintf("counting versions of app %q: %v", name, err))
		return
	}
	invocations, err := d.store.CountAppInvocations(name)
	if err != nil {
		d.sendError(conn, fmt.Sprintf("counting invocations of app %q: %v", name, err))
		return
	}
	recent, err := d.store.ListAppInvocations(name, recentInvocationLimit)
	if err != nil {
		d.sendError(conn, fmt.Sprintf("reading invocations of app %q: %v", name, err))
		return
	}
	allVersions, err := d.store.ListAppVersions(name)
	if err != nil {
		d.sendError(conn, fmt.Sprintf("reading versions of app %q: %v", name, err))
		return
	}
	history, steps, err := d.store.ListAppServingHistory(name, servingHistoryLimit)
	if err != nil {
		d.sendError(conn, fmt.Sprintf("reading the serving history of app %q: %v", name, err))
		return
	}
	recentVersions := allVersions
	if len(recentVersions) > recentVersionLimit {
		recentVersions = recentVersions[:recentVersionLimit]
	}

	reconcile, err := d.appReconcileStatusForWire(name)
	if err != nil {
		d.sendError(conn, fmt.Sprintf("reading the reconcile state of app %q: %v", name, err))
		return
	}

	result := protocol.AppStatusResult{App: summary, Versions: versions, Invocations: invocations, Reconcile: reconcile}
	for _, version := range recentVersions {
		result.RecentVersions = append(result.RecentVersions, protocol.AppVersionInfo{
			ID:           int(version.ID),
			ContentHash:  version.ContentHash,
			ArtifactPath: version.ArtifactPath,
			CreatedAt:    stampForWire(version.CreatedAt),
		})
	}
	byID := make(map[int64]store.AppVersion, len(allVersions))
	for _, version := range allVersions {
		byID[version.ID] = version
	}
	for _, id := range history {
		version, ok := byID[id]
		if !ok {
			continue
		}
		result.ServingHistory = append(result.ServingHistory, protocol.AppVersionInfo{
			ID:           int(version.ID),
			ContentHash:  version.ContentHash,
			ArtifactPath: version.ArtifactPath,
			CreatedAt:    stampForWire(version.CreatedAt),
		})
	}
	if steps > 0 {
		result.ServingHistorySteps = protocol.Ptr(steps)
	}
	for _, inv := range recent {
		result.Recent = append(result.Recent, appInvocationForWire(inv.ID, inv))
	}
	if snapshot, ok := d.appRuntimeSnapshot(); ok {
		info := d.appRuntimeInfo(snapshot)
		result.Runtime = &info
	}
	if stall, ok := d.appStallSnapshot(name); ok {
		info := d.appStallForWire(stall)
		result.Stall = &info
	}
	d.sendDocResponse(conn, protocol.Response{Ok: true, AppStatusResult: &result})
}

// It refuses an app with no consumer instead of creating one: minting a cursor
// here would silently decide where an app that never ran starts reading from.
func (d *Daemon) handleAppSetEnabled(conn net.Conn, msg *protocol.AppSetEnabledMessage) {
	name := strings.TrimSpace(msg.Name)
	verb := "disable"
	if msg.Enabled {
		verb = "enable"
	}
	if err := apps.ValidateName(name); err != nil {
		d.sendError(conn, err.Error())
		return
	}
	if d.store == nil {
		d.sendError(conn, "no database")
		return
	}
	lane := d.appLane(name)
	lane.Lock()
	defer lane.Unlock()
	if _, ok, err := d.store.GetApp(name); err != nil {
		d.sendError(conn, fmt.Sprintf("reading app %q: %v", name, err))
		return
	} else if !ok {
		d.sendError(conn, d.unknownAppError(verb, name))
		return
	}
	consumer := apps.ConsumerName(name)
	if _, ok, err := d.store.GetBusConsumer(consumer); err != nil {
		d.sendError(conn, fmt.Sprintf("reading the bus consumer for app %q: %v", name, err))
		return
	} else if !ok {
		d.sendError(conn, fmt.Sprintf(
			"app %q has no bus consumer (%s), so there is no enabled bit to %s: nothing is delivering facts to it. "+
				"A consumer is registered when a version is applied or rolled onto, and again when the daemon starts; `attn app status %s` shows what exists.",
			name, consumer, verb, name))
		return
	}
	flipped, changed, err := d.store.SetAppBusConsumerEnabled(name, msg.Enabled, time.Now())
	if err != nil {
		d.sendError(conn, fmt.Sprintf("%s app %q: %v", verb, name, err))
		return
	}
	if !flipped {
		d.sendError(conn, fmt.Sprintf(
			"app %q: its bus consumer %s was removed while %s was running, so nothing was changed. "+
				"`attn app status %s` shows what is left.", name, consumer, verb, name))
		return
	}
	if msg.Enabled && changed {
		// Enabling is the way back from an auto-disable, so it clears both streaks
		// that cause one — otherwise the next failure disables the app again.
		d.clearAppStall(name)
		d.clearAppCrashes(name)
	}
	if changed {
		d.publishFact(FactAppEnabledChanged, name, appEnabledChanged{
			Name: name, Consumer: consumer, Enabled: msg.Enabled,
		})
	}
	d.sendDocResponse(conn, protocol.Response{
		Ok: true,
		AppSetEnabledResult: &protocol.AppSetEnabledResult{
			Name: name, Consumer: consumer, Enabled: msg.Enabled,
		},
	})
}

// Unregister through the bus, never delete from the store: a live delivery loop
// reading a registration that vanished retries that error forever.
func (d *Daemon) handleAppRemove(conn net.Conn, msg *protocol.AppRemoveMessage) {
	name := strings.TrimSpace(msg.Name)
	if err := apps.ValidateName(name); err != nil {
		d.sendError(conn, err.Error())
		return
	}
	if d.store == nil {
		d.sendError(conn, "no database")
		return
	}
	_, appExists, err := d.store.GetApp(name)
	if err != nil {
		d.sendError(conn, fmt.Sprintf("reading app %q: %v", name, err))
		return
	}
	consumer := apps.ConsumerName(name)
	_, consumerExists, err := d.store.GetBusConsumer(consumer)
	if err != nil {
		d.sendError(conn, fmt.Sprintf("reading the bus consumer for app %q: %v", name, err))
		return
	}
	if !appExists && !consumerExists {
		d.sendError(conn, d.unknownAppError("remove", name))
		return
	}

	if consumerExists {
		if err := d.unregisterConsumer(consumer); err != nil {
			d.sendError(conn, fmt.Sprintf("removing app %q: stopping its bus consumer %s: %v", name, consumer, err))
			return
		}
	}
	if _, err := d.store.DeleteApp(name); err != nil {
		d.sendError(conn, fmt.Sprintf("removing app %q: %v", name, err))
		return
	}

	versions, err := d.store.CountAppVersions(name)
	if err != nil {
		d.sendError(conn, fmt.Sprintf("counting versions of app %q: %v", name, err))
		return
	}
	invocations, err := d.store.CountAppInvocations(name)
	if err != nil {
		d.sendError(conn, fmt.Sprintf("counting invocations of app %q: %v", name, err))
		return
	}
	namespace := apps.Namespace(name)
	d.publishFact(FactAppRemoved, name, appRemoved{Name: name, Consumer: consumer, Namespace: namespace})
	d.sendDocResponse(conn, protocol.Response{
		Ok: true,
		AppRemoveResult: &protocol.AppRemoveResult{
			Name:            name,
			ConsumerRemoved: consumerExists,
			VersionsKept:    versions,
			InvocationsKept: invocations,
			NamespaceKept:   namespace,
		},
	})
}

func (d *Daemon) unregisterConsumer(consumer string) error {
	if d.eventBus != nil {
		return d.eventBus.Unregister(consumer)
	}
	return d.store.DeleteBusConsumer(consumer)
}

func (d *Daemon) appSummary(row store.App, head int64) (protocol.AppSummary, error) {
	summary := protocol.AppSummary{
		Name:      row.Name,
		CreatedAt: stampForWire(row.CreatedAt),
		UpdatedAt: stampForWire(row.UpdatedAt),
	}
	if row.CurrentVersionID != 0 {
		version, ok, err := d.store.GetAppVersion(row.CurrentVersionID)
		if err != nil {
			return protocol.AppSummary{}, fmt.Errorf("reading version %d of app %q: %w", row.CurrentVersionID, row.Name, err)
		}
		if ok {
			summary.CurrentVersion = &protocol.AppVersionInfo{
				ID:           int(version.ID),
				ContentHash:  version.ContentHash,
				ArtifactPath: version.ArtifactPath,
				CreatedAt:    stampForWire(version.CreatedAt),
			}
			// From the serving version's frozen declaration, not the manifest on
			// disk: after a rollback those differ, and what docks is what serves.
			summary.Views = appViewsForWire(version.Declaration, d.logf)
			summary.Commands = appDeclaredCommands(version.Declaration, d.logf)
		}
	}
	consumer, ok, err := d.store.GetBusConsumer(apps.ConsumerName(row.Name))
	if err != nil {
		return protocol.AppSummary{}, fmt.Errorf("reading the bus consumer for app %q: %w", row.Name, err)
	}
	if ok {
		lag := head - consumer.Cursor
		if lag < 0 {
			lag = 0
		}
		summary.Consumer = &protocol.AppConsumerInfo{
			Name:    consumer.Name,
			Enabled: consumer.Enabled,
			Cursor:  int(consumer.Cursor),
			Lag:     int(lag),
			Filter:  consumer.Filter,
		}
	}
	return summary, nil
}

func (d *Daemon) busHead() (int64, error) {
	_, head, err := d.store.BusBounds()
	return head, err
}

func (d *Daemon) unknownAppError(verb, name string) string {
	msg := fmt.Sprintf("app %s: no app named %q is registered", verb, name)
	if rows, err := d.store.ListApps(); err == nil {
		if len(rows) == 0 {
			msg += "; no apps are registered"
		} else {
			names := make([]string, 0, len(rows))
			for _, row := range rows {
				names = append(names, row.Name)
			}
			sort.Strings(names)
			msg += "; registered apps: " + strings.Join(names, ", ")
		}
	}
	var leftovers []string
	if versions, err := d.store.CountAppVersions(name); err == nil && versions > 0 {
		leftovers = append(leftovers, fmt.Sprintf("%d version(s) of it remain as history", versions))
	}
	if _, ok, err := d.store.GetBusConsumer(apps.ConsumerName(name)); err == nil && ok {
		leftovers = append(leftovers, fmt.Sprintf("its bus consumer %s still exists (`attn app remove %s` deletes it)", apps.ConsumerName(name), name))
	}
	if len(leftovers) > 0 {
		msg += ". " + strings.Join(leftovers, "; ")
	}
	return msg
}

func stampForWire(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}
