package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/appbuild"
	"github.com/victorarias/attn/internal/apps"
	"github.com/victorarias/attn/internal/bus"
	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/jobs"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/supervise"
)

// Design: docs/plans/2026-08-06-ext-a4-app-registry-and-runtime.md.

const (
	appInvocationStatusOK           = "ok"
	appInvocationStatusError        = "error"
	appInvocationStatusRuntimeError = "runtime_error"
)

// appRuntimeConnectWait bounds the wait for the sidecar to come up. Cold start
// (spawn, connect, hello, import, run) measured at 77ms; ten seconds is ~130×.
const appRuntimeConnectWait = 10 * time.Second

func (d *Daemon) appConnectWait() time.Duration {
	if d.appRuntimeWait > 0 {
		return d.appRuntimeWait
	}
	return appRuntimeConnectWait
}

// Fifteen minutes is roughly five rounds at the bus's two-minute retry cap.
const appAutoDisableStall = 15 * time.Minute

const (
	appAutoDisableStallEnv = "ATTN_APP_AUTO_DISABLE_STALL"
	appDispatchTimeoutEnv  = "ATTN_APP_DISPATCH_TIMEOUT"
	appPingTimeoutEnv      = "ATTN_APP_RUNTIME_PING_TIMEOUT"
)

const (
	appStallKindSubscription = "subscription"
	appStallKindReconcile    = "reconcile"

	appReconcileStateNotNeeded   = "not_needed"
	appReconcileStateUnsupported = "unsupported"
	appReconcileStateIdle        = "idle"
	appReconcileStateOwed        = "owed"
	appReconcileStateRunning     = "running"
)

func (d *Daemon) appAutoDisableWindow() time.Duration {
	if d.appAutoDisableWait > 0 {
		return d.appAutoDisableWait
	}
	return appAutoDisableStall
}

func (d *Daemon) resolveAppRuntimeTripwires() error {
	for _, setting := range []struct {
		name   string
		target *time.Duration
	}{
		{appAutoDisableStallEnv, &d.appAutoDisableWait},
		{appDispatchTimeoutEnv, &d.appDispatchWait},
		{appPingTimeoutEnv, &d.appPingWait},
	} {
		raw := strings.TrimSpace(os.Getenv(setting.name))
		if raw == "" {
			continue
		}
		value, err := time.ParseDuration(raw)
		if err != nil || value <= 0 {
			return fmt.Errorf("%s=%q is not a positive duration", setting.name, raw)
		}
		*setting.target = value
	}
	return nil
}

// Three: supervise parks the whole sidecar after DefaultGiveUpAfter (10) restarts, so the
// culprit has to go first; above one, because a single crash can be a machine event.
const appCrashStrikes = 3

const appCrashWindow = appAutoDisableStall

const notificationKindAppAutoDisabled = "app_auto_disabled"

type appStall struct {
	kind               string
	seq                int64
	eventName          string
	reconcileRequestID int64
	since              time.Time
	attempts           int
	lastError          string
}

type appDispatchPlan struct {
	app         string
	namespace   string
	versionID   int64
	artifact    string
	handler     string
	label       string
	collections []string
}

// A channel rather than a sync.Mutex so a waiter can carry a deadline.
type appLane chan struct{}

func (l appLane) Lock() { l <- struct{}{} }

func (l appLane) Unlock() { <-l }

