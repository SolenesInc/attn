// Package relaytest checks a relay against docs/internal/relay-contract.md through its public HTTP and WebSocket
// boundary. It keeps its own copy of the contract's numbers and strings, so a change to attn's clients or stand-in
// relay fails here instead of moving the contract with it.
package relaytest

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"nhooyr.io/websocket"
)

const (
	challenge = 1
	auth      = 2
	ready     = 3
	open      = 4
	data      = 5
	closeConn = 6
	allowlist = 7
	presence  = 8
	failure   = 9
	push      = 10

	closedByHome   = 4000
	badFrame       = 4400
	authFailed     = 4401
	notAdmitted    = 4403
	replacedByNew  = 4409
	notAdmittedWhy = "device is not on the home's allowlist and no pairing window is open"
	allowlistLimit = 1000
	newHomesLimit  = 100
	pushToken      = "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"
)

// replyWait is a tripwire for a relay that never answers. A local relay answers in milliseconds.
const replyWait = 30 * time.Second

// Run drives the relay at baseURL through the contract. Run it against a fresh relay of your own, because it uses up the day's new homes.
func Run(t *testing.T, baseURL string) {
	r := relay{base: strings.TrimRight(baseURL, "/")}
	t.Run("version", r.version)
	t.Run("routes", r.routes)
	t.Run("registration", r.registration)
	t.Run("home authentication", r.homeAuthentication)
	t.Run("device admission", r.deviceAdmission)
	t.Run("device authentication", r.deviceAuthentication)
	t.Run("data", r.routing)
	t.Run("close", r.closing)
	t.Run("presence", r.presenceChanges)
	t.Run("a newer home replaces the older", r.replacement)
	t.Run("frames", r.frames)
	t.Run("heartbeat", r.heartbeat)
	t.Run("allowlist", r.allowlists)
	t.Run("push tokens", r.pushTokens)
	t.Run("push frames", r.pushFrames)
	t.Run("new homes past the limit", r.newHomeLimit)
}

type relay struct{ base string }

type key struct {
	private ed25519.PrivateKey
	pub     string
}

func newKey(t *testing.T) key {
	t.Helper()
	pub, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return key{private, b64(pub)}
}

func (k key) sign(message []byte) string { return b64(ed25519.Sign(k.private, message)) }

func b64(raw []byte) string { return base64.RawURLEncoding.EncodeToString(raw) }

func envelope(kind byte, connID uint32, payload []byte) []byte {
	out := append([]byte{1, kind, 0, 0, 0, 0}, payload...)
	binary.BigEndian.PutUint32(out[2:6], connID)
	return out
}

type frame struct {
	kind    byte
	connID  uint32
	payload []byte
}

type socket struct {
	t    *testing.T
	name string
	conn *websocket.Conn
}

func (s *socket) sendBytes(raw []byte) {
	s.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), replyWait)
	defer cancel()
	if err := s.conn.Write(ctx, websocket.MessageBinary, raw); err != nil {
		s.t.Fatalf("%s: sending: %v", s.name, err)
	}
}

func (s *socket) send(kind byte, connID uint32, payload []byte) {
	s.t.Helper()
	s.sendBytes(envelope(kind, connID, payload))
}

func (s *socket) sendJSON(kind byte, value any) {
	s.t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		s.t.Fatal(err)
	}
	s.send(kind, 0, raw)
}

func (s *socket) sendText(text string) {
	s.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), replyWait)
	defer cancel()
	if err := s.conn.Write(ctx, websocket.MessageText, []byte(text)); err != nil {
		s.t.Fatalf("%s: sending text: %v", s.name, err)
	}
}

func (s *socket) read() (websocket.MessageType, []byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), replyWait)
	defer cancel()
	return s.conn.Read(ctx)
}

func (s *socket) next() frame {
	s.t.Helper()
	kind, raw, err := s.read()
	if err != nil {
		s.t.Fatalf("%s: waiting %s for a frame: %v", s.name, replyWait, err)
	}
	if kind != websocket.MessageBinary {
		s.t.Fatalf("%s: text frame %q; the contract's only text frames are the heartbeat", s.name, raw)
	}
	if len(raw) < 6 {
		s.t.Fatalf("%s: a %d-byte frame; an envelope is at least 6 bytes", s.name, len(raw))
	}
	if raw[0] != 1 {
		s.t.Fatalf("%s: envelope version %d, want 1", s.name, raw[0])
	}
	return frame{raw[1], binary.BigEndian.Uint32(raw[2:6]), raw[6:]}
}

