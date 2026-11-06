package testworld

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"nhooyr.io/websocket"
)

// A stand-in Cloud Relay, as docs/internal/relay-contract.md specifies it; internal/cloudlink/relaytest holds it to that.

const (
	frameChallenge = 1
	frameAuth      = 2
	frameReady     = 3
	frameOpen      = 4
	frameData      = 5
	frameClose     = 6
	frameAllowlist = 7
	framePresence  = 8
	frameError     = 9
	framePush      = 10

	notAdmittedReason = "device is not on the home's allowlist and no pairing window is open"
	maxAllowlist      = 1000
	maxNewHomes       = 100
)

type Cloud struct {
	t       *testing.T
	URL     string
	HomeURL string
	proxy   *cutProxy
	relay   *fakeRelay
}

// Push is a push frame the home sent the relay, which the real Cloud would hand to APNs.
type Push struct {
	Device     string `json:"device"`
	Ciphertext string `json:"ciphertext"`
	CollapseID string `json:"collapse_id"`
}

func StartCloud(t *testing.T) *Cloud {
	t.Helper()
	relay := strings.TrimRight(os.Getenv("ATTN_TEST_CLOUD_URL"), "/")
	var fake *fakeRelay
	if relay == "" {
		fake = newFakeRelay()
		server := httptest.NewServer(fake.handler())
		t.Cleanup(server.Close)
		relay = server.URL
	}
	target, err := url.Parse(relay)
	if err != nil {
		t.Fatal(err)
	}
	proxy := startCutProxy(t, target.Host)
	return &Cloud{t: t, URL: relay, HomeURL: "http://" + proxy.addr, proxy: proxy, relay: fake}
}

// Pushes yields the push frames the relay accepted, in order. A push needs a registered token: see PhoneSocket.RegisterForPush. A real Cloud hands them to APNs instead, so a test
// that reads them skips under ATTN_TEST_CLOUD_URL.
func (c *Cloud) Pushes() <-chan Push {
	if c.relay == nil {
		c.t.Skip("pushes are visible only through the fake relay; this run uses ATTN_TEST_CLOUD_URL")
	}
	return c.relay.pushes
}

func (c *Cloud) Dials() <-chan struct{} { return c.proxy.dials }

func (c *Cloud) Cut() int { return c.proxy.cut() }

// Outage cuts the home's connections and refuses new ones until the returned function runs.
func (c *Cloud) Outage() (restore func()) { return c.proxy.outage() }

func (c *Cloud) Open() int { return c.proxy.open() }

func (c *Cloud) Closed() <-chan struct{} { return c.proxy.closed }

type cutProxy struct {
	addr   string
	target string
	dials  chan struct{}
	closed chan struct{}
	mu     sync.Mutex
	down   bool
	conns  map[net.Conn]net.Conn
}

func startCutProxy(t *testing.T, target string) *cutProxy {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &cutProxy{addr: listener.Addr().String(), target: target, dials: make(chan struct{}, 64), closed: make(chan struct{}, 64), conns: map[net.Conn]net.Conn{}}
	t.Cleanup(func() {
		listener.Close()
		p.cut()
	})
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go p.serve(conn)
		}
	}()
	return p
}

func (p *cutProxy) serve(home net.Conn) {
	p.mu.Lock()
	down := p.down
	p.mu.Unlock()
	if down {
		home.Close()
		return
	}
	cloud, err := net.Dial("tcp", p.target)
	if err != nil {
		home.Close()
		return
	}
	p.mu.Lock()
	p.conns[home] = cloud
	p.mu.Unlock()
	p.dials <- struct{}{}
	done := make(chan struct{}, 2)
	go func() { io.Copy(cloud, home); done <- struct{}{} }()
	go func() { io.Copy(home, cloud); done <- struct{}{} }()
	<-done
	home.Close()
	cloud.Close()
	p.mu.Lock()
	_, tracked := p.conns[home]
	delete(p.conns, home)
	p.mu.Unlock()
	if tracked {
		p.closed <- struct{}{}
	}
}

