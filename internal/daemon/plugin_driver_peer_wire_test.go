package daemon_test

import (
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

const scriptedAgent = fakeagent.Harness("snipe")

type driverLaunch struct {
	Method          string          `json:"-"`
	Agent           string          `json:"agent"`
	SessionID       string          `json:"session_id"`
	RunID           string          `json:"run_id"`
	CWD             string          `json:"cwd"`
	Yolo            bool            `json:"yolo"`
	Model           string          `json:"model"`
	Effort          string          `json:"effort"`
	InitialPrompt   string          `json:"initial_prompt"`
	Metadata        json.RawMessage `json:"metadata"`
	ResumeSessionID string          `json:"resume_session_id"`
	Instructions    *struct {
		Kind         string `json:"kind"`
		Content      string `json:"content"`
		WorkspaceID  string `json:"workspace_id"`
		NotebookRoot string `json:"notebook_root"`
	} `json:"instructions"`
}

type driverClosed struct {
	SessionID string `json:"session_id"`
	RunID     string `json:"run_id"`
	Reason    string `json:"reason"`
	ExitCode  *int   `json:"exit_code"`
	Signal    string `json:"signal"`
}

type driverActiveRun struct {
	SessionID string          `json:"session_id"`
	RunID     string          `json:"run_id"`
	Metadata  json.RawMessage `json:"metadata"`
	Seq       uint64          `json:"seq"`
}

type driverRegistered struct {
	OK         bool              `json:"ok"`
	ActiveRuns []driverActiveRun `json:"active_runs"`
	AutoMode   json.RawMessage   `json:"auto_mode"`
}

type driverPeer struct {
	t          *testing.T
	name       string
	conn       net.Conn
	writing    sync.Mutex
	mu         sync.Mutex
	nextID     int
	pending    map[string]chan pluginWireMessage
	argv       []string
	launches   chan driverLaunch
	closes     chan driverClosed
	requests   chan pluginWireMessage
	gone       chan struct{}
	agent      string
	registered driverRegistered
}

func connectDriver(t *testing.T, w *world, name, agent string, capabilities map[string]bool) *driverPeer {
	t.Helper()
	d := dialDriver(t, w, name)
	d.register(agent, capabilities)
	return d
}

func dialDriver(t *testing.T, w *world, name string) *driverPeer {
	t.Helper()
	conn, err := w.DialUnix()
	if err != nil {
		t.Fatalf("plugin %s dials the daemon: %v", name, err)
	}
	d := &driverPeer{
		t: t, name: name, conn: conn,
		pending:  map[string]chan pluginWireMessage{},
		argv:     []string{"/bin/cat"},
		launches: make(chan driverLaunch, 8),
		closes:   make(chan driverClosed, 8),
		requests: make(chan pluginWireMessage, 8),
		gone:     make(chan struct{}),
	}
	t.Cleanup(d.hangUp)
	go d.listen()
	if err := d.call("hello", pluginHelloParams(name, pluginWireAPIVersion, 1), nil); err != nil {
		t.Fatalf("the daemon refused plugin %s: %v", name, err)
	}
	return d
}

func (d *driverPeer) register(agent string, capabilities map[string]bool) {
	d.t.Helper()
	d.agent, d.registered = agent, driverRegistered{}
	if err := d.call("driver.register", map[string]any{"agent": agent, "capabilities": capabilities}, &d.registered); err != nil {
		d.t.Fatalf("plugin %s registers %s: %v", d.name, agent, err)
	}
}

func (d *driverPeer) listen() {
	defer close(d.gone)
	decoder := json.NewDecoder(d.conn)
	for {
		var message pluginWireMessage
		if err := decoder.Decode(&message); err != nil {
			return
		}
		switch message.Method {
		case "":
			d.mu.Lock()
			waiter := d.pending[string(message.ID)]
			delete(d.pending, string(message.ID))
			d.mu.Unlock()
			if waiter != nil {
				waiter <- message
			}
		case "attn.health":
			_ = d.transmit(map[string]any{"jsonrpc": "2.0", "id": message.ID, "result": pluginHealth{OK: true}})
		case "driver.spawn", "driver.resume":
			var launch driverLaunch
			_ = json.Unmarshal(message.Params, &launch)
			launch.Method = message.Method
			d.mu.Lock()
			argv := d.argv
			d.mu.Unlock()
			_ = d.transmit(map[string]any{"jsonrpc": "2.0", "id": message.ID, "result": map[string]any{"argv": argv}})
			d.launches <- launch
		case "driver.session_closed":
			var closed driverClosed
			_ = json.Unmarshal(message.Params, &closed)
			_ = d.transmit(map[string]any{"jsonrpc": "2.0", "id": message.ID, "result": map[string]any{"ok": true}})
			d.closes <- closed
		default:
			d.requests <- message
		}
	}
}

func (d *driverPeer) transmit(message any) error {
	payload, err := json.Marshal(message)
	if err != nil {
		return err
	}
	d.writing.Lock()
	defer d.writing.Unlock()
	_, err = d.conn.Write(append(payload, '\n'))
	return err
}

func (d *driverPeer) call(method string, params, result any) error {
	d.t.Helper()
	d.mu.Lock()
	d.nextID++
	id := fmt.Sprint(d.nextID)
	waiter := make(chan pluginWireMessage, 1)
	d.pending[id] = waiter
	d.mu.Unlock()
	if err := d.transmit(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(id), "method": method, "params": params}); err != nil {
		d.t.Fatalf("plugin %s sends %s: %v", d.name, method, err)
	}
	select {
	case answer := <-waiter:
		if answer.Error != nil {
			return fmt.Errorf("%s", answer.Error.Message)
		}
		if result != nil {
			if err := json.Unmarshal(answer.Result, result); err != nil {
				d.t.Fatalf("plugin %s decodes the %s answer %s: %v", d.name, method, answer.Result, err)
			}
		}
		return nil
	case <-d.gone:
		d.t.Fatalf("the daemon hung up on plugin %s while it awaited %s", d.name, method)
	case <-time.After(fakeagent.HangGuard):
		d.t.Fatalf("plugin %s heard no answer to %s within %s", d.name, method, fakeagent.HangGuard)
	}
	return nil
}