func (s *socket) expect(kind byte) frame {
	s.t.Helper()
	f := s.next()
	if f.kind != kind {
		s.t.Fatalf("%s: frame type %d (%q), want %d", s.name, f.kind, f.payload, kind)
	}
	return f
}

func (s *socket) expectPresence(connID uint32, online byte) {
	s.t.Helper()
	f := s.expect(presence)
	if f.connID != connID || !bytes.Equal(f.payload, []byte{online}) {
		s.t.Fatalf("%s: presence on conn %d with %v, want conn %d with [%d]", s.name, f.connID, f.payload, connID, online)
	}
}

func (s *socket) expectError(code string) string {
	s.t.Helper()
	f := s.expect(failure)
	if f.connID != 0 {
		s.t.Fatalf("%s: error frame on conn %d, want 0", s.name, f.connID)
	}
	var body struct{ Code, Message string }
	if err := json.Unmarshal(f.payload, &body); err != nil {
		s.t.Fatalf("%s: error payload %q is not JSON: %v", s.name, f.payload, err)
	}
	if body.Code != code || body.Message == "" {
		s.t.Fatalf("%s: error %+v, want code %s with a message", s.name, body, code)
	}
	return body.Message
}

func (s *socket) expectClosed(code int, reason string) {
	s.t.Helper()
	kind, raw, err := s.read()
	if err == nil {
		s.t.Fatalf("%s: message type %v %q, want close %d", s.name, kind, raw, code)
	}
	var closed websocket.CloseError
	if !errors.As(err, &closed) {
		s.t.Fatalf("%s: ended with %v, want close %d", s.name, err, code)
	}
	if int(closed.Code) != code {
		s.t.Fatalf("%s: closed with %d %q, want %d", s.name, closed.Code, closed.Reason, code)
	}
	if reason != "" && closed.Reason != reason {
		s.t.Fatalf("%s: close reason %q, want %q", s.name, closed.Reason, reason)
	}
}

func (r relay) dial(t *testing.T, name, path string) *socket {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), replyWait)
	defer cancel()
	conn, res, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(r.base, "http")+path, nil)
	if err != nil {
		status := 0
		if res != nil {
			status = res.StatusCode
		}
		t.Fatalf("%s: dialing %s: status %d: %v", name, path, status, err)
	}
	conn.SetReadLimit(1 << 20)
	t.Cleanup(func() { conn.CloseNow() })
	return &socket{t: t, name: name, conn: conn}
}

type answer struct {
	status int
	body   []byte
}

func (r relay) request(t *testing.T, method, path string, headers map[string]string, body any) answer {
	t.Helper()
	var reader io.Reader
	switch value := body.(type) {
	case nil:
	case string:
		reader = strings.NewReader(value)
	default:
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(raw)
	}
	ctx, cancel := context.WithTimeout(context.Background(), replyWait)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, r.base+path, reader)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("content-type", "application/json")
	}
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("%s %s: reading the answer: %v", method, path, err)
	}
	return answer{res.StatusCode, raw}
}

func (a answer) refusedWith(t *testing.T, status int, code string) {
	t.Helper()
	var body struct {
		Error struct{ Code, Message string } `json:"error"`
	}
	if err := json.Unmarshal(a.body, &body); err != nil {
		t.Fatalf("answer %d %q: every HTTP error has a JSON body: %v", a.status, a.body, err)
	}
	if a.status != status || body.Error.Code != code || body.Error.Message == "" {
		t.Fatalf("answer %d %s, want %d %s with a message", a.status, a.body, status, code)
	}
}

func registration(k key, name string) map[string]string {
	return map[string]string{"home_sign_pub": k.pub, "name": name, "sig": k.sign([]byte("attn-cloud/v1 register\n" + k.pub + "\n" + name))}
}

