package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/victorarias/attn/internal/analytics"
	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/jobs"
	"github.com/victorarias/attn/internal/protocol"
)

const (
	analyticsTokenFile = "analytics-token"

	analyticsShipKind     = "analytics_ship"
	analyticsShipInterval = 5 * time.Minute
	analyticsShipTimeout  = 3 * time.Minute

	analyticsShipBatchRecords = 5000

	analyticsUnsentRetention = 30 * 24 * time.Hour
)

type analyticsUpload struct {
	run sync.Mutex

	mu      sync.Mutex
	refusal string
	retryAt time.Time
}

func (u *analyticsUpload) settle(err error, now time.Time) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.refusal, u.retryAt = "", time.Time{}
	if err == nil {
		return
	}
	u.refusal = err.Error()
	var refused *analytics.RefusalError
	if errors.As(err, &refused) && refused.Refusal.RetryAfterS > 0 {
		u.retryAt = now.Add(time.Duration(refused.Refusal.RetryAfterS) * time.Second)
	}
}

func (u *analyticsUpload) lastRefusal() string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.refusal
}

func (u *analyticsUpload) waiting(now time.Time) bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	return now.Before(u.retryAt)
}

type appIdentity struct {
	Version, Channel string
}

func (d *Daemon) analyticsBuild() analytics.Build {
	build := analytics.CurrentBuild()
	if app := d.appIdentity.Load(); app != nil {
		build.AppVersion, build.AppChannel = app.Version, app.Channel
	}
	return build
}

func (d *Daemon) analyticsSharing() bool {
	return d.analyticsEnabled() && parseBooleanSetting(d.daemonSetting(SettingAnalyticsSharingEnabled))
}

func (d *Daemon) queueAnalyticsShip() {
	queue := d.jobQueueRef()
	if queue == nil || !d.analyticsSharing() {
		return
	}
	if _, err := queue.Enqueue(analyticsShipKind, jobs.EnqueueOptions{UniqueKey: jobs.CronKey, RunNow: true}); err != nil {
		d.logf("analytics: queue an upload: %v", err)
	}
}

func (d *Daemon) analyticsShipHandler(ctx context.Context, _ *jobs.Job) (any, error) {
	d.analyticsUpload.run.Lock()
	defer d.analyticsUpload.run.Unlock()
	if !d.analyticsSharing() || d.analyticsUpload.waiting(time.Now()) {
		return nil, nil
	}
	sent, err := d.shipAnalyticsBatch(ctx)
	d.analyticsUpload.settle(err, time.Now())
	if err != nil {
		return nil, fmt.Errorf("uploading analytics: %w", err)
	}
	return map[string]any{"sent": sent}, nil
}

func (d *Daemon) shipAnalyticsBatch(ctx context.Context) (int, error) {
	rows, err := d.store.UnsentAnalyticsRecords(analyticsShipBatchRecords)
	if err != nil || len(rows) == 0 {
		return 0, err
	}
	records := make([]analytics.Record, len(rows))
	ids := make([]int64, len(rows))
	for i, row := range rows {
		ids[i] = row.ID
		if err := json.Unmarshal([]byte(row.Record), &records[i]); err != nil {
			return 0, fmt.Errorf("outbox row %d: %w", row.ID, err)
		}
	}
	ingest := analytics.NewIngest(config.AnalyticsIngestURL())
	registration, err := d.analyticsRegistration(ctx, ingest)
	if err != nil {
		return 0, err
	}
	if _, err := ingest.Send(ctx, registration.Token, analytics.Batch{Build: d.analyticsBuild(), Records: records}); err != nil {
		if errors.Is(err, analytics.ErrUnknownInstall) {
			return 0, fmt.Errorf("%w; Forget this install on Settings › Analytics to start a new one", err)
		}
		return 0, err
	}
	vents, err := d.store.MarkAnalyticsSent(ids, time.Now())
	if err != nil {
		return 0, fmt.Errorf("attn-ingest accepted %d records, but marking them sent failed, so they will be sent again and counted as duplicates: %w", len(rows), err)
	}
	d.announceVentSends(vents)
	return len(rows), nil
}

func (d *Daemon) analyticsRegistration(ctx context.Context, ingest analytics.Ingest) (analytics.Registration, error) {
	registration, ok, err := readAnalyticsRegistration(d.dataRoot)
	if err != nil || ok {
		return registration, err
	}
	if registration, err = ingest.Register(ctx); err != nil {
		return registration, err
	}
	return registration, writeAnalyticsRegistration(d.dataRoot, registration)
}

func readAnalyticsRegistration(dataRoot string) (analytics.Registration, bool, error) {
	var registration analytics.Registration
	path := filepath.Join(dataRoot, analyticsTokenFile)
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return registration, false, nil
	}
	if err != nil {
		return registration, false, err
	}
	if err := json.Unmarshal(data, &registration); err != nil || registration.Token == "" {
		return registration, false, fmt.Errorf("%s holds no install token; Forget this install on Settings › Analytics to start a new one", path)
	}
	return registration, true, nil
}

func writeAnalyticsRegistration(dataRoot string, registration analytics.Registration) error {
	encoded, err := json.Marshal(registration)
	if err != nil {
		return err
	}
	draft, err := os.CreateTemp(dataRoot, analyticsTokenFile+".*")
	if err != nil {
		return fmt.Errorf("create analytics token draft: %w", err)
	}
	defer os.Remove(draft.Name())
	if _, err := draft.Write(encoded); err != nil {
		draft.Close()
		return fmt.Errorf("write analytics token draft: %w", err)
	}
	if err := draft.Close(); err != nil {
		return fmt.Errorf("write analytics token draft: %w", err)
	}
	return os.Rename(draft.Name(), filepath.Join(dataRoot, analyticsTokenFile))
}

func (d *Daemon) handleForgetAnalyticsInstall(client *wsClient, msg *protocol.ForgetAnalyticsInstallMessage) {
	result := protocol.ForgetAnalyticsInstallResultMessage{Event: protocol.EventForgetAnalyticsInstallResult, RequestID: msg.RequestID}
	if err := d.forgetAnalyticsInstall(context.Background()); err != nil {
		result.Error = protocol.Ptr(err.Error())
	} else {
		result.Success = true
	}
	d.sendToClient(client, result)
}

func (d *Daemon) forgetAnalyticsInstall(ctx context.Context) error {
	d.analyticsUpload.run.Lock()
	defer d.analyticsUpload.run.Unlock()
	registration, registered, err := readAnalyticsRegistration(d.dataRoot)
	if err != nil {
		return err
	}
	if registered {
		err := analytics.NewIngest(config.AnalyticsIngestURL()).Delete(ctx, registration.Token)
		if err != nil && !errors.Is(err, analytics.ErrUnknownInstall) {
			return fmt.Errorf("%w. Nothing changed on this machine", err)
		}
	}
	for _, name := range []string{analyticsTokenFile, analyticsSecretFile} {
		if err := os.Remove(filepath.Join(d.dataRoot, name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("the server copy is deleted, but starting a new install failed: %w", err)
		}
	}
	if err := d.stopAnalyticsMirror(); err != nil {
		return fmt.Errorf("the server copy is deleted, but starting a new install failed: %w", err)
	}
	d.configureAnalytics()
	d.analyticsUpload.settle(nil, time.Now())
	return nil
}
