package daemon

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/victorarias/attn/internal/bridge"
	"github.com/victorarias/attn/internal/enrollment"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"golang.org/x/crypto/ssh"
)

const bridgeCallCopy = "copy"

func (d *Daemon) sendOutpostCopy(ctx context.Context, host string, conn *bridge.Conn) {
	for {
		changed := d.store.OutboxChanged()
		batch, err := d.store.OutboxBatch(host, bridge.MaxCallRequest)
		if err == nil && len(batch.IDs) > 0 {
			err = conn.Call(ctx, bridgeCallCopy, batch.Rows, nil)
			if err == nil {
				err = d.store.AckOutbox(batch.IDs)
			}
			if err == nil {
				continue
			}
		}
		if err != nil && ctx.Err() == nil {
			d.logf("outpost %s copy: %v", host, err)
		}
		select {
		case <-changed:
		case <-conn.Done():
			return
		case <-ctx.Done():
			return
		}
	}
}

func (d *Daemon) outpostHandlers() bridge.Handlers {
	return bridge.Handlers{
		bridgeCallCopy: func(home *bridge.Conn, incoming ssh.NewChannel) {
			d.life.Go("applyCopy", func() {
				bridge.Answer(incoming, func(request []byte) (any, error) { return d.applyCopyCall(home.Peer().DaemonID, request) })
			})
		},
	}
}

func (d *Daemon) applyCopyCall(home string, request []byte) (any, error) {
	var rows []protocol.CopyRow
	if err := json.Unmarshal(request, &rows); err != nil {
		return nil, fmt.Errorf("decode copy batch: %w", err)
	}
	if err := d.store.ApplyCopy(home, rows); err != nil {
		return nil, err
	}
	pausepoint.At(pausepoint.CopyBatchApplied)
	return nil, nil
}

// dropStaleCopy keeps only the copy from the home this daemon is enrolled to, and closes a bridge from any
// other home.
func (d *Daemon) dropStaleCopy() error {
	status, err := enrollment.Load(d.dataRoot)
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
	return d.store.DropCopyNotFrom(home)
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
