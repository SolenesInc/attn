package daemon

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/analytics"
	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/jobs"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/setting"
	"github.com/victorarias/attn/internal/store"
)

const (
	analyticsSecretFile = "analytics-secret"

	analyticsPruneKind     = "analytics_outbox_prune"
	analyticsPruneInterval = time.Hour
	analyticsPruneTimeout  = time.Minute
	analyticsRetention     = 7 * 24 * time.Hour
)

func (d *Daemon) analyticsEnabled() bool {
	if !d.on(setting.AnalyticsEnabled) {
		return false
	}
	return d.enrollmentStatus().IsHome() || d.setting(setting.AnalyticsSubjectSecret) != ""
}

func (d *Daemon) analyticsCopySource() string {
	status := d.enrollmentStatus()
	if status.IsHome() {
		return ""
	}
	return status.HomeDaemonID
}

func (d *Daemon) analyticsSecret() ([]byte, error) {
	if !d.enrollmentStatus().IsHome() {
		return hex.DecodeString(d.setting(setting.AnalyticsSubjectSecret))
	}
	secret, err := ensureAnalyticsSecret(d.dataRoot)
	if err != nil {
		return nil, err
	}
	if encoded := hex.EncodeToString(secret); d.store.GetSetting(setting.AnalyticsSubjectSecret.Key) != encoded {
		if err := d.store.SetSettingChecked(setting.AnalyticsSubjectSecret.Key, encoded); err != nil {
			return nil, fmt.Errorf("copy the analytics secret to outposts: %w", err)
		}
	}
	return secret, nil
}

func (d *Daemon) analyticsSecretChanged() {
	if err := d.stopAnalyticsMirror(); err != nil {
		d.logf("analytics: %v", err)
	}
	if _, err := d.store.ClearAnalyticsOutbox(); err != nil {
		d.logf("analytics: clear records hashed with the old secret: %v", err)
	}
	d.configureAnalytics()
}

func (d *Daemon) endOutpostAnalyticsGeneration() error {
	return d.store.SetSettingChecked(setting.AnalyticsClearedAt.Key, time.Now().UTC().Format(time.RFC3339Nano))
}

func (d *Daemon) analyticsCapturedSince(at time.Time) bool {
	since := d.analyticsOnSince.Load()
	return since != nil && !at.Before(*since) && d.analyticsEnabled()
}

func (d *Daemon) configureAnalytics() {
	if !d.analyticsEnabled() {
		d.analyticsOnSince.Store(nil)
		if err := d.stopAnalyticsMirror(); err != nil {
			d.logf("analytics: %v", err)
		}
		return
	}
	secret, err := d.analyticsSecret()
	if err != nil {
		d.logf("analytics: %v", err)
		return
	}
	if err := d.startAnalyticsMirror(secret); err != nil {
		d.logf("analytics: %v", err)
	}
	now := time.Now()
	d.analyticsOnSince.CompareAndSwap(nil, &now)
}

func ensureAnalyticsSecret(dataRoot string) ([]byte, error) {
	path := filepath.Join(dataRoot, analyticsSecretFile)
	if _, err := os.Stat(path); err != nil {
		if err := createAnalyticsSecret(dataRoot, path); err != nil {
			return nil, err
		}
	}
	encoded, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	secret, err := hex.DecodeString(strings.TrimSpace(string(encoded)))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return secret, nil
}

func createAnalyticsSecret(dataRoot, path string) error {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return fmt.Errorf("generate analytics secret: %w", err)
	}
	draft, err := os.CreateTemp(dataRoot, analyticsSecretFile+".*")
	if err != nil {
		return fmt.Errorf("create analytics secret draft: %w", err)
	}
	defer os.Remove(draft.Name())
	if _, err := draft.WriteString(hex.EncodeToString(secret)); err != nil {
		draft.Close()
		return fmt.Errorf("write analytics secret draft: %w", err)
	}
	if err := draft.Close(); err != nil {
		return fmt.Errorf("write analytics secret draft: %w", err)
	}
	if err := os.Link(draft.Name(), path); err != nil && !errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("create %s: %w", path, err)
	}
	return nil
}