func (r relay) register(t *testing.T, k key, name string) string {
	t.Helper()
	got := r.request(t, http.MethodPost, "/v1/homes", nil, registration(k, name))
	if got.status != http.StatusOK {
		t.Fatalf("registering: %d %s", got.status, got.body)
	}
	var body struct {
		HomeID string `json:"home_id"`
	}
	if err := json.Unmarshal(got.body, &body); err != nil {
		t.Fatalf("registering: %s: %v", got.body, err)
	}
	return body.HomeID
}

type home struct {
	*socket
	id  string
	key key
}

func (r relay) newHome(t *testing.T) *home {
	t.Helper()
	k := newKey(t)
	id := r.register(t, k, "Mac")
	return r.connectHome(t, k, id)
}

func (r relay) connectHome(t *testing.T, k key, id string) *home {
	t.Helper()
	s := r.dial(t, "home", "/v1/homes/"+id+"/home")
	nonce := s.expect(challenge)
	if nonce.connID != 0 || len(nonce.payload) != 32 {
		t.Fatalf("home challenge on conn %d with %d bytes, want conn 0 with a 32-byte nonce", nonce.connID, len(nonce.payload))
	}
	s.send(auth, 0, ed25519.Sign(k.private, append([]byte("attn-cloud/v1 auth home\n"+id+"\n"), nonce.payload...)))
	if got := s.expect(ready); got.connID != 0 || len(got.payload) != 0 {
		t.Fatalf("home ready on conn %d with %d bytes, want conn 0 and empty", got.connID, len(got.payload))
	}
	return &home{s, id, k}
}

func (h *home) applyList(pairingOpenUntil int64, devices ...key) {
	h.t.Helper()
	pubs := []string{}
	for _, device := range devices {
		pubs = append(pubs, device.pub)
	}
	h.sendJSON(allowlist, map[string]any{"devices": pubs, "pairing_open_until": pairingOpenUntil})
	if got := h.expect(allowlist); got.connID != 0 || len(got.payload) != 0 {
		h.t.Fatalf("allowlist answered on conn %d with %q, want conn 0 and empty", got.connID, got.payload)
	}
}

func (h *home) expectOpen(device key) uint32 {
	h.t.Helper()
	f := h.expect(open)
	if b64(f.payload) != device.pub {
		h.t.Fatalf("open names %s, want %s", b64(f.payload), device.pub)
	}
	return f.connID
}

func (h *home) expectClose(connID uint32) {
	h.t.Helper()
	if f := h.expect(closeConn); f.connID != connID || len(f.payload) != 0 {
		h.t.Fatalf("close on conn %d with %q, want conn %d and empty", f.connID, f.payload, connID)
	}
}

func (r relay) dialDevice(t *testing.T, h *home, k key) *socket {
	t.Helper()
	return r.dial(t, "device", "/v1/homes/"+h.id+"/device?device_sign_pub="+k.pub)
}

// connectDevice leaves the socket after ready, with the presence frame that follows it unread.
func (r relay) connectDevice(t *testing.T, h *home, k key) (*socket, uint32) {
	t.Helper()
	s := r.dialDevice(t, h, k)
	nonce := s.expect(challenge)
	if nonce.connID != 0 || len(nonce.payload) != 32 {
		t.Fatalf("device challenge on conn %d with %d bytes, want conn 0 with a 32-byte nonce", nonce.connID, len(nonce.payload))
	}
	s.send(auth, 0, ed25519.Sign(k.private, append([]byte("attn-cloud/v1 auth device\n"+h.id+"\n"), nonce.payload...)))
	got := s.expect(ready)
	if got.connID == 0 || len(got.payload) != 0 {
		t.Fatalf("device ready on conn %d with %d bytes, want its non-zero conn_id and empty", got.connID, len(got.payload))
	}
	return s, got.connID
}

// pairedDevice connects an allowed device to a connected home, with both sockets' opening frames read.
func (r relay) pairedDevice(t *testing.T, h *home) (*socket, key, uint32) {
	t.Helper()
	k := newKey(t)
	h.applyList(0, k)
	s, connID := r.connectDevice(t, h, k)
	s.expectPresence(connID, 1)
	if got := h.expectOpen(k); got != connID {
		t.Fatalf("the home was told conn %d, the device conn %d", got, connID)
	}
	return s, k, connID
}