func (l appLane) acquire(ctx context.Context) error {
	select {
	case l <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (d *Daemon) appLane(name string) appLane {
	d.appLaneMu.Lock()
	defer d.appLaneMu.Unlock()
	if d.appLanes == nil {
		d.appLanes = make(map[string]appLane)
	}
	if d.appLanes[name] == nil {
		d.appLanes[name] = make(appLane, 1)
	}
	return d.appLanes[name]
}

func (d *Daemon) registerAppConsumers() {
	if d.store == nil || d.eventBus == nil {
		return
	}
	rows, err := d.store.ListApps()
	if err != nil {
		d.logf("apps: listing apps to register their consumers: %v", err)
		return
	}
	for _, row := range rows {
		if err := d.registerAppConsumer(row.Name); err != nil {
			d.logf("apps: %v", err)
		}
	}
}

// A live consumer gets SetFilter, never a re-registration: unregistering
// deletes the cursor, and the app skips every fact published meanwhile.
func (d *Daemon) registerAppConsumer(name string) error {
	filter, err := d.appFilter(name)
	if err != nil {
		return err
	}
	consumer := apps.ConsumerName(name)
	if d.eventBus.Registered(consumer) {
		if err := d.eventBus.SetFilter(consumer, filter); err != nil {
			return fmt.Errorf("re-pointing the bus consumer for app %q at its new subscriptions: %w", name, err)
		}
		return nil
	}
	if err := d.eventBus.RegisterWithPreDrain(consumer, filter, d.appPreDrain(name), d.appEventHandler(name)); err != nil {
		return fmt.Errorf("registering the bus consumer for app %q: %w", name, err)
	}
	return nil
}

func (d *Daemon) appPreDrain(name string) bus.PreDrain {
	return func(ctx context.Context, consumer bus.Consumer, gap *bus.Gap) error {
		lane := d.appLane(name)
		lane.Lock()
		defer lane.Unlock()

		manifest, version, err := d.appDeclaration(name)
		if err != nil {
			return err
		}
		if len(manifest.EventPatterns()) == 0 {
			claim, err := d.store.AppReconcilePending(name)
			if err != nil {
				return err
			}
			if len(claim.Requests) != 0 {
				if err := d.store.CompleteAppReconcile(name, claim.ThroughRequestID, claim.ThroughSeq, d.appNow()); err != nil {
					return err
				}
			}
			if gap != nil && gap.Head > claim.ThroughSeq {
				if err := d.store.AdvanceBusConsumerCursor(consumer.Name, gap.Head, d.appNow()); err != nil {
					return err
				}
			}
			return nil
		}
		if gap != nil {
			if _, err := d.store.RequestAppReconcileGap(name, gap.Cursor, gap.Earliest, d.appNow()); err != nil {
				return fmt.Errorf("recording the gap reconciliation for app %q: %w", name, err)
			}
		}
		claim, err := d.appReconcileClaim(name)
		if err != nil {
			return fmt.Errorf("reading reconciliation owed by app %q: %w", name, err)
		}
		if len(claim.Requests) == 0 {
			return nil
		}
		if !manifest.Reconcile {
			return d.disableAppMissingReconcile(name, version, claim)
		}
		return d.runAppReconcile(ctx, name, manifest, version, claim)
	}
}

func (d *Daemon) disableAppMissingReconcile(name string, version store.AppVersion, claim store.AppReconcileClaim) error {
	reason := foldAppReconcileReason(version.ID, claim)
	reasonJSON, err := json.Marshal(reason)
	if err != nil {
		return err
	}
	failure := fmt.Sprintf(
		"app %s reconciliation is owed through bus seq %d, but subscribed version %d does not declare reconcile",
		name, claim.ThroughSeq, version.ID)
	latest, found, readErr := d.store.LatestOwedAppReconcileInvocation(name)
	if readErr != nil {
		return readErr
	}
	if !found || latest.ThroughRequestID != claim.ThroughRequestID || latest.Handler != "missing_reconcile" {
		now := d.appNow()
		d.recordAppInvocation(store.AppInvocation{
			AppName: name, VersionID: version.ID, Kind: store.AppInvocationKindReconcile,
			Handler: "missing_reconcile", Status: store.AppInvocationStatusError,
			Error: failure, StartedAt: now, FinishedAt: now,
			ReconcileReason: string(reasonJSON), ThroughRequestID: claim.ThroughRequestID,
			ThroughSeq: claim.ThroughSeq,
		})
	}
	d.disableAppAutomatically(name, failure,
		fmt.Sprintf("apps: disabled %s — %s", name, failure),
		fmt.Sprintf(
			"%s reached a reconcile fence, but version %d has no reconcile handler, so attn disabled it without moving its cursor. Add `reconcile = true`, implement the reconcile export, apply that version, and enable the app again.",
			name, version.ID))
	return errors.New(failure)
}

func (d *Daemon) runAppReconcile(ctx context.Context, name string, manifest appbuild.Manifest, version store.AppVersion, claim store.AppReconcileClaim) error {
	plan := &appDispatchPlan{
		app:       name,
		namespace: apps.Namespace(name),
		versionID: version.ID,
		artifact:  version.ArtifactPath,
		label:     "reconcile",
	}
	for _, collection := range manifest.Collections {
		plan.collections = append(plan.collections, collection.Name)
	}

	reason := foldAppReconcileReason(version.ID, claim)
	reasonJSON, err := json.Marshal(reason)
	if err != nil {
		return fmt.Errorf("encoding reconciliation owed by app %q: %w", name, err)
	}
	started := d.appNow()
	invocation := store.AppInvocation{
		AppName: name, VersionID: version.ID, Kind: store.AppInvocationKindReconcile,
		Handler: "reconcile", Status: store.AppInvocationStatusRunning,
		StartedAt: started, ReconcileReason: string(reasonJSON),
		ThroughRequestID: claim.ThroughRequestID, ThroughSeq: claim.ThroughSeq,
	}
	invocationID, err := d.startAppInvocation(invocation)
	if err != nil {
		return err
	}
	invocation.ID = invocationID
	result, err := d.dispatchAppReconcile(ctx, plan, reason)
	if ctx.Err() != nil {
		if settleErr := d.settleAppInvocation(&invocation, store.AppInvocationStatusInterrupted, ""); settleErr != nil {
			return errors.Join(ctx.Err(), settleErr)
		}
		return ctx.Err()
	}
	if err != nil {
		status := store.AppInvocationStatusRuntimeError
		failure := err.Error()
		if errors.Is(err, context.DeadlineExceeded) {
			status = store.AppInvocationStatusError
			failure = fmt.Sprintf("reconcile for app %s did not return within timeout=%s; attn terminated sidecar generation and left request %d owed",
				name, d.appDispatchBudget(), claim.ThroughRequestID)
		}
		if settleErr := d.settleAppInvocation(&invocation, status, failure); settleErr != nil {
			return errors.Join(err, settleErr)
		}
		if status == store.AppInvocationStatusError {
			d.noteAppReconcileFailure(name, claim, failure)
		} else {
			d.clearAppStall(name)
		}
		return errors.New(failure)
	}
	if !result.OK {
		failure := result.Error
		if settleErr := d.settleAppInvocation(&invocation, store.AppInvocationStatusError, failure); settleErr != nil {
			return settleErr
		}
		d.noteAppReconcileFailure(name, claim, failure)
		return fmt.Errorf("app %s reconcile threw: %s", name, firstLine(failure))
	}
	finished := d.appNow()
	if err := d.store.CompleteAppReconcileInvocation(
		name, invocationID, claim.ThroughRequestID, claim.ThroughSeq, finished,
	); err != nil {
		return fmt.Errorf("completing reconciliation of app %q: %w", name, err)
	}
	invocation.Status = store.AppInvocationStatusOK
	invocation.FinishedAt = finished
	invocation.Duration = finished.Sub(started)
	d.notifyAppWatchers(appInvocationForWire(invocation.ID, invocation), name)
	d.clearAppStall(name)
	return nil
}

func (d *Daemon) appReconcileClaim(name string) (store.AppReconcileClaim, error) {
	previous, ok, err := d.store.LatestOwedAppReconcileInvocation(name)
	if err != nil {
		return store.AppReconcileClaim{}, err
	}
	if ok && previous.ThroughRequestID > 0 {
		claim, err := d.store.AppReconcilePendingThrough(name, previous.ThroughRequestID)
		if err != nil || len(claim.Requests) != 0 {
			return claim, err
		}
	}
	return d.store.AppReconcilePending(name)
}

func (d *Daemon) appReconcileStatusForWire(name string) (protocol.AppReconcileStatus, error) {
	manifest, version, err := d.appDeclaration(name)
	if err != nil {
		return protocol.AppReconcileStatus{}, err
	}
	if version.ID == 0 || len(manifest.EventPatterns()) == 0 {
		return protocol.AppReconcileStatus{State: appReconcileStateNotNeeded}, nil
	}
	claim, err := d.appReconcileClaim(name)
	if err != nil {
		return protocol.AppReconcileStatus{}, err
	}
	status := protocol.AppReconcileStatus{State: appReconcileStateIdle}
	if !manifest.Reconcile {
		status.State = appReconcileStateUnsupported
	}
	if len(claim.Requests) == 0 {
		return status, nil
	}
	if manifest.Reconcile {
		status.State = appReconcileStateOwed
	}
	status.Reason = appReconcileReasonForWire(foldAppReconcileReason(version.ID, claim))
	attempt, ok, err := d.store.LatestOwedAppReconcileInvocation(name)
	if err != nil {
		return protocol.AppReconcileStatus{}, err
	}
	if !ok {
		return status, nil
	}
	if attempt.Status == store.AppInvocationStatusRunning {
		if manifest.Reconcile {
			status.State = appReconcileStateRunning
		}
		info := appInvocationForWire(attempt.ID, attempt)
		status.CurrentAttempt = &info
		return status, nil
	}
	if attempt.Error != "" {
		status.LastError = protocol.Ptr(attempt.Error)
	}
	return status, nil
}

func foldAppReconcileReason(versionID int64, claim store.AppReconcileClaim) appReconcileReason {
	reason := appReconcileReason{
		Version:          versionID,
		ThroughSeq:       claim.ThroughSeq,
		Causes:           make([]string, 0, 3),
		PreviousVersions: []int64{},
	}
	seenCauses := make(map[string]bool, 3)
	seenVersions := make(map[int64]bool)
	for _, request := range claim.Requests {
		seenCauses[request.Reason] = true
		if request.Reason == store.AppReconcileGap && reason.Gap == nil {
			reason.Gap = &appReconcileGap{Cursor: request.Cursor, Earliest: request.Earliest, Missed: request.Missed}
		}
		if request.Reason == store.AppReconcileVersionChange && request.PreviousVersionID != 0 && !seenVersions[request.PreviousVersionID] {
			seenVersions[request.PreviousVersionID] = true
			reason.PreviousVersions = append(reason.PreviousVersions, request.PreviousVersionID)
		}
	}
	for _, cause := range []string{store.AppReconcileGap, store.AppReconcileVersionChange} {
		if seenCauses[cause] {
			reason.Causes = append(reason.Causes, cause)
		}
	}
	return reason
}

func (d *Daemon) dispatchAppReconcile(ctx context.Context, plan *appDispatchPlan, reason appReconcileReason) (appDispatchResult, error) {
	runtime, err := d.awaitAppRuntime(ctx)
	if err != nil {
		return appDispatchResult{}, err
	}
	dispatch := &appDispatch{
		app:         plan.app,
		namespace:   plan.namespace,
		versionID:   plan.versionID,
		collections: make(map[string]struct{}, len(plan.collections)),
	}
	for _, collection := range plan.collections {
		dispatch.collections[collection] = struct{}{}
	}
	d.registerAppDispatch(dispatch)
	defer d.releaseAppDispatch(dispatch.id)

	request := appReconcileRequest{
		Dispatch: dispatch.id, App: plan.app, VersionID: plan.versionID,
		Artifact: plan.artifact, Collections: plan.collections, Reason: reason,
	}
	if request.Collections == nil {
		request.Collections = []string{}
	}
	callCtx, cancel := context.WithTimeout(ctx, d.appDispatchBudget())
	defer cancel()
	result, err := runtime.reconcile(callCtx, request)
	if err != nil {
		if ctx.Err() == nil && callCtx.Err() != nil {
			return appDispatchResult{}, d.attributeWedgedDispatch(ctx, runtime, plan.app)
		}
		if ctx.Err() != nil {
			return appDispatchResult{}, ctx.Err()
		}
		return appDispatchResult{}, runtimeFailure("%v", err)
	}
	return result, nil
}

// An app with no version must subscribe to nothing: bus.ParseFilter reads an
// empty expression as All.
func (d *Daemon) appFilter(name string) (bus.Filter, error) {
	manifest, _, err := d.appDeclaration(name)
	if err != nil {
		return nil, err
	}
	patterns := manifest.EventPatterns()
	if len(patterns) == 0 {
		return bus.Filter{apps.NoSubscriptionsPattern}, nil
	}
	return bus.Filter(patterns), nil
}

func (d *Daemon) appDeclaration(name string) (appbuild.Manifest, store.AppVersion, error) {
	row, ok, err := d.store.GetApp(name)
	if err != nil {
		return appbuild.Manifest{}, store.AppVersion{}, fmt.Errorf("reading app %q: %w", name, err)
	}
	if !ok || row.CurrentVersionID == 0 {
		return appbuild.Manifest{}, store.AppVersion{}, nil
	}
	version, ok, err := d.store.GetAppVersion(row.CurrentVersionID)
	if err != nil {
		return appbuild.Manifest{}, store.AppVersion{}, fmt.Errorf("reading version %d of app %q: %w", row.CurrentVersionID, name, err)
	}
	if !ok {
		return appbuild.Manifest{}, store.AppVersion{}, nil
	}
	var manifest appbuild.Manifest
	if err := json.Unmarshal([]byte(version.Declaration), &manifest); err != nil {
		return appbuild.Manifest{}, store.AppVersion{}, fmt.Errorf(
			"the declaration frozen into version %d of app %q is not readable (%v); that snapshot is written at apply time and never edited, so this version cannot be run — `attn app rollback %s` moves off it",
			version.ID, name, err, name)
	}
	return manifest, version, nil
}

// Leaves alone a collection an older version declared and this one dropped: a
// version bump is not consent to delete the user's documents.
func (d *Daemon) declareAppCollections(name string, manifest appbuild.Manifest) {
	namespace := apps.Namespace(name)
	for _, collection := range manifest.Collections {
		schema := docstore.CollectionSchema{Namespace: namespace, Collection: collection.Name}
		for _, field := range collection.Fields {
			schema.Fields = append(schema.Fields, docstore.FieldSpec{Name: field, Type: docstore.FieldString})
		}
		if err := schema.Validate(); err != nil {
			d.logf("apps: app %s declares collection %q, which the document store refuses: %v", name, collection.Name, err)
			continue
		}
		redeclared, err := d.store.DefineDocumentCollection(schema, d.appNow())
		if err != nil {
			d.logf("apps: declaring collection %s/%s for app %s: %v", namespace, collection.Name, name, err)
			continue
		}
		if redeclared {
			d.publishCollectionRedeclared(namespace, collection.Name)
		}
	}
}

func (d *Daemon) syncAppRuntimeForVersion(name string) {
	if d.store == nil {
		return
	}
	manifest, _, err := d.appDeclaration(name)
	if err != nil {
		d.logf("apps: %v", err)
		return
	}
	d.declareAppCollections(name, manifest)
	if d.eventBus == nil {
		return
	}
	if err := d.registerAppConsumer(name); err != nil {
		d.logf("apps: %v", err)
	}
}

func (d *Daemon) appEventHandler(name string) bus.Handler {
	return func(ctx context.Context, ev bus.Event) error {
		return d.deliverAppEvent(ctx, name, ev)
	}
}

// Returning an error stalls this app's consumer and has the bus redeliver the
// event — never skip it.
func (d *Daemon) deliverAppEvent(ctx context.Context, name string, ev bus.Event) error {
	lane := d.appLane(name)
	lane.Lock()
	defer lane.Unlock()

	claim, err := d.store.AppReconcilePending(name)
	if err != nil {
		return fmt.Errorf("reading reconciliation owed by app %q: %w", name, err)
	}
	if len(claim.Requests) != 0 {
		return fmt.Errorf("app %q reconciliation is owed through bus seq %d; facts remain fenced until it succeeds", name, claim.ThroughSeq)
	}

	plan, err := d.planAppDispatch(name, ev)
	if err != nil {
		return err
	}
	if plan == nil {
		// Advance rather than stall: a permanent stall pins retention.
		return nil
	}

	started := d.appNow()
	result, dispatchErr := d.dispatchToAppRuntime(ctx, plan, ev)
	took := d.appNow().Sub(started)

	if ctx.Err() != nil {
		return ctx.Err()
	}

	invocation := store.AppInvocation{
		AppName:      name,
		VersionID:    plan.versionID,
		Kind:         store.AppInvocationKindSubscription,
		EventSeq:     ev.Seq,
		EventName:    ev.Name,
		EventSubject: ev.Subject,
		Handler:      plan.label,
		Duration:     took,
		StartedAt:    started,
	}

	switch {
	case dispatchErr != nil && isRuntimeFailure(dispatchErr):
		invocation.Status = appInvocationStatusRuntimeError
		invocation.Error = dispatchErr.Error()
		d.recordAppInvocation(invocation)
		d.clearAppStall(name)
		return dispatchErr

	case dispatchErr != nil && errors.Is(dispatchErr, context.DeadlineExceeded):
		invocation.Status = appInvocationStatusError
		invocation.Error = fmt.Sprintf(
			"the handler for %s did not return within %s; attn abandoned the dispatch. A handler awaits attn's own APIs, which always settle — an await on something else needs its own timeout.",
			ev.Name, d.appDispatchBudget())
		d.recordAppInvocation(invocation)
		d.noteAppFailure(name, ev, invocation.Error)
		return errors.New(invocation.Error)

	case dispatchErr != nil:
		invocation.Status = appInvocationStatusRuntimeError
		invocation.Error = dispatchErr.Error()
		d.recordAppInvocation(invocation)
		d.clearAppStall(name)
		return dispatchErr

	case !result.OK:
		invocation.Status = appInvocationStatusError
		invocation.Error = result.Error
		d.recordAppInvocation(invocation)
		d.noteAppFailure(name, ev, result.Error)
		return fmt.Errorf("app %s handler %s threw: %s", name, plan.handler, firstLine(result.Error))

	default:
		invocation.Status = appInvocationStatusOK
		d.recordAppInvocation(invocation)
		d.clearAppStall(name)
		return nil
	}
}

func (d *Daemon) planAppDispatch(name string, ev bus.Event) (*appDispatchPlan, error) {
	manifest, version, err := d.appDeclaration(name)
	if err != nil {
		return nil, err
	}
	if version.ID == 0 {
		return nil, nil
	}
	handler := resolveAppHandler(manifest.EventPatterns(), ev.Name)
	if handler == "" {
		return nil, nil
	}
	plan := &appDispatchPlan{
		app:       name,
		namespace: apps.Namespace(name),
		versionID: version.ID,
		artifact:  version.ArtifactPath,
		handler:   handler,
		label:     apps.SubscriptionLabel(handler),
	}
	for _, collection := range manifest.Collections {
		plan.collections = append(plan.collections, collection.Name)
	}
	return plan, nil
}

func resolveAppHandler(patterns []string, eventName string) string {
	best := ""
	for _, pattern := range patterns {
		if !bus.MatchPattern(pattern, eventName) {
			continue
		}
		if pattern == eventName {
			return pattern
		}
		if len(pattern) > len(best) {
			best = pattern
		}
	}
	return best
}

func (d *Daemon) dispatchToAppRuntime(ctx context.Context, plan *appDispatchPlan, ev bus.Event) (appDispatchResult, error) {
	runtime, err := d.awaitAppRuntime(ctx)
	if err != nil {
		return appDispatchResult{}, err
	}

	dispatch := &appDispatch{
		app:         plan.app,
		namespace:   plan.namespace,
		versionID:   plan.versionID,
		collections: make(map[string]struct{}, len(plan.collections)),
	}
	for _, collection := range plan.collections {
		dispatch.collections[collection] = struct{}{}
	}
	d.registerAppDispatch(dispatch)
	// Released whatever happens: an id left behind lets a handler that finally
	// woke up write documents from outside any delivery.
	defer d.releaseAppDispatch(dispatch.id)

	var payload any
	if len(ev.Payload) > 0 {
		payload = json.RawMessage(ev.Payload)
	}
	request := appDispatchRequest{
		Dispatch:    dispatch.id,
		App:         plan.app,
		VersionID:   plan.versionID,
		Artifact:    plan.artifact,
		Handler:     plan.handler,
		Collections: plan.collections,
		Event: appDispatchEvent{
			Name:        ev.Name,
			Subject:     ev.Subject,
			Seq:         ev.Seq,
			Payload:     payload,
			PublishedAt: stampForWire(ev.CreatedAt),
		},
	}
	if request.Collections == nil {
		request.Collections = []string{}
	}

	callCtx, cancel := context.WithTimeout(ctx, d.appDispatchBudget())
	defer cancel()
	result, err := runtime.dispatch(callCtx, request)
	if err != nil {
		if ctx.Err() == nil && callCtx.Err() != nil {
			// Attributed here: the dispatch must still be in the in-flight set.
			return appDispatchResult{}, d.attributeWedgedDispatch(ctx, runtime, plan.app)
		}
		if ctx.Err() != nil {
			return appDispatchResult{}, ctx.Err()
		}
		return appDispatchResult{}, runtimeFailure("%v", err)
	}
	return result, nil
}

// The order dispatches were sent is not the order handlers hold the loop, so the culprit
// is the host's most recent unanswered entry; the ledger is dropped only on facts.
func (d *Daemon) attributeWedgedDispatch(ctx context.Context, runtime *appRuntimeConnection, name string) error {
	pingCtx, cancel := context.WithTimeout(ctx, d.appPingBudget())
	defer cancel()

	asked := d.appNow()
	err := runtime.ping(pingCtx)
	// Microseconds: an answered ping rounds to "0s" in milliseconds.
	d.logf("apps: %s hit the dispatch timeout; the app runtime %s a liveness ping after %s",
		name, pingOutcome(err), d.appNow().Sub(asked).Round(time.Microsecond))

	// Generation-fenced, so a stale waiter cannot kill the replacement that has
	// already taken over.
	terminated, terminateErr := d.ensureAppRuntimeSupervisor().TerminateGeneration(appRuntimeChildName, runtime.generation)
	if terminateErr != nil {
		d.logf("apps: terminating timed-out app runtime generation %d: %v", runtime.generation, terminateErr)
	} else if terminated {
		d.logf("apps: terminated timed-out app runtime generation %d; the supervisor will start its replacement", runtime.generation)
	}

	if err == nil {
		return context.DeadlineExceeded
	}

	culprit, ok := d.wedgedAppCulprit()
	if !ok || culprit == name {
		return context.DeadlineExceeded
	}
	return runtimeFailure(
		"the app runtime stopped answering while %s held its event loop, so this handler never ran; %s is what attn charged for the stall, and `attn app status %s` shows it",
		culprit, culprit, culprit)
}

func pingOutcome(err error) string {
	if err == nil {
		return "answered"
	}
	return "did not answer"
}

// Runs inline on the connection's read loop, never in a goroutine per frame:
// entries must be stamped in the order the host made them.
func (d *Daemon) noteEnteredHandler(generation uint64, dispatchID, name string) {
	d.appEnteredMu.Lock()
	defer d.appEnteredMu.Unlock()
	if d.appEntered == nil || d.appEnteredGen != generation {
		d.appEntered = make(map[string]enteredHandler)
		d.appEnteredGen = generation
	}
	d.appEnteredSeq++
	d.appEntered[dispatchID] = enteredHandler{app: name, order: d.appEnteredSeq}
}

func (d *Daemon) forgetEnteredHandler(dispatchID string) {
	d.appEnteredMu.Lock()
	delete(d.appEntered, dispatchID)
	d.appEnteredMu.Unlock()
}

func (d *Daemon) forgetEnteredHandlers() {
	d.appEnteredMu.Lock()
	d.appEntered = nil
	d.appEnteredGen = 0
	d.appEnteredMu.Unlock()
}

func (d *Daemon) wedgedAppCulprit() (string, bool) {
	d.appEnteredMu.Lock()
	defer d.appEnteredMu.Unlock()
	var latest enteredHandler
	for _, entry := range d.appEntered {
		if entry.order > latest.order {
			latest = entry
		}
	}
	return latest.app, latest.order > 0
}

// A tripwire, not a fit: answered pings measured on a live daemon cost 344µs and 416µs, so
// two seconds is ~5,000× and only a loop that is genuinely not turning reaches it.
const appRuntimePingWait = 2 * time.Second

func (d *Daemon) appPingBudget() time.Duration {
	if d.appPingWait > 0 {
		return d.appPingWait
	}
	return appRuntimePingWait
}

func (d *Daemon) appDispatchBudget() time.Duration {
	if d.appDispatchWait > 0 {
		return d.appDispatchWait
	}
	return appDispatchTimeout
}

func (d *Daemon) awaitAppRuntime(ctx context.Context) (*appRuntimeConnection, error) {
	runtime, ready := d.appRuntimeOrReady()
	if runtime != nil {
		return runtime, nil
	}
	if err := d.startAppRuntimeForDispatch(); err != nil {
		if errors.Is(err, supervise.ErrParked) {
			return nil, runtimeFailure(
				"the app runtime is parked after repeated crashes and is not being restarted; `attn app runtime status` shows why it exited and `attn app runtime restart` tries again")
		}
		return nil, runtimeFailure("%v", err)
	}
	wait := d.appConnectWait()
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ready:
		if runtime := d.appRuntimeConnected(); runtime != nil {
			return runtime, nil
		}
		return nil, runtimeFailure("the app runtime connected and went away again before this handler could run")
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
		return nil, runtimeFailure(
			"the app runtime did not connect within %s; `attn app runtime status` shows what it is doing and `attn app logs runtime` shows what it printed",
			wait)
	}
}

// Both under one lock: fetched separately, a connection landing between the
// check and the wait leaves the waiter asleep beside a healthy runtime.
func (d *Daemon) appRuntimeOrReady() (*appRuntimeConnection, chan struct{}) {
	d.appRuntimeMu.Lock()
	defer d.appRuntimeMu.Unlock()
	if d.appRuntimeConn != nil {
		return d.appRuntimeConn, nil
	}
	if d.appRuntimeReady == nil {
		d.appRuntimeReady = make(chan struct{})
	}
	return nil, d.appRuntimeReady
}

func (d *Daemon) recordAppInvocation(invocation store.AppInvocation) {
	if d.store == nil {
		return
	}
	id, err := d.store.AppendAppInvocation(invocation)
	if err != nil {
		d.logf("apps: recording an invocation of app %s: %v", invocation.AppName, err)
		return
	}
	d.notifyAppWatchers(appInvocationForWire(id, invocation), invocation.AppName)
}

func (d *Daemon) startAppInvocation(invocation store.AppInvocation) (int64, error) {
	if d.store == nil {
		return 0, errors.New("apps: cannot start an invocation without a store")
	}
	id, err := d.store.StartAppInvocation(invocation)
	if err != nil {
		return 0, fmt.Errorf("starting %s invocation of app %s: %w", invocation.Kind, invocation.AppName, err)
	}
	invocation.ID = id
	invocation.Status = store.AppInvocationStatusRunning
	d.notifyAppWatchers(appInvocationForWire(id, invocation), invocation.AppName)
	return id, nil
}

func (d *Daemon) repairInterruptedAppInvocations() error {
	if d.store == nil {
		return nil
	}
	interrupted, err := d.store.InterruptRunningAppInvocations(d.appNow())
	if err != nil {
		return fmt.Errorf("repair interrupted app invocations: %w", err)
	}
	if interrupted > 0 {
		d.logf("apps: marked %d invocation(s) interrupted during startup repair", interrupted)
	}
	return nil
}

func (d *Daemon) settleAppInvocation(invocation *store.AppInvocation, status, failure string) error {
	finished := d.appNow()
	settled, err := d.store.SettleAppInvocation(invocation.ID, status, failure, finished)
	if err != nil {
		return fmt.Errorf("settling invocation %d of app %s: %w", invocation.ID, invocation.AppName, err)
	}
	if !settled {
		return fmt.Errorf("settling invocation %d of app %s: it is no longer running", invocation.ID, invocation.AppName)
	}
	invocation.Status = status
	invocation.Error = failure
	invocation.FinishedAt = finished
	invocation.Duration = finished.Sub(invocation.StartedAt)
	d.notifyAppWatchers(appInvocationForWire(invocation.ID, *invocation), invocation.AppName)
	return nil
}

func firstLine(s string) string {
	if idx := strings.IndexByte(s, '\n'); idx >= 0 {
		return strings.TrimSpace(s[:idx])
	}
	return strings.TrimSpace(s)
}

func (d *Daemon) noteAppFailure(name string, ev bus.Event, message string) {
	stalled, attempts, disable := d.noteAppStall(name, appStallKindSubscription, ev.Seq, ev.Name, 0, message)
	if !disable {
		return
	}
	d.autoDisableApp(name, ev, stalled, attempts, message)
}

func (d *Daemon) noteAppReconcileFailure(name string, claim store.AppReconcileClaim, message string) {
	stalled, attempts, disable := d.noteAppStall(
		name, appStallKindReconcile, claim.ThroughRequestID, "", claim.ThroughRequestID, message)
	if !disable {
		return
	}
	d.disableAppAutomatically(name, message,
		fmt.Sprintf("apps: disabled %s — reconcile request %d stalled for %s across %d attempts: %s",
			name, claim.ThroughRequestID, stalled.Round(time.Second), attempts, firstLine(message)),
		fmt.Sprintf(
			"%s failed to reconcile through bus seq %d for %s across %d attempts, so attn disabled it. The rebuild remains owed. Fix the reconcile handler and `attn app enable %s`; `attn app status %s` shows the failure and fence.",
			name, claim.ThroughSeq, stalled.Round(time.Second), attempts, name, name))
}

func (d *Daemon) noteAppStall(name, kind string, key int64, eventName string, reconcileRequestID int64, message string) (time.Duration, int, bool) {
	now := d.appNow()
	d.appStallMu.Lock()
	if d.appStalls == nil {
		d.appStalls = make(map[string]*appStall)
	}
	stall := d.appStalls[name]
	if stall == nil || stall.kind != kind || stall.seq != key {
		stall = &appStall{
			kind: kind, seq: key, eventName: eventName,
			reconcileRequestID: reconcileRequestID, since: now,
		}
		d.appStalls[name] = stall
	}
	stall.attempts++
	stall.lastError = message
	stalled := now.Sub(stall.since)
	attempts := stall.attempts
	d.appStallMu.Unlock()
	return stalled, attempts, stalled >= d.appAutoDisableWindow()
}

func (d *Daemon) clearAppStall(name string) {
	d.appStallMu.Lock()
	delete(d.appStalls, name)
	d.appStallMu.Unlock()
}

// The host names the culprit from the killing error's stack: the rejection can surface
// long after that app's dispatch returned, so "who was running" would name an innocent.
func (d *Daemon) noteAppRuntimeCrash(name, kind, message string) {
	now := d.appNow()

	d.appCrashMu.Lock()
	if d.appCrashes == nil {
		d.appCrashes = make(map[string][]time.Time)
	}
	kept := d.appCrashes[name][:0]
	for _, at := range d.appCrashes[name] {
		if now.Sub(at) < appCrashWindow {
			kept = append(kept, at)
		}
	}
	kept = append(kept, now)
	d.appCrashes[name] = kept
	strikes := len(kept)
	d.appCrashMu.Unlock()

	d.logf("apps: %s crashed the app runtime (%s), strike %d of %d: %s",
		name, kind, strikes, appCrashStrikes, firstLine(message))
	if strikes < appCrashStrikes {
		return
	}
	d.disableAppAutomatically(name, message,
		fmt.Sprintf("apps: disabled %s — crashed the app runtime %d times within %s: %s",
			name, strikes, appCrashWindow, firstLine(message)),
		fmt.Sprintf(
			"%s crashed the shared app runtime %d times in %s, so attn disabled it — one app taking the runtime down stops every other app with it. The failure was an unhandled %s; a handler must catch what it starts, including work it does not await. Fix it and `attn app enable %s`; `attn app logs runtime` shows what it printed.",
			name, strikes, appCrashWindow, kind, name))
}

func (d *Daemon) clearAppCrashes(name string) {
	d.appCrashMu.Lock()
	delete(d.appCrashes, name)
	d.appCrashMu.Unlock()
}

func (d *Daemon) appStallSnapshot(name string) (appStall, bool) {
	d.appStallMu.Lock()
	defer d.appStallMu.Unlock()
	stall, ok := d.appStalls[name]
	if !ok {
		return appStall{}, false
	}
	return *stall, true
}

func (d *Daemon) autoDisableApp(name string, ev bus.Event, stalled time.Duration, attempts int, message string) {
	d.disableAppAutomatically(name, message,
		fmt.Sprintf("apps: disabled %s — stuck on %s (seq %d) for %s across %d attempts: %s",
			name, ev.Name, ev.Seq, stalled.Round(time.Second), attempts, firstLine(message)),
		fmt.Sprintf(
			"%s failed on the same event (%s, seq %d) for %s across %d attempts, so attn disabled it — a stalled app holds the event log open for every other consumer. Fix the handler and `attn app enable %s`; `attn app status %s` shows the failures.",
			name, ev.Name, ev.Seq, stalled.Round(time.Second), attempts, name, name))
}

func (d *Daemon) disableAppAutomatically(name, detail, logLine, body string) {
	consumer := apps.ConsumerName(name)
	flipped, err := d.store.SetBusConsumerEnabled(consumer, false, d.appNow())
	if err != nil {
		d.logf("apps: disabling app %s: %v", name, err)
		return
	}
	if !flipped {
		return
	}
	// Old clocks left in place would disable a re-enabled app on its next failure.
	d.clearAppStall(name)
	d.clearAppCrashes(name)

	d.logf("%s", logLine)
	d.publishFact(FactAppEnabledChanged, name, appEnabledChanged{
		Name: name, Consumer: consumer, Enabled: false,
	})
	if d.store == nil {
		return
	}
	record, err := d.store.AddNotification(store.NotificationRecord{
		Kind:       notificationKindAppAutoDisabled,
		Severity:   store.NotificationWarning,
		Title:      fmt.Sprintf("App disabled: %s", name),
		Body:       body,
		Detail:     detail,
		SourceKind: "app",
		SourceID:   name,
	}, d.appNow())
	if err != nil {
		d.logf("notifications: add app-auto-disabled notification for %s: %v", name, err)
		return
	}
	d.publishFact(FactNotificationCreated, record.ID, nil)
}

const (
	appInvocationRetentionKind     = "app_invocation_retention"
	appInvocationRetentionInterval = time.Hour
	appInvocationRetentionTimeout  = 30 * time.Second

	// AppInvocationRetention matches the bus's own DefaultRetention: an invocation
	// whose event has been trimmed cannot be re-read against the log.
	AppInvocationRetention = 30 * 24 * time.Hour

	// AppInvocationsPerApp bounds the table the age window cannot. Measured over 7.5 days of
	// production: `session.state.changed` runs at 1,141/hour, so 20,000 is ~17 hours, ~4MB.
	AppInvocationsPerApp = 20_000
)

func (d *Daemon) appInvocationRetentionHandler(_ context.Context, _ *jobs.Job) (any, error) {
	if d.store == nil {
		return map[string]any{"removed": 0}, nil
	}
	removed, err := d.store.TrimAppInvocations(d.appNow().Add(-AppInvocationRetention), AppInvocationsPerApp)
	if err != nil {
		return nil, fmt.Errorf("trimming the app invocation log: %w", err)
	}
	if removed > 0 {
		d.logf("apps: trimmed %d invocation(s) — older than %s, or past the newest %d of an app",
			removed, AppInvocationRetention, AppInvocationsPerApp)
	}
	return map[string]any{"removed": removed}, nil
}
