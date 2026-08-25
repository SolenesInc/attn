package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/store"
)

const appRuntimeHelloMethod = "app_runtime.hello"

type appRuntimeHelloParams struct {
	Generation uint64 `json:"generation"`
	APIVersion int    `json:"api_version"`
	PID        int    `json:"pid"`
}

type appRuntimeHelloResult struct {
	OK bool `json:"ok"`
}

type appRuntimeConnection struct {
	*jsonrpcPeer

	generation  uint64
	pid         int
	connectedAt time.Time
}

func (c *appRuntimeConnection) dispatch(ctx context.Context, req appDispatchRequest) (appDispatchResult, error) {
	var result appDispatchResult
	if err := c.request(ctx, "app runtime", "app.dispatch", req, &result); err != nil {
		return appDispatchResult{}, err
	}
	return result, nil
}

func (c *appRuntimeConnection) command(ctx context.Context, req appCommandRequest) (appCommandDispatchResult, error) {
	var result appCommandDispatchResult
	if err := c.request(ctx, "app runtime", "app.command", req, &result); err != nil {
		return appCommandDispatchResult{}, err
	}
	return result, nil
}

func (c *appRuntimeConnection) reconcile(ctx context.Context, req appReconcileRequest) (appDispatchResult, error) {
	var result appDispatchResult
	if err := c.request(ctx, "app runtime", "app.reconcile", req, &result); err != nil {
		return appDispatchResult{}, err
	}
	return result, nil
}

// Served without touching app code, so a silent ping means the loop is blocked.
type appRuntimePingResult struct {
	OK bool `json:"ok"`
}

func (c *appRuntimeConnection) ping(ctx context.Context) error {
	var result appRuntimePingResult
	if err := c.request(ctx, "app runtime", "app.runtime.ping", struct{}{}, &result); err != nil {
		return err
	}
	if !result.OK {
		return errors.New("the app runtime answered a ping without ok")
	}
	return nil
}

// Runs before the plugin hello sniff, which would refuse this frame with "first
// plugin method must be hello" — a true sentence about the wrong protocol.
func parseAppRuntimeHello(data []byte) (json.RawMessage, appRuntimeHelloParams, bool, error) {
	var msg jsonRPCMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		return nil, appRuntimeHelloParams{}, false, nil
	}
	if msg.Method != appRuntimeHelloMethod {
		return nil, appRuntimeHelloParams{}, false, nil
	}
	if msg.JSONRPC != "2.0" {
		return msg.ID, appRuntimeHelloParams{}, true, errors.New(`jsonrpc must be "2.0"`)
	}
	if jsonRPCIDKey(msg.ID) == "" {
		return msg.ID, appRuntimeHelloParams{}, true, errors.New(appRuntimeHelloMethod + " requires an id")
	}
	var params appRuntimeHelloParams
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		return msg.ID, appRuntimeHelloParams{}, true, fmt.Errorf("decode %s params: %w", appRuntimeHelloMethod, err)
	}
	if params.Generation == 0 {
		return msg.ID, appRuntimeHelloParams{}, true, errors.New("params.generation is required; the supervisor fences stale runtimes by generation")
	}
	if params.APIVersion != appRuntimeAPIVersion {
		return msg.ID, appRuntimeHelloParams{}, true, fmt.Errorf(
			"app runtime api version %d, but this daemon speaks %d; the runtime binary and the daemon ship together, so this is a stale install rather than a configuration to change",
			params.APIVersion, appRuntimeAPIVersion)
	}
	return msg.ID, params, true, nil
}