func (r relay) version(t *testing.T) {
	got := r.request(t, http.MethodGet, "/v1/version", nil, nil)
	var body struct {
		Commit          string `json:"commit"`
		EnvelopeVersion *int   `json:"envelope_version"`
	}
	if err := json.Unmarshal(got.body, &body); err != nil || got.status != http.StatusOK || body.Commit == "" || body.EnvelopeVersion == nil || *body.EnvelopeVersion != 1 {
		t.Fatalf("GET /v1/version = %d %s (%v), want 200 with a commit and envelope_version 1", got.status, got.body, err)
	}
	r.request(t, http.MethodPost, "/v1/version", nil, "{}").refusedWith(t, http.StatusMethodNotAllowed, "method_not_allowed")
}

func (r relay) routes(t *testing.T) {
	r.request(t, http.MethodGet, "/v1/nothing", nil, nil).refusedWith(t, http.StatusNotFound, "not_found")
	r.request(t, http.MethodGet, "/v1/homes", nil, nil).refusedWith(t, http.StatusMethodNotAllowed, "method_not_allowed")

	unknown := strings.Repeat("a", 26)
	upgrade := map[string]string{"Upgrade": "websocket", "Connection": "Upgrade", "Sec-WebSocket-Key": "dGhlIHNhbXBsZSBub25jZQ==", "Sec-WebSocket-Version": "13"}
	r.request(t, http.MethodGet, "/v1/homes/"+unknown+"/home", upgrade, nil).refusedWith(t, http.StatusNotFound, "unknown_home")
	r.request(t, http.MethodGet, "/v1/homes/"+unknown+"/device?device_sign_pub="+newKey(t).pub, upgrade, nil).refusedWith(t, http.StatusNotFound, "unknown_home")

	k := newKey(t)
	id := r.register(t, k, "Mac")
	r.request(t, http.MethodGet, "/v1/homes/"+id+"/home", nil, nil).refusedWith(t, http.StatusUpgradeRequired, "upgrade_required")
	r.request(t, http.MethodGet, "/v1/homes/"+id+"/device?device_sign_pub="+newKey(t).pub, nil, nil).refusedWith(t, http.StatusUpgradeRequired, "upgrade_required")
}

func (r relay) registration(t *testing.T) {
	k := newKey(t)
	id := r.register(t, k, "Mac")
	pub, err := base64.RawURLEncoding.DecodeString(k.pub)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(pub)
	want := strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(sum[:16]))
	if id != want || !regexp.MustCompile(`^[a-z2-7]{26}$`).MatchString(id) {
		t.Fatalf("home_id %q, want %q: the first 16 bytes of SHA-256(home_sign_pub) in lowercase base32", id, want)
	}
	if again := r.register(t, k, "Renamed Mac"); again != id {
		t.Fatalf("registering the key again gave %q, want the same %q", again, id)
	}

	stranger := newKey(t)
	forged := registration(k, "Mac")
	forged["sig"] = stranger.sign([]byte("attn-cloud/v1 register\n" + k.pub + "\nMac"))
	r.request(t, http.MethodPost, "/v1/homes", nil, forged).refusedWith(t, http.StatusUnauthorized, "bad_signature")

	r.request(t, http.MethodPost, "/v1/homes", nil, "not json").refusedWith(t, http.StatusBadRequest, "bad_request")
	short := registration(k, "Mac")
	short["home_sign_pub"] = b64([]byte("short"))
	r.request(t, http.MethodPost, "/v1/homes", nil, short).refusedWith(t, http.StatusBadRequest, "bad_request")
	r.request(t, http.MethodPost, "/v1/homes", nil, registration(k, "")).refusedWith(t, http.StatusBadRequest, "bad_request")
	r.request(t, http.MethodPost, "/v1/homes", nil, registration(k, strings.Repeat("x", 101))).refusedWith(t, http.StatusBadRequest, "bad_request")
	if id := r.register(t, k, strings.Repeat("x", 100)); id != want {
		t.Fatalf("a 100-character name gave %q, want %q", id, want)
	}
}

