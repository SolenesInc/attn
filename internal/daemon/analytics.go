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
	"time"

	"github.com/victorarias/attn/internal/analytics"
	"github.com/victorarias/attn/internal/jobs"
	"github.com/victorarias/attn/internal/protocol"
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
	return parseBooleanSetting(d.daemonSetting(SettingAnalyticsEnabled))
}

func (d *Daemon) configureAnalytics() {
	if !d.analyticsEnabled() {
		return
	}
	if err := ensureAnalyticsSecret(d.dataRoot); err != nil {
		d.logf("analytics: %v", err)
	}
}

func ensureAnalyticsSecret(dataRoot string) error {
	path := filepath.Join(dataRoot, analyticsSecretFile)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, fs.ErrExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		file.Close()
		os.Remove(path)
		return fmt.Errorf("generate analytics secret: %w", err)
	}
	if _, err := file.WriteString(hex.EncodeToString(secret)); err != nil {
		file.Close()
		os.Remove(path)
		return fmt.Errorf("write %s: %w", path, err)
	}
	return file.Close()
}

func (d *Daemon) analyticsPruneHandler(_ context.Context, _ *jobs.Job) (any, error) {
	pruned, err := d.store.PruneAnalyticsOutbox(time.Now().Add(-analyticsRetention))
	if err != nil {
		return nil, fmt.Errorf("pruning the analytics outbox: %w", err)
	}
	return map[string]any{"pruned": pruned}, nil
}

const analyticsPageLimit = 1000

func (d *Daemon) sendAnalyticsRecordsWSResult(client *wsClient, msg *protocol.ListAnalyticsRecordsMessage) {
	result := protocol.ListAnalyticsRecordsResultMessage{Event: protocol.EventListAnalyticsRecordsResult, RequestID: protocol.Deref(msg.RequestID)}
	page, err := d.analyticsRecords(msg)
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

func (d *Daemon) analyticsRecords(msg *protocol.ListAnalyticsRecordsMessage) (store.AnalyticsPage, error) {
	if msg.Limit < 1 || msg.Limit > analyticsPageLimit {
		return store.AnalyticsPage{}, fmt.Errorf("limit must be between 1 and %d, got %d", analyticsPageLimit, msg.Limit)
	}
	stream := analytics.Stream(protocol.Deref(msg.Stream))
	switch stream {
	case "", analytics.StreamBus, analytics.StreamApp, analytics.StreamCLI, analytics.StreamSample, analytics.StreamVent:
	default:
		return store.AnalyticsPage{}, fmt.Errorf("unknown analytics stream %q: use bus, app, cli, sample or vent", stream)
	}
	return d.store.AnalyticsRecords(stream, int64(protocol.Deref(msg.BeforeID)), msg.Limit)
}
