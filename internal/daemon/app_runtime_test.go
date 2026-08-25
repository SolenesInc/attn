package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/appbuild"
	"github.com/victorarias/attn/internal/apps"
	"github.com/victorarias/attn/internal/bus"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/supervise"
)

type fakeAppRuntime struct {
	t    *testing.T
	conn net.Conn

	handler func(*fakeAppRuntime, appDispatchRequest) error

	command   func(*fakeAppRuntime, appCommandRequest) (json.RawMessage, error)
	reconcile func(*fakeAppRuntime, appReconcileRequest) error

	writeMu sync.Mutex

	mu         sync.Mutex
	dispatches []appDispatchRequest
	commands   []appCommandRequest
	reconciles []appReconcileRequest
	pending    map[string]chan jsonRPCMessage
	nextID     int
	loopFrozen bool
}

func (f *fakeAppRuntime) freezeLoop() {
	f.mu.Lock()
	f.loopFrozen = true
	f.mu.Unlock()
}

func (f *fakeAppRuntime) frozen() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.loopFrozen
}

func startFakeAppRuntime(t *testing.T, d *Daemon, handler func(*fakeAppRuntime, appDispatchRequest) error) *fakeAppRuntime {
	t.Helper()
	serverConn, clientConn := net.Pipe()
	runtime := &fakeAppRuntime{
		t:       t,
		conn:    clientConn,
		handler: handler,
		pending: make(map[string]chan jsonRPCMessage),
	}

	served := make(chan struct{})
	go func() {
		defer close(served)
		d.handleConnection(serverConn)
	}()
	t.Cleanup(func() {
		_ = clientConn.Close()
		select {
		case <-served:
		case <-time.After(2 * time.Second):
			t.Error("the daemon did not let go of the app runtime connection")
		}
	})

	reader := bufio.NewReader(clientConn)
	runtime.sendRaw(jsonRPCMessage{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`"hello"`),
		Method:  appRuntimeHelloMethod,
		Params:  mustJSON(t, appRuntimeHelloParams{Generation: 1, APIVersion: appRuntimeAPIVersion, PID: 4242}),
	})
	frame, err := readSocketFrame(reader)
	if err != nil {
		t.Fatalf("app runtime hello got no answer: %v", err)
	}
	var ack jsonRPCMessage
	if err := json.Unmarshal(frame, &ack); err != nil {
		t.Fatalf("decode hello answer: %v", err)
	}
	if ack.Error != nil {
		t.Fatalf("hello refused: %s", ack.Error.Message)
	}
	go runtime.serve(reader)

	// The daemon publishes the connection from the same goroutine that answered
	// hello, so an answered hello does not yet mean appRuntimeConnected() is set.
	waitFor(t, "the daemon to adopt the app runtime", func() bool {
		return d.appRuntimeConnected() != nil
	})
	return runtime
}

func (f *fakeAppRuntime) serve(reader *bufio.Reader) {
	for {
		data, err := readSocketFrame(reader)
		if err != nil {
			f.mu.Lock()
			for _, ch := range f.pending {
				close(ch)
			}
			f.pending = map[string]chan jsonRPCMessage{}
			f.mu.Unlock()
			return
		}
		var msg jsonRPCMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}
		if msg.Method == "" {
			f.mu.Lock()
			ch := f.pending[jsonRPCIDKey(msg.ID)]
			delete(f.pending, jsonRPCIDKey(msg.ID))
			f.mu.Unlock()
			if ch != nil {
				ch <- msg
			}
			continue
		}
		if msg.Method == "app.runtime.ping" {
			if !f.frozen() {
				f.sendRaw(jsonRPCResult(msg.ID, appRuntimePingResult{OK: true}))
			}
			continue
		}
		if f.frozen() {
			continue
		}
		if msg.Method == "app.command" {
			f.serveCommand(msg)
			continue
		}
		if msg.Method == "app.reconcile" {
			f.serveReconcile(msg)
			continue
		}
		if msg.Method != "app.dispatch" {
			f.sendRaw(jsonRPCFailure(msg.ID, jsonRPCInvalidRequest, "the fake sidecar only serves app.dispatch, app.command, app.reconcile and app.runtime.ping"))
			continue
		}
		var req appDispatchRequest
		if err := json.Unmarshal(msg.Params, &req); err != nil {
			f.sendRaw(jsonRPCFailure(msg.ID, jsonRPCInvalidRequest, err.Error()))
			continue
		}
		f.mu.Lock()
		f.dispatches = append(f.dispatches, req)
		f.mu.Unlock()
		// Announced here rather than inside the goroutine so the daemon sees dispatches
		// in arrival order, as the real host single loop guarantees.
		f.sendRaw(jsonRPCMessage{
			JSONRPC: "2.0",
			Method:  appRuntimeEnteredMethod,
			Params:  mustMarshalHandlerParams(f.t, appRuntimeHandlerParams{Dispatch: req.Dispatch, App: req.App}),
		})
		// On its own goroutine: a handler that calls back into the daemon would
		// otherwise deadlock against this loop, the only reader of the answer.
		go func(id json.RawMessage, req appDispatchRequest) {
			result := appDispatchResult{OK: true}
			if f.handler != nil {
				if err := f.handler(f, req); err != nil {
					result = appDispatchResult{OK: false, Error: err.Error()}
				}
			}
			f.sendRaw(jsonRPCMessage{
				JSONRPC: "2.0",
				Method:  appRuntimeLeftMethod,
				Params:  mustMarshalHandlerParams(f.t, appRuntimeHandlerParams{Dispatch: req.Dispatch, App: req.App}),
			})
			f.sendRaw(jsonRPCResult(id, result))
		}(msg.ID, req)
	}
}