func (r relay) homeAuthentication(t *testing.T) {
	t.Run("a signature from another key closes the socket", func(t *testing.T) {
		k := newKey(t)
		id := r.register(t, k, "Mac")
		s := r.dial(t, "home", "/v1/homes/"+id+"/home")
		nonce := s.expect(challenge)
		s.send(auth, 0, ed25519.Sign(newKey(t).private, append([]byte("attn-cloud/v1 auth home\n"+id+"\n"), nonce.payload...)))
		s.expectClosed(authFailed, "")
	})
	t.Run("a signature over the device prefix closes the socket", func(t *testing.T) {
		k := newKey(t)
		id := r.register(t, k, "Mac")
		s := r.dial(t, "home", "/v1/homes/"+id+"/home")
		nonce := s.expect(challenge)
		s.send(auth, 0, ed25519.Sign(k.private, append([]byte("attn-cloud/v1 auth device\n"+id+"\n"), nonce.payload...)))
		s.expectClosed(authFailed, "")
	})
	t.Run("any other frame before ready closes the socket", func(t *testing.T) {
		k := newKey(t)
		s := r.dial(t, "home", "/v1/homes/"+r.register(t, k, "Mac")+"/home")
		s.expect(challenge)
		s.send(data, 0, []byte("early"))
		s.expectClosed(authFailed, "")
	})
}

func (h *home) dropsWithList(connID uint32, pairingOpenUntil int64, devices ...key) {
	h.t.Helper()
	pubs := []string{}
	for _, device := range devices {
		pubs = append(pubs, device.pub)
	}
	h.sendJSON(allowlist, map[string]any{"devices": pubs, "pairing_open_until": pairingOpenUntil})
	h.expectClose(connID)
	h.expect(allowlist)
}

func (r relay) deviceAdmission(t *testing.T) {
	far := time.Now().Add(time.Hour).UnixMilli()
	past := time.Now().Add(-time.Hour).UnixMilli()

	t.Run("an unknown device gets its upgrade and then 4403, before any frame", func(t *testing.T) {
		h := r.newHome(t)
		h.applyList(0)
		r.dialDevice(t, h, newKey(t)).expectClosed(notAdmitted, notAdmittedWhy)
	})
	t.Run("a pairing window that has ended admits nobody", func(t *testing.T) {
		h := r.newHome(t)
		h.applyList(past)
		r.dialDevice(t, h, newKey(t)).expectClosed(notAdmitted, notAdmittedWhy)
	})
	t.Run("an open pairing window admits an unknown device, until a list closes the window", func(t *testing.T) {
		h := r.newHome(t)
		h.applyList(far)
		k := newKey(t)
		s, connID := r.connectDevice(t, h, k)
		s.expectPresence(connID, 1)
		h.expectOpen(k)
		h.dropsWithList(connID, 0)
		s.expectClosed(notAdmitted, notAdmittedWhy)
	})
	t.Run("a device on the list stays when the window closes, and goes when the list drops it", func(t *testing.T) {
		h := r.newHome(t)
		k := newKey(t)
		h.applyList(far, k)
		s, connID := r.connectDevice(t, h, k)
		s.expectPresence(connID, 1)
		h.expectOpen(k)
		h.applyList(0, k)
		s.send(data, connID, []byte("still here"))
		if got := h.expect(data); string(got.payload) != "still here" {
			t.Fatalf("the home got %q", got.payload)
		}
		h.dropsWithList(connID, 0)
		s.expectClosed(notAdmitted, notAdmittedWhy)
	})
	t.Run("one device holds several sockets", func(t *testing.T) {
		h := r.newHome(t)
		k := newKey(t)
		h.applyList(0, k)
		first, firstID := r.connectDevice(t, h, k)
		first.expectPresence(firstID, 1)
		h.expectOpen(k)
		second, secondID := r.connectDevice(t, h, k)
		second.expectPresence(secondID, 1)
		h.expectOpen(k)
		if firstID == secondID {
			t.Fatalf("two open sockets share conn_id %d", firstID)
		}
	})
	t.Run("a malformed device key is refused", func(t *testing.T) {
		h := r.newHome(t)
		upgrade := map[string]string{"Upgrade": "websocket", "Connection": "Upgrade", "Sec-WebSocket-Key": "dGhlIHNhbXBsZSBub25jZQ==", "Sec-WebSocket-Version": "13"}
		r.request(t, http.MethodGet, "/v1/homes/"+h.id+"/device?device_sign_pub=short", upgrade, nil).refusedWith(t, http.StatusBadRequest, "bad_request")
	})
}

