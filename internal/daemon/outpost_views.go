package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/victorarias/attn/internal/bridge"
	attngit "github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/jobs"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessionmonitor"
	"golang.org/x/crypto/ssh"
)

const bridgeCallView = "monitor_view"

func (d *Daemon) copiedSession(id protocol.SessionID) bool {
	if d.daemonSetting(settingOutpostListen) == "" {
		return false
	}
	_, origin := d.store.SessionHost(id)
	return origin != ""
}

func (d *Daemon) homeHandlers(host string) bridge.Handlers {
	return bridge.Handlers{bridgeCallView: func(conn *bridge.Conn, incoming ssh.NewChannel) {
		if !d.life.Go("applyOutpostView", func() {
			bridge.Answer(incoming, func(_ context.Context, raw []byte) (any, error) {
				var msg protocol.ApplyMonitorViewMessage
				if err := json.Unmarshal(raw, &msg); err != nil {
					return nil, err
				}
				if !d.isOutpostConn(host, conn) {
					return nil, fmt.Errorf("outpost bridge was replaced")
				}
				if d.sessionHost(msg.SessionID) != host {
					return nil, nil
				}
				var view sessionmonitor.View
				if err := json.Unmarshal([]byte(msg.View), &view); err != nil {
					return nil, err
				}
				if view.Epoch.V != msg.Epoch {
					return nil, fmt.Errorf("monitor epoch differs from its view")
				}
				if !d.applyHomeView(msg.SessionID, view, host, conn) {
					return nil, fmt.Errorf("monitor view was not applied")
				}
				return nil, nil
			})
		}) {
			_ = incoming.Reject(ssh.ResourceShortage, "daemon is stopping")
		}
	}}
}

func (d *Daemon) sendOutpostViews(ctx context.Context, conn *bridge.Conn) {
	acknowledged := map[protocol.SessionID]sessionmonitor.View{}
	var retry time.Duration
	for {
		var failure error
		r := d.monitors()
		changed := r.viewChanges()
		live := d.liveTerminals(ctx)
		for id, monitor := range r.each() {
			if !d.copiedSession(id) {
				continue
			}
			view := monitor.Snapshot()
			if _, running := live[view.Key.V.Terminal]; !running && view.Exit.V == nil {
				continue
			}
			previous := acknowledged[id]
			if previous.Epoch.V == view.Epoch.V && previous.Revision.V >= view.Revision.V {
				continue
			}
			raw, err := json.Marshal(view)
			if err == nil {
				err = conn.Call(ctx, bridgeCallView, protocol.ApplyMonitorViewMessage{Cmd: "apply_monitor_view", SessionID: id, Epoch: view.Epoch.V, View: string(raw)}, nil)
			}
			if err != nil {
				failure = err
				break
			}
			acknowledged[id] = view
			if exit := view.Exit.V; exit != nil && !exit.Held && (previous.Epoch.V != view.Epoch.V || !sessionmonitor.SameExit(exit, previous.Exit.V)) {
				monitor.RecordExitPublished(*exit)
			}
		}
		var again <-chan time.Time
		if failure != nil {
			if retry == 0 {
				retry = jobs.DefaultBackoffBase
			} else {
				retry = min(retry*2, jobs.DefaultBackoffCap)
			}
			d.logf("outpost views: %v; sending again in %s", failure, retry)
			again = time.After(retry)
		} else {
			retry = 0
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

func (d *Daemon) storedMonitorView(id protocol.SessionID) sessionmonitor.View {
	launch := d.monitorLaunch(id)
	return sessionmonitor.View{
		State:              sessionmonitor.Value[sessionmonitor.StateView]{V: sessionmonitor.StateView{State: launch.State, Since: launch.StateSince, Updated: launch.StateUpdated}},
		Turn:               sessionmonitor.Value[sessionmonitor.TurnStamps]{V: launch.Turn},
		Branch:             sessionmonitor.Value[attngit.BranchInfo]{V: launch.Branch},
		LastSeen:           sessionmonitor.Value[time.Time]{V: launch.LastSeen},
		LastModelRequestAt: sessionmonitor.Value[time.Time]{V: launch.LastModelRequestAt},
		HarnessTitle:       sessionmonitor.Value[string]{V: launch.HarnessTitle},
		Exit:               sessionmonitor.Value[*sessionmonitor.Exit]{V: launch.Exit},
	}
}

func (d *Daemon) applyHomeView(id protocol.SessionID, view sessionmonitor.View, host string, conn *bridge.Conn) bool {
	r := d.monitors()
	r.applyMu.Lock()
	if d.store.Get(id) == nil {
		r.applyMu.Unlock()
		return true
	}
	r.mu.Lock()
	if r.sessions[id] == nil {
		r.sessions[id] = &monitorBinding{applied: d.storedMonitorView(id)}
	}
	r.mu.Unlock()
	r.applyMu.Unlock()
	h := &monitorHome{daemon: d, bridge: conn, host: host}
	h.session.Store(id)
	return h.apply(view, true)
}

func (d *Daemon) recoverSessionState(id protocol.SessionID) {
	if d.sessionHost(id) == "" {
		d.applyState(id, sessionmonitor.Change{State: string(protocol.SessionStateRecoverable), Cause: sessionmonitor.StartupRecovery{}})
		return
	}
	view, ok := d.monitorViewFor(id)
	if !ok {
		view = d.storedMonitorView(id)
	}
	now := time.Now()
	view.State.V = sessionmonitor.StateView{State: protocol.SessionStateRecoverable, Since: now, Updated: now, Sequence: view.State.V.Sequence + 1, Broadcast: true, Cause: "startup_recovery"}
	view.Live.V = false
	view.Epoch.V = "home:" + d.daemonInstanceID
	view.Revision.V++
	d.applyHomeView(id, view, "", nil)
}