func (f *fakeAppRuntime) serveReconcile(msg jsonRPCMessage) {
	var req appReconcileRequest
	if err := json.Unmarshal(msg.Params, &req); err != nil {
		f.sendRaw(jsonRPCFailure(msg.ID, jsonRPCInvalidRequest, err.Error()))
		return
	}
	f.mu.Lock()
	f.reconciles = append(f.reconciles, req)
	f.mu.Unlock()
	f.sendRaw(jsonRPCMessage{
		JSONRPC: "2.0", Method: appRuntimeEnteredMethod,
		Params: mustMarshalHandlerParams(f.t, appRuntimeHandlerParams{Dispatch: req.Dispatch, App: req.App}),
	})
	go func(id json.RawMessage, req appReconcileRequest) {
		result := appDispatchResult{OK: true}
		if f.reconcile != nil {
			if err := f.reconcile(f, req); err != nil {
				result = appDispatchResult{OK: false, Error: err.Error()}
			}
		}
		f.sendRaw(jsonRPCMessage{
			JSONRPC: "2.0", Method: appRuntimeLeftMethod,
			Params: mustMarshalHandlerParams(f.t, appRuntimeHandlerParams{Dispatch: req.Dispatch, App: req.App}),
		})
		f.sendRaw(jsonRPCResult(id, result))
	}(msg.ID, req)
}

func (f *fakeAppRuntime) serveCommand(msg jsonRPCMessage) {
	var req appCommandRequest
	if err := json.Unmarshal(msg.Params, &req); err != nil {
		f.sendRaw(jsonRPCFailure(msg.ID, jsonRPCInvalidRequest, err.Error()))
		return
	}
	f.mu.Lock()
	f.commands = append(f.commands, req)
	f.mu.Unlock()
	f.sendRaw(jsonRPCMessage{
		JSONRPC: "2.0",
		Method:  appRuntimeEnteredMethod,
		Params:  mustMarshalHandlerParams(f.t, appRuntimeHandlerParams{Dispatch: req.Dispatch, App: req.App}),
	})
	go func(id json.RawMessage, req appCommandRequest) {
		result := appCommandDispatchResult{OK: true}
		if f.command != nil {
			payload, err := f.command(f, req)
			if err != nil {
				result = appCommandDispatchResult{OK: false, Error: err.Error()}
			} else {
				result = appCommandDispatchResult{OK: true, Payload: payload}
			}
		}
		f.sendRaw(jsonRPCMessage{
			JSONRPC: "2.0",
			Method:  appRuntimeLeftMethod,
			Params:  mustMarshalHandlerParams(f.t, appRuntimeHandlerParams{Dispatch: req.Dispatch, App: req.App}),
		})
		f.sendRaw(jsonRPCResult(id, result))
	}(msg.ID, req)
}

func (f *fakeAppRuntime) commandLog() []appCommandRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]appCommandRequest, len(f.commands))
	copy(out, f.commands)
	return out
}

func (f *fakeAppRuntime) reconcileLog() []appReconcileRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]appReconcileRequest, len(f.reconciles))
	copy(out, f.reconciles)
	return out
}

func mustMarshalHandlerParams(t *testing.T, params appRuntimeHandlerParams) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal handler-movement params: %v", err)
	}
	return data
}

func (f *fakeAppRuntime) sendRaw(msg jsonRPCMessage) {
	data, err := json.Marshal(msg)
	if err != nil {
		return
	}
	f.writeMu.Lock()
	defer f.writeMu.Unlock()
	_, _ = f.conn.Write(append(data, '\n'))
}

func (f *fakeAppRuntime) call(method string, params any) (json.RawMessage, error) {
	f.mu.Lock()
	f.nextID++
	id := json.RawMessage(fmt.Sprintf(`"cb-%d"`, f.nextID))
	answer := make(chan jsonRPCMessage, 1)
	f.pending[jsonRPCIDKey(id)] = answer
	f.mu.Unlock()

	body, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	f.sendRaw(jsonRPCMessage{JSONRPC: "2.0", ID: id, Method: method, Params: body})

	select {
	case msg, ok := <-answer:
		if !ok {
			return nil, errors.New("the connection closed before the daemon answered")
		}
		if msg.Error != nil {
			return nil, errors.New(msg.Error.Message)
		}
		return msg.Result, nil
	case <-time.After(5 * time.Second):
		return nil, fmt.Errorf("the daemon never answered %s", method)
	}
}

