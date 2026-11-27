package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
	"nhooyr.io/websocket"

	"github.com/victorarias/attn/internal/bridge"
	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/setting"
)

const appRelayChannel = "attn-app"

// appRelayCredentials are the app machine's own tokens. A relayed app proves them in its client_hello, as it would
// to its own daemon.
type appRelayCredentials struct {
	ClientToken      string `json:"client_token"`
	BrowserHostToken string `json:"browser_host_token,omitempty"`
}

type appRelay struct {
	machine string
	appRelayCredentials
}

type appRelayKey struct{}

func appRelayFrom(ctx context.Context) *appRelay {
	relay, _ := ctx.Value(appRelayKey{}).(*appRelay)
	return relay
}

// relaysApps says whether this daemon passes its apps to its home. A machine that dials its home is an app machine
// whose home runs elsewhere (attn enrollment join, behind remote_home_enabled); the home decides whether it serves
// the app.
func (d *Daemon) relaysApps() bool {
	status := d.enrollmentStatus()
	return status.IsOutpost() && status.Dial != ""
}

func (d *Daemon) relayAppToHome(w http.ResponseWriter, r *http.Request) {
	release, held := d.life.Hold("relayApp")
	if !held {
		http.Error(w, "daemon is stopping", http.StatusServiceUnavailable)
		return
	}
	defer release()
	changed := d.homeStateChanges()
	home := d.homeBridge()
	if home == nil {
		d.answerAwayFromHome(w, r, changed, nil)
		return
	}
	credentials, _ := json.Marshal(appRelayCredentials{ClientToken: d.clientToken, BrowserHostToken: config.BrowserHostToken()})
	channel, requests, err := home.Open(r.Context(), appRelayChannel, credentials)
	if err != nil {
		d.answerAwayFromHome(w, r, changed, err)
		return
	}
	defer channel.Close()
	raw, buffered, err := http.NewResponseController(w).Hijack()
	if err != nil {
		d.logf("app relay: hijack: %v", err)
		return
	}
	defer raw.Close()
	if err := r.Write(channel); err != nil {
		d.logf("app relay: send the app's request to the home: %v", err)
		return
	}
	stop := context.AfterFunc(d.life.Context(), func() { _ = raw.Close() })
	defer stop()
	goTransport(func() {
		for range requests {
		}
		_ = raw.SetWriteDeadline(time.Now().Add(d.wsWriteTimeoutDuration()))
	})
	sent := make(chan struct{})
	goTransport(func() {
		defer close(sent)
		_, _ = io.Copy(channel, buffered.Reader)
		_ = channel.CloseWrite()
	})
	_, _ = io.Copy(raw, channel)
	_ = raw.Close()
	_ = channel.Close()
	<-sent
}

func (d *Daemon) answerAwayFromHome(w http.ResponseWriter, r *http.Request, changed <-chan struct{}, refused error) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: localOriginPatterns})
	if err != nil {
		d.logf("app relay: WebSocket accept error: %v", err)
		return
	}
	defer conn.CloseNow()
	ctx, cancel := context.WithCancel(d.life.Context())
	defer cancel()
	stop := context.AfterFunc(r.Context(), cancel)
	defer stop()
	write := func(message any) error {
		payload, err := json.Marshal(message)
		if err != nil {
			return err
		}
		written, done := context.WithTimeout(ctx, d.wsWriteTimeoutDuration())
		defer done()
		return conn.Write(written, websocket.MessageText, payload)
	}
	_, data, err := conn.Read(ctx)
	if err != nil {
		return
	}
	var hello protocol.ClientHelloMessage
	if json.Unmarshal(data, &hello) != nil || hello.Cmd != protocol.CmdClientHello {
		_ = conn.Close(websocket.StatusPolicyViolation, "send client_hello first")
		return
	}
	if refusal := d.helloRefusal(nil, &hello); refusal != nil {
		_ = write(refusal)
		_ = conn.Close(websocket.StatusPolicyViolation, protocol.ErrorCodeUnauthorizedClient)
		return
	}
	if err := write(d.homeUnreachableMessage(refused)); err != nil {
		return
	}
	retried := make(chan struct{})
	goTransport(func() {
		for {
			_, data, err := conn.Read(ctx)
			if err != nil {
				cancel()
				return
			}
			var command struct {
				Cmd string `json:"cmd"`
			}
			if json.Unmarshal(data, &command) == nil && command.Cmd == protocol.CmdRetryHome {
				close(retried)
				return
			}
		}
	})
	select {
	case <-changed:
	case <-retried:
		d.redialHome()
	case <-ctx.Done():
		return
	}
	_ = conn.Close(websocket.StatusTryAgainLater, "the home's state changed")
}

