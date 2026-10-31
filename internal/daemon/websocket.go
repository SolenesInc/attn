package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"nhooyr.io/websocket"

	"github.com/victorarias/attn/internal/buildinfo"
	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
)

type wsClient struct {
	conn         *websocket.Conn
	rawConn      net.Conn
	send         chan outboundMessage
	streamSlots  chan struct{}
	controlSlots chan struct{}
	recv         chan []byte
	writing      atomic.Bool
	sendMu       sync.RWMutex
	sendClosed   bool
	closeCode    websocket.StatusCode
	closeReason  string
	connectedAt  time.Time

	trustedTauriOrigin       bool
	browserHostAuthenticated bool

	attachedStreams map[protocol.TerminalID]ptybackend.Stream
	attachMu        sync.Mutex
	terminalOrder   terminalOrder

	docSubscriptions clientDocSubscriptions

	admitted          sync.Once
	saidHello         bool
	clientKind        string
	clientVersion     string
	clientID          string
	selectedProfileID string
	capabilities      map[string]struct{}
	identityMu        sync.RWMutex

	arrangementMu      sync.Mutex
	shownTiles         []desktopMarkdownTile
	arrangementsHeld   int
	arrangementsMissed bool
	arrangementPrint   [sha256.Size]byte
	arrangementSeq     int64

	gardenMu   sync.Mutex
	gardenHeld *gardenHeld

	presence   clientPresence
	presenceMu sync.RWMutex
}

func (c *wsClient) HasCapability(cap string) bool {
	c.identityMu.RLock()
	defer c.identityMu.RUnlock()
	_, ok := c.capabilities[cap]
	return ok
}

func (c *wsClient) IsBrowserHost() bool {
	c.identityMu.RLock()
	defer c.identityMu.RUnlock()
	_, capable := c.capabilities[protocol.CapabilityBrowserHost]
	return c.trustedTauriOrigin &&
		c.browserHostAuthenticated &&
		c.clientKind == "tauri-app" &&
		capable
}

func (c *wsClient) setBrowserHostAuthenticated(authenticated bool) {
	c.identityMu.Lock()
	defer c.identityMu.Unlock()
	c.browserHostAuthenticated = authenticated
}

func (c *wsClient) isTrustedAppClient() bool {
	c.identityMu.RLock()
	defer c.identityMu.RUnlock()
	return c.trustedTauriOrigin && c.browserHostAuthenticated && c.clientKind == "tauri-app"
}

func websocketReadLimit(client *wsClient) int64 {
	if client.IsBrowserHost() {
		return maxBrowserHostWebSocketReadBytes
	}
	return defaultWebSocketReadBytes
}

func (c *wsClient) updateReadLimit() {
	if c.conn != nil {
		c.conn.SetReadLimit(websocketReadLimit(c))
	}
}

func (c *wsClient) hasSaidHello() bool {
	c.identityMu.RLock()
	defer c.identityMu.RUnlock()
	return c.saidHello
}

func (c *wsClient) setIdentity(kind, version string, caps []string) {
	c.identityMu.Lock()
	defer c.identityMu.Unlock()
	c.saidHello = true
	c.clientKind = kind
	c.clientVersion = version
	c.capabilities = make(map[string]struct{}, len(caps))
	for _, cap := range caps {
		c.capabilities[cap] = struct{}{}
	}
}

func (c *wsClient) setClientID(id string) {
	c.identityMu.Lock()
	defer c.identityMu.Unlock()
	c.clientID = id
}

func (c *wsClient) ClientID() string {
	c.identityMu.RLock()
	defer c.identityMu.RUnlock()
	return c.clientID
}

func (c *wsClient) owed() int {
	n := len(c.send)
	if c.writing.Load() {
		n++
	}
	return n
}

func (c *wsClient) closeSendChannel() {
	c.closeSendChannelWithStatus(websocket.StatusNormalClosure, "")
}

func (c *wsClient) closeSendChannelWithStatus(code websocket.StatusCode, reason string) bool {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	if c.closeCode == 0 {
		c.closeCode = code
		c.closeReason = reason
	}
	if c.sendClosed {
		return false
	}
	c.sendClosed = true
	close(c.send)
	return true
}

func (c *wsClient) closeStatus() (websocket.StatusCode, string) {
	c.sendMu.RLock()
	defer c.sendMu.RUnlock()
	if c.closeCode == 0 {
		return websocket.StatusNormalClosure, ""
	}
	return c.closeCode, c.closeReason
}

func (c *wsClient) lane(message outboundMessage) chan struct{} {
	if message.stream {
		return c.streamSlots
	}
	return c.controlSlots
}

func (c *wsClient) offer(message outboundMessage) (queued, full bool) {
	slots := c.lane(message)
	if slots != nil {
		select {
		case slots <- struct{}{}:
		default:
			return false, !c.sendChannelClosed()
		}
	}
	return c.push(message, slots)
}

func (c *wsClient) sendStream(message outboundMessage, wait time.Duration) bool {
	message.stream = true
	if c.streamSlots != nil {
		timer := time.NewTimer(wait)
		defer timer.Stop()
		select {
		case c.streamSlots <- struct{}{}:
		case <-timer.C:
			return false
		}
	}
	queued, _ := c.push(message, c.streamSlots)
	return queued
}

func (c *wsClient) push(message outboundMessage, slots chan struct{}) (queued, full bool) {
	c.sendMu.RLock()
	defer c.sendMu.RUnlock()
	if !c.sendClosed {
		select {
		case c.send <- message:
			return true, false
		default:
			full = true
		}
	}
	if slots != nil {
		<-slots
	}
	return false, full
}

type BroadcastListener func(event *protocol.WebSocketEvent)

type messageKind int

const (
	messageKindText messageKind = iota
	messageKindBinary
)

type outboundMessage struct {
	kind    messageKind
	payload []byte
	stream  bool
}

type wsHub struct {
	clients           map[*wsClient]bool
	connections       map[*wsClient]struct{}
	mu                sync.RWMutex
	stopped           bool
	evictions         map[string]evictionRecord
	evictionMu        sync.Mutex
	logf              func(format string, args ...interface{})
	broadcastListener BroadcastListener
	evictionListener  func(string, evictionRecord)
}

const (
	streamQueueSlots                 = 256
	controlQueueSlots                = 256
	slowClientCloseReason            = "client too slow"
	maxPTYDimValue                   = 65535
	maxPTYPixelValue                 = 65535
	defaultWebSocketReadBytes        = protocol.MaxSocketFrameBytes
	maxBrowserHostWebSocketReadBytes = 32 << 20
	ptyOutputSendWait                = 1 * time.Second
)

func newWSHub() *wsHub {
	return &wsHub{
		clients:     make(map[*wsClient]bool),
		connections: make(map[*wsClient]struct{}),
		logf:        func(format string, args ...interface{}) {},
	}
}

func previewBinaryForLog(data []byte) string {
	if len(data) == 0 {
		return ""
	}
	const maxPreview = 32
	preview := string(data)
	if len(preview) > maxPreview {
		preview = preview[:maxPreview]
	}
	preview = strings.ReplaceAll(preview, "\n", "\\n")
	preview = strings.ReplaceAll(preview, "\r", "\\r")
	preview = strings.ReplaceAll(preview, "\t", "\\t")
	return preview
}

func (h *wsHub) closeAll() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.stopped = true
	for client := range h.connections {
		delete(h.connections, client)
		delete(h.clients, client)
		client.closeSendChannel()
		if client.conn != nil {
			client.abortTransport()
		}
	}
}

func (h *wsHub) track(client *wsClient) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.stopped {
		return false
	}
	h.connections[client] = struct{}{}
	return true
}

func (h *wsHub) add(client *wsClient) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.stopped {
		return false
	}
	h.connections[client] = struct{}{}
	h.clients[client] = true
	return true
}