func (f *fakeAppRuntime) dispatchLog() []appDispatchRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]appDispatchRequest, len(f.dispatches))
	copy(out, f.dispatches)
	return out
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal %T: %v", v, err)
	}
	return data
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func newAppDaemon(t *testing.T) *Daemon {
	t.Helper()
	d := newDaemonForTest(t)
	if err := d.eventBus.Start(); err != nil {
		t.Fatalf("start the event bus: %v", err)
	}
	// Registered first so it runs LAST: a sidecar or a parked handler released by a
	// later cleanup has to be gone before the bus stops waiting on it.
	t.Cleanup(d.stopEventBus)
	return d
}

func installApp(t *testing.T, d *Daemon, name string, manifest appbuild.Manifest) store.AppVersion {
	t.Helper()
	manifest.Name = name
	if manifest.AttnAppAPI == 0 {
		manifest.AttnAppAPI = appbuild.APIVersion
	}
	if manifest.Entrypoint == "" {
		manifest.Entrypoint = "src/index.ts"
	}
	declaration, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal declaration: %v", err)
	}
	now := time.Now().UTC()
	if err := d.store.SaveApp(name, now); err != nil {
		t.Fatalf("save app %s: %v", name, err)
	}
	hash := fmt.Sprintf("sha256:%s-%x", name, len(declaration))
	version, _, err := d.store.CommitAppVersion(store.AppVersion{
		AppName:      name,
		ContentHash:  hash,
		Declaration:  string(declaration),
		ArtifactPath: filepath.Join("apps", name, hash+".js"),
	}, now)
	if err != nil {
		t.Fatalf("commit version for %s: %v", name, err)
	}
	if err := d.store.SetAppCurrentVersion(name, version.ID, now); err != nil {
		t.Fatalf("point %s at version %d: %v", name, version.ID, err)
	}
	d.syncAppRuntimeForVersion(name)
	return version
}

func subscribing(events ...string) appbuild.Manifest {
	m := subscribingWithoutReconcile(events...)
	m.Reconcile = true
	return m
}

func subscribingWithoutReconcile(events ...string) appbuild.Manifest {
	return appbuild.Manifest{Subscribe: []appbuild.Subscribe{{Events: events}}}
}

func settleAppReconcile(t *testing.T, d *Daemon, name string) {
	t.Helper()
	hook := d.appPreDrain(name)
	if err := hook(context.Background(), bus.Consumer{Name: apps.ConsumerName(name)}, nil); err != nil {
		t.Fatalf("reconcile %s: %v", name, err)
	}
}

func appEvent(name, subject string, seq int64) bus.Event {
	return bus.Event{Name: name, Subject: subject, Seq: seq, CreatedAt: time.Now().UTC()}
}

func invocationsOf(t *testing.T, d *Daemon, name string) []store.AppInvocation {
	t.Helper()
	rows, err := d.store.ListAppInvocations(name, 50)
	if err != nil {
		t.Fatalf("list invocations for %s: %v", name, err)
	}
	return rows
}

func TestAppConsumerDispatchesAndAdvancesItsCursor(t *testing.T) {
	d := newAppDaemon(t)
	version := installApp(t, d, "greeter", subscribing("ticket.*"))
	runtime := startFakeAppRuntime(t, d, nil)

	d.publishFact("ticket.created", "tk-1", map[string]string{"title": "work"})

	waitFor(t, "the handler to be dispatched", func() bool { return len(runtime.dispatchLog()) == 1 })
	got := runtime.dispatchLog()[0]
	if got.App != "greeter" || got.Handler != "ticket.*" {
		t.Fatalf("dispatch = app %q handler %q, want greeter/ticket.*", got.App, got.Handler)
	}
	if got.VersionID != version.ID {
		t.Fatalf("dispatch carried version %d, want the RUNNING version %d", got.VersionID, version.ID)
	}
	if got.Event.Name != "ticket.created" || got.Event.Subject != "tk-1" {
		t.Fatalf("dispatch event = %+v", got.Event)
	}
	if got.Artifact == "" {
		t.Fatal("dispatch carried no artifact path, so the host has nothing to import")
	}

	waitFor(t, "the invocation to be recorded", func() bool { return len(invocationsOf(t, d, "greeter")) == 1 })
	inv := invocationsOf(t, d, "greeter")[0]
	if inv.Status != appInvocationStatusOK {
		t.Fatalf("status = %q (%s), want ok", inv.Status, inv.Error)
	}
	if inv.VersionID != version.ID {
		t.Fatalf("invocation stamped version %d, want %d", inv.VersionID, version.ID)
	}

	waitFor(t, "the cursor to advance past the delivered event", func() bool {
		consumer, ok, err := d.store.GetBusConsumer(apps.ConsumerName("greeter"))
		return err == nil && ok && consumer.Cursor >= got.Event.Seq
	})
}

