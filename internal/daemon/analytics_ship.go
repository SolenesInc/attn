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
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/setting"
)

const (
	analyticsTokenFile = "analytics-token"

	analyticsShipKind     = "analytics_ship"
	analyticsShipInterval = 5 * time.Minute
	analyticsShipTimeout  = 3 * time.Minute

	analyticsShipBatchRecords    = 5000
	analyticsForwardKind         = "analytics_forward"
	analyticsForwardTimeout      = time.Minute
	analyticsForwardBatchRecords = 1000

	analyticsUnsentRetention = 30 * 24 * time.Hour
)

type analyticsUpload struct {
	run sync.Mutex

	mu      sync.Mutex
	refusal string
	retryAt time.Time
	cancel  context.CancelFunc
}

func (u *analyticsUpload) begin(ctx context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(ctx)
	u.mu.Lock()
	u.cancel = cancel
	u.mu.Unlock()
	return ctx, func() {
		u.mu.Lock()
		u.cancel = nil
		u.mu.Unlock()
		cancel()
	}
}

func (u *analyticsUpload) withdraw() {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.cancel != nil {
		u.cancel()
	}
	u.refusal, u.retryAt = "", time.Time{}
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
	return d.analyticsEnabled() && d.on(setting.AnalyticsSharingEnabled)
}

func (d *Daemon) queueAnalyticsShip() {
	queue := d.jobQueueRef()
	if queue == nil || !d.analyticsSharing() || !d.enrollmentStatus().IsHome() {
		return
	}
	if _, err := queue.Enqueue(analyticsShipKind, jobs.EnqueueOptions{UniqueKey: jobs.CronKey, RunNow: true}); err != nil {
		d.logf("analytics: queue an upload: %v", err)
	}
}

func (d *Daemon) analyticsShipHandler(ctx context.Context, _ *jobs.Job) (any, error) {
	d.analyticsUpload.run.Lock()
	defer d.analyticsUpload.run.Unlock()
	ctx, end := d.analyticsUpload.begin(ctx)
	defer end()
	if !d.analyticsSharing() || d.analyticsUpload.waiting(time.Now()) {
		return nil, nil
	}
	sent, err := d.shipAnalyticsBatch(ctx)
	if !d.analyticsSharing() {
		return nil, nil
	}
	d.analyticsUpload.settle(err, time.Now())
	if err != nil {
		return nil, fmt.Errorf("uploading analytics: %w", err)
	}
	return map[string]any{"sent": sent}, nil
}

func (d *Daemon) queueAnalyticsForward() {
	queue := d.jobQueueRef()
	if queue == nil || d.enrollmentStatus().IsHome() || !d.analyticsEnabled() {
		return
	}
	if _, err := queue.Enqueue(analyticsForwardKind, jobs.EnqueueOptions{UniqueKey: jobs.CronKey, RunNow: true}); err != nil {
		d.logf("analytics: queue a forward: %v", err)
	}
}

func (d *Daemon) analyticsForwardHandler(ctx context.Context, _ *jobs.Job) (any, error) {
	forwarded := 0
	for d.analyticsEnabled() && d.homeBridge() != nil {
		sent, err := d.forwardAnalyticsBatch(ctx)
		if err != nil {
			d.logf("analytics: forward to the home: %v; the records wait for the next tick", err)
			break
		}
		if sent == 0 {
			break
		}
		forwarded += sent
	}
	pausepoint.At(pausepoint.AnalyticsForwarded)
	return map[string]any{"forwarded": forwarded}, nil
}

func (d *Daemon) forwardAnalyticsBatch(ctx context.Context) (int, error) {
	unsent, err := d.store.UnsentAnalyticsRecords(analyticsForwardBatchRecords, d.analyticsBuild(), d.analyticsCopySource())
	if err != nil || len(unsent.Rows) == 0 {
		return 0, err
	}
	pausepoint.At(pausepoint.AnalyticsForwardRead)
	secret, err := d.analyticsSecret()
	if err != nil {
		return 0, err
	}
	machine := analytics.HashSubject(secret, d.enrollmentStatus().DaemonID)
	records := make([]analytics.Record, len(unsent.Rows))
	forwarded := make([]string, len(unsent.Rows))
	for i, row := range unsent.Rows {
		if err := json.Unmarshal([]byte(row.Record), &records[i]); err != nil {
			return 0, fmt.Errorf("outbox row %d: %w", row.ID, err)
		}
		onMachine := records[i]
		onMachine.Stream = records[i].Stream.OnMachine(machine)
		encoded, err := json.Marshal(onMachine)
		if err != nil {
			return 0, fmt.Errorf("outbox row %d: %w", row.ID, err)
		}
		forwarded[i] = string(encoded)
	}
	if _, err := Call[struct{}](ctx, d.router(), protocol.ForwardAnalyticsMessage{Cmd: protocol.CmdForwardAnalytics, Build: unsent.Build, Records: forwarded, Generation: unsent.Generation}); err != nil {
		return 0, err
	}
	if _, err := d.store.MarkAnalyticsSent(records, time.Now()); err != nil {
		return 0, fmt.Errorf("the home took %d records, but marking them sent failed, so they will be forwarded again and stored once: %w", len(records), err)
	}
	return len(records), nil
}

func (d *Daemon) receiveForwardedAnalytics(_ context.Context, msg protocol.ForwardAnalyticsMessage) (struct{}, error) {
	if !d.analyticsEnabled() {
		return struct{}{}, errors.New("usage analytics is off at this home")
	}
	pausepoint.At(pausepoint.AnalyticsForwardImporting)
	return struct{}{}, d.store.ImportAnalyticsRecords(msg.Build, msg.Records, msg.Generation)
}

func (d *Daemon) shipAnalyticsBatch(ctx context.Context) (int, error) {
	unsent, err := d.store.UnsentAnalyticsRecords(analyticsShipBatchRecords, d.analyticsBuild(), "")
	if err != nil || len(unsent.Rows) == 0 {
		return 0, err
	}
	records := make([]analytics.Record, len(unsent.Rows))
	for i, row := range unsent.Rows {
		if err := json.Unmarshal([]byte(row.Record), &records[i]); err != nil {
			return 0, fmt.Errorf("outbox row %d: %w", row.ID, err)
		}
	}
	ingest := analytics.NewIngest(config.AnalyticsIngestURL())
	registration, err := d.analyticsRegistration(ctx, ingest)
	if err != nil {
		return 0, err
	}
	if _, err := ingest.Send(ctx, registration.Token, analytics.Batch{Build: unsent.Build, Records: records}); err != nil {
		if errors.Is(err, analytics.ErrUnknownInstall) {
			return 0, fmt.Errorf("%w; Forget this install on Settings › Analytics to start a new one", err)
		}
		return 0, err
	}
	vents, err := d.store.MarkAnalyticsSent(records, time.Now())
	if err != nil {
		return 0, fmt.Errorf("attn-ingest accepted %d records, but marking them sent failed, so they will be sent again and counted as duplicates: %w", len(records), err)
	}
	d.announceVentSends(vents)
	return len(records), nil
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
	if _, err := d.analyticsSecret(); err != nil {
		return fmt.Errorf("the server copy is deleted, but starting a new install failed: %w", err)
	}
	if err := d.store.DeleteUnsentAnalyticsRecords(); err != nil {
		return fmt.Errorf("the server copy is deleted, but starting a new install failed: %w", err)
	}
	d.configureAnalytics()
	d.analyticsUpload.settle(nil, time.Now())
	return nil
}