// sendLocalAppsToHome closes the apps this daemon serves itself, so they connect again and reach the home through
// the relay. Only apps that connected before this machine had a home to relay to are served here.
func (d *Daemon) sendLocalAppsToHome() {
	var local []*wsClient
	d.wsHub.ForEachClient(func(client *wsClient) { local = append(local, client) })
	for _, client := range local {
		if client.closeSendChannelWithStatus(websocket.StatusTryAgainLater, "this machine now relays its apps to its home") {
			goTransport(func() {
				client.hangUp(websocket.StatusTryAgainLater, "this machine now relays its apps to its home", evictionCloseGrace)
			})
		}
	}
}

func (d *Daemon) homeUnreachableMessage(refused error) protocol.HomeUnreachableMessage {
	state, _, lastError := d.outpostSettings()
	if refused != nil {
		state, lastError = "refused", refused.Error()
		var rejected *bridge.CallRejected
		if errors.As(refused, &rejected) {
			lastError = rejected.Reason
		}
	}
	status := d.enrollmentStatus()
	message := protocol.HomeUnreachableMessage{Event: protocol.EventHomeUnreachable, Home: status.HomeName, State: state}
	if message.Home == "" {
		message.Home = status.CopiedFrom()
	}
	if lastError != "" {
		message.Reason = protocol.Ptr(lastError)
	}
	return message
}

func (d *Daemon) redialHome() {
	r := &d.outpost
	r.mu.Lock()
	dial := r.dial
	r.mu.Unlock()
	if dial != nil {
		dial.kick()
	}
}

func (d *Daemon) appRelayHandler(machine string) func(*bridge.Conn, ssh.NewChannel) {
	return func(conn *bridge.Conn, incoming ssh.NewChannel) {
		if !d.life.Go("appRelay", func() { d.serveRelayedApp(machine, conn, incoming) }) {
			_ = incoming.Reject(ssh.ResourceShortage, "daemon is stopping")
		}
	}
}

func (d *Daemon) serveRelayedApp(machine string, conn *bridge.Conn, incoming ssh.NewChannel) {
	var credentials appRelayCredentials
	switch {
	case json.Unmarshal(incoming.ExtraData(), &credentials) != nil || credentials.ClientToken == "":
		_ = incoming.Reject(ssh.Prohibited, "an app relay carries the app machine's client token")
		return
	case !d.on(setting.RemoteHomeEnabled):
		_ = incoming.Reject(ssh.Prohibited, "home "+d.bridgeIdentity().Name+" does not serve apps on other machines; run attn settings set remote_home_enabled true there")
		return
	case !d.isOutpostConn(machine, conn):
		_ = incoming.Reject(ssh.Prohibited, "outpost bridge was replaced")
		return
	}
	served, relay, err := socketPair()
	if err != nil {
		_ = incoming.Reject(ssh.ResourceShortage, err.Error())
		return
	}
	defer relay.Close()
	channel, requests, err := incoming.Accept()
	if err != nil {
		_ = served.Close()
		return
	}
	defer channel.Close()
	goTransport(func() { ssh.DiscardRequests(requests) })
	stop := context.AfterFunc(conn.Context(), func() { _ = relay.Close() })
	defer stop()
	received := make(chan struct{})
	goTransport(func() {
		defer close(received)
		_, _ = io.Copy(relay, channel)
		_ = relay.CloseWrite()
	})
	sent := make(chan struct{})
	goTransport(func() {
		defer close(sent)
		_, _ = io.Copy(channel, relay)
	})
	session := &appRelay{machine: machine, appRelayCredentials: credentials}
	server := &http.Server{Handler: http.HandlerFunc(d.handleWS), ConnContext: func(ctx context.Context, c net.Conn) context.Context {
		return context.WithValue(withRawConn(ctx, c), appRelayKey{}, session)
	}}
	_ = server.Serve(newOneConnListener(served))
	tail := time.NewTimer(d.wsWriteTimeoutDuration())
	select {
	case <-sent:
	case <-tail.C:
	}
	tail.Stop()
	_ = channel.Close()
	<-received
}

type oneConnListener struct {
	conn  chan net.Conn
	done  chan struct{}
	close sync.Once
}

func newOneConnListener(conn net.Conn) *oneConnListener {
	l := &oneConnListener{conn: make(chan net.Conn, 1), done: make(chan struct{})}
	l.conn <- &listenedConn{Conn: conn, listener: l}
	return l
}

func (l *oneConnListener) Accept() (net.Conn, error) {
	select {
	case conn := <-l.conn:
		return conn, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *oneConnListener) Close() error {
	l.close.Do(func() { close(l.done) })
	return nil
}

func (l *oneConnListener) Addr() net.Addr { return appRelayAddr{} }

type listenedConn struct {
	net.Conn
	listener *oneConnListener
}

func (c *listenedConn) Close() error {
	err := c.Conn.Close()
	_ = c.listener.Close()
	return err
}

type appRelayAddr struct{}

func (appRelayAddr) Network() string { return appRelayChannel }
func (appRelayAddr) String() string  { return appRelayChannel }