func TestAppHandlerThrowRecordsFailureAndKeepsTheEvent(t *testing.T) {
	d := newAppDaemon(t)
	installApp(t, d, "greeter", subscribing("ticket.*"))
	startFakeAppRuntime(t, d, func(_ *fakeAppRuntime, _ appDispatchRequest) error {
		return errors.New("TypeError: cannot read properties of undefined\n    at handle (index.ts:4:11)")
	})

	err := d.deliverAppEvent(context.Background(), "greeter", appEvent("ticket.created", "tk-1", 7))
	if err == nil {
		t.Fatal("a thrown handler returned no error, so the bus would advance past the event")
	}
	if !strings.Contains(err.Error(), "TypeError") {
		t.Fatalf("the bus was told %q, which does not name what threw", err)
	}

	rows := invocationsOf(t, d, "greeter")
	if len(rows) != 1 {
		t.Fatalf("recorded %d invocation(s), want 1", len(rows))
	}
	if rows[0].Status != appInvocationStatusError {
		t.Fatalf("status = %q, want %q", rows[0].Status, appInvocationStatusError)
	}
	if !strings.Contains(rows[0].Error, "at handle (index.ts:4:11)") {
		t.Fatalf("recorded error lost the stack: %q", rows[0].Error)
	}
	stall, ok := d.appStallSnapshot("greeter")
	if !ok || stall.seq != 7 || stall.attempts != 1 {
		t.Fatalf("stall = %+v (present=%t), want seq 7 attempt 1", stall, ok)
	}
}

func TestSidecarDeathIsARuntimeFailureAndDoesNotBlameTheApp(t *testing.T) {
	d := newAppDaemon(t)
	installApp(t, d, "greeter", subscribing("ticket.*"))

	var die atomic.Bool
	startFakeAppRuntime(t, d, func(f *fakeAppRuntime, _ appDispatchRequest) error {
		if die.Load() {
			_ = f.conn.Close()
		}
		return errors.New("boom")
	})
	if err := d.deliverAppEvent(context.Background(), "greeter", appEvent("ticket.created", "tk-1", 3)); err == nil {
		t.Fatal("the throwing handler reported success")
	}
	if _, ok := d.appStallSnapshot("greeter"); !ok {
		t.Fatal("a thrown handler did not start the stall clock")
	}

	die.Store(true)
	err := d.deliverAppEvent(context.Background(), "greeter", appEvent("ticket.created", "tk-1", 3))
	if err == nil {
		t.Fatal("a dispatch into a dead sidecar reported success")
	}
	if !isRuntimeFailure(err) {
		t.Fatalf("error %q was not classified as the runtime's", err)
	}

	rows := invocationsOf(t, d, "greeter")
	if len(rows) != 2 {
		t.Fatalf("recorded %d invocation(s), want 2", len(rows))
	}
	if rows[0].Status != appInvocationStatusRuntimeError {
		t.Fatalf("status = %q, want %q — a dead sidecar is not the app's fault",
			rows[0].Status, appInvocationStatusRuntimeError)
	}
	if stall, ok := d.appStallSnapshot("greeter"); ok {
		t.Fatalf("the runtime dying left the app on the auto-disable clock: %+v", stall)
	}
}

func TestMissingRuntimeBinaryIsARuntimeFailure(t *testing.T) {
	d := newAppDaemon(t)
	installApp(t, d, "greeter", subscribing("ticket.*"))
	t.Setenv(appRuntimeHostOverride, filepath.Join(t.TempDir(), "not-installed"))

	err := d.deliverAppEvent(context.Background(), "greeter", appEvent("ticket.created", "tk-1", 1))
	if !isRuntimeFailure(err) {
		t.Fatalf("error %v was not classified as the runtime's", err)
	}
	if stall, ok := d.appStallSnapshot("greeter"); ok {
		t.Fatalf("a missing runtime binary put the app on the auto-disable clock: %+v", stall)
	}
	rows := invocationsOf(t, d, "greeter")
	if len(rows) != 1 || rows[0].Status != appInvocationStatusRuntimeError {
		t.Fatalf("invocations = %+v, want one runtime_error", rows)
	}
	if !strings.Contains(rows[0].Error, appRuntimeHostOverride) {
		t.Fatalf("the recorded error does not say how to point attn at a runtime: %q", rows[0].Error)
	}
}

func TestCancelledDeliveryReturnsPromptlyAndRecordsNothing(t *testing.T) {
	d := newAppDaemon(t)
	installApp(t, d, "greeter", subscribing("ticket.*"))

	entered := make(chan struct{})
	release := make(chan struct{})
	// LIFO cleanup: registered after the harness own so it runs FIRST, or a failing
	// assert below leaves the handler parked forever (#793).
	t.Cleanup(func() { close(release) })
	startFakeAppRuntime(t, d, func(_ *fakeAppRuntime, _ appDispatchRequest) error {
		close(entered)
		<-release
		return nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- d.deliverAppEvent(ctx, "greeter", appEvent("ticket.created", "tk-1", 1)) }()
	<-entered
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("a cancelled delivery returned %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a cancelled delivery did not return; removing an app would hang on its in-flight handler")
	}
	if rows := invocationsOf(t, d, "greeter"); len(rows) != 0 {
		t.Fatalf("a cancelled delivery recorded %d invocation(s): %+v", len(rows), rows)
	}
}