func (p *cutProxy) cut() int {
	p.mu.Lock()
	conns := p.conns
	p.conns = map[net.Conn]net.Conn{}
	p.mu.Unlock()
	for home, cloud := range conns {
		home.Close()
		cloud.Close()
	}
	return len(conns)
}

func (p *cutProxy) outage() func() {
	p.mu.Lock()
	p.down = true
	p.mu.Unlock()
	p.cut()
	return func() {
		p.mu.Lock()
		p.down = false
		p.mu.Unlock()
	}
}

func (p *cutProxy) open() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.conns)
}

func b64(data []byte) string { return base64.RawURLEncoding.EncodeToString(data) }

func keyID(pub []byte) string {
	sum := sha256.Sum256(pub)
	return strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(sum[:16]))
}

func encodeEnvelope(kind byte, connID uint32, payload []byte) []byte {
	out := make([]byte, 6+len(payload))
	out[0], out[1] = 1, kind
	binary.BigEndian.PutUint32(out[2:6], connID)
	copy(out[6:], payload)
	return out
}

type envelope struct {
	kind    byte
	connID  uint32
	payload []byte
}

func decodeEnvelope(data []byte) (envelope, error) {
	if len(data) < 6 || data[0] != 1 {
		return envelope{}, errors.New("malformed envelope")
	}
	return envelope{kind: data[1], connID: binary.BigEndian.Uint32(data[2:6]), payload: data[6:]}, nil
}

type fakeRelay struct {
	mu     sync.Mutex
	homes  map[string]*fakeHome
	pushes chan Push
}

type fakeHome struct {
	signPub     ed25519.PublicKey
	home        *websocket.Conn
	devices     map[uint32]*fakeDevice
	allowed     []string
	openUntil   int64
	nextConn    uint32
	tokens      map[string]bool
	listApplied chan struct{}
}

type fakeDevice struct {
	conn    *websocket.Conn
	signPub string
}

func newFakeRelay() *fakeRelay {
	return &fakeRelay{homes: map[string]*fakeHome{}, pushes: make(chan Push, 64)}
}

func (r *fakeRelay) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/version", r.version)
	mux.HandleFunc("/v1/homes", r.register)
	mux.HandleFunc("/v1/homes/{id}/home", r.serveHome)
	mux.HandleFunc("/v1/homes/{id}/device", r.serveDevice)
	mux.HandleFunc("/v1/homes/{id}/push-token", r.registerPushToken)
	mux.HandleFunc("/", func(w http.ResponseWriter, req *http.Request) {
		cloudRefusal(w, 404, "not_found", "no route for "+req.Method+" "+req.URL.Path)
	})
	return mux
}

func cloudRefusal(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("content-type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": code, "message": message}})
}

func decodeKey(text string) (ed25519.PublicKey, bool) {
	raw, err := base64.RawURLEncoding.DecodeString(text)
	return raw, err == nil && len(raw) == ed25519.PublicKeySize
}

func (r *fakeRelay) version(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		cloudRefusal(w, 405, "method_not_allowed", "use GET /v1/version")
		return
	}
	json.NewEncoder(w).Encode(map[string]any{"commit": "stand-in", "envelope_version": 1})
}