func (h *wsHub) remove(client *wsClient) {
	h.mu.Lock()
	delete(h.connections, client)
	delete(h.clients, client)
	client.closeSendChannel()
	h.mu.Unlock()
}

func (h *wsHub) Broadcast(event *protocol.WebSocketEvent) {
	if h.broadcastListener != nil {
		h.broadcastListener(event)
	}

	h.BroadcastValue(event)
}

func (h *wsHub) BroadcastValue(message interface{}) {
	data, err := json.Marshal(message)
	if err != nil {
		h.logf("WebSocket broadcast marshal error: %v", err)
		return
	}
	outbound := outboundMessage{kind: messageKindText, payload: data}
	h.mu.Lock()
	defer h.mu.Unlock()
	for client := range h.clients {
		if _, evicted := h.deliver(client, outbound); evicted {
			delete(h.clients, client)
		}
	}
}

func (h *wsHub) SendArrangementToMatchingClients(data []byte, match func(*wsClient) bool, delivery *arrangementDelivery) {
	var targets []*wsClient
	h.ForEachClient(func(client *wsClient) {
		if match(client) {
			targets = append(targets, client)
		}
	})
	for _, client := range targets {
		if h.deliverArrangement(client, outboundMessage{kind: messageKindText, payload: data}, delivery) {
			h.forget(client)
		}
	}
}

func (h *wsHub) forget(client *wsClient) {
	h.mu.Lock()
	delete(h.clients, client)
	h.mu.Unlock()
}

func (h *wsHub) deliver(client *wsClient, message outboundMessage) (queued, evicted bool) {
	queued, full := client.offer(message)
	if !full || client.conn == nil {
		return queued, false
	}
	h.logf("WebSocket client stopped draining its %d queued messages, disconnecting", len(client.send))
	h.evict(client, slowClientCloseReason)
	return false, true
}

func (h *wsHub) ForEachClient(fn func(*wsClient)) {
	if fn == nil {
		return
	}
	h.mu.RLock()
	clients := make([]*wsClient, 0, len(h.clients))
	for client := range h.clients {
		clients = append(clients, client)
	}
	h.mu.RUnlock()
	for _, client := range clients {
		fn(client)
	}
}

func (h *wsHub) AnyClientMatches(match func(*wsClient) bool) bool {
	if match == nil {
		return false
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	for client := range h.clients {
		if match(client) {
			return true
		}
	}
	return false
}

func (h *wsHub) NewestClientMatching(match func(*wsClient) bool) *wsClient {
	if match == nil {
		return nil
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	var newest *wsClient
	for client := range h.clients {
		if !match(client) {
			continue
		}
		if newest == nil || client.connectedAt.After(newest.connectedAt) {
			newest = client
		}
	}
	return newest
}

func (h *wsHub) ClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

func isAllowedWSOrigin(origin string, requestHost string) bool {
	if origin == "" {
		return true
	}
	if isAllowedLocalOrigin(origin) {
		return true
	}
	parsed, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return normalizeWSHost(parsed.Host) != "" && normalizeWSHost(parsed.Host) == normalizeWSHost(requestHost)
}

func isAllowedLocalOrigin(origin string) bool {
	allowedPrefixes := []string{
		"tauri://localhost",
		"http://tauri.localhost",
		"http://localhost",
		"http://127.0.0.1",
		"https://localhost",
		"https://127.0.0.1",
		"localhost:",
		"127.0.0.1:",
		"tauri.localhost",
	}
	for _, prefix := range allowedPrefixes {
		if strings.HasPrefix(origin, prefix) {
			return true
		}
	}
	return false
}

func isTrustedTauriOrigin(origin string) bool {
	parsed, err := url.Parse(origin)
	if err != nil {
		return false
	}
	host := normalizeWSHost(parsed.Host)
	if (parsed.Scheme == "tauri" && host == "localhost") ||
		(parsed.Scheme == "http" && host == "tauri.localhost") {
		return true
	}
	return config.Instance() == "dev" &&
		parsed.Scheme == "http" &&
		strings.EqualFold(parsed.Hostname(), "localhost") &&
		parsed.Port() == "1420"
}

func normalizeWSHost(value string) string {
	trimmed := strings.TrimSpace(strings.ToLower(value))
	if trimmed == "" {
		return ""
	}
	if host, _, err := net.SplitHostPort(trimmed); err == nil {
		return host
	}
	return trimmed
}

func websocketOriginPatternsForRequest(r *http.Request) []string {
	patterns := []string{
		"localhost",
		"localhost:*",
		"127.0.0.1",
		"127.0.0.1:*",
		"tauri.localhost",
		"tauri.localhost:*",
	}
	host := normalizeWSHost(r.Host)
	if host != "" && host != "localhost" && host != "127.0.0.1" && host != "tauri.localhost" {
		patterns = append(patterns, host)
	}
	return patterns
}

func (d *Daemon) handleWS(w http.ResponseWriter, r *http.Request) {
	origin := r.Header.Get("Origin")
	if !isAllowedWSOrigin(origin, r.Host) {
		d.logf("WebSocket rejected origin: %s", origin)
		http.Error(w, "forbidden origin", http.StatusForbidden)
		return
	}
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns: websocketOriginPatternsForRequest(r),
	})
	if err != nil {
		d.logf("WebSocket accept error: %v", err)
		return
	}
	conn.SetReadLimit(defaultWebSocketReadBytes)

	client := &wsClient{
		conn:               conn,
		rawConn:            rawConnFrom(r.Context()),
		send:               make(chan outboundMessage, streamQueueSlots+controlQueueSlots),
		streamSlots:        make(chan struct{}, streamQueueSlots),
		controlSlots:       make(chan struct{}, controlQueueSlots),
		recv:               make(chan []byte, 256),
		connectedAt:        time.Now(),
		trustedTauriOrigin: isTrustedTauriOrigin(origin),
		attachedStreams:    make(map[protocol.TerminalID]ptybackend.Stream),
	}
	release, held := d.life.Hold("handleWS")
	if !held {
		client.abortTransport()
		return
	}
	defer release()
	if !d.wsHub.track(client) {
		client.abortTransport()
		return
	}

	d.logf("WebSocket connection accepted, awaiting client_hello")

	done := make(chan struct{})
	d.life.Go("wsPingLoop", func() { d.wsPingLoop(client, done) })
	d.life.Go("wsWritePump", func() { d.wsWritePump(client) })
	d.life.Go("wsMsgPump", func() { d.wsMsgPump(client) })
	d.wsReadPump(client)

	close(done)
}

func (d *Daemon) sendInitialState(client *wsClient) {
	state := d.currentStateProjection()
	event := &protocol.InitialStateMessage{
		TerminalBindings:       d.terminals().Bindings(),
		Event:                  protocol.EventInitialState,
		ProtocolVersion:        protocol.Ptr(protocol.ProtocolVersion),
		SourceFingerprint:      protocol.Ptr(buildinfo.SourceFingerprint),
		HomeBuild:              protocol.Ptr(d.bridgeIdentity().Build.String()),
		DaemonInstanceID:       protocol.Ptr(d.daemonInstanceID),
		Sessions:               state.Sessions,
		Prs:                    state.Prs,
		Repos:                  state.Repos,
		Authors:                state.Authors,
		GithubHosts:            state.GithubHosts,
		GithubPollingOffReason: gitHubPollingOffReasonField(),
		Settings:               d.settingsSnapshot(d.profileForClient(client)),
		Warnings:               d.getWarnings(),
		Crew:                   state.Crew,
		Asks:                   asksSnapshot(client.selectedProfile(), d.asks()).Asks,
		Vents:                  d.ventsSnapshot(client.selectedProfile()).Vents,
		SharedCodex:            d.codexShared().wireState(client.selectedProfile()),
		Outposts:               d.outpostsUpdatedMessage().Outposts,
	}
	d.fillInitialMigrationPhase(event)
	d.sendInitialArrangement(client, event)
	d.nudgeDesktopTileContent()

	d.life.Go("fetchAllPRDetails", d.fetchAllPRDetails)
}

