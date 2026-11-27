package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/victorarias/attn/internal/harness"
	"slices"
	"time"

	"github.com/victorarias/attn/internal/bridge"
	"github.com/victorarias/attn/internal/enrollment"
	"github.com/victorarias/attn/internal/jobs"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
	"github.com/victorarias/attn/internal/setting"
	"golang.org/x/crypto/ssh"
)

const bridgeCallCopy = "copy"

func (d *Daemon) sendOutpostCopy(ctx context.Context, host string, conn *bridge.Conn) {
	var retry time.Duration
	for {
		changed := d.store.OutboxChanged()
		sent, err := d.sendOutpostCopyBatch(ctx, host, conn)
		if sent && err == nil {
			retry = 0
			continue
		}
		var again <-chan time.Time
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			if retry == 0 {
				retry = jobs.DefaultBackoffBase
			} else {
				retry = min(retry*2, jobs.DefaultBackoffCap)
			}
			d.logf("outpost %s copy: %v; sending again in %s", host, err, retry)
			again = time.After(retry)
		}
		select {
		case <-changed:
		case <-again:
		case <-conn.Done():
			return
		case <-ctx.Done():
			return
		}
	}
}

func (d *Daemon) sendOutpostCopyBatch(ctx context.Context, host string, conn *bridge.Conn) (bool, error) {
	batch, err := d.store.OutboxBatch(host, bridge.MaxCallRequest)
	if err != nil || len(batch.IDs) == 0 {
		return false, err
	}
	if carriesSession(batch.Rows) {
		pausepoint.At(pausepoint.OutboxSessionRead)
	}
	if err := conn.Call(ctx, bridgeCallCopy, batch.Rows, nil); err != nil {
		return false, err
	}
	return true, d.store.AckOutbox(batch.IDs)
}

func carriesSession(rows []protocol.CopyRow) bool {
	return slices.ContainsFunc(rows, func(row protocol.CopyRow) bool { return row.Table == protocol.CopyTableSessions })
}

func (d *Daemon) outpostHandlers() bridge.Handlers {
	order := &terminalOrder{}
	return bridge.Handlers{
		notebookChannel: func(conn *bridge.Conn, incoming ssh.NewChannel) {
			if !d.life.Go("notebookStream", func() {
				d.configureNotebookSync()
				d.startNotebookSyncReports(conn)
				d.serveNotebookSync(conn, incoming)
			}) {
				_ = incoming.Reject(ssh.ResourceShortage, "daemon is stopping")
			}
		},
		bridgeCall:          d.callBridgeHandler("", order),
		bridge.InputChannel: d.inputBridgeHandler(order),
		bridgeCallRecovery: func(conn *bridge.Conn, incoming ssh.NewChannel) {
			if !d.life.Go("reportOutpostMachine", func() {
				bridge.Answer(incoming, func(_ context.Context, _ []byte) (any, error) {
					if err := d.awaitRecovered(); err != nil {
						return nil, err
					}
					return d.outpostMachineReport(), nil
				})
			}) {
				_ = incoming.Reject(ssh.ResourceShortage, "daemon is stopping")
			}
		},
		bridge.AttachChannel: func(conn *bridge.Conn, incoming ssh.NewChannel) {
			if !d.life.Go("bridgeAttach", func() {
				if err := conn.ServeAttach(d.life.Context(), incoming, func(req bridge.AttachRequest) (ptybackend.AttachInfo, ptybackend.Stream, error) {
					if err := d.awaitRecovered(); err != nil {
						return ptybackend.AttachInfo{}, nil, err
					}
					info, stream, err := d.ptyBackend.Attach(d.life.Context(), req.Terminal, req.Subscriber, req.Options)
					if err != nil {
						return info, nil, err
					}
					return info, d.watchTerminalStream(req.Terminal, stream), nil
				}); err != nil {
					d.logf("outpost attach: %v", err)
				}
			}) {
				_ = incoming.Reject(ssh.ResourceShortage, "daemon is stopping")
			}
		},
		bridgeCallCopy: func(home *bridge.Conn, incoming ssh.NewChannel) {
			if !d.life.Go("applyCopy", func() {
				bridge.Answer(incoming, func(_ context.Context, request []byte) (any, error) {
					return d.applyCopyCall(home.Context(), home.Peer().HomeID, request)
				})
			}) {
				_ = incoming.Reject(ssh.ResourceShortage, "daemon is stopping")
			}
		},
	}
}