func (d *Daemon) analyticsPruneHandler(_ context.Context, _ *jobs.Job) (any, error) {
	now := time.Now()
	before, unsentBefore := now.Add(-analyticsRetention), now.Add(-analyticsRetention)
	if d.analyticsSharing() {
		unsentBefore = now.Add(-analyticsUnsentRetention)
	}
	pruned, err := d.store.PruneAnalyticsOutbox(before, unsentBefore)
	if err != nil {
		return nil, fmt.Errorf("pruning the analytics outbox: %w", err)
	}
	return map[string]any{"pruned": pruned}, nil
}

const analyticsPageLimit = 1000

func (d *Daemon) sendAnalyticsRecordsWSResult(client *wsClient, msg *protocol.ListAnalyticsRecordsMessage) {
	result := protocol.ListAnalyticsRecordsResultMessage{Event: protocol.EventListAnalyticsRecordsResult, RequestID: protocol.Deref(msg.RequestID)}
	page, err := d.analyticsRecords(msg, client.selectedProfile())
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
		d.sendToClient(client, result)
		return
	}
	result.Success = true
	result.Waiting = page.Waiting
	result.Records = make([]protocol.AnalyticsOutboxRow, 0, len(page.Rows))
	for _, row := range page.Rows {
		result.Records = append(result.Records, protocol.AnalyticsOutboxRow{ID: int(row.ID), Record: row.Record})
	}
	d.sendToClient(client, result)
}

func (d *Daemon) sendAnalyticsStatusWSResult(client *wsClient, msg *protocol.GetAnalyticsStatusMessage) {
	result := protocol.GetAnalyticsStatusResultMessage{Event: protocol.EventGetAnalyticsStatusResult, RequestID: msg.RequestID}
	if status, err := d.store.AnalyticsStatus(); err != nil {
		result.Error = protocol.Ptr(err.Error())
	} else {
		result.Success = true
		result.Records, result.Bytes, result.Waiting = int(status.Records), int(status.Bytes), int(status.Waiting)
		if !status.LastSentAt.IsZero() {
			result.LastSentAt = protocol.Ptr(status.LastSentAt.UTC().Format(time.RFC3339Nano))
		}
		if refusal := d.analyticsUpload.lastRefusal(); refusal != "" {
			result.LastRefusal = &refusal
		}
		result.IngestURL = config.AnalyticsIngestURL()
		if reason := config.AnalyticsIngestOffReason(); reason != "" {
			result.IngestOffReason = &reason
		}
	}
	d.sendToClient(client, result)
}

func (d *Daemon) sendClearAnalyticsRecordsWSResult(client *wsClient, msg *protocol.ClearAnalyticsRecordsMessage) {
	result := protocol.ClearAnalyticsRecordsResultMessage{Event: protocol.EventClearAnalyticsRecordsResult, RequestID: msg.RequestID}
	if err := d.endOutpostAnalyticsGeneration(); err != nil {
		result.Error = protocol.Ptr(fmt.Sprintf("nothing was cleared: telling outposts to clear failed: %v", err))
		d.sendToClient(client, result)
		return
	}
	cleared, err := d.store.ClearAnalyticsOutbox()
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
		d.sendToClient(client, result)
		return
	}
	result.Records = int(cleared)
	vents, err := d.store.DeleteDismissedVents(client.selectedProfile())
	if err != nil {
		result.Error = protocol.Ptr(fmt.Sprintf("cleared %d records but not the dismissed vents: %v", cleared, err))
		d.sendToClient(client, result)
		return
	}
	d.announceDeletedVents(vents)
	result.Success = true
	result.Vents = len(vents)
	d.sendToClient(client, result)
}

func (d *Daemon) analyticsRecords(msg *protocol.ListAnalyticsRecordsMessage, profileID string) (store.AnalyticsPage, error) {
	if msg.Limit < 1 || msg.Limit > analyticsPageLimit {
		return store.AnalyticsPage{}, fmt.Errorf("limit must be between 1 and %d, got %d", analyticsPageLimit, msg.Limit)
	}
	stream := analytics.Stream(protocol.Deref(msg.Stream))
	switch stream {
	case "", analytics.StreamBus, analytics.StreamApp, analytics.StreamCLI, analytics.StreamSample, analytics.StreamVent, analytics.StreamRun:
	default:
		return store.AnalyticsPage{}, fmt.Errorf("unknown analytics stream %q: use bus, app, cli, sample, vent or run", stream)
	}
	return d.store.AnalyticsRecords(profileID, stream, int64(protocol.Deref(msg.BeforeID)), msg.Limit)
}
