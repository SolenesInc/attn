package testworld

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
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

// Attn Cloud, as docs/protocol.md in SolenesInc/attn-cloud specifies it.

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

// Pushes yields the push frames the relay accepted, in order. A real Cloud hands them to APNs instead, so a test
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
	signPub   ed25519.PublicKey
	home      *websocket.Conn
	devices   map[uint32]*fakeDevice
	allowed   []string
	openUntil int64
	nextConn  uint32
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
	mux.HandleFunc("POST /v1/homes", r.register)
	mux.HandleFunc("GET /v1/homes/{id}/home", r.serveHome)
	mux.HandleFunc("GET /v1/homes/{id}/device", r.serveDevice)
	return mux
}

func cloudRefusal(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("content-type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": code, "message": message}})
}

func (r *fakeRelay) register(w http.ResponseWriter, req *http.Request) {
	var body struct {
		HomeSignPub string `json:"home_sign_pub"`
		Name        string `json:"name"`
		Sig         string `json:"sig"`
	}
	if json.NewDecoder(req.Body).Decode(&body) != nil {
		cloudRefusal(w, 400, "bad_request", "body")
		return
	}
	pub, err1 := base64.RawURLEncoding.DecodeString(body.HomeSignPub)
	sig, err2 := base64.RawURLEncoding.DecodeString(body.Sig)
	if err1 != nil || err2 != nil || len(pub) != ed25519.PublicKeySize || !ed25519.Verify(pub, []byte("attn-cloud/v1 register\n"+body.HomeSignPub+"\n"+body.Name), sig) {
		cloudRefusal(w, 401, "bad_signature", "registration signature does not verify")
		return
	}
	id := keyID(pub)
	r.mu.Lock()
	if r.homes[id] == nil {
		r.homes[id] = &fakeHome{signPub: pub, devices: map[uint32]*fakeDevice{}}
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

func (r *fakeRelay) serveHome(w http.ResponseWriter, req *http.Request) {
	id := req.PathValue("id")
	h := r.lookup(id)
	if h == nil {
		cloudRefusal(w, 404, "unknown_home", "no home is registered under "+id)
		return
	}
	conn, err := websocket.Accept(w, req, nil)
	if err != nil {
		return
	}
	ctx := req.Context()
	if !authenticate(ctx, conn, "attn-cloud/v1 auth home\n"+id+"\n", h.signPub) {
		return
	}
	r.mu.Lock()
	if h.home != nil {
		h.home.Close(4409, "replaced")
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
			if string(data) == "ping" {
				conn.Write(ctx, websocket.MessageText, []byte("pong"))
			}
			continue
		}
		f, err := decodeEnvelope(data)
		if err != nil {
			conn.Close(4400, "malformed frame")
			return
		}
		r.fromHome(ctx, h, f)
	}
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
			dev.conn.Close(4000, "the home closed this connection")
		}
	case frameAllowlist:
		var list struct {
			Devices          []string `json:"devices"`
			PairingOpenUntil int64    `json:"pairing_open_until"`
		}
		if json.Unmarshal(f.payload, &list) != nil {
			h.home.Write(ctx, websocket.MessageBinary, encodeEnvelope(frameError, 0, []byte(`{"code":"bad_allowlist","message":"malformed"}`)))
			return
		}
		h.allowed, h.openUntil = list.Devices, list.PairingOpenUntil
		for connID, dev := range h.devices {
			if !h.admitsLocked(dev.signPub) {
				delete(h.devices, connID)
				dev.conn.Close(4403, "the device is no longer on the allowlist")
			}
		}
		h.home.Write(ctx, websocket.MessageBinary, encodeEnvelope(frameAllowlist, 0, nil))
	case framePush:
		var push Push
		if json.Unmarshal(f.payload, &push) != nil || push.Ciphertext == "" || push.CollapseID == "" {
			h.home.Write(ctx, websocket.MessageBinary, encodeEnvelope(frameError, 0, []byte(`{"code":"bad_push","message":"malformed"}`)))
			return
		}
		if !slices.Contains(h.allowed, push.Device) {
			h.home.Write(ctx, websocket.MessageBinary, encodeEnvelope(frameError, 0, []byte(`{"code":"push_refused","message":"device is not on the allowlist"}`)))
			return
		}
		r.pushes <- push
	}
}

func (h *fakeHome) admitsLocked(signPub string) bool {
	return slices.Contains(h.allowed, signPub) || time.Now().UnixMilli() < h.openUntil
}

func (r *fakeRelay) serveDevice(w http.ResponseWriter, req *http.Request) {
	id := req.PathValue("id")
	h := r.lookup(id)
	if h == nil {
		cloudRefusal(w, 404, "unknown_home", "no home is registered under "+id)
		return
	}
	signPub := req.URL.Query().Get("device_sign_pub")
	r.mu.Lock()
	admitted := h.admitsLocked(signPub)
	r.mu.Unlock()
	if !admitted {
		cloudRefusal(w, 403, "device_not_allowed", "device is not on the home's allowlist and no pairing window is open")
		return
	}
	conn, err := websocket.Accept(w, req, nil)
	if err != nil {
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
		_, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		f, err := decodeEnvelope(data)
		if err != nil || f.kind != frameData || f.connID != connID {
			conn.Close(4400, "malformed frame")
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
