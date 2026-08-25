package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/victorarias/attn/internal/appbuild"
	"github.com/victorarias/attn/internal/apps"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

type appReconcileOwedError struct {
	app    string
	reason appReconcileReason
}

func (e *appReconcileOwedError) Error() string {
	return fmt.Sprintf(
		"%s is rebuilding what it derives from the event log (owed through bus seq %d: %s), so it runs no commands until that finishes; `attn app status %s` shows the rebuild",
		e.app, e.reason.ThroughSeq, strings.Join(e.reason.Causes, ", "), e.app)
}

// The serving version's declaration is the contract, never the manifest on disk. A failing
// command does not advance the auto-disable clock, which exists for bus-retention pins.

const appCommandEvent = "app.command"

// appCommandPayloadLimit bounds what one command may carry in either direction; the document
// store moves anything larger. 256KB is ~8x the crash reporter's 32KB component stack.
const appCommandPayloadLimit = 256 * 1024

func (d *Daemon) handleAppCommand(client *wsClient, msg *protocol.AppCommandMessage) {
	requestID := strings.TrimSpace(msg.RequestID)
	if requestID == "" {
		d.sendCommandError(client, protocol.CmdAppCommand,
			"app_command requires a request_id: the result is correlated by it, and one without an id could never reach the caller")
		return
	}
	go func() {
		payload, err := d.runAppCommand(msg)
		d.sendToClient(client, appCommandResult(requestID, payload, err))
	}()
}

func appCommandResult(requestID string, payload json.RawMessage, err error) protocol.AppCommandResultMessage {
	result := protocol.AppCommandResultMessage{
		Event:     protocol.EventAppCommandResult,
		RequestID: requestID,
		Success:   err == nil,
	}
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
		var owed *appReconcileOwedError
		if errors.As(err, &owed) {
			result.ErrorCode = protocol.Ptr(protocol.ErrorCodeReconcileOwed)
			result.Reconcile = appReconcileReasonForWire(owed.reason)
		}
		return result
	}
	if len(payload) > 0 {
		result.Payload = protocol.Ptr(string(payload))
	}
	return result
}

func (d *Daemon) runAppCommand(msg *protocol.AppCommandMessage) (json.RawMessage, error) {
	name := strings.TrimSpace(msg.App)
	if err := apps.ValidateName(name); err != nil {
		return nil, err
	}
	command := strings.TrimSpace(msg.Command)
	if err := apps.ValidateCommandName(command); err != nil {
		return nil, err
	}
	payload := json.RawMessage(strings.TrimSpace(protocol.Deref(msg.Payload)))
	if len(payload) > appCommandPayloadLimit {
		return nil, fmt.Errorf(
			"the payload for %s/%s is %d bytes, over the %d-byte limit for one command; a command is an action, and anything larger belongs in a document the handler reads",
			name, command, len(payload), appCommandPayloadLimit)
	}
	if len(payload) > 0 && !json.Valid(payload) {
		return nil, fmt.Errorf("the payload for %s/%s is not valid JSON", name, command)
	}
	if d.store == nil {
		return nil, fmt.Errorf("this daemon has no store, so it runs no apps")
	}
	budget := d.appDispatchBudget()
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()

	lane := d.appLane(name)
	if err := lane.acquire(ctx); err != nil {
		return nil, fmt.Errorf(
			"command %q of app %s waited %s for the app to finish what it was already running and never got a turn; %s is running one thing at a time, and `attn app logs %s` names it",
			command, name, budget, name, name)
	}
	defer lane.Unlock()

	plan, err := d.planAppCommand(name, command)
	if err != nil {
		return nil, err
	}

	started := d.appNow()
	result, dispatchErr := d.dispatchAppCommand(ctx, plan, payload)
	took := d.appNow().Sub(started)

	invocation := store.AppInvocation{
		AppName:      name,
		VersionID:    plan.versionID,
		Kind:         store.AppInvocationKindCommand,
		EventName:    appCommandEvent,
		EventSubject: command,
		Handler:      plan.label,
		Duration:     took,
		StartedAt:    started,
	}
	switch {
	case dispatchErr != nil:
		if errors.Is(dispatchErr, context.DeadlineExceeded) {
			dispatchErr = fmt.Errorf(
				"the handler for command %q of app %s did not return within %s, so attn abandoned it; a handler awaits attn's own APIs, which always settle — an await on anything else needs its own timeout",
				command, name, d.appDispatchBudget())
			invocation.Status = appInvocationStatusError
		} else {
			invocation.Status = appInvocationStatusRuntimeError
		}
		invocation.Error = dispatchErr.Error()
		d.recordAppInvocation(invocation)
		return nil, dispatchErr

	case !result.OK:
		invocation.Status = appInvocationStatusError
		invocation.Error = result.Error
		d.recordAppInvocation(invocation)
		return nil, fmt.Errorf("%s threw running command %q: %s", name, command, firstLine(result.Error))

	case len(result.Payload) > appCommandPayloadLimit:
		invocation.Status = appInvocationStatusError
		invocation.Error = fmt.Sprintf("the answer is %d bytes, over the %d-byte limit", len(result.Payload), appCommandPayloadLimit)
		d.recordAppInvocation(invocation)
		return nil, fmt.Errorf(
			"the handler for command %q of app %s answered with %d bytes, over the %d-byte limit for one command; a command is an action, and anything larger belongs in a document the view reads",
			command, name, len(result.Payload), appCommandPayloadLimit)

	default:
		invocation.Status = appInvocationStatusOK
		d.recordAppInvocation(invocation)
		return result.Payload, nil
	}
}