func TestRemovingAnAppWithAnInFlightDispatchReturnsPromptly(t *testing.T) {
	d := newAppDaemon(t)
	installApp(t, d, "greeter", subscribing("ticket.*"))

	entered := make(chan struct{})
	release := make(chan struct{})
	// LIFO: this has to run FIRST, so a failed assert releases the handler instead
	// of reading as a hang (#793).
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	var once sync.Once
	startFakeAppRuntime(t, d, func(*fakeAppRuntime, appDispatchRequest) error {
		once.Do(func() { close(entered) })
		<-release
		return nil
	})

	d.publishFact("ticket.created", "tk-1", nil)
	<-entered

	removed := make(chan protocol.Response, 1)
	go func() { removed <- appRemove(t, d, "greeter") }()
	select {
	case resp := <-removed:
		if !resp.Ok {
			t.Fatalf("app remove: %v", protocol.Deref(resp.Error))
		}
		if !resp.AppRemoveResult.ConsumerRemoved {
			t.Fatal("remove did not report deleting the consumer")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("app remove hung on an in-flight handler")
	}

	if rows := invocationsOf(t, d, "greeter"); len(rows) != 0 {
		t.Fatalf("an interrupted delivery recorded %d invocation(s): %+v", len(rows), rows)
	}
	if _, ok, err := d.store.GetBusConsumer(apps.ConsumerName("greeter")); err != nil || ok {
		t.Fatalf("the consumer row survived the remove (ok=%t, err=%v)", ok, err)
	}
}

func TestALateAnswerWithNobodyWaitingIsDropped(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()
	// net.Pipe is unbuffered: the far end has to be drained or the peer write
	// blocks.
	go func() { _, _ = io.Copy(io.Discard, clientConn) }()
	peer := newJSONRPCPeer(serverConn, bufio.NewReader(serverConn))

	if routed := peer.routeResponse(jsonRPCMessage{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`"gone"`),
		Result:  json.RawMessage(`{"ok":true}`),
	}); routed {
		t.Fatal("an answer nobody was waiting for was reported as routed")
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		waitFor(t, "the abandoned request to go out", func() bool {
			peer.pendingMu.Lock()
			defer peer.pendingMu.Unlock()
			return len(peer.pending) == 1
		})
		cancel()
	}()
	var out appDispatchResult
	if err := peer.request(ctx, "app runtime", "app.dispatch", appDispatchRequest{}, &out); !errors.Is(err, context.Canceled) {
		t.Fatalf("a request whose caller gave up returned %v, want context.Canceled", err)
	}
	if routed := peer.routeResponse(jsonRPCMessage{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`1`),
		Result:  json.RawMessage(`{"ok":true}`),
	}); routed {
		t.Fatal("the abandoned request was still holding its slot in the pending map")
	}
}

func TestCollectionCallbackAfterTheHandlerReturnedIsRefused(t *testing.T) {
	d := newAppDaemon(t)
	installApp(t, d, "greeter", appbuild.Manifest{
		Subscribe:   []appbuild.Subscribe{{Events: []string{"ticket.*"}}},
		Collections: []appbuild.Collection{{Name: "seen"}},
	})

	var escaped string
	runtime := startFakeAppRuntime(t, d, func(f *fakeAppRuntime, req appDispatchRequest) error {
		escaped = req.Dispatch
		return nil
	})
	if err := d.deliverAppEvent(context.Background(), "greeter", appEvent("ticket.created", "tk-1", 1)); err != nil {
		t.Fatalf("deliver: %v", err)
	}

	_, err := runtime.call("app.collection.get", appCollectionParams{
		Dispatch: escaped, Collection: "seen", ID: "tk-1",
	})
	if err == nil {
		t.Fatal("a collection call from a finished handler was served")
	}
	if !strings.Contains(err.Error(), "after that handler returned") {
		t.Fatalf("the refusal does not say what went wrong: %q", err)
	}
}

func TestAppCannotReachACollectionItDidNotDeclare(t *testing.T) {
	d := newAppDaemon(t)
	installApp(t, d, "neighbour", appbuild.Manifest{
		Subscribe:   []appbuild.Subscribe{{Events: []string{"ticket.*"}}},
		Collections: []appbuild.Collection{{Name: "secrets"}},
	})
	installApp(t, d, "greeter", appbuild.Manifest{
		Subscribe:   []appbuild.Subscribe{{Events: []string{"ticket.*"}}},
		Collections: []appbuild.Collection{{Name: "seen"}},
	})

	var refusal error
	var wrote appDocument
	runtime := startFakeAppRuntime(t, d, func(f *fakeAppRuntime, req appDispatchRequest) error {
		raw, err := f.call("app.collection.put", appCollectionParams{
			Dispatch: req.Dispatch, Collection: "seen", ID: "tk-1",
			Body: json.RawMessage(`{"note":"mine"}`),
		})
		if err != nil {
			return err
		}
		if err := json.Unmarshal(raw, &wrote); err != nil {
			return err
		}
		_, refusal = f.call("app.collection.get", appCollectionParams{
			Dispatch: req.Dispatch, Collection: "secrets", ID: "anything",
		})
		return nil
	})
	_ = runtime

	if err := d.deliverAppEvent(context.Background(), "greeter", appEvent("ticket.created", "tk-1", 1)); err != nil {
		t.Fatalf("deliver: %v", err)
	}
	if wrote.ID != "tk-1" {
		t.Fatalf("the app could not write its own collection: %+v", wrote)
	}
	if refusal == nil {
		t.Fatal("greeter read a collection belonging to neighbour")
	}
	if !strings.Contains(refusal.Error(), "did not declare a collection") {
		t.Fatalf("the refusal does not teach: %q", refusal)
	}

	read, declared, err := d.store.ReadDocument(apps.Namespace("greeter"), "seen", "tk-1")
	if err != nil || !declared || !read.Found {
		t.Fatalf("document not in %s: declared=%t found=%t err=%v", apps.Namespace("greeter"), declared, read.Found, err)
	}
}