const defaultWSWriteTimeout = 10 * time.Second

func (d *Daemon) wsWriteTimeoutDuration() time.Duration {
	if d.wsWriteTimeout > 0 {
		return d.wsWriteTimeout
	}
	return defaultWSWriteTimeout
}

func (d *Daemon) wsWritePump(client *wsClient) {
	writeTimeout := d.wsWriteTimeoutDuration()
	stalled := false
	defer func() {
		code, reason := client.closeStatus()
		if stalled {
			code, reason = websocket.StatusPolicyViolation, slowClientCloseReason
			d.wsHub.rememberEviction(client.ClientID(), evictionRecord{
				at:          time.Now(),
				reason:      reason,
				undelivered: len(client.send) + 1,
			})
		}
		client.conn.Close(code, reason)
	}()

	for message := range client.send {
		if slots := client.lane(message); slots != nil {
			<-slots
		}
		ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
		wsType := websocket.MessageText
		if message.kind != messageKindText {
			wsType = websocket.MessageBinary
		}
		client.writing.Store(true)
		err := client.conn.Write(ctx, wsType, message.payload)
		client.writing.Store(false)
		deadlineExpired := ctx.Err() == context.DeadlineExceeded
		cancel()
		if err != nil {
			stalled = deadlineExpired
			if stalled {
				d.logf("WebSocket client took longer than %s to accept a message, giving up on it", writeTimeout)
			}
			return
		}
	}
}

func (d *Daemon) sendOutbound(client *wsClient, message outboundMessage) bool {
	if d.wsHub == nil {
		queued, _ := client.offer(message)
		return queued
	}
	queued, evicted := d.wsHub.deliver(client, message)
	if evicted {
		d.wsHub.forget(client)
	}
	return queued
}

func (d *Daemon) sendStream(client *wsClient, message outboundMessage) bool {
	return client.sendStream(message, ptyOutputSendWait)
}

func (d *Daemon) wsMsgPump(client *wsClient) {
	for data := range client.recv {
		if client.sendChannelClosed() && !isAppResultFrame(data) {
			continue
		}
		d.handleClientMessage(client, data)
	}

	d.browserControl.failFor(client, browserControlResult{err: "the in-app browser host disconnected"})
	d.calloutRequests.failFor(client, calloutReply{err: fmt.Errorf("the attn app disconnected")})
	d.logf("WebSocket message pump exited")
}

func isAppResultFrame(data []byte) bool {
	var peek struct {
		Cmd string `json:"cmd"`
	}
	return json.Unmarshal(data, &peek) == nil && (peek.Cmd == protocol.CmdBrowserControlResult || peek.Cmd == protocol.CmdCalloutResult)
}

func (c *wsClient) sendChannelClosed() bool {
	c.sendMu.RLock()
	defer c.sendMu.RUnlock()
	return c.sendClosed
}

const (
	defaultWSPingInterval = 30 * time.Second
	defaultWSPingTimeout  = 10 * time.Second
)

func (d *Daemon) wsPingIntervalDuration() time.Duration {
	if d.wsPingInterval > 0 {
		return d.wsPingInterval
	}
	return defaultWSPingInterval
}

func (d *Daemon) wsPingTimeoutDuration() time.Duration {
	if d.wsPingTimeout > 0 {
		return d.wsPingTimeout
	}
	return defaultWSPingTimeout
}

func (d *Daemon) wsPingLoop(client *wsClient, done <-chan struct{}) {
	pingTimeout := d.wsPingTimeoutDuration()
	ticker := time.NewTicker(d.wsPingIntervalDuration())
	defer ticker.Stop()

	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), pingTimeout)
			err := client.conn.Ping(ctx)
			cancel()
			if err != nil {
				d.logf("WebSocket ping failed: %v", err)
				if owed := client.owed(); owed > 0 {
					d.logf("WebSocket client stopped answering with %d messages still owed to it; filing that for its return", owed)
					d.wsHub.rememberEviction(client.ClientID(), evictionRecord{
						at:          time.Now(),
						reason:      slowClientCloseReason,
						undelivered: owed,
					})
				}
				client.hangUp(websocket.StatusGoingAway, "ping timeout", evictionCloseGrace)
				return
			}
		}
	}
}

func (d *Daemon) wsReadPump(client *wsClient) {
	defer func() {
		d.dropPendingInitialState(client)
		d.dropFsWatchClient(client)
		d.dropDocSubscriptions(client)
		d.detachAllSessions(client)
		close(client.recv)
		d.wsHub.remove(client)
		client.conn.Close(websocket.StatusNormalClosure, "")
		d.logf("WebSocket client disconnected (%d remaining)", d.wsHub.ClientCount())
	}()

	for {
		_, data, err := client.conn.Read(context.Background())
		if err != nil {
			d.logf("WebSocket read error: %v", err)
			return
		}

		select {
		case client.recv <- data:
		default:
			d.logf("WebSocket client recv buffer full; closing client connection")
			_ = client.conn.Close(websocket.StatusPolicyViolation, "command buffer overflow")
			return
		}
	}
}