func (r relay) deviceAuthentication(t *testing.T) {
	t.Run("a signature from another key closes the socket", func(t *testing.T) {
		h := r.newHome(t)
		k := newKey(t)
		h.applyList(0, k)
		s := r.dialDevice(t, h, k)
		nonce := s.expect(challenge)
		s.send(auth, 0, ed25519.Sign(newKey(t).private, append([]byte("attn-cloud/v1 auth device\n"+h.id+"\n"), nonce.payload...)))
		s.expectClosed(authFailed, "")
	})
	t.Run("any other frame before ready closes the socket", func(t *testing.T) {
		h := r.newHome(t)
		k := newKey(t)
		h.applyList(0, k)
		s := r.dialDevice(t, h, k)
		s.expect(challenge)
		s.send(data, 1, []byte("early"))
		s.expectClosed(authFailed, "")
	})
}

func (r relay) routing(t *testing.T) {
	h := r.newHome(t)
	device, _, connID := r.pairedDevice(t, h)
	opaque := []byte{0, 1, 2, 0xfe, 0xff}

	device.send(data, connID, opaque)
	if got := h.expect(data); got.connID != connID || !bytes.Equal(got.payload, opaque) {
		t.Fatalf("the home got conn %d %v, want conn %d %v", got.connID, got.payload, connID, opaque)
	}
	h.send(data, connID, opaque)
	if got := device.expect(data); got.connID != connID || !bytes.Equal(got.payload, opaque) {
		t.Fatalf("the device got conn %d %v, want conn %d %v", got.connID, got.payload, connID, opaque)
	}
	large := bytes.Repeat([]byte{7}, 64<<10)
	h.send(data, connID, large)
	if got := device.expect(data); !bytes.Equal(got.payload, large) {
		t.Fatalf("a 64 KiB frame reached the device as %d bytes", len(got.payload))
	}

	h.send(data, connID+1000, opaque)
	h.expectClose(connID + 1000)

	device.send(data, connID+1000, opaque)
	device.expectClosed(badFrame, "")
	h.expectClose(connID)
}

func (r relay) closing(t *testing.T) {
	t.Run("the home closes a device with 4000", func(t *testing.T) {
		h := r.newHome(t)
		device, _, connID := r.pairedDevice(t, h)
		h.send(closeConn, connID, nil)
		device.expectClosed(closedByHome, "")
	})
	t.Run("the home hears when a device goes", func(t *testing.T) {
		h := r.newHome(t)
		device, _, connID := r.pairedDevice(t, h)
		device.conn.Close(websocket.StatusNormalClosure, "")
		h.expectClose(connID)
	})
}

func (r relay) presenceChanges(t *testing.T) {
	k := newKey(t)
	id := r.register(t, k, "Mac")
	h := r.connectHome(t, k, id)
	device, phone, connID := r.pairedDevice(t, h)

	h.conn.Close(websocket.StatusNormalClosure, "")
	device.expectPresence(connID, 0)

	device.send(data, connID, []byte("into the void"))
	device.expectPresence(connID, 0)

	h = r.connectHome(t, k, id)
	if got := h.expectOpen(phone); got != connID {
		t.Fatalf("the returning home was told conn %d, want %d", got, connID)
	}
	device.expectPresence(connID, 1)

	h.conn.Close(websocket.StatusNormalClosure, "")
	device.expectPresence(connID, 0)
	second, secondID := r.connectDevice(t, h, phone)
	second.expectPresence(secondID, 0)
}

func (r relay) replacement(t *testing.T) {
	k := newKey(t)
	id := r.register(t, k, "Mac")
	older := r.connectHome(t, k, id)
	device, phone, connID := r.pairedDevice(t, older)

	newer := r.connectHome(t, k, id)
	older.expectClosed(replacedByNew, "")
	if got := newer.expectOpen(phone); got != connID {
		t.Fatalf("the newer home was told conn %d, want %d", got, connID)
	}
	device.expectPresence(connID, 1)
	device.send(data, connID, []byte("to the newer home"))
	if got := newer.expect(data); string(got.payload) != "to the newer home" {
		t.Fatalf("the newer home got %q", got.payload)
	}
}