func TestHotReloadStampsTheNewVersionOnTheNextDispatch(t *testing.T) {
	d := newAppDaemon(t)
	first := installApp(t, d, "greeter", subscribing("ticket.*"))

	inFlight := make(chan struct{})
	release := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	runtime := startFakeAppRuntime(t, d, func(_ *fakeAppRuntime, req appDispatchRequest) error {
		if req.VersionID == first.ID {
			close(inFlight)
			<-release
		}
		return nil
	})

	done := make(chan error, 1)
	go func() {
		done <- d.deliverAppEvent(context.Background(), "greeter", appEvent("ticket.created", "tk-1", 1))
	}()
	<-inFlight

	second := installApp(t, d, "greeter", subscribing("ticket.*", "session.*"))
	if second.ID == first.ID {
		t.Fatal("the second apply produced the same version, so this proves nothing")
	}

	close(release)
	if err := <-done; err != nil {
		t.Fatalf("the in-flight delivery failed: %v", err)
	}
	settleAppReconcile(t, d, "greeter")
	if err := d.deliverAppEvent(context.Background(), "greeter", appEvent("ticket.created", "tk-2", 2)); err != nil {
		t.Fatalf("the second delivery failed: %v", err)
	}

	log := runtime.dispatchLog()
	if len(log) != 2 {
		t.Fatalf("dispatches = %d, want 2", len(log))
	}
	if log[0].VersionID != first.ID {
		t.Fatalf("the in-flight dispatch was re-stamped to %d, want the OLD version %d", log[0].VersionID, first.ID)
	}
	if log[1].VersionID != second.ID {
		t.Fatalf("the next dispatch used version %d, want the NEW version %d", log[1].VersionID, second.ID)
	}
	if log[0].Artifact == log[1].Artifact {
		t.Fatalf("both versions resolved to the same artifact %q, so import() would hand back the old module", log[0].Artifact)
	}
	consumer, ok, err := d.store.GetBusConsumer(apps.ConsumerName("greeter"))
	if err != nil || !ok {
		t.Fatalf("consumer: %v ok=%t", err, ok)
	}
	if !strings.Contains(consumer.Filter, "session.*") {
		t.Fatalf("filter = %q, want the new version's subscriptions", consumer.Filter)
	}
}

func TestUnhandledFactAdvancesRatherThanStalling(t *testing.T) {
	d := newAppDaemon(t)
	installApp(t, d, "greeter", subscribing("ticket.*"))
	runtime := startFakeAppRuntime(t, d, nil)

	if err := d.deliverAppEvent(context.Background(), "greeter", appEvent("session.state.changed", "s-1", 1)); err != nil {
		t.Fatalf("an unhandled fact stalled the consumer: %v", err)
	}
	if got := len(runtime.dispatchLog()); got != 0 {
		t.Fatalf("an unhandled fact produced %d dispatch(es)", got)
	}
	if rows := invocationsOf(t, d, "greeter"); len(rows) != 0 {
		t.Fatalf("an unhandled fact recorded %d invocation(s)", len(rows))
	}
}

func TestHandlerResolutionPrefersTheMostSpecificSubscription(t *testing.T) {
	patterns := []string{"*", "session.*", "session.state.changed", "ticket.*"}
	for _, tc := range []struct{ event, want string }{
		{"session.state.changed", "session.state.changed"},
		{"session.spawned", "session.*"},
		{"ticket.created", "ticket.*"},
		{"pr.merged", "*"},
	} {
		if got := resolveAppHandler(patterns, tc.event); got != tc.want {
			t.Errorf("resolveAppHandler(%q) = %q, want %q", tc.event, got, tc.want)
		}
	}
	if got := resolveAppHandler([]string{"ticket.*"}, "session.spawned"); got != "" {
		t.Errorf("an unmatched fact resolved to handler %q", got)
	}
}

// bus.ParseFilter reads an empty expression as All, so an app with no
// subscriptions needs its own nothing-matches pattern.
func TestAppWithNoSubscriptionsSubscribesToNothing(t *testing.T) {
	d := newAppDaemon(t)
	installApp(t, d, "quiet", appbuild.Manifest{})

	filter, err := d.appFilter("quiet")
	if err != nil {
		t.Fatalf("appFilter: %v", err)
	}
	if len(filter) != 1 || filter[0] != apps.NoSubscriptionsPattern {
		t.Fatalf("filter = %v, want the nothing-matches pattern", filter)
	}
	for _, name := range []string{"ticket.created", "session.state.changed", "app.enabled.changed"} {
		if bus.MatchPattern(filter[0], name) {
			t.Fatalf("an app that declared no subscriptions would be woken by %s", name)
		}
	}
}

func writeExecutableStub(t *testing.T, script string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "attn-app-runtime")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatalf("write runtime stub: %v", err)
	}
	return path
}