func (r *fakeRelay) register(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		cloudRefusal(w, 405, "method_not_allowed", "use POST /v1/homes")
		return
	}
	var body struct {
		HomeSignPub string `json:"home_sign_pub"`
		Name        string `json:"name"`
		Sig         string `json:"sig"`
	}
	if json.NewDecoder(req.Body).Decode(&body) != nil {
		cloudRefusal(w, 400, "bad_request", "body must be JSON: {home_sign_pub, name, sig}")
		return
	}
	pub, ok := decodeKey(body.HomeSignPub)
	if !ok {
		cloudRefusal(w, 400, "bad_request", "home_sign_pub must be a 32-byte Ed25519 key, base64url")
		return
	}
	if length := len([]rune(body.Name)); length < 1 || length > 100 {
		cloudRefusal(w, 400, "bad_request", fmt.Sprintf("name must be 1 to 100 characters; got %d", length))
		return
	}
	sig, err := base64.RawURLEncoding.DecodeString(body.Sig)
	if err != nil || !ed25519.Verify(pub, []byte("attn-cloud/v1 register\n"+body.HomeSignPub+"\n"+body.Name), sig) {
		cloudRefusal(w, 401, "bad_signature", "registration signature does not verify")
		return
	}
	id := keyID(pub)
	r.mu.Lock()
	if r.homes[id] == nil && len(r.homes) >= maxNewHomes {
		r.mu.Unlock()
		cloudRefusal(w, 429, "registration_limited", fmt.Sprintf("%d new relays today, the limit is %d; try after 00:00 UTC", len(r.homes), maxNewHomes))
		return
	}
	if r.homes[id] == nil {
		r.homes[id] = &fakeHome{signPub: pub, devices: map[uint32]*fakeDevice{}, tokens: map[string]bool{}, listApplied: make(chan struct{})}
	}
	r.mu.Unlock()
	json.NewEncoder(w).Encode(map[string]string{"home_id": id})
}

func (r *fakeRelay) lookup(id string) *fakeHome {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.homes[id]
}

func authenticate(ctx context.Context, conn *websocket.Conn, prefix string, pub ed25519.PublicKey) bool {
	nonce := make([]byte, 32)
	rand.Read(nonce)
	if conn.Write(ctx, websocket.MessageBinary, encodeEnvelope(frameChallenge, 0, nonce)) != nil {
		return false
	}
	_, data, err := conn.Read(ctx)
	if err != nil {
		return false
	}
	f, err := decodeEnvelope(data)
	if err != nil || f.kind != frameAuth || !ed25519.Verify(pub, append([]byte(prefix), nonce...), f.payload) {
		conn.Close(4401, "authentication failed")
		return false
	}
	return true
}

func isUpgrade(req *http.Request) bool {
	return strings.EqualFold(req.Header.Get("Upgrade"), "websocket")
}

func (r *fakeRelay) serveHome(w http.ResponseWriter, req *http.Request) {
	id := req.PathValue("id")
	h := r.lookup(id)
	if h == nil {
		cloudRefusal(w, 404, "unknown_home", "home "+id+" is not registered")
		return
	}
	if !isUpgrade(req) {
		cloudRefusal(w, 426, "upgrade_required", req.URL.Path+" is a WebSocket endpoint")
		return
	}
	conn, err := websocket.Accept(w, req, nil)
	if err != nil {
		return
	}
	conn.SetReadLimit(-1)
	ctx := req.Context()
	if !authenticate(ctx, conn, "attn-cloud/v1 auth home\n"+id+"\n", h.signPub) {
		return
	}
	r.mu.Lock()
	if h.home != nil {
		closeWithoutWaiting(h.home, 4409, "replaced by a newer home connection")
	}
	h.home = conn
	conn.Write(ctx, websocket.MessageBinary, encodeEnvelope(frameReady, 0, nil))
	for connID, dev := range h.devices {
		conn.Write(ctx, websocket.MessageBinary, encodeEnvelope(frameOpen, connID, mustUnb64(dev.signPub)))
		dev.conn.Write(ctx, websocket.MessageBinary, encodeEnvelope(framePresence, connID, []byte{1}))
	}
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		if h.home == conn {
			h.home = nil
			for connID, dev := range h.devices {
				dev.conn.Write(context.Background(), websocket.MessageBinary, encodeEnvelope(framePresence, connID, []byte{0}))
			}
		}
		r.mu.Unlock()
	}()
	for {
		kind, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		if kind == websocket.MessageText {
			respondToText(ctx, conn, data)
			continue
		}
		f, ok := readEnvelope(conn, data)
		if !ok {
			return
		}
		r.fromHome(ctx, h, f)
	}
}

