package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/victorarias/attn/internal/codexshared"
	"github.com/victorarias/attn/internal/store"
)

type codexNameState struct {
	Name     *string
	Revision uint64
}

func (r *codexRuntime) nameRevision(root string) uint64 {
	r.nameMu.Lock()
	defer r.nameMu.Unlock()
	return r.names[root].Revision
}

func (r *codexRuntime) projectNativeName(root string, name *string, revision uint64, notification bool) {
	r.nameMu.Lock()
	defer r.nameMu.Unlock()
	owner, err := r.d.store.CodexOwnerByRoot(r.serverID, root)
	if err != nil {
		return
	}
	if owner != nil && owner.Archived {
		delete(r.names, root)
		return
	}
	state := r.names[root]
	if notification || state.Revision == revision {
		state.Name = name
		state.Revision++
		r.names[root] = state
	}
	if state.Name == nil || strings.TrimSpace(*state.Name) == "" {
		return
	}
	if owner == nil {
		return
	}
	session := r.d.store.Get(owner.SessionID)
	if session == nil || session.Label == *state.Name {
		return
	}
	r.d.store.UpdateSessionLabel(owner.SessionID, *state.Name)
	r.d.publishFact(FactSessionRenamed, owner.SessionID, nil)
}

func (r *codexRuntime) observeNativeName(m codexshared.Message) {
	if m.Method != "thread/name/updated" {
		return
	}
	var p struct {
		ThreadID   string  `json:"threadId"`
		ThreadName *string `json:"threadName"`
	}
	if json.Unmarshal(m.Params, &p) == nil && p.ThreadID != "" {
		r.projectNativeName(p.ThreadID, p.ThreadName, 0, true)
	}
}

// Pending launch names are consumed before either native or owner-addressed work.
func (r *codexRuntime) applyInitialNameLocked(ctx context.Context, owner *store.CodexOwner) error {
	launch, err := r.ownerContext(owner)
	if err != nil || launch.InitialName == "" {
		return err
	}
	if owner.NativeRootID == "" {
		return fmt.Errorf("codex owner %s has not initialized", owner.SessionID)
	}
	if err := r.ensureServer(ctx, launch); err != nil {
		return err
	}
	owner, err = r.d.store.CodexOwner(owner.SessionID)
	if err != nil {
		return err
	}
	if owner == nil {
		return fmt.Errorf("codex owner disappeared before naming")
	}
	launch, err = r.ownerContext(owner)
	if err != nil {
		return err
	}
	return r.applyInitialName(ctx, r.control, owner, launch)
}

func (r *codexRuntime) applyInitialName(ctx context.Context, control *codexshared.Client, owner *store.CodexOwner, launch codexLaunchContext) error {
	if launch.InitialName == "" {
		return nil
	}
	if _, err := control.Call(ctx, "thread/name/set", map[string]any{"threadId": owner.NativeRootID, "name": launch.InitialName}); err != nil {
		return fmt.Errorf("set initial Codex name for %s: %w", owner.SessionID, err)
	}
	launch.InitialName = ""
	raw, err := json.Marshal(launch)
	if err != nil {
		return err
	}
	return r.d.store.UpdateCodexContext(owner.SessionID, raw)
}

func (r *codexRuntime) rename(ctx context.Context, id, name string) error {
	r.mu.Lock()
	owner, err := r.d.store.CodexOwner(id)
	if err != nil || owner == nil {
		r.mu.Unlock()
		return fmt.Errorf("read Codex owner %s: %v", id, err)
	}
	launch, err := r.ownerContext(owner)
	if err != nil {
		r.mu.Unlock()
		return err
	}
	if owner.NativeRootID == "" {
		launch.InitialName = name
		raw, err := json.Marshal(launch)
		if err == nil {
			err = r.d.store.UpdateCodexContext(id, raw)
		}
		r.mu.Unlock()
		if err == nil {
			r.d.store.UpdateSessionLabel(id, name)
			r.d.publishFact(FactSessionRenamed, id, nil)
		}
		return err
	}
	defer r.mu.Unlock()
	if err := r.ensureServer(ctx, launch); err != nil {
		return err
	}
	revision := r.nameRevision(owner.NativeRootID)
	if _, err := r.control.Call(ctx, "thread/name/set", map[string]any{"threadId": owner.NativeRootID, "name": name}); err != nil {
		return err
	}
	if launch.InitialName != "" {
		launch.InitialName = ""
		raw, err := json.Marshal(launch)
		if err != nil {
			return err
		}
		if err := r.d.store.UpdateCodexContext(id, raw); err != nil {
			return err
		}
	}
	r.projectNativeName(owner.NativeRootID, &name, revision, false)
	return nil
}