func (d *Daemon) handleAppRuntimeConnection(conn net.Conn, reader *bufio.Reader, helloID json.RawMessage, params appRuntimeHelloParams) {
	runtime := &appRuntimeConnection{
		jsonrpcPeer: newJSONRPCPeer(conn, reader),
		generation:  params.Generation,
		pid:         params.PID,
		connectedAt: d.appNow(),
	}
	if !d.ensureAppRuntimeSupervisor().NoteConnected(appRuntimeChildName, runtime.generation) {
		_ = runtime.send(jsonRPCFailure(helloID, jsonRPCInvalidRequest,
			"this app runtime's generation is no longer current; a newer one has already been started"))
		return
	}
	d.setAppRuntimeConnection(runtime)
	defer func() {
		// Before the connection stops being reachable, so a replacement's NoteConnected
		// cancels the grace timer instead of an old defer arming it afterwards.
		d.ensureAppRuntimeSupervisor().NoteDisconnected(appRuntimeChildName, runtime.generation)
		d.clearAppRuntimeConnection(runtime)
		// Otherwise every parked dispatch waits out its whole timeout.
		runtime.closePending(io.EOF)
	}()

	if err := runtime.send(jsonRPCResult(helloID, appRuntimeHelloResult{OK: true})); err != nil {
		return
	}
	d.logf("app runtime connected (generation %d, pid %d)", runtime.generation, runtime.pid)
	d.publishFact(FactAppRuntimeChanged, appRuntimeChildName, nil)

	for {
		data, err := readSocketFrame(reader)
		if err != nil {
			return
		}
		var msg jsonRPCMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			_ = runtime.send(jsonRPCFailure(nil, jsonRPCParseError, "parse JSON-RPC message"))
			continue
		}
		if msg.JSONRPC != "2.0" {
			_ = runtime.send(jsonRPCFailure(msg.ID, jsonRPCInvalidRequest, `jsonrpc must be "2.0"`))
			continue
		}
		if msg.Method == "" {
			if !runtime.routeResponse(msg) {
				d.logf("app runtime: answer to request %s arrived with nobody waiting", jsonRPCIDKey(msg.ID))
			}
			continue
		}
		if msg.Method == appRuntimeEnteredMethod || msg.Method == appRuntimeLeftMethod {
			// On the read loop on purpose: it is a map write whose whole value is the
			// order it arrives in.
			d.appRuntimeHandlerMoved(runtime, msg)
			continue
		}
		// Off the read loop: a collection read for one app must not hold up another's.
		go d.serveAppRuntimeMethod(runtime, msg)
	}
}

func (d *Daemon) setAppRuntimeConnection(runtime *appRuntimeConnection) {
	d.appRuntimeMu.Lock()
	d.appRuntimeConn = runtime
	if d.appRuntimeReady != nil {
		close(d.appRuntimeReady)
		d.appRuntimeReady = nil
	}
	d.appRuntimeMu.Unlock()
}

func (d *Daemon) clearAppRuntimeConnection(runtime *appRuntimeConnection) {
	d.appRuntimeMu.Lock()
	if d.appRuntimeConn == runtime {
		d.appRuntimeConn = nil
	}
	d.appRuntimeMu.Unlock()
	d.forgetEnteredHandlers()
	d.publishFact(FactAppRuntimeChanged, appRuntimeChildName, nil)
}

func (d *Daemon) serveAppRuntimeMethod(runtime *appRuntimeConnection, msg jsonRPCMessage) {
	if jsonRPCIDKey(msg.ID) == "" {
		_ = runtime.send(jsonRPCFailure(msg.ID, jsonRPCInvalidRequest, "app runtime method calls require an id"))
		return
	}
	result, err := d.appRuntimeMethod(msg)
	if err != nil {
		_ = runtime.send(jsonRPCFailure(msg.ID, jsonRPCInvalidRequest, err.Error()))
		return
	}
	_ = runtime.send(jsonRPCResult(msg.ID, result))
}

const appRuntimeCrashedMethod = "app_runtime.crashed"

type appRuntimeCrashParams struct {
	App   string `json:"app"`
	Kind  string `json:"kind"`
	Error string `json:"error"`
}