func (d *Daemon) planAppCommand(name, command string) (*appDispatchPlan, error) {
	_, ok, err := d.store.GetApp(name)
	if err != nil {
		return nil, fmt.Errorf("reading app %q: %w", name, err)
	}
	if !ok {
		return nil, fmt.Errorf("no app named %s is installed; `attn app apply <path>` installs one", name)
	}
	consumer, found, err := d.store.GetBusConsumer(apps.ConsumerName(name))
	if err != nil {
		return nil, fmt.Errorf("reading the bus consumer of app %q: %w", name, err)
	}
	if !found || !consumer.Enabled {
		return nil, fmt.Errorf("%s is disabled, so it runs nothing; `attn app enable %s` turns it back on", name, name)
	}

	manifest, version, err := d.appDeclaration(name)
	if err != nil {
		return nil, err
	}
	if version.ID == 0 {
		return nil, fmt.Errorf("%s has no version serving, so there is no code to run; `attn app apply <path>` builds one", name)
	}
	claim, err := d.appReconcileClaim(name)
	if err != nil {
		return nil, fmt.Errorf("reading reconciliation owed by app %q: %w", name, err)
	}
	if len(claim.Requests) != 0 {
		return nil, &appReconcileOwedError{app: name, reason: foldAppReconcileReason(version.ID, claim)}
	}
	declared := manifest.CommandNames()
	if !containsString(declared, command) {
		// The version, not the manifest on disk: after a rollback the two differ,
		// and the running code is what the caller is actually talking to.
		return nil, fmt.Errorf(
			"the version of %s serving now (%d) declares no command %q; it declares %s. Add a [[commands]] block and `attn app apply`, or roll back to a version that has it",
			name, version.ID, command, commandList(declared))
	}

	plan := &appDispatchPlan{
		app:       name,
		namespace: apps.Namespace(name),
		versionID: version.ID,
		artifact:  version.ArtifactPath,
		handler:   command,
		label:     apps.CommandLabel(command),
	}
	for _, collection := range manifest.Collections {
		plan.collections = append(plan.collections, collection.Name)
	}
	return plan, nil
}

func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func commandList(commands []string) string {
	if len(commands) == 0 {
		return "none"
	}
	return strings.Join(commands, ", ")
}

func (d *Daemon) dispatchAppCommand(ctx context.Context, plan *appDispatchPlan, payload json.RawMessage) (appCommandDispatchResult, error) {
	runtime, err := d.awaitAppRuntime(ctx)
	if err != nil {
		return appCommandDispatchResult{}, err
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
	// Released whatever happens, including the abandoned-timeout path: an id left behind would
	// let a handler that finally woke up write documents from outside any invocation.
	defer d.releaseAppDispatch(dispatch.id)

	request := appCommandRequest{
		Dispatch:    dispatch.id,
		App:         plan.app,
		VersionID:   plan.versionID,
		Artifact:    plan.artifact,
		Handler:     plan.handler,
		Collections: plan.collections,
		Payload:     payload,
	}
	if request.Collections == nil {
		request.Collections = []string{}
	}

	result, err := runtime.command(ctx, request)
	if err != nil {
		if ctx.Err() != nil {
			// Our own deadline: the host says which app holds the frozen loop, and this
			// dispatch must still be in the in-flight set for that answer to be right.
			return appCommandDispatchResult{}, d.attributeWedgedDispatch(context.Background(), runtime, plan.app)
		}
		return appCommandDispatchResult{}, runtimeFailure("%v", err)
	}
	return result, nil
}

func appDeclaredCommands(declaration string, logf func(string, ...any)) []protocol.AppCommandInfo {
	var snapshot struct {
		Commands []appbuild.Command `json:"commands"`
	}
	if err := json.Unmarshal([]byte(declaration), &snapshot); err != nil {
		if logf != nil {
			logf("apps: reading the commands of a stored declaration: %v", err)
		}
		return nil
	}
	out := make([]protocol.AppCommandInfo, 0, len(snapshot.Commands))
	for _, c := range snapshot.Commands {
		info := protocol.AppCommandInfo{Name: c.Name}
		if c.Description != "" {
			info.Description = protocol.Ptr(c.Description)
		}
		out = append(out, info)
	}
	return out
}