func (d *driverPeer) report(method string, params map[string]any) error {
	d.t.Helper()
	return d.call(method, params, nil)
}

func (d *driverPeer) mustReport(method string, params map[string]any) {
	d.t.Helper()
	if err := d.report(method, params); err != nil {
		d.t.Fatalf("plugin %s reports %s %v: %v", d.name, method, params, err)
	}
}

func (d *driverPeer) launched() driverLaunch {
	d.t.Helper()
	select {
	case launch := <-d.launches:
		return launch
	case <-time.After(fakeagent.HangGuard):
		d.t.Fatalf("plugin %s was asked to launch nothing within %s", d.name, fakeagent.HangGuard)
		return driverLaunch{}
	}
}

func (d *driverPeer) closed() driverClosed {
	d.t.Helper()
	select {
	case closed := <-d.closes:
		return closed
	case <-time.After(fakeagent.HangGuard):
		d.t.Fatalf("plugin %s was told of no closed session within %s", d.name, fakeagent.HangGuard)
		return driverClosed{}
	}
}

func (d *driverPeer) asked(method string, params any) json.RawMessage {
	d.t.Helper()
	select {
	case request := <-d.requests:
		if request.Method != method {
			d.t.Fatalf("plugin %s was asked %s %s, want %s", d.name, request.Method, request.Params, method)
		}
		if params != nil {
			if err := json.Unmarshal(request.Params, params); err != nil {
				d.t.Fatalf("plugin %s decodes %s params %s: %v", d.name, method, request.Params, err)
			}
		}
		return request.ID
	case <-time.After(fakeagent.HangGuard):
		d.t.Fatalf("plugin %s was asked no %s within %s", d.name, method, fakeagent.HangGuard)
		return nil
	}
}

func (d *driverPeer) answer(id json.RawMessage, result any) {
	d.t.Helper()
	if err := d.transmit(map[string]any{"jsonrpc": "2.0", "id": id, "result": result}); err != nil {
		d.t.Fatalf("plugin %s answers: %v", d.name, err)
	}
}

func (d *driverPeer) launchWith(argv ...string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.argv = argv
}

func (d *driverPeer) hangUp() {
	_ = d.conn.Close()
	<-d.gone
}

func (d *driverPeer) leave(w *world) {
	d.t.Helper()
	observer := w.App()
	if observer.Initial.Settings[d.agent+"_available"] != "true" {
		d.t.Fatalf("plugin %s leaves before the app sees its %s driver", d.name, d.agent)
	}
	d.hangUp()
	testworld.Await(observer, protocol.EventSettingsUpdated, func(m protocol.SettingsUpdatedMessage) bool {
		return m.Settings[d.agent+"_available"] != "true"
	})
}

func (d *driverPeer) state(run driverLaunch, seq uint64, state string) error {
	d.t.Helper()
	return d.report("session.report_state", map[string]any{"session_id": run.SessionID, "run_id": run.RunID, "seq": seq, "state": state})
}

func spawnDriven(w *world, app *testworld.Peer, driver *driverPeer, cwd string, opts ...func(*protocol.SpawnSessionMessage)) (string, driverLaunch) {
	w.T.Helper()
	session := w.Spawn(app, fakeagent.Harness(driver.agent), cwd, opts...)
	launch := driver.launched()
	if terminal := app.Terminal(session); launch.SessionID != terminal {
		w.T.Fatalf("plugin %s was asked to launch %s, want terminal %s of session %s", driver.name, launch.SessionID, terminal, session)
	}
	return session, launch
}

func awaitDriverAvailable(app *testworld.Peer, agent string) {
	app.T.Helper()
	pluginDriverSettings(app, agent)
}

func errorSays(err error, want string) bool {
	return err != nil && strings.Contains(err.Error(), want)
}