func (d *Daemon) appRuntimeCrashed(msg jsonRPCMessage) (any, error) {
	var params appRuntimeCrashParams
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		return nil, fmt.Errorf("decode %s params: %w", appRuntimeCrashedMethod, err)
	}
	if params.App == "" {
		d.logf("app runtime: crashing on an unhandled %s that names no app: %s",
			params.Kind, firstLine(params.Error))
		return appRuntimeHelloResult{OK: true}, nil
	}
	d.noteAppRuntimeCrash(params.App, params.Kind, params.Error)
	return appRuntimeHelloResult{OK: true}, nil
}

// entered reaches the daemon *before* the handler runs, so it is already on the wire when
// a handler that never yields freezes the loop behind it. See attributeWedgedDispatch.
const (
	appRuntimeEnteredMethod = "app_runtime.entered"
	appRuntimeLeftMethod    = "app_runtime.left"
)

type appRuntimeHandlerParams struct {
	Dispatch string `json:"dispatch"`
	App      string `json:"app"`
}

type enteredHandler struct {
	app   string
	order uint64
}

func (d *Daemon) appRuntimeHandlerMoved(runtime *appRuntimeConnection, msg jsonRPCMessage) {
	var params appRuntimeHandlerParams
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		d.logf("app runtime: decode %s params: %v", msg.Method, err)
		return
	}
	if params.Dispatch == "" || params.App == "" {
		return
	}
	if msg.Method == appRuntimeLeftMethod {
		d.forgetEnteredHandler(params.Dispatch)
		return
	}
	d.noteEnteredHandler(runtime.generation, params.Dispatch, params.App)
}

// Deliberately no namespace: the daemon reads it off the dispatch record, so an
// app cannot name one — its own or anybody else's.
type appCollectionParams struct {
	Dispatch   string          `json:"dispatch"`
	Collection string          `json:"collection"`
	ID         string          `json:"id"`
	Body       json.RawMessage `json:"body"`
	IfRev      *int64          `json:"if_rev"`
	Query      *docstore.Query `json:"query"`
}

type appCurrentStateParams struct {
	Dispatch string `json:"dispatch"`
}

func (d *Daemon) appRuntimeMethod(msg jsonRPCMessage) (any, error) {
	if msg.Method == appRuntimeCrashedMethod {
		return d.appRuntimeCrashed(msg)
	}

	switch msg.Method {
	case "app.current.snapshot":
		var params appCurrentStateParams
		if err := json.Unmarshal(msg.Params, &params); err != nil {
			return nil, fmt.Errorf("decode %s params: %w", msg.Method, err)
		}
		if _, err := d.lookupAppDispatch(params.Dispatch); err != nil {
			return nil, err
		}
		return d.appCurrentStateSnapshot()
	case "app.collection.get", "app.collection.put", "app.collection.delete",
		"app.collection.query", "app.collection.count":
	default:
		return nil, fmt.Errorf("unknown method %q", msg.Method)
	}

	var params appCollectionParams
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		return nil, fmt.Errorf("decode %s params: %w", msg.Method, err)
	}
	dispatch, err := d.lookupAppDispatch(params.Dispatch)
	if err != nil {
		return nil, err
	}
	if _, declared := dispatch.collections[params.Collection]; !declared {
		return nil, fmt.Errorf(
			"app %s did not declare a collection named %q; ctx.collections only carries what attn-app.toml declares, and adding a [[collections]] block plus `attn app apply` is what creates one",
			dispatch.app, params.Collection)
	}

	switch msg.Method {
	case "app.collection.get":
		return d.appCollectionGet(dispatch, params)
	case "app.collection.put":
		return d.appCollectionPut(dispatch, params)
	case "app.collection.delete":
		return d.appCollectionDelete(dispatch, params)
	case "app.collection.query":
		return d.appCollectionQuery(dispatch, params)
	default:
		return d.appCollectionCount(dispatch, params)
	}
}

type appDocument struct {
	ID        string          `json:"id"`
	Body      json.RawMessage `json:"body"`
	Rev       int64           `json:"rev"`
	CreatedAt string          `json:"created_at"`
	UpdatedAt string          `json:"updated_at"`
}

