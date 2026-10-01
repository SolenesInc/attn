package daemon

import (
	"github.com/victorarias/attn/internal/codexshared"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/store"
	"net/http"
	"nhooyr.io/websocket"
	"os"
	"path/filepath"
	"time"
)

func (r *codexRuntime) addView(v store.CodexView) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.addViewLocked(v)
}

func (r *codexRuntime) addViewLocked(v store.CodexView) error {
	if _, ok := r.servers[v.RuntimeID]; ok {
		previous := r.views[v.RuntimeID]
		if previous.Generation != v.Generation {
			v.Revision = previous.Revision + 1
			if err := r.d.store.SaveCodexView(v); err != nil {
				return err
			}
			r.views[v.RuntimeID] = v
			r.projectViewLocked(v)
		}
		return nil
	}
	if err := r.d.store.SaveCodexView(v); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(r.socket(v.RuntimeID)), 0700); err != nil {
		return err
	}
	listener, err := listenUnixAtomically(r.socket(v.RuntimeID))
	if err != nil {
		return err
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		release, admitted := r.d.life.Hold("codexViewConnection")
		if !admitted {
			http.Error(w, "daemon is stopping", http.StatusServiceUnavailable)
			return
		}
		defer release()
		owner, err := r.d.store.CodexOwner(v.LaunchOwnerID)
		if err != nil || owner == nil {
			http.Error(w, "Codex launch owner unavailable", http.StatusServiceUnavailable)
			return
		}
		launch, err := r.ownerContext(owner)
		if err == nil {
			r.mu.Lock()
			err = r.ensureServer(r.d.life.Context(), launch)
			r.mu.Unlock()
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		r.d.logf("Codex view connected: %s", v.RuntimeID)
		down, err := websocket.Accept(w, req, nil)
		if err != nil {
			return
		}
		down.SetReadLimit(-1)
		up, err := codexshared.Dial(r.d.life.Context(), r.socket(""))
		if err != nil {
			down.Close(websocket.StatusInternalError, err.Error())
			return
		}
		codexshared.Proxy(r.d.life.Context(), down, up, func(m *codexshared.Message) (func(codexshared.Message), error) { return r.prepareRPC(v.RuntimeID, m) }, r.observeNative)
	})}
	r.views[v.RuntimeID] = v
	r.servers[v.RuntimeID] = server
	r.d.life.Go("codexViewProxy", func() { _ = server.Serve(listener) })
	return nil
}

func (r *codexRuntime) observeTitle(runtimeID string, obs pty.Observation) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	v, ok := r.views[runtimeID]
	if !ok {
		return false
	}
	if v.Resolution == "disconnected" || (obs.Generation != "" && obs.Generation != v.Generation) {
		return true
	}
	if previous, err := time.Parse(time.RFC3339Nano, v.ObservedAt); err == nil && obs.At.Before(previous) {
		return true
	}
	v.ObservedAt = obs.At.UTC().Format(time.RFC3339Nano)
	r.views[runtimeID] = v
	r.resolveTitleLocked(runtimeID, obs.Detail)
	return true
}

func (r *codexRuntime) resolveTitleLocked(runtimeID, title string) {
	v := r.views[runtimeID]
	owners, err := r.d.store.CodexOwners(r.serverID)
	if err != nil {
		r.d.logf("Codex resolve title: %v", err)
		return
	}
	ids := make([]string, 0, len(owners))
	byRoot := make(map[string]string)
	for _, owner := range owners {
		if owner.NativeRootID != "" && !owner.Archived {
			ids = append(ids, owner.NativeRootID)
			byRoot[owner.NativeRootID] = owner.SessionID
		}
	}
	root := codexshared.ResolveTitle(title, ids)
	previous := v
	v.RawTitle = title
	v.Resolution = "unresolved"
	v.SessionID = ""
	if root != "" {
		v.SessionID = byRoot[root]
		v.Resolution = "resolved"
	}
	changed := previous.SessionID != v.SessionID || previous.Resolution != v.Resolution
	if changed {
		v.Revision++
	}
	if err := r.d.store.SaveCodexView(v); err != nil {
		r.d.logf("Codex view %s: %v", runtimeID, err)
		return
	}
	r.views[runtimeID] = v
	if changed {
		r.projectViewLocked(v)
	}
}

func (r *codexRuntime) projectViewLocked(v store.CodexView) {
	for _, workspaceID := range r.d.store.WorkspaceLayoutIDs() {
		snapshot := r.d.store.GetWorkspaceLayout(workspaceID)
		if snapshot == nil {
			continue
		}
		changed := false
		for i, pane := range snapshot.Panes {
			if pane.RuntimeID != v.RuntimeID {
				continue
			}
			snapshot.Panes[i].SessionID = v.SessionID
			snapshot.Panes[i].CodexResolution = v.Resolution
			snapshot.Panes[i].CodexRevision = v.Revision
			changed = true
		}
		if changed {
			if err := r.d.store.SaveWorkspaceLayout(*snapshot); err != nil {
				r.d.logf("Codex layout view %s: %v", v.RuntimeID, err)
				continue
			}
			r.d.broadcastWorkspaceLayoutUpdated(workspaceID)
		}
	}
}

func (r *codexRuntime) loadViews() error {
	views, err := r.d.store.CodexViews()
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, v := range views {
		r.views[v.RuntimeID] = v
	}
	return nil
}

func (r *codexRuntime) disconnectView(id string, generation ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	v, ok := r.views[id]
	if !ok || (len(generation) > 0 && generation[0] != "" && generation[0] != v.Generation) {
		return
	}
	v.SessionID = ""
	v.Resolution = "disconnected"
	v.Revision++
	if err := r.d.store.SaveCodexView(v); err != nil {
		r.d.logf("disconnect Codex view: %v", err)
		return
	}
	r.views[id] = v
	r.projectViewLocked(v)
}

func (r *codexRuntime) reconcileTitles() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for runtimeID, v := range r.views {
		if v.RawTitle != "" && v.Resolution != "disconnected" {
			r.resolveTitleLocked(runtimeID, v.RawTitle)
		}
	}
}