// closeWithoutWaiting closes a socket whose peer may not read for a while, because Close waits for the peer's answer and the relay's lock must not.
func closeWithoutWaiting(conn *websocket.Conn, code websocket.StatusCode, reason string) {
	go conn.Close(code, reason)
}

func respondToText(ctx context.Context, conn *websocket.Conn, data []byte) {
	if string(data) == "ping" {
		conn.Write(ctx, websocket.MessageText, []byte("pong"))
		return
	}
	writeRelayError(ctx, conn, "text_frame", `text frames carry only "ping"; send binary frames`)
}

func readEnvelope(conn *websocket.Conn, data []byte) (envelope, bool) {
	if len(data) >= 1 && data[0] != 1 {
		conn.Close(4400, fmt.Sprintf("envelope version %d unsupported; relay speaks 1", data[0]))
		return envelope{}, false
	}
	f, err := decodeEnvelope(data)
	if err != nil {
		conn.Close(4400, "frame shorter than the 6-byte header")
		return envelope{}, false
	}
	return f, true
}

func writeRelayError(ctx context.Context, conn *websocket.Conn, code, message string) {
	body, _ := json.Marshal(map[string]string{"code": code, "message": message})
	conn.Write(ctx, websocket.MessageBinary, encodeEnvelope(frameError, 0, body))
}

func mustUnb64(s string) []byte {
	out, _ := base64.RawURLEncoding.DecodeString(s)
	return out
}

func (r *fakeRelay) fromHome(ctx context.Context, h *fakeHome, f envelope) {
	r.mu.Lock()
	defer r.mu.Unlock()
	switch f.kind {
	case frameData:
		if dev := h.devices[f.connID]; dev != nil {
			dev.conn.Write(ctx, websocket.MessageBinary, encodeEnvelope(frameData, f.connID, f.payload))
		} else {
			h.home.Write(ctx, websocket.MessageBinary, encodeEnvelope(frameClose, f.connID, nil))
		}
	case frameClose:
		if dev := h.devices[f.connID]; dev != nil {
			delete(h.devices, f.connID)
			closeWithoutWaiting(dev.conn, 4000, "closed by home")
		}
	case frameAllowlist:
		r.applyAllowlist(ctx, h, f.payload)
	case framePush:
		r.pushFromHome(ctx, h, f.payload)
	default:
		writeRelayError(ctx, h.home, "unsupported_type", fmt.Sprintf("the relay takes no frame type %d from a home", f.kind))
	}
}

func (r *fakeRelay) applyAllowlist(ctx context.Context, h *fakeHome, payload []byte) {
	var list struct {
		Devices          *[]string `json:"devices"`
		PairingOpenUntil *int64    `json:"pairing_open_until"`
	}
	if json.Unmarshal(payload, &list) != nil || list.Devices == nil || list.PairingOpenUntil == nil {
		writeRelayError(ctx, h.home, "bad_allowlist", "allowlist payload must be JSON: {devices, pairing_open_until}")
		return
	}
	if count := len(*list.Devices); count > maxAllowlist {
		writeRelayError(ctx, h.home, "bad_allowlist", fmt.Sprintf("allowlist has %d devices; the limit is %d", count, maxAllowlist))
		return
	}
	for _, device := range *list.Devices {
		if _, ok := decodeKey(device); !ok {
			writeRelayError(ctx, h.home, "bad_allowlist", fmt.Sprintf("%q is not a 32-byte key in base64url", device))
			return
		}
	}
	h.allowed, h.openUntil = *list.Devices, *list.PairingOpenUntil
	for device := range h.tokens {
		if !slices.Contains(h.allowed, device) {
			delete(h.tokens, device)
		}
	}
	for connID, dev := range h.devices {
		if !h.admitsLocked(dev.signPub) {
			delete(h.devices, connID)
			closeWithoutWaiting(dev.conn, 4403, notAdmittedReason)
			h.home.Write(ctx, websocket.MessageBinary, encodeEnvelope(frameClose, connID, nil))
		}
	}
	close(h.listApplied)
	h.listApplied = make(chan struct{})
	h.home.Write(ctx, websocket.MessageBinary, encodeEnvelope(frameAllowlist, 0, nil))
}