func appDocumentOf(doc docstore.Document) appDocument {
	return appDocument{
		ID:        doc.ID,
		Body:      doc.Body,
		Rev:       doc.Rev,
		CreatedAt: stampForWire(doc.CreatedAt),
		UpdatedAt: stampForWire(doc.UpdatedAt),
	}
}

func (d *Daemon) appCollectionGet(dispatch *appDispatch, params appCollectionParams) (any, error) {
	read, declared, err := d.store.ReadDocument(dispatch.namespace, params.Collection, params.ID)
	if err != nil {
		return nil, err
	}
	if !declared {
		return nil, undeclaredCollectionError(dispatch.namespace, params.Collection)
	}
	if !read.Found {
		// The SDK types this as Document | null, so the absent case is a value
		// rather than a failure.
		return nil, nil
	}
	return appDocumentOf(*read.Document), nil
}

func (d *Daemon) appCollectionPut(dispatch *appDispatch, params appCollectionParams) (any, error) {
	schema, err := d.collectionFor(dispatch.namespace, params.Collection)
	if err != nil {
		return nil, err
	}
	if err := docstore.ValidateDocumentID(params.ID); err != nil {
		return nil, err
	}
	if err := docstore.ValidateBody(params.Body); err != nil {
		return nil, err
	}
	fact := documentChangedFact(dispatch.namespace, params.Collection, params.ID, false)
	written, err := d.store.CommitDocumentWrite(store.DocumentWrite{
		Schema: *schema, ID: params.ID, Body: params.Body, Expected: params.IfRev,
	}, fact, d.appNow())
	if err != nil {
		return nil, err
	}
	d.announceCommittedWrite(fact, written.Seq)
	// Read back rather than synthesize: the timestamps must be the store's.
	read, _, err := d.store.ReadDocument(dispatch.namespace, params.Collection, params.ID)
	if err == nil && read.Found {
		return appDocumentOf(*read.Document), nil
	}
	return appDocument{ID: params.ID, Body: params.Body, Rev: written.Rev}, nil
}

func (d *Daemon) appCollectionDelete(dispatch *appDispatch, params appCollectionParams) (any, error) {
	schema, err := d.collectionFor(dispatch.namespace, params.Collection)
	if err != nil {
		return nil, err
	}
	fact := documentChangedFact(dispatch.namespace, params.Collection, params.ID, true)
	written, err := d.store.CommitDocumentWrite(store.DocumentWrite{
		Schema: *schema, ID: params.ID, Delete: true, Expected: params.IfRev,
	}, fact, d.appNow())
	if err != nil {
		return nil, err
	}
	if written.Changed {
		d.announceCommittedWrite(fact, written.Seq)
	}
	return written.Changed, nil
}

// Fills in the two fields the app is not allowed to choose.
func (d *Daemon) appQuery(dispatch *appDispatch, params appCollectionParams) docstore.Query {
	q := docstore.Query{}
	if params.Query != nil {
		q = *params.Query
	}
	q.Namespace = dispatch.namespace
	q.Collection = params.Collection
	return q
}

func (d *Daemon) appCollectionQuery(dispatch *appDispatch, params appCollectionParams) (any, error) {
	read, took, err := d.runDocQuery(d.appQuery(dispatch, params))
	if err != nil {
		return nil, err
	}
	d.logSlowDocQuery(read.Schema, took)
	out := make([]appDocument, 0, len(read.Documents))
	for _, doc := range read.Documents {
		out = append(out, appDocumentOf(doc))
	}
	return out, nil
}

func (d *Daemon) appCollectionCount(dispatch *appDispatch, params appCollectionParams) (any, error) {
	q := d.appQuery(dispatch, params)
	if err := docstore.ValidateNamespace(q.Namespace); err != nil {
		return nil, docstore.InvalidQuery(err)
	}
	if err := docstore.ValidateCollection(q.Collection); err != nil {
		return nil, docstore.InvalidQuery(err)
	}
	read, found, err := d.store.CountQuery(q)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, undeclaredCollectionError(q.Namespace, q.Collection)
	}
	return read.Count, nil
}