func (d *Daemon) awaitRecovered() error {
	select {
	case <-d.recoverySettledSignal():
		return nil
	case <-d.life.Done():
		return errDaemonStopping
	}
}

func (d *Daemon) applyCopyCall(ctx context.Context, home string, request []byte) (any, error) {
	var rows []protocol.CopyRow
	if err := json.Unmarshal(request, &rows); err != nil {
		return nil, fmt.Errorf("decode copy batch: %w", err)
	}
	if err := d.applyOutpostCopy(ctx, home, rows); err != nil {
		return nil, err
	}
	d.pruneRuntimesWithoutSession(d.life.Context())
	if carriesSession(rows) {
		pausepoint.At(pausepoint.OutboxSessionApplied)
	}
	pausepoint.At(pausepoint.CopyBatchApplied)
	return nil, nil
}

func (d *Daemon) applyOutpostCopy(ctx context.Context, home string, rows []protocol.CopyRow) error {
	return d.applyOutpostCopyForLaunch(ctx, home, rows, "")
}

func (d *Daemon) applyOutpostCopyForLaunch(ctx context.Context, home string, rows []protocol.CopyRow, launching protocol.SessionID) error {
	released := make(map[string]string)
	for _, row := range rows {
		if row.Table == protocol.CopyTableSessions && row.Columns == nil {
			if session := d.store.Get(protocol.SessionID(row.Key)); session != nil && d.lifetime(session.ID) == harness.InServer {
				released[session.ProfileID] = session.Agent
			}
		}
	}
	var before map[string]string
	for _, row := range rows {
		if row.Table == protocol.CopyTableSettings {
			before = d.settingValues()
			break
		}
	}
	if err := d.store.ApplyCopy(ctx, home, rows); err != nil {
		return err
	}
	if before != nil {
		d.refreshChangedSettings(before)
	}
	if copiesServingApps(rows) && d.on(setting.RemoteHomeEnabled) {
		d.wakeHomeWaiters()
	}
	d.replayPendingConversations()
	d.bindMonitorTerminalsSkipping(launching)
	for profile, agent := range released {
		d.life.Go("linkReleasedAfterCopy", func() { d.released(agent, profile) })
	}
	return nil
}

func copiesServingApps(rows []protocol.CopyRow) bool {
	return slices.ContainsFunc(rows, func(row protocol.CopyRow) bool {
		return row.Table == protocol.CopyTableSettings && row.Key == setting.RemoteHomeEnabled.Key
	})
}

// dropStaleCopy keeps only the copy from the home this daemon is enrolled to, and closes a bridge from any
// other home.
func (d *Daemon) dropStaleCopy() error {
	released := make(map[string]string)
	for _, session := range d.store.List("") {
		if d.copiedSession(session.ID) && d.lifetime(session.ID) == harness.InServer {
			released[session.ProfileID] = session.Agent
		}
	}
	before := d.settingValues()
	d.outpost.mu.Lock()
	status, err := enrollment.Load(d.dataRoot)
	if err == nil {
		d.installEnrollmentLocked(status)
	}
	d.outpost.mu.Unlock()
	if err != nil {
		return err
	}
	if record, ok := d.readSharedBuild(); ok && record.HomeID != status.HomeID {
		d.forgetSharedBuild()
	}
	if status.Adopting() {
		if err := d.adoptHomeID(status); err != nil {
			return err
		}
	}
	d.configureHomeDial()
	home := status.CopiedFrom()
	if conn := d.homeBridge(); conn != nil && (home == "" || conn.Peer().HomeID != home) {
		_ = conn.Close()
	}
	if err := d.store.DropCopyNotFrom(home); err != nil {
		return err
	}
	d.refreshChangedSettings(before)
	d.configurePhoneAccess()
	d.refreshJobQueueRole()
	d.pruneRuntimesWithoutSession(d.life.Context())
	for profile, agent := range released {
		d.life.Go("linkReleasedAfterEnrollment", func() { d.released(agent, profile) })
	}
	if err := d.dropNotebookSyncFromOtherHome(d.notebookHome(status)); err != nil {
		return err
	}
	d.configureNotebookSync()
	return nil
}

func (d *Daemon) homeBridge() *bridge.Conn {
	r := &d.outpost
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.listener == nil || r.listener.conn == nil {
		return nil
	}
	select {
	case <-r.listener.conn.Done():
		return nil
	default:
		return r.listener.conn
	}
}