func (r *fakeRelay) pushFromHome(ctx context.Context, h *fakeHome, payload []byte) {
	var push Push
	if json.Unmarshal(payload, &push) != nil {
		writeRelayError(ctx, h.home, "bad_push", "push payload must be JSON: {device, ciphertext, collapse_id}")
		return
	}
	if _, err := base64.RawURLEncoding.DecodeString(push.Ciphertext); err != nil || push.Ciphertext == "" {
		writeRelayError(ctx, h.home, "bad_push", "ciphertext must be non-empty base64url")
		return
	}
	if length := len(push.CollapseID); length < 1 || length > 64 {
		writeRelayError(ctx, h.home, "bad_push", fmt.Sprintf("collapse_id must be 1 to 64 bytes; got %d", length))
		return
	}
	if !slices.Contains(h.allowed, push.Device) {
		writeRelayError(ctx, h.home, "push_refused", "device "+push.Device+" is not on the home's allowlist")
		return
	}
	if !h.tokens[push.Device] {
		writeRelayError(ctx, h.home, "push_no_token", "device "+push.Device+" has not registered a push token")
		return
	}
	r.pushes <- push
}

func (r *fakeRelay) registerPushToken(w http.ResponseWriter, req *http.Request) {
	id := req.PathValue("id")
	if req.Method != http.MethodPost {
		cloudRefusal(w, 405, "method_not_allowed", "use POST /v1/homes/"+id+"/push-token")
		return
	}
	var body struct {
		DeviceSignPub string `json:"device_sign_pub"`
		Platform      string `json:"platform"`
		Environment   string `json:"environment"`
		Token         string `json:"token"`
		Sig           string `json:"sig"`
	}
	if json.NewDecoder(req.Body).Decode(&body) != nil {
		cloudRefusal(w, 400, "bad_request", "body must be JSON: {device_sign_pub, platform, environment, token, sig}")
		return
	}
	pub, ok := decodeKey(body.DeviceSignPub)
	if !ok {
		cloudRefusal(w, 400, "bad_request", "device_sign_pub must be a 32-byte Ed25519 key, base64url")
		return
	}
	if body.Platform != "apns" {
		cloudRefusal(w, 400, "bad_request", fmt.Sprintf("platform must be \"apns\"; got %q", body.Platform))
		return
	}
	if body.Environment != "sandbox" && body.Environment != "production" {
		cloudRefusal(w, 400, "bad_request", fmt.Sprintf("environment must be sandbox or production; got %q", body.Environment))
		return
	}
	token, err := hex.DecodeString(body.Token)
	if err != nil || body.Token != strings.ToLower(body.Token) {
		cloudRefusal(w, 400, "bad_request", "token must be lowercase hex with an even number of digits")
		return
	}
	if len(token) < 16 || len(token) > 200 {
		cloudRefusal(w, 400, "bad_request", fmt.Sprintf("token must be 16 to 200 bytes; got %d", len(token)))
		return
	}
	sig, err := base64.RawURLEncoding.DecodeString(body.Sig)
	message := "attn-cloud/v1 push-token\n" + id + "\n" + body.DeviceSignPub + "\n" + body.Environment + "\n" + body.Token
	if err != nil || !ed25519.Verify(pub, []byte(message), sig) {
		cloudRefusal(w, 401, "bad_signature", "sig does not verify against device_sign_pub")
		return
	}
	h := r.lookup(id)
	if h == nil {
		cloudRefusal(w, 404, "unknown_home", "home "+id+" is not registered")
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !slices.Contains(h.allowed, body.DeviceSignPub) {
		cloudRefusal(w, 403, "device_not_allowed", "device "+body.DeviceSignPub+" is not on the home's allowlist")
		return
	}
	h.tokens[body.DeviceSignPub] = true
	w.Header().Set("content-type", "application/json")
	w.Write([]byte("{}"))
}

// awaitAllowed returns once the home's allowlist holds the device, which is when a real phone's retried token registration succeeds.
func (r *fakeRelay) awaitAllowed(ctx context.Context, homeID, signPub string) error {
	h := r.lookup(homeID)
	for {
		r.mu.Lock()
		allowed, applied := slices.Contains(h.allowed, signPub), h.listApplied
		r.mu.Unlock()
		if allowed {
			return nil
		}
		select {
		case <-applied:
		case <-ctx.Done():
			return fmt.Errorf("the home's allowlist never named device %s: %w", signPub, ctx.Err())
		}
	}
}

func (h *fakeHome) admitsLocked(signPub string) bool {
	return slices.Contains(h.allowed, signPub) || time.Now().UnixMilli() < h.openUntil
}

func (r *fakeRelay) serveDevice(w http.ResponseWriter, req *http.Request) {
	id := req.PathValue("id")
	h := r.lookup(id)
	if h == nil {
		cloudRefusal(w, 404, "unknown_home", "home "+id+" is not registered")
		return
	}
	if !isUpgrade(req) {
		cloudRefusal(w, 426, "upgrade_required", req.URL.Path+" is a WebSocket endpoint")
		return
	}
	signPub := req.URL.Query().Get("device_sign_pub")
	if _, ok := decodeKey(signPub); !ok {
		cloudRefusal(w, 400, "bad_request", "device_sign_pub must be a 32-byte Ed25519 key, base64url")
		return
	}
	r.mu.Lock()
	admitted := h.admitsLocked(signPub)
	r.mu.Unlock()
	conn, err := websocket.Accept(w, req, nil)
	if err != nil {
		return
	}
	conn.SetReadLimit(-1)
	if !admitted {
		conn.Close(4403, notAdmittedReason)
		return
	}
	ctx := req.Context()
	if !authenticate(ctx, conn, "attn-cloud/v1 auth device\n"+id+"\n", mustUnb64(signPub)) {
		return
	}
	r.mu.Lock()
	h.nextConn++
	connID := h.nextConn
	h.devices[connID] = &fakeDevice{conn: conn, signPub: signPub}
	online := byte(0)
	conn.Write(ctx, websocket.MessageBinary, encodeEnvelope(frameReady, connID, nil))
	if h.home != nil {
		online = 1
		h.home.Write(ctx, websocket.MessageBinary, encodeEnvelope(frameOpen, connID, mustUnb64(signPub)))
	}
	conn.Write(ctx, websocket.MessageBinary, encodeEnvelope(framePresence, connID, []byte{online}))
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		if h.devices[connID] != nil {
			delete(h.devices, connID)
			if h.home != nil {
				h.home.Write(context.Background(), websocket.MessageBinary, encodeEnvelope(frameClose, connID, nil))
			}
		}
		r.mu.Unlock()
	}()
	for {
		kind, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		if kind == websocket.MessageText {
			respondToText(ctx, conn, data)
			continue
		}
		f, ok := readEnvelope(conn, data)
		if !ok {
			return
		}
		if f.kind != frameData {
			writeRelayError(ctx, conn, "unsupported_type", fmt.Sprintf("devices send only data frames; got type %d", f.kind))
			continue
		}
		if f.connID != connID {
			conn.Close(4400, fmt.Sprintf("conn_id %d is not this socket's %d", f.connID, connID))
			return
		}
		r.mu.Lock()
		if h.home != nil {
			h.home.Write(ctx, websocket.MessageBinary, data)
		} else {
			conn.Write(ctx, websocket.MessageBinary, encodeEnvelope(framePresence, connID, []byte{0}))
		}
		r.mu.Unlock()
	}
}