func (r relay) frames(t *testing.T) {
	t.Run("a frame shorter than the header closes the socket", func(t *testing.T) {
		h := r.newHome(t)
		h.sendBytes([]byte{1, data})
		h.expectClosed(badFrame, "")
	})
	t.Run("another envelope version closes the socket and names both versions", func(t *testing.T) {
		h := r.newHome(t)
		h.sendBytes([]byte{2, data, 0, 0, 0, 1})
		h.expectClosed(badFrame, "envelope version 2 unsupported; relay speaks 1")
	})
	t.Run("a frame type the relay does not take from a home is an error, and the socket stays", func(t *testing.T) {
		h := r.newHome(t)
		h.send(presence, 0, []byte{1})
		h.expectError("unsupported_type")
		h.applyList(0)
	})
	t.Run("a device sends only data frames", func(t *testing.T) {
		h := r.newHome(t)
		device, phone, connID := r.pairedDevice(t, h)
		device.sendJSON(push, map[string]string{"device": phone.pub, "ciphertext": "AQ", "collapse_id": "c"})
		device.expectError("unsupported_type")
		device.send(data, connID, []byte("still open"))
		h.expect(data)
	})
}

func (r relay) heartbeat(t *testing.T) {
	h := r.newHome(t)
	device, _, _ := r.pairedDevice(t, h)
	for name, s := range map[string]*socket{"home": h.socket, "device": device} {
		s.sendText("ping")
		kind, raw, err := s.read()
		if err != nil || kind != websocket.MessageText || string(raw) != "pong" {
			t.Fatalf("%s: ping answered with %v %q (%v), want the text frame pong", name, kind, raw, err)
		}
	}
	h.sendText("hello")
	h.expectError("text_frame")
}

func (r relay) allowlists(t *testing.T) {
	h := r.newHome(t)
	send := func(body string) { h.send(allowlist, 0, []byte(body)) }

	send("not json")
	h.expectError("bad_allowlist")
	send(`{"devices":"nobody","pairing_open_until":0}`)
	h.expectError("bad_allowlist")
	send(`{"devices":["short"],"pairing_open_until":0}`)
	h.expectError("bad_allowlist")

	pubs := make([]string, allowlistLimit+1)
	for i := range pubs {
		raw := make([]byte, 32)
		binary.BigEndian.PutUint32(raw, uint32(i))
		pubs[i] = b64(raw)
	}
	h.sendJSON(allowlist, map[string]any{"devices": pubs, "pairing_open_until": 0})
	want := fmt.Sprintf("allowlist has %d devices; the limit is %d", allowlistLimit+1, allowlistLimit)
	if message := h.expectError("bad_allowlist"); !strings.Contains(message, want) {
		t.Fatalf("the refusal says %q, want it to name %q", message, want)
	}
	h.sendJSON(allowlist, map[string]any{"devices": pubs[:allowlistLimit], "pairing_open_until": 0})
	h.expect(allowlist)

	k := newKey(t)
	h.applyList(0, k)
	again, connID := r.connectDevice(t, h, k)
	again.expectPresence(connID, 1)
	h.expectOpen(k)
	h.send(allowlist, 0, []byte("not json"))
	h.expectError("bad_allowlist")
	again.send(data, connID, []byte("a refused list changed nothing"))
	if got := h.expect(data); string(got.payload) != "a refused list changed nothing" {
		t.Fatalf("the home got %q", got.payload)
	}
}

func tokenBody(homeID string, device key, environment, token string) map[string]string {
	return map[string]string{
		"device_sign_pub": device.pub, "platform": "apns", "environment": environment, "token": token,
		"sig": device.sign([]byte("attn-cloud/v1 push-token\n" + homeID + "\n" + device.pub + "\n" + environment + "\n" + token)),
	}
}

