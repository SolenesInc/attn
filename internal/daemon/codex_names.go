package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

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
func (r *codexRuntime) applyInitialName(ctx context.Context, id string) error {
	owner, err := r.d.store.CodexOwner(id)
	if err != nil || owner == nil {
		return err
	}
	launch, err := r.ownerContext(owner)
	if err != nil || launch.InitialName == "" {
		return err
	}
	lock := r.d.sessionLifecycleLockFor(id)
	lock.Lock()
	defer lock.Unlock()
	r.mu.Lock()
	owner, launch, control, err := r.nameOwnerLocked(ctx, id)
	r.mu.Unlock()
	if err != nil || launch.InitialName == "" {
		return err
	}
	if owner.NativeRootID == "" {
		return fmt.Errorf("codex owner %s has not initialized", id)
	}
	revision := r.nameRevision(owner.NativeRootID)
	if _, err := control.Call(ctx, "thread/name/set", map[string]any{"threadId": owner.NativeRootID, "name": launch.InitialName}); err != nil {
		return fmt.Errorf("set initial Codex name for %s: %w", id, err)
	}
	if err := r.d.store.ConsumeCodexInitialName(id, owner.NativeRootID); err != nil {
		return err
	}
	r.projectNativeName(owner.NativeRootID, &launch.InitialName, revision, false)
	return nil
}

func (r *codexRuntime) nameOwnerLocked(ctx context.Context, id string) (*store.CodexOwner, codexLaunchContext, *codexshared.Client, error) {
	owner, err := r.d.store.CodexOwner(id)
	if err != nil {
		return nil, codexLaunchContext{}, nil, err
	}
	if owner == nil || owner.Archived || r.d.store.Get(id) == nil {
		return nil, codexLaunchContext{}, nil, fmt.Errorf("codex owner %s is closed", id)
	}
	launch, err := r.ownerContext(owner)
	if err != nil {
		return nil, launch, nil, err
	}
	if err := r.ensureServer(ctx, launch); err != nil {
		return nil, launch, nil, err
	}
	// Recovery can update owner context while establishing the control connection.
	owner, err = r.d.store.CodexOwner(id)
	if err != nil {
		return nil, launch, nil, err
	}
	if owner == nil || owner.Archived || r.d.store.Get(id) == nil {
		return nil, launch, nil, fmt.Errorf("codex owner %s is closed", id)
	}
	launch, err = r.ownerContext(owner)
	if err != nil {
		return nil, launch, nil, err
	}
	return owner, launch, r.control, nil
}

func (r *codexRuntime) notifyInitialNameFailure(id string, nameErr error) {
	r.d.logf("Codex initial name for %s: %v", id, nameErr)
	if r.d.store.Get(id) == nil {
		return
	}
	record, err := r.d.store.AddNotification(store.NotificationRecord{
		Kind:       "codex_initial_name_failed",
		Severity:   store.NotificationWarning,
		Title:      "Could not set the Codex agent name",
		Body:       "The conversation was created. Rename the agent to correct its name before sending work.",
		Detail:     nameErr.Error(),
		SourceKind: "session",
		SourceID:   id,
		Actions:    []store.NotificationAction{{Kind: notificationActionOpenSession, Label: "Open agent", TargetID: id}},
	}, time.Now())
	if err != nil {
		r.d.logf("notifications: initial Codex name for %s: %v", id, err)
		return
	}
	r.d.publishFact(FactNotificationCreated, record.ID, nil)
}

func (r *codexRuntime) rename(ctx context.Context, id, name string) error {
	lock := r.d.sessionLifecycleLockFor(id)
	lock.Lock()
	defer lock.Unlock()
	r.mu.Lock()
	owner, launch, control, err := r.nameOwnerLocked(ctx, id)
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
	r.mu.Unlock()
	revision := r.nameRevision(owner.NativeRootID)
	if _, err := control.Call(ctx, "thread/name/set", map[string]any{"threadId": owner.NativeRootID, "name": name}); err != nil {
		return err
	}
	if err := r.d.store.ConsumeCodexInitialName(id, owner.NativeRootID); err != nil {
		return err
	}
	r.projectNativeName(owner.NativeRootID, &name, revision, false)
	return nil
}