func appRuntimeStatus(t *testing.T, d *Daemon) *protocol.AppRuntimeStatusResult {
	t.Helper()
	resp := docCall(t, func(c net.Conn) {
		d.handleAppRuntimeStatus(c, &protocol.AppRuntimeStatusMessage{Cmd: protocol.CmdAppRuntimeStatus})
	})
	if !resp.Ok {
		t.Fatalf("app runtime status: %v", protocol.Deref(resp.Error))
	}
	return resp.AppRuntimeStatusResult
}

func appRuntimeRestart(t *testing.T, d *Daemon) *protocol.AppRuntimeRestartResult {
	t.Helper()
	resp := docCall(t, func(c net.Conn) {
		d.handleAppRuntimeRestart(c, &protocol.AppRuntimeRestartMessage{Cmd: protocol.CmdAppRuntimeRestart})
	})
	if !resp.Ok {
		t.Fatalf("app runtime restart: %v", protocol.Deref(resp.Error))
	}
	return resp.AppRuntimeRestartResult
}

func TestRuntimeStatusIsHonestBeforeAnythingHasStarted(t *testing.T) {
	d := newAppDaemon(t)
	installApp(t, d, "greeter", subscribing("ticket.*"))
	host := writeExecutableStub(t, "sleep 60")
	t.Setenv(appRuntimeHostOverride, host)

	before := appRuntimeStatus(t, d)
	if before.Runtime != nil {
		t.Fatalf("a daemon that has never run an app reported a runtime: %+v", before.Runtime)
	}
	if before.HostPath == nil || *before.HostPath != host {
		t.Fatalf("host path = %v, want %s", before.HostPath, host)
	}
	if before.Apps != 1 || before.AppsEnabled != 1 {
		t.Fatalf("apps = %d installed / %d enabled, want 1/1", before.Apps, before.AppsEnabled)
	}
	if before.LogPath != AppRuntimeLogPath(d.socketPath) {
		t.Fatalf("log path = %q, want %q", before.LogPath, AppRuntimeLogPath(d.socketPath))
	}

	t.Cleanup(d.stopAppRuntime)
	started := appRuntimeRestart(t, d)
	if started.Was != "stopped" {
		t.Fatalf("was = %q, want stopped", started.Was)
	}
	if started.Runtime.Desired != "running" {
		t.Fatalf("after a restart the runtime is %+v, want desired running", started.Runtime)
	}
	if after := appRuntimeStatus(t, d); after.Runtime == nil {
		t.Fatal("status still reports no runtime after one was started")
	}
}

func TestParkedRuntimeIsVisibleOnEveryAppAndRevivable(t *testing.T) {
	d := newAppDaemon(t)
	d.appRuntimeSupervise = supervise.Options{GiveUpAfter: 1}
	installApp(t, d, "greeter", subscribing("ticket.*"))
	installApp(t, d, "auditor", subscribing("pr.*"))
	t.Setenv(appRuntimeHostOverride, writeExecutableStub(t, "exit 3"))
	t.Cleanup(d.stopAppRuntime)

	if err := d.ensureAppRuntime(); err != nil {
		t.Fatalf("ensure runtime: %v", err)
	}
	waitFor(t, "the crash-looping runtime to be parked", func() bool {
		snapshot, ok := d.appRuntimeSnapshot()
		return ok && snapshot.Phase == supervise.PhaseParked
	})

	for _, name := range []string{"greeter", "auditor"} {
		resp := appStatus(t, d, name)
		if !resp.Ok {
			t.Fatalf("app status %s: %v", name, protocol.Deref(resp.Error))
		}
		runtime := resp.AppStatusResult.Runtime
		if runtime == nil {
			t.Fatalf("app status %s carried no runtime, so a reader cannot tell why nothing runs", name)
		}
		if runtime.Phase != string(supervise.PhaseParked) {
			t.Fatalf("app status %s reported runtime phase %q, want parked", name, runtime.Phase)
		}
		if runtime.LastExit == nil || !strings.Contains(*runtime.LastExit, "3") {
			t.Fatalf("app status %s does not say how the runtime died: %v", name, runtime.LastExit)
		}
	}

	var parked *store.NotificationRecord
	waitFor(t, "the app-runtime-parked notification to be written", func() bool {
		notifications, err := d.store.ListNotifications()
		if err != nil {
			t.Fatalf("list notifications: %v", err)
		}
		parked = nil
		for i := range notifications {
			if notifications[i].Kind == notificationKindAppRuntimeParked {
				parked = &notifications[i]
			}
		}
		return parked != nil
	})
	if !strings.Contains(parked.Body, "attn app runtime restart") {
		t.Fatalf("the notification does not name the way back: %q", parked.Body)
	}
	if parked.Severity != store.NotificationCritical {
		t.Fatalf("severity = %q, want critical", parked.Severity)
	}
	waitFor(t, "the notification.created fact", func() bool {
		created := appFacts(t, d, FactNotificationCreated)
		return len(created) == 1 && created[0].Subject == parked.ID
	})

	revived := appRuntimeRestart(t, d)
	if revived.Was != string(supervise.PhaseParked) {
		t.Fatalf("was = %q, want parked — that answer is what tells a reader it was revived", revived.Was)
	}
	if revived.Runtime.Phase == string(supervise.PhaseParked) {
		t.Fatalf("restart left the runtime parked: %+v", revived.Runtime)
	}
}