func (r relay) pushTokens(t *testing.T) {
	h := r.newHome(t)
	path := "/v1/homes/" + h.id + "/push-token"
	phone := newKey(t)

	h.applyList(time.Now().Add(time.Hour).UnixMilli())
	r.request(t, http.MethodPost, path, nil, tokenBody(h.id, phone, "sandbox", pushToken)).refusedWith(t, http.StatusForbidden, "device_not_allowed")

	h.applyList(0, phone)
	for _, environment := range []string{"sandbox", "production"} {
		got := r.request(t, http.MethodPost, path, nil, tokenBody(h.id, phone, environment, pushToken))
		if got.status != http.StatusOK || strings.TrimSpace(string(got.body)) != "{}" {
			t.Fatalf("registering a %s token: %d %s, want 200 {}", environment, got.status, got.body)
		}
	}

	forged := tokenBody(h.id, phone, "sandbox", pushToken)
	forged["sig"] = newKey(t).sign([]byte("attn-cloud/v1 push-token\n" + h.id + "\n" + phone.pub + "\nsandbox\n" + pushToken))
	r.request(t, http.MethodPost, path, nil, forged).refusedWith(t, http.StatusUnauthorized, "bad_signature")

	r.request(t, http.MethodPost, path, nil, "not json").refusedWith(t, http.StatusBadRequest, "bad_request")
	r.request(t, http.MethodPost, path, nil, tokenBody(h.id, phone, "staging", pushToken)).refusedWith(t, http.StatusBadRequest, "bad_request")
	r.request(t, http.MethodPost, path, nil, tokenBody(h.id, phone, "sandbox", "NOT HEX")).refusedWith(t, http.StatusBadRequest, "bad_request")
	r.request(t, http.MethodPost, path, nil, tokenBody(h.id, phone, "sandbox", "aabb")).refusedWith(t, http.StatusBadRequest, "bad_request")
	wrongPlatform := tokenBody(h.id, phone, "sandbox", pushToken)
	wrongPlatform["platform"] = "fcm"
	r.request(t, http.MethodPost, path, nil, wrongPlatform).refusedWith(t, http.StatusBadRequest, "bad_request")

	unknown := strings.Repeat("a", 26)
	r.request(t, http.MethodPost, "/v1/homes/"+unknown+"/push-token", nil, tokenBody(unknown, phone, "sandbox", pushToken)).refusedWith(t, http.StatusNotFound, "unknown_home")
	r.request(t, http.MethodGet, path, nil, nil).refusedWith(t, http.StatusMethodNotAllowed, "method_not_allowed")
}

func (r relay) pushFrames(t *testing.T) {
	h := r.newHome(t)
	phone := newKey(t)
	h.applyList(0, phone)
	send := func(device key, ciphertext, collapseID string) {
		h.sendJSON(push, map[string]string{"device": device.pub, "ciphertext": ciphertext, "collapse_id": collapseID})
	}

	h.send(push, 0, []byte("not json"))
	h.expectError("bad_push")
	send(phone, "", "c")
	h.expectError("bad_push")
	send(phone, "not+base64url", "c")
	h.expectError("bad_push")
	send(phone, "AQ", "")
	h.expectError("bad_push")
	send(phone, "AQ", strings.Repeat("c", 65))
	h.expectError("bad_push")

	send(newKey(t), "AQ", "c")
	h.expectError("push_refused")
	send(phone, "AQ", "c")
	h.expectError("push_no_token")

	got := r.request(t, http.MethodPost, "/v1/homes/"+h.id+"/push-token", nil, tokenBody(h.id, phone, "sandbox", pushToken))
	if got.status != http.StatusOK {
		t.Fatalf("registering a token: %d %s", got.status, got.body)
	}
	h.applyList(0)
	h.applyList(0, phone)
	send(phone, "AQ", "c")
	h.expectError("push_no_token")
}

func (r relay) newHomeLimit(t *testing.T) {
	known := newKey(t)
	id := r.register(t, known, "Mac")
	for range newHomesLimit {
		got := r.request(t, http.MethodPost, "/v1/homes", nil, registration(newKey(t), "Mac"))
		if got.status == http.StatusOK {
			continue
		}
		got.refusedWith(t, http.StatusTooManyRequests, "registration_limited")
		if want := fmt.Sprintf("the limit is %d", newHomesLimit); !strings.Contains(string(got.body), want) {
			t.Fatalf("the refusal says %s, want it to name %q", got.body, want)
		}
		if again := r.register(t, known, "Mac"); again != id {
			t.Fatalf("a key that has a relay registered as %q, want %q", again, id)
		}
		return
	}
	t.Fatalf("%d more new homes registered with no refusal, past the contract's limit of %d a day", newHomesLimit, newHomesLimit)
}
