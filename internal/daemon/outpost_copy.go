package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/victorarias/attn/internal/bridge"
	"github.com/victorarias/attn/internal/enrollment"
	"github.com/victorarias/attn/internal/jobs"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
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
	if err := conn.Call(ctx, bridgeCallCopy, batch.Rows, nil); err != nil {
		return false, err
	}
	return true, d.store.AckOutbox(batch.IDs)
}

func (d *Daemon) outpostHandlers() bridge.Handlers {
	return bridge.Handlers{
		bridgeCall: d.callBridgeHandler(""),
		bridgeCallRecovery: func(conn *bridge.Conn, incoming ssh.NewChannel) {
			if !d.life.Go("reportOutpostMachine", func() {
				bridge.Answer(incoming, func(_ context.Context, _ []byte) (any, error) {
					select {
					case <-d.recoverySettledSignal():
					case <-d.life.Done():
						return nil, errDaemonStopping
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
					return d.applyCopyCall(home.Context(), home.Peer().DaemonID, request)
				})
			}) {
				_ = incoming.Reject(ssh.ResourceShortage, "daemon is stopping")
			}
		},
	}
}

func (d *Daemon) applyCopyCall(ctx context.Context, home string, request []byte) (any, error) {
	var rows []protocol.CopyRow
	if err := json.Unmarshal(request, &rows); err != nil {
		return nil, fmt.Errorf("decode copy batch: %w", err)
	}
	if err := d.store.ApplyCopy(ctx, home, rows); err != nil {
		return nil, err
	}
	d.pruneRuntimesWithoutSession(d.life.Context())
	pausepoint.At(pausepoint.CopyBatchApplied)
	return nil, nil
}

// dropStaleCopy keeps only the copy from the home this daemon is enrolled to, and closes a bridge from any
// other home.
func (d *Daemon) dropStaleCopy() error {
	d.outpost.mu.Lock()
	status, err := enrollment.Load(d.dataRoot)
	if err == nil {
		d.outpost.enrollment = status
	}
	d.outpost.mu.Unlock()
	if err != nil {
		return err
	}
	home := status.HomeDaemonID
	if status.IsHome() {
		home = ""
	}
	if conn := d.homeBridge(); conn != nil && conn.Peer().DaemonID != home {
		_ = conn.Close()
	}
	if err := d.store.DropCopyNotFrom(home); err != nil {
		return err
	}
	d.pruneRuntimesWithoutSession(d.life.Context())
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