// Measured on a broken host before the split: three parkings and three critical
// notifications in five and a half minutes.
func TestDispatchLeavesAParkedRuntimeParked(t *testing.T) {
	d := newAppDaemon(t)
	d.appRuntimeSupervise = supervise.Options{GiveUpAfter: 1}
	installApp(t, d, "greeter", subscribing("ticket.*"))
	t.Setenv(appRuntimeHostOverride, writeExecutableStub(t, "exit 3"))
	t.Cleanup(d.stopAppRuntime)

	if err := d.ensureAppRuntime(); err != nil {
		t.Fatalf("ensure runtime: %v", err)
	}
	// Wait on the notification, not on the phase: the supervisor sets PhaseParked under its
	// lock and releases it before running the OnGiveUp sink that writes this.
	waitFor(t, "the crash-looping runtime to be parked", func() bool {
		return len(appNotifications(t, d, notificationKindAppRuntimeParked)) > 0
	})
	parked, ok := d.appRuntimeSnapshot()
	if !ok || parked.Phase != supervise.PhaseParked {
		t.Fatalf("the runtime notified a park without being parked: %+v", parked)
	}

	for seq := int64(1); seq <= 3; seq++ {
		err := d.deliverAppEvent(context.Background(), "greeter", appEvent("ticket.created", "tk-1", seq))
		if !isRuntimeFailure(err) {
			t.Fatalf("dispatch %d into a parked runtime returned %v, want a runtime failure", seq, err)
		}
		if !strings.Contains(err.Error(), "parked") || !strings.Contains(err.Error(), "attn app runtime restart") {
			t.Fatalf("the dispatch error does not say what happened or how to fix it: %q", err)
		}
	}

	after, ok := d.appRuntimeSnapshot()
	if !ok || after.Phase != supervise.PhaseParked {
		t.Fatalf("three dispatches moved the runtime off parked: %+v", after)
	}
	if after.Generation != parked.Generation {
		t.Fatalf("generation went %d → %d, so a dispatch started the runtime again",
			parked.Generation, after.Generation)
	}
	if notes := appNotifications(t, d, notificationKindAppRuntimeParked); len(notes) != 1 {
		t.Fatalf("app-runtime-parked notifications = %d, want 1", len(notes))
	}
	rows := invocationsOf(t, d, "greeter")
	if len(rows) != 3 {
		t.Fatalf("recorded %d invocation(s), want 3", len(rows))
	}
	for _, row := range rows {
		if row.Status != appInvocationStatusRuntimeError {
			t.Fatalf("status = %q, want %q", row.Status, appInvocationStatusRuntimeError)
		}
	}
	if stall, ok := d.appStallSnapshot("greeter"); ok {
		t.Fatalf("a parked runtime put the app on the auto-disable clock: %+v", stall)
	}

	if revived := appRuntimeRestart(t, d); revived.Runtime.Phase == string(supervise.PhaseParked) {
		t.Fatalf("restart left the runtime parked: %+v", revived.Runtime)
	}
}

func TestRuntimeWithTheWrongAPIVersionIsRefusedAtHello(t *testing.T) {
	_, _, recognized, err := parseAppRuntimeHello([]byte(
		`{"jsonrpc":"2.0","id":"1","method":"app_runtime.hello","params":{"generation":1,"api_version":99,"pid":7}}`))
	if !recognized {
		t.Fatal("the app runtime hello was not recognized as one")
	}
	if err == nil {
		t.Fatal("a runtime speaking api version 99 was accepted")
	}
	if !strings.Contains(err.Error(), "stale install") {
		t.Fatalf("the refusal does not say what to do: %q", err)
	}
}

func TestAppRuntimeHelloSniffIgnoresEverythingElse(t *testing.T) {
	for _, frame := range []string{
		`{"jsonrpc":"2.0","id":"1","method":"hello","params":{"name":"worktree-provider"}}`,
		`{"cmd":"heartbeat","id":"sess"}`,
		`not json at all`,
	} {
		if _, _, recognized, _ := parseAppRuntimeHello([]byte(frame)); recognized {
			t.Fatalf("the app runtime sniff claimed %q", frame)
		}
	}
}

func TestAppRuntimeHostCandidates(t *testing.T) {
	bundled := appRuntimeHostCandidates("/Applications/attn.app/Contents/MacOS/attn", "")
	if len(bundled) != 2 || bundled[0] != "/Applications/attn.app/Contents/Resources/app-runtime/attn-app-runtime" {
		t.Fatalf("bundled candidates = %v", bundled)
	}

	remote := appRuntimeHostCandidates("/home/v/.local/bin/attn-dev", "dev")
	want := []string{
		"/home/v/.local/bin/attn-app-runtime-dev",
		"/home/v/.local/bin/attn-app-runtime",
	}
	if len(remote) != len(want) {
		t.Fatalf("profile candidates = %v, want %v", remote, want)
	}
	for i := range want {
		if remote[i] != want[i] {
			t.Fatalf("profile candidates = %v, want %v", remote, want)
		}
	}

	checkout := appRuntimeHostCandidates("/src/attn/attn", "dev")
	if checkout[len(checkout)-1] != "/src/attn/attn-app-runtime" {
		t.Fatalf("checkout candidates = %v", checkout)
	}
}