func (d *Daemon) handleClientMessage(client *wsClient, data []byte) {
	cmd, msg, err := protocol.ParseMessage(data)
	if err != nil {
		var peek struct {
			Cmd string `json:"cmd"`
		}
		_ = json.Unmarshal(data, &peek)
		d.logf("WebSocket parse error for cmd=%s: %v", peek.Cmd, err)
		d.sendCommandError(client, peek.Cmd, err.Error())
		return
	}
	if shouldLogWSCommand(cmd) {
		d.logf("WebSocket parsed cmd: %s", cmd)
	}
	if cmd != protocol.CmdClientHello && !client.hasSaidHello() {
		errMsg := "client must send client_hello first"
		d.logf("rejecting websocket command %s: %s", cmd, errMsg)
		d.sendCommandError(client, cmd, errMsg)
		if client.conn != nil {
			_ = client.conn.Close(websocket.StatusPolicyViolation, errMsg)
		}
		return
	}
	if d.isRecovering() && blocksDuringRecovery(cmd) {
		d.sendCommandError(client, cmd, "daemon_recovering")
		if unregister, ok := msg.(*protocol.UnregisterMessage); ok {
			d.answerSessionClose(client, unregister.ID, errors.New("daemon_recovering"))
		}
		return
	}
	if err := d.scopeGardenRequest(cmd, msg, client.selectedProfile()); err != nil {
		d.sendGardenScopeError(client, cmd, msg, err)
		return
	}

	switch cmd {
	case protocol.CmdProfileCreate:
		d.handleProfileCreate(client, msg.(*protocol.ProfileCreateMessage))
	case protocol.CmdProfileRename:
		d.handleProfileRename(client, msg.(*protocol.ProfileRenameMessage))
	case protocol.CmdProfileDelete:
		d.handleProfileDelete(client, msg.(*protocol.ProfileDeleteMessage))
	case protocol.CmdProfileSelect:
		d.handleProfileSelect(client, msg.(*protocol.ProfileSelectMessage))
	case protocol.CmdLaunchDesktopGet:
		d.handleLaunchDesktopGet(client, msg.(*protocol.LaunchDesktopGetMessage))
	case protocol.CmdLaunchDesktopSet:
		d.handleLaunchDesktopSet(client, msg.(*protocol.LaunchDesktopSetMessage))
	case protocol.CmdMigrationGet:
		d.handleMigrationGet(client, msg.(*protocol.MigrationGetMessage))
	case protocol.CmdMigrationKeep:
		d.handleMigrationKeep(client, msg.(*protocol.MigrationKeepMessage))
	case protocol.CmdMigrationMove:
		d.handleMigrationMove(client, msg.(*protocol.MigrationMoveMessage))
	case protocol.CmdMigrationSuggest:
		d.handleMigrationSuggest(client, msg.(*protocol.MigrationSuggestMessage))
	case protocol.CmdMigrationUndo:
		d.handleMigrationUndo(client, msg.(*protocol.MigrationUndoMessage))
	case protocol.CmdMigrationFinish:
		d.handleMigrationFinish(client, msg.(*protocol.MigrationFinishMessage))
	case protocol.CmdDesktopCreate:
		d.handleDesktopCreate(client, msg.(*protocol.DesktopCreateMessage))
	case protocol.CmdDesktopClose:
		d.handleDesktopClose(client, msg.(*protocol.DesktopCloseMessage))
	case protocol.CmdDesktopRename:
		d.handleDesktopRename(client, msg.(*protocol.DesktopRenameMessage))
	case protocol.CmdDesktopSetOrder:
		d.handleDesktopSetOrder(client, msg.(*protocol.DesktopSetOrderMessage))
	case protocol.CmdDesktopReorder:
		d.handleDesktopReorder(client, msg.(*protocol.DesktopReorderMessage))
	case protocol.CmdDesktopSetCurrent:
		d.handleDesktopSetCurrent(client, msg.(*protocol.DesktopSetCurrentMessage))
	case protocol.CmdDesktopSetActivePane:
		d.handleDesktopSetActivePane(client, msg.(*protocol.DesktopSetActivePaneMessage))
	case protocol.CmdDesktopPlaceSession:
		d.handleDesktopPlaceSession(client, msg.(*protocol.DesktopPlaceSessionMessage))
	case protocol.CmdDesktopShowSession:
		d.handleDesktopShowSession(client, msg.(*protocol.DesktopShowSessionMessage))
	case protocol.CmdDesktopShowLeaf:
		d.handleDesktopShowLeaf(client, msg.(*protocol.DesktopShowLeafMessage))
	case protocol.CmdDesktopMoveLeaf:
		d.handleDesktopMoveLeaf(client, msg.(*protocol.DesktopMoveLeafMessage))
	case protocol.CmdDesktopRemoveLeaf:
		d.handleDesktopRemoveLeaf(client, msg.(*protocol.DesktopRemoveLeafMessage))
	case protocol.CmdDesktopCloseTile:
		d.handleDesktopCloseTile(client, msg.(*protocol.DesktopCloseTileMessage))
	case protocol.CmdDesktopSetSplitRatio:
		d.handleDesktopSetSplitRatio(client, msg.(*protocol.DesktopSetSplitRatioMessage))
	case protocol.CmdDesktopDockTile:
		d.handleDesktopDockTile(client, msg.(*protocol.DesktopDockTileMessage))
	case protocol.CmdDesktopUpdateTile:
		d.handleDesktopUpdateTile(client, msg.(*protocol.DesktopUpdateTileMessage))
	case protocol.CmdClientHello:
		d.handleClientHello(client, msg.(*protocol.ClientHelloMessage))
	case protocol.CmdDelegate:
		request := msg.(*protocol.DelegateMessage)
		profile, err := d.resolveGardenProfile(protocol.Deref(request.SourceSessionID), protocol.Deref(request.ProfileID), client.selectedProfile())
		if err == nil && profile.ID != client.selectedProfile() {
			selected, readErr := d.store.GetProfile(client.selectedProfile())
			if readErr != nil {
				err = readErr
			} else {
				err = fmt.Errorf("source session belongs to profile %q; app belongs to profile %q", profile.Name, selected.Name)
			}
		}
		if err != nil {
			d.sendToClient(client, protocol.DelegateResultMessage{Event: protocol.EventDelegateResult, RequestID: protocol.Ptr(request.RequestID), Error: protocol.Ptr(err.Error())})
			break
		}
		request.ProfileID = protocol.Ptr(profile.ID)
		d.life.Go("handleDelegateWS", func() { d.handleDelegateWS(client, request) })
	case protocol.CmdHarnessModels:
		d.life.Go("handleHarnessModels", func() { d.handleHarnessModels(client, msg.(*protocol.HarnessModelsMessage)) })
	case protocol.CmdDelegationPreferencesGet:
		d.handleDelegationPreferencesGet(client, msg.(*protocol.DelegationPreferencesGetMessage))
	case protocol.CmdDelegationPreferencesSave:
		d.handleDelegationPreferencesSave(client, msg.(*protocol.DelegationPreferencesSaveMessage))
	case protocol.CmdDelegateStatus:
		d.life.Go("handleDelegateStatusWS", func() { d.handleDelegateStatusWS(client, msg.(*protocol.DelegateStatusMessage)) })
	case protocol.CmdNotebookList:
		nbList := msg.(*protocol.NotebookListMessage)
		d.life.Go("sendNotebookListWSResult", func() {
			d.sendNotebookListWSResult(client, protocol.Deref(nbList.RequestID), protocol.Deref(nbList.Prefix))
		})
	case protocol.CmdNotebookRead:
		nbRead := msg.(*protocol.NotebookReadMessage)
		d.life.Go("sendNotebookReadWSResult", func() { d.sendNotebookReadWSResult(client, protocol.Deref(nbRead.RequestID), nbRead.Path) })
	case protocol.CmdNotebookBacklinks:
		nbBack := msg.(*protocol.NotebookBacklinksMessage)
		d.life.Go("sendNotebookBacklinksWSResult", func() { d.sendNotebookBacklinksWSResult(client, protocol.Deref(nbBack.RequestID), nbBack.Path) })
	case protocol.CmdNotebookWrite:
		nbWrite := msg.(*protocol.NotebookWriteMessage)
		d.life.Go("sendNotebookWriteWSResult", func() {
			d.sendNotebookWriteWSResult(client, protocol.Deref(nbWrite.RequestID), nbWrite.Path, nbWrite.Content, protocol.Deref(nbWrite.BaseHash))
		})
	case protocol.CmdTaskList:
		nbTaskList := msg.(*protocol.TaskListMessage)
		d.life.Go("sendTaskListWSResult", func() { d.sendTaskListWSResult(client, protocol.Deref(nbTaskList.RequestID)) })
	case protocol.CmdTaskRetry:
		nbTaskRetry := msg.(*protocol.TaskRetryMessage)
		d.life.Go("sendTaskRetryWSResult", func() { d.sendTaskRetryWSResult(client, protocol.Deref(nbTaskRetry.RequestID), nbTaskRetry.TaskID) })
	case protocol.CmdSessionList:
		d.life.Go("sendSessionListWSResult", func() { d.sendSessionListWSResult(client, msg.(*protocol.SessionListMessage)) })
	case protocol.CmdSessionShow:
		d.life.Go("sendSessionShowWSResult", func() { d.sendSessionShowWSResult(client, msg.(*protocol.SessionShowMessage)) })
	case protocol.CmdSessionReopen:
		d.life.Go("sendSessionReopenWSResult", func() { d.sendSessionReopenWSResult(client, msg.(*protocol.SessionReopenMessage)) })
	case protocol.CmdNotificationList:
		notifList := msg.(*protocol.NotificationListMessage)
		d.life.Go("sendNotificationListWSResult", func() { d.sendNotificationListWSResult(client, protocol.Deref(notifList.RequestID)) })
	case protocol.CmdListAnalyticsRecords:
		d.life.Go("sendAnalyticsRecordsWSResult", func() { d.sendAnalyticsRecordsWSResult(client, msg.(*protocol.ListAnalyticsRecordsMessage)) })
	case protocol.CmdGetAnalyticsStatus:
		d.life.Go("sendAnalyticsStatusWSResult", func() { d.sendAnalyticsStatusWSResult(client, msg.(*protocol.GetAnalyticsStatusMessage)) })
	case protocol.CmdClearAnalyticsRecords:
		d.life.Go("sendClearAnalyticsRecordsWSResult", func() { d.sendClearAnalyticsRecordsWSResult(client, msg.(*protocol.ClearAnalyticsRecordsMessage)) })
	case protocol.CmdForgetAnalyticsInstall:
		d.life.Go("handleForgetAnalyticsInstall", func() { d.handleForgetAnalyticsInstall(client, msg.(*protocol.ForgetAnalyticsInstallMessage)) })
	case protocol.CmdSetVentSend:
		d.life.Go("handleSetVentSend", func() { d.handleSetVentSend(client, msg.(*protocol.SetVentSendMessage)) })
	case protocol.CmdSetVentKind:
		d.life.Go("handleSetVentKind", func() { d.handleSetVentKind(client, msg.(*protocol.SetVentKindMessage)) })
	case protocol.CmdNotificationMarkRead:
		notifMark := msg.(*protocol.NotificationMarkReadMessage)
		d.life.Go("sendNotificationMarkReadWSResult", func() {
			d.sendNotificationMarkReadWSResult(client, protocol.Deref(notifMark.RequestID), notifMark.NotificationID)
		})

	case protocol.CmdSeedArtifactTransfer:
		d.life.Go("handleSeedArtifactTransferWS", func() { d.handleSeedArtifactTransferWS(client, msg.(*protocol.SeedArtifactTransferMessage)) })
	case protocol.CmdSeedArtifactTarget:
		d.life.Go("handleSeedArtifactTarget", func() { d.handleSeedArtifactTarget(client, msg.(*protocol.SeedArtifactTargetMessage)) })
	case protocol.CmdSeedResume:
		d.life.Go("handleSeedResume", func() { d.handleSeedResume(client, msg.(*protocol.SeedResumeMessage)) })
	case protocol.CmdSeedSendToChief:
		d.life.Go("handleSeedSendToChiefWS", func() { d.handleSeedSendToChiefWS(client, msg.(*protocol.SeedSendToChiefMessage)) })
	case protocol.CmdSeedReviewStart:
		d.life.Go("handleSeedReviewStartWS", func() { d.handleSeedReviewStartWS(client, msg.(*protocol.SeedReviewStartMessage)) })
	case protocol.CmdSeedReviewShow:
		d.life.Go("handleSeedReviewShowWS", func() { d.handleSeedReviewShowWS(client, msg.(*protocol.SeedReviewShowMessage)) })
	case protocol.CmdSeedReviewCancel:
		d.life.Go("handleSeedReviewCancelWS", func() { d.handleSeedReviewCancelWS(client, msg.(*protocol.SeedReviewCancelMessage)) })
	case protocol.CmdSeedReviewRetry:
		d.life.Go("handleSeedReviewRetryWS", func() { d.handleSeedReviewRetryWS(client, msg.(*protocol.SeedReviewRetryMessage)) })
	case protocol.CmdSeedReviewKeep:
		d.life.Go("handleSeedReviewKeepWS", func() { d.handleSeedReviewKeepWS(client, msg.(*protocol.SeedReviewKeepMessage)) })
	case protocol.CmdSeedReviewDraft:
		d.life.Go("handleSeedReviewDraftWS", func() { d.handleSeedReviewDraftWS(client, msg.(*protocol.SeedReviewDraftMessage)) })
	case protocol.CmdQuickCaptureSend, protocol.CmdQuickCaptureGet, protocol.CmdQuickCaptureList, protocol.CmdQuickCaptureAttachmentPut, protocol.CmdQuickCaptureAttachmentGet, protocol.CmdQuickCaptureAttachmentDiscard:
		selected := client.selectedProfile()
		d.life.Go("handleQuickCaptureWS", func() { d.handleQuickCaptureWS(client, selected, msg) })
	case protocol.CmdCrewWake:
		wake := msg.(*protocol.CrewWakeMessage)
		wake.ProfileID = client.profileOr(wake.ProfileID)
		d.life.Go("handleCrewWakeWS", func() { d.handleCrewWakeWS(client, wake) })
	case protocol.CmdCrewCharterGet:
		asked := msg.(*protocol.CrewCharterGetMessage)
		asked.ProfileID = client.profileOr(asked.ProfileID)
		d.life.Go("handleCrewCharterGetWS", func() { d.handleCrewCharterGetWS(client, asked) })
	case protocol.CmdCrewCharterSet:
		asked := msg.(*protocol.CrewCharterSetMessage)
		asked.ProfileID = client.profileOr(asked.ProfileID)
		d.life.Go("handleCrewCharterSetWS", func() { d.handleCrewCharterSetWS(client, asked) })
	case protocol.CmdCrewHandoffsGet:
		asked := msg.(*protocol.CrewHandoffsGetMessage)
		asked.ProfileID = client.profileOr(asked.ProfileID)
		d.life.Go("handleCrewHandoffsGetWS", func() { d.handleCrewHandoffsGetWS(client, asked) })
	case protocol.CmdCrewHandoffGet:
		asked := msg.(*protocol.CrewHandoffGetMessage)
		asked.ProfileID = client.profileOr(asked.ProfileID)
		d.life.Go("handleCrewHandoffGetWS", func() { d.handleCrewHandoffGetWS(client, asked) })
	case protocol.CmdCrewSleep:
		asked := msg.(*protocol.CrewSleepMessage)
		asked.ProfileID = client.profileOr(asked.ProfileID)
		d.life.Go("handleCrewSleepWS", func() { d.handleCrewSleepWS(client, asked) })
	case protocol.CmdCrewApply:
		asked := msg.(*protocol.CrewApplyMessage)
		asked.ProfileID = client.profileOr(asked.ProfileID)
		d.life.Go("handleCrewApplyWS", func() { d.handleCrewApplyWS(client, asked) })
	case protocol.CmdCrewRename:
		asked := msg.(*protocol.CrewRenameMessage)
		asked.ProfileID = client.profileOr(asked.ProfileID)
		d.life.Go("handleCrewRenameWS", func() { d.handleCrewRenameWS(client, asked) })
	case protocol.CmdCrewSet:
		asked := msg.(*protocol.CrewSetMessage)
		asked.ProfileID = client.profileOr(asked.ProfileID)
		d.life.Go("handleCrewSetWS", func() { d.handleCrewSetWS(client, asked) })
	case protocol.CmdFsList:
		fsList := msg.(*protocol.FsListMessage)
		d.life.Go("sendFsListWSResult", func() {
			d.sendFsListWSResult(client, protocol.Deref(fsList.RequestID), protocol.Deref(fsList.Path), protocol.Deref(fsList.Root))
		})
	case protocol.CmdFsRead:
		fsRead := msg.(*protocol.FsReadMessage)
		d.life.Go("sendFsReadWSResult", func() {
			d.sendFsReadWSResult(client, protocol.Deref(fsRead.RequestID), fsRead.Path, protocol.Deref(fsRead.Root))
		})
	case protocol.CmdFsReadAsset:
		fsReadAsset := msg.(*protocol.FsReadAssetMessage)
		d.life.Go("sendFsReadAssetWSResult", func() {
			d.sendFsReadAssetWSResult(client, protocol.Deref(fsReadAsset.RequestID), fsReadAsset.Path, protocol.Deref(fsReadAsset.Root))
		})
	case protocol.CmdFsWrite:
		fsWrite := msg.(*protocol.FsWriteMessage)
		d.life.Go("sendFsWriteWSResult", func() {
			d.sendFsWriteWSResult(client, protocol.Deref(fsWrite.RequestID), fsWrite.Path, fsWrite.Content, protocol.Deref(fsWrite.BaseHash), protocol.Deref(fsWrite.Root))
		})
	case protocol.CmdFsRename:
		fsRename := msg.(*protocol.FsRenameMessage)
		d.life.Go("sendFsRenameWSResult", func() {
			d.sendFsRenameWSResult(client, protocol.Deref(fsRename.RequestID), fsRename.Path, fsRename.NewPath, protocol.Deref(fsRename.Root))
		})
	case protocol.CmdFsDelete:
		fsDelete := msg.(*protocol.FsDeleteMessage)
		d.life.Go("sendFsDeleteWSResult", func() {
			d.sendFsDeleteWSResult(client, protocol.Deref(fsDelete.RequestID), fsDelete.Path, protocol.Deref(fsDelete.Root))
		})
	case protocol.CmdFsExists:
		fsExists := msg.(*protocol.FsExistsMessage)
		d.life.Go("sendFsExistsWSResult", func() {
			d.sendFsExistsWSResult(client, protocol.Deref(fsExists.RequestID), fsExists.Path, protocol.Deref(fsExists.Root))
		})
	case protocol.CmdFsWatch:
		fsWatch := msg.(*protocol.FsWatchMessage)
		d.life.Go("handleFsWatch", func() { d.handleFsWatch(client, protocol.Deref(fsWatch.RequestID), protocol.Deref(fsWatch.Root)) })
	case protocol.CmdFsUnwatch:
		fsUnwatch := msg.(*protocol.FsUnwatchMessage)
		d.life.Go("handleFsUnwatch", func() { d.handleFsUnwatch(client, protocol.Deref(fsUnwatch.RequestID), protocol.Deref(fsUnwatch.Root)) })
	case protocol.CmdFsIndex:
		fsIndex := msg.(*protocol.FsIndexMessage)
		d.life.Go("handleFsIndex", func() {
			d.handleFsIndex(client, protocol.Deref(fsIndex.RequestID), protocol.Deref(fsIndex.Root), fsIndex.Extensions)
		})
	case protocol.CmdApprovePR:
		d.handleApprovePRWS(client, msg.(*protocol.ApprovePRMessage))
	case protocol.CmdMergePR:
		d.handleMergePRWS(client, msg.(*protocol.MergePRMessage))
	case protocol.CmdMutePR:
		d.handleMutePRWS(msg.(*protocol.MutePRMessage))
	case protocol.CmdMuteRepo:
		d.handleMuteRepoWS(msg.(*protocol.MuteRepoMessage))
	case protocol.CmdMuteAuthor:
		d.handleMuteAuthorWS(msg.(*protocol.MuteAuthorMessage))
	case protocol.CmdSetSessionContextWindowCap:
		d.handleSetSessionContextWindowCap(client, msg.(*protocol.SetSessionContextWindowCapMessage))
	case protocol.CmdRefreshPRs:
		d.handleRefreshPRsWS(client)
	case protocol.CmdFetchPRDetails:
		d.handleFetchPRDetailsWS(client, msg.(*protocol.FetchPRDetailsMessage))
	case protocol.CmdClearWarnings:
		d.handleClearWarningsWS()
	case protocol.CmdSetSessionPriority:
		if err := d.setSessionPriority(msg.(*protocol.SetSessionPriorityMessage)); err != nil {
			d.sendCommandError(client, protocol.CmdSetSessionPriority, err.Error())
		}
	case protocol.CmdSettleTurn:
		settle := msg.(*protocol.SettleTurnMessage)
		d.inSessionOrder(client, settle.SessionID, func() {
			if err := d.handleSettleTurn(settle); err != nil {
				d.logf("settle rejected: %v", err)
				if d.sessionHost(settle.SessionID) != "" {
					d.sendCommandError(client, protocol.CmdSettleTurn, err.Error())
				}
			}
		})
	case protocol.CmdSnoozeTurn:
		snooze := msg.(*protocol.SnoozeTurnMessage)
		d.inSessionOrder(client, snooze.SessionID, func() {
			if err := d.handleSnoozeTurn(snooze); err != nil {
				d.logf("snooze rejected: %v", err)
				if d.sessionHost(snooze.SessionID) != "" {
					d.sendCommandError(client, protocol.CmdSnoozeTurn, err.Error())
				}
			}
		})
	case protocol.CmdWakeTurn:
		wake := msg.(*protocol.WakeTurnMessage)
		d.inSessionOrder(client, wake.SessionID, func() {
			if err := d.handleWakeTurn(wake); err != nil {
				d.logf("unsnooze rejected: %v", err)
				if d.sessionHost(wake.SessionID) != "" {
					d.sendCommandError(client, protocol.CmdWakeTurn, err.Error())
				}
			}
		})
	case protocol.CmdCancelCountdown:
		cancel := msg.(*protocol.CancelCountdownMessage)
		d.inSessionOrder(client, cancel.SessionID, func() {
			if err := d.handleCancelCountdown(cancel); err != nil {
				d.logf("cancel countdown rejected: %v", err)
				if d.sessionHost(cancel.SessionID) != "" {
					d.sendCommandError(client, protocol.CmdCancelCountdown, err.Error())
				}
			}
		})

	case protocol.CmdPRVisited:
		d.handlePRVisitedWS(msg.(*protocol.PRVisitedMessage))
	case protocol.CmdListWorktrees:
		d.handleListWorktreesWS(client, msg.(*protocol.ListWorktreesMessage))
	case protocol.CmdCreateWorktree:
		d.handleCreateWorktreeWS(client, msg.(*protocol.CreateWorktreeMessage))
	case protocol.CmdDeleteWorktree:
		d.handleDeleteWorktreeWS(client, msg.(*protocol.DeleteWorktreeMessage))
	case protocol.CmdKeptConversationList:
		d.handleKeptConversationListWS(client, msg.(*protocol.KeptConversationListMessage))
	case protocol.CmdKeptConversationKeep:
		d.handleKeptConversationKeepWS(client, msg.(*protocol.KeptConversationKeepMessage))
	case protocol.CmdKeptConversationForget:
		d.handleKeptConversationForgetWS(client, msg.(*protocol.KeptConversationForgetMessage))
	case protocol.CmdWorktreeList:
		d.handleWorktreeListWS(client, msg.(*protocol.WorktreeListMessage))
	case protocol.CmdWorktreeKeep:
		d.handleWorktreeKeepWS(client, msg.(*protocol.WorktreeKeepMessage))
	case protocol.CmdWorktreeSweepLog:
		d.handleWorktreeSweepLogWS(client, msg.(*protocol.WorktreeSweepLogMessage))
	case protocol.CmdWorktreeRefresh:
		d.handleWorktreeRefreshWS(client, msg.(*protocol.WorktreeRefreshMessage))
	case protocol.CmdGetSettings:
		d.handleGetSettingsWS(client)
	case protocol.CmdSetSetting:
		setting := msg.(*protocol.SetSettingMessage)
		if setting.Key == string(SettingSharedPTYHostEnabled) {
			d.life.Go("handleSetSettingWS", func() { d.handleSetSettingWS(client, setting) })
		} else {
			d.handleSetSettingWS(client, setting)
		}
	case protocol.CmdUpdateOutpost:
		d.handleUpdateOutpost(client, msg.(*protocol.UpdateOutpostMessage).ID)
	case protocol.CmdAddOutpost:
		m := msg.(*protocol.AddOutpostMessage)
		d.handleOutpostCommand(client, "add", "", m.Name, m.Destination)
	case protocol.CmdRemoveOutpost:
		m := msg.(*protocol.RemoveOutpostMessage)
		d.handleOutpostCommand(client, "remove", m.ID, "", "")
	case protocol.CmdConnectOutpostInTerminal:
		d.handleConnectOutpostInTerminal(client, msg.(*protocol.ConnectOutpostInTerminalMessage))
	case protocol.CmdRepairOutpost:
		m := msg.(*protocol.RepairOutpostMessage)
		d.handleOutpostCommand(client, "repair", m.ID, "", "")
	case protocol.CmdListPlugins:
		d.handleListPluginsWS(client)
	case protocol.CmdInstallPlugin:
		d.handleInstallPluginWS(client, msg.(*protocol.InstallPluginMessage))
	case protocol.CmdInstallBundledPlugin:
		d.handleInstallBundledPluginWS(client, msg.(*protocol.InstallBundledPluginMessage))
	case protocol.CmdUninstallPlugin:
		d.handleUninstallPluginWS(client, msg.(*protocol.UninstallPluginMessage))
	case protocol.CmdRemovePlugin:
		d.handleRemovePluginWS(client, msg.(*protocol.RemovePluginMessage))
	case protocol.CmdSetPluginPriority:
		d.handleSetPluginPriorityWS(client, msg.(*protocol.SetPluginPriorityMessage))
	case protocol.CmdUnregister:
		close := msg.(*protocol.UnregisterMessage)
		if d.sessionHost(close.ID) != "" {
			d.offPumpInTerminalOrder(client, d.primaryTerminal(close.ID), func() { d.handleUnregisterWS(client, close) })
		} else {
			d.handleUnregisterWS(client, close)
		}
	case protocol.CmdGetRecentLocations:
		d.handleGetRecentLocationsWS(client, msg.(*protocol.GetRecentLocationsMessage))
	case protocol.CmdGetCommandUsage:
		d.handleGetCommandUsage(client, msg.(*protocol.GetCommandUsageMessage))
	case protocol.CmdRecordCommandUsage:
		d.handleRecordCommandUsage(client, msg.(*protocol.RecordCommandUsageMessage))
	case protocol.CmdRecordAnalytics:
		d.handleRecordAnalytics(msg.(*protocol.RecordAnalyticsMessage), "")
	case protocol.CmdRecentFiles:
		d.life.Go("handleRecentFiles", func() {
			d.handleRecentFilesWS(client, msg.(*protocol.RecentFilesMessage))
		})
	case protocol.CmdBrowseDirectory:
		d.handleBrowseDirectoryWS(client, msg.(*protocol.BrowseDirectoryMessage))
	case protocol.CmdInspectPath:
		d.handleInspectPathWS(client, msg.(*protocol.InspectPathMessage))
	case protocol.CmdListBranches:
		d.handleListBranchesWS(client, msg.(*protocol.ListBranchesMessage))
	case protocol.CmdCreateWorktreeFromBranch:
		d.handleCreateWorktreeFromBranchWS(client, msg.(*protocol.CreateWorktreeFromBranchMessage))
	case protocol.CmdGetDefaultBranch:
		d.handleGetDefaultBranchWS(client, msg.(*protocol.GetDefaultBranchMessage))
	case protocol.CmdFetchRemotes:
		d.handleFetchRemotesWS(client, msg.(*protocol.FetchRemotesMessage))
	case protocol.CmdListRemoteBranches:
		d.handleListRemoteBranchesWS(client, msg.(*protocol.ListRemoteBranchesMessage))
	case protocol.CmdEnsureRepo:
		d.handleEnsureRepoWS(client, msg.(*protocol.EnsureRepoMessage))
	case protocol.CmdGetRepoInfo:
		d.handleGetRepoInfoWS(client, msg.(*protocol.GetRepoInfoMessage))
	case protocol.CmdAskAnswer:
		answer := msg.(*protocol.AskAnswerMessage)
		if stored, err := d.store.GetOpenAsk(strings.TrimSpace(answer.AskID)); err == nil {
			if holder := d.inboxHolder(stored.To); holder != nil {
				d.inSessionOrder(client, holder.ID, func() { d.handleAskAnswer(client, answer) })
				return
			}
		}
		d.handleAskAnswer(client, answer)
	case protocol.CmdSetVentDismissed:
		d.handleSetVentDismissed(client, msg.(*protocol.SetVentDismissedMessage))
	case protocol.CmdPlantVentSeed:
		d.handlePlantVentSeed(client, msg.(*protocol.PlantVentSeedMessage))
	case protocol.CmdUnplantVentSeed:
		d.handleUnplantVentSeed(client, msg.(*protocol.UnplantVentSeedMessage))
	case protocol.CmdAutomationDefinitionsGet:
		d.handleAutomationDefinitionsGetWS(client, msg.(*protocol.AutomationDefinitionsGetMessage))
	case protocol.CmdAutomationDefinitionGet:
		d.handleAutomationDefinitionGetWS(client, msg.(*protocol.AutomationDefinitionGetMessage))
	case protocol.CmdAutomationRunsGet:
		d.handleAutomationRunsGetWS(client, msg.(*protocol.AutomationRunsGetMessage))
	case protocol.CmdAutomationSetEnabled:
		d.handleAutomationSetEnabledWS(client, msg.(*protocol.AutomationSetEnabledMessage))
	case protocol.CmdAutomationApply:
		apply := msg.(*protocol.AutomationApplyMessage)
		apply.ProfileID = client.profileOr(apply.ProfileID)
		d.handleAutomationApplyWS(client, apply)
	case protocol.CmdAutomationValidate:
		d.handleAutomationValidateWS(client, msg.(*protocol.AutomationValidateMessage))
	case protocol.CmdAutomationDelete:
		d.handleAutomationDeleteWS(client, msg.(*protocol.AutomationDeleteMessage))
	case protocol.CmdAutomationCleanup:
		d.handleAutomationCleanupWS(client, msg.(*protocol.AutomationCleanupMessage))
	case protocol.CmdAutomationRun:
		d.handleAutomationRunWS(client, msg.(*protocol.AutomationRunMessage))
	case protocol.CmdSpawnSession:
		spawn := msg.(*protocol.SpawnSessionMessage)
		spawn.ProfileID = protocol.Deref(client.profileOr(protocol.Ptr(spawn.ProfileID)))
		d.life.Go("handleSpawnSession", func() { d.handleSpawnSession(client, spawn) })
	case protocol.CmdAttachSession:
		attach := msg.(*protocol.AttachSessionMessage)
		attachNow := func() { d.handleAttachSession(client, attach) }
		if protocol.Deref(attach.AttachPolicy) == protocol.AttachPolicyRevive {
			d.offPumpInTerminalOrder(client, attach.ID, attachNow)
		} else {
			d.inTerminalOrder(client, attach.ID, attachNow)
		}
	case protocol.CmdDetachSession:
		detach := msg.(*protocol.DetachSessionMessage)
		d.inTerminalOrder(client, detach.ID, func() { d.handleDetachSessionWS(client, detach) })
	case protocol.CmdGetScreenSnapshot:
		snapshot := msg.(*protocol.GetScreenSnapshotMessage)
		d.inTerminalOrder(client, snapshot.ID, func() { d.handleGetScreenSnapshot(client, snapshot) })
	case protocol.CmdGetKittyImage:
		image := msg.(*protocol.GetKittyImageMessage)
		d.inTerminalOrder(client, image.ID, func() { d.handleGetKittyImage(client, image) })
	case protocol.CmdPtyInput:
		d.handlePtyInput(client, msg.(*protocol.PtyInputMessage))
	case protocol.CmdSupportSnapshot:
		d.handleSupportSnapshot(client, msg.(*protocol.SupportSnapshotMessage))
	case protocol.CmdTerminalPointerActivity:
		pointer := msg.(*protocol.TerminalPointerActivityMessage)
		d.handleTerminalPointerActivity(client, pointer)
	case protocol.CmdBusStatusGet:
		d.handleBusStatusGet(client, msg.(*protocol.BusStatusGetMessage))
	case protocol.CmdBusSetConsumerEnabled:
		d.handleBusSetConsumerEnabled(client, msg.(*protocol.BusSetConsumerEnabledMessage))
	case protocol.CmdAutoModeGet:
		d.handleAutoModeGet(client, msg.(*protocol.AutoModeGetMessage))
	case protocol.CmdAutoModePromote:
		d.handleAutoModePromote(client, msg.(*protocol.AutoModePromoteMessage))
	case protocol.CmdAutoModeDiscard:
		d.handleAutoModeDiscard(client, msg.(*protocol.AutoModeDiscardMessage))
	case protocol.CmdAutoModeEnvSlot:
		d.handleAutoModeEnvSlotWS(client, msg.(*protocol.AutoModeEnvSlotMessage))
	case protocol.CmdAutoModeEnvNotes:
		d.handleAutoModeEnvNotesWS(client, msg.(*protocol.AutoModeEnvNotesMessage))
	case protocol.CmdAutoModeRuleAdd:
		d.handleAutoModeRuleAdd(client, msg.(*protocol.AutoModeRuleAddMessage))
	case protocol.CmdAutoModeRuleRemove:
		d.handleAutoModeRuleRemoveWS(client, msg.(*protocol.AutoModeRuleRemoveMessage))
	case protocol.CmdAutoModeHostAdd:
		d.handleAutoModeHostAdd(client, msg.(*protocol.AutoModeHostAddMessage))
	case protocol.CmdAutoModeHostRemove:
		d.handleAutoModeHostRemoveWS(client, msg.(*protocol.AutoModeHostRemoveMessage))
	case protocol.CmdAutoModePolicySet:
		d.handleAutoModePolicySetWS(client, msg.(*protocol.AutoModePolicySetMessage))
	case protocol.CmdAutoModeLegacyDismiss:
		d.handleAutoModeLegacyDismissWS(client, msg.(*protocol.AutoModeLegacyDismissMessage))
	case protocol.CmdPtyResize:
		resize := msg.(*protocol.PtyResizeMessage)
		d.handlePtyResize(client, resize)
	case protocol.CmdKillSession:
		kill := msg.(*protocol.KillSessionMessage)
		if d.sessionHost(kill.ID) != "" {
			d.offPumpInTerminalOrder(client, d.primaryTerminal(kill.ID), func() { d.handleKillSession(client, kill) })
		} else {
			d.handleKillSession(client, kill)
		}
	case protocol.CmdReloadSession:
		reload := msg.(*protocol.ReloadSessionMessage)
		if d.sessionHost(reload.ID) != "" {
			d.offPumpInTerminalOrder(client, d.primaryTerminal(reload.ID), func() { d.handleReloadSession(client, reload) })
		} else {
			d.life.Go("handleReloadSession", func() { d.handleReloadSession(client, reload) })
		}
	case protocol.CmdRestartSharedCodex:
		d.life.Go("handleRestartSharedCodex", func() { d.handleRestartSharedCodex(client, msg.(*protocol.RestartSharedCodexMessage)) })
	case protocol.CmdInstallCodexUpdate:
		d.life.Go("handleInstallCodexUpdate", func() { d.handleInstallCodexUpdate(client, msg.(*protocol.InstallCodexUpdateMessage)) })
	case protocol.CmdSetTerminalTheme:
		d.handleSetTerminalTheme(client, msg.(*protocol.SetTerminalThemeMessage))
	case protocol.CmdSetClientPresence:
		d.handleSetClientPresence(client, msg.(*protocol.SetClientPresenceMessage))
	case protocol.CmdDocSubscribe:
		d.handleDocSubscribeWS(client, msg.(*protocol.DocSubscribeMessage))
	case protocol.CmdDocUnsubscribe:
		d.handleDocUnsubscribeWS(client, msg.(*protocol.DocUnsubscribeMessage))
	case protocol.CmdOpenMarkdown:
		d.handleOpenMarkdownWS(client, msg.(*protocol.OpenMarkdownMessage))
	case protocol.CmdOpenSeed:
		d.handleOpenSeedWS(client, msg.(*protocol.OpenSeedMessage))
	case protocol.CmdSeedDocumentGet:
		d.handleSeedDocumentGet(client, msg.(*protocol.SeedDocumentGetMessage))
	case protocol.CmdSeedTransition:
		transition := msg.(*protocol.SeedTransitionMessage)
		if harvestWhenRequested(transition) {
			d.inSessionOrder(client, protocol.Deref(transition.SourceSessionID), func() { d.handleSeedTransitionWS(client, transition) })
		} else {
			d.handleSeedTransitionWS(client, transition)
		}
	case protocol.CmdSeedNote:
		d.handleSeedNoteWS(client, msg.(*protocol.SeedNoteMessage))
	case protocol.CmdSeedCheck:
		d.handleSeedCheckWS(client, msg.(*protocol.SeedCheckMessage))
	case protocol.CmdSessionMessagesGet:
		messages := msg.(*protocol.SessionMessagesGetMessage)
		d.inSessionOrder(client, messages.SessionID, func() { d.handleSessionMessagesGet(client, messages) })
	case protocol.CmdSessionAnnotationsGet:
		d.handleSessionAnnotationsGet(client, msg.(*protocol.SessionAnnotationsGetMessage))
	case protocol.CmdSessionAnnotationsSave:
		d.handleSessionAnnotationsSave(client, msg.(*protocol.SessionAnnotationsSaveMessage))
	case protocol.CmdSessionAnnotationsClear:
		d.handleSessionAnnotationsClear(client, msg.(*protocol.SessionAnnotationsClearMessage))
	case protocol.CmdSessionAnnotationsSubmit:
		submit := msg.(*protocol.SessionAnnotationsSubmitMessage)
		d.inSessionOrder(client, submit.SessionID, func() { d.handleSessionAnnotationsSubmit(client, submit) })
	case protocol.CmdMarkdownAnnotationsGet:
		d.handleMarkdownAnnotationsGet(client, msg.(*protocol.MarkdownAnnotationsGetMessage))
	case protocol.CmdMarkdownAnnotationsSave:
		d.handleMarkdownAnnotationsSave(client, msg.(*protocol.MarkdownAnnotationsSaveMessage))
	case protocol.CmdMarkdownAnnotationsClear:
		d.handleMarkdownAnnotationsClear(client, msg.(*protocol.MarkdownAnnotationsClearMessage))
	case protocol.CmdMarkdownAnnotationsSubmit:
		submit := msg.(*protocol.MarkdownAnnotationsSubmitMessage)
		d.inSessionOrder(client, protocol.SessionID(protocol.Deref(submit.TargetSessionID)), func() { d.handleMarkdownAnnotationsSubmit(client, submit) })
	case protocol.CmdCalloutResult:
		d.handleCalloutResult(client, msg.(*protocol.CalloutResultMessage))
	case protocol.CmdBrowserControlResult:
		d.handleBrowserControlResult(client, msg.(*protocol.BrowserControlResultMessage))
	case protocol.CmdRenameSession:
		rename := msg.(*protocol.RenameSessionMessage)
		d.inSessionOrder(client, rename.SessionID, func() { d.handleRenameSession(client, rename) })
	case protocol.CmdSetChiefOfStaff:
		d.handleSetChiefOfStaff(client, msg.(*protocol.SetChiefOfStaffMessage))
	default:
		d.sendCommandError(client, cmd, "unsupported command")
	}
}

func (d *Daemon) sendCommandError(client *wsClient, cmd, errMsg string) {
	event := &protocol.WebSocketEvent{
		Event:   protocol.EventCommandError,
		Cmd:     protocol.Ptr(cmd),
		Success: protocol.Ptr(false),
		Error:   protocol.Ptr(errMsg),
	}
	d.sendToClient(client, event)
}

func (d *Daemon) sendTerminalRefusal(client *wsClient, cmd string, terminal protocol.TerminalID, err error) {
	d.sendToClient(client, &protocol.CommandErrorMessage{Event: protocol.EventCommandError, Cmd: protocol.Ptr(cmd), Error: err.Error(), TerminalID: protocol.Ptr(string(terminal))})
}

func (d *Daemon) sendToClient(client *wsClient, message interface{}) bool {
	data, err := json.Marshal(message)
	if err != nil {
		return false
	}
	return d.sendOutbound(client, outboundMessage{
		kind:    messageKindText,
		payload: data,
	})
}

func (d *Daemon) broadcastMessage(message interface{}) {
	if d.wsHub == nil {
		return
	}
	d.wsHub.BroadcastValue(message)
}
