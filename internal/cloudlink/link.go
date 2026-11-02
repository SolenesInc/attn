package cloudlink

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"sync"
	"time"

	"nhooyr.io/websocket"
)

const (
	PairingLifetime = 10 * time.Minute
	// pingEvery stays under the contract's 20 s; a ping still unanswered at the next tick means the path is gone.
	// The deployed relay echoed at p99 44.7 ms (s-9ywtg5).
	pingEvery = 15 * time.Second
	// connectWithin bounds dial, challenge and ready; the relay round trip measured 42 ms at p50.
	connectWithin = 15 * time.Second
	// A reconnect through the same relay took about 100 ms (s-9ywtg5), so the first retry waits that long.
	firstRetry = 100 * time.Millisecond
	lastRetry  = 10 * time.Second
)

const closeReplaced websocket.StatusCode = 4409

type handshakeKind byte

const (
	handshakePaired handshakeKind = 1
	handshakePair   handshakeKind = 2
)

type Pairing struct {
	DeviceID       string
	ProfileID      string
	DeviceSignPub  []byte
	DeviceNoisePub []byte
	Name           string
}

type Device struct {
	ID        string
	ProfileID string
}

type Stream interface {
	Recv() ([]byte, error)
	Send([]byte) error
	Close() error
}

type Home interface {
	Pairings() ([]Pairing, error)
	AddPairing(Pairing) error
	Serve(Device, Stream)
	Changed()
	Logf(format string, args ...any)
}

type Config struct {
	CloudURL string
	HomeName string
	Identity Identity
	Home     Home
	HTTP     *http.Client
}

type State string

const (
	StateConnecting State = "connecting"
	StateConnected  State = "connected"
)

type Status struct {
	State  State
	Error  string
	HomeID string
}

type Offer struct {
	ProfileID string
	PairingID string
	ExpiresAt time.Time
	QR        string
	secret    []byte
}

type QR struct {
	V            int    `json:"v"`
	Cloud        string `json:"cloud"`
	HomeID       string `json:"home_id"`
	HomeName     string `json:"home_name"`
	HomeNoisePub string `json:"home_noise_pub"`
	ProfileID    string `json:"profile_id"`
	ProfileName  string `json:"profile_name"`
	PairingID    string `json:"pairing_id"`
	Secret       string `json:"secret"`
}

type Link struct {
	cfg Config
	now func() time.Time

	mu      sync.Mutex
	status  Status
	offers  map[string]*Offer
	session *session
}

func New(cfg Config) *Link {
	if cfg.HTTP == nil {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.DisableKeepAlives = true
		cfg.HTTP = &http.Client{Transport: transport}
	}
	return &Link{cfg: cfg, now: time.Now, offers: map[string]*Offer{}, status: Status{State: StateConnecting, HomeID: cfg.Identity.HomeID()}}
}

func (l *Link) Status() Status {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.status
}

func (l *Link) setStatus(state State, err error) {
	l.mu.Lock()
	l.status.State = state
	l.status.Error = ""
	if err != nil {
		l.status.Error = err.Error()
	}
	l.status.HomeID = l.cfg.Identity.HomeID()
	l.mu.Unlock()
	l.cfg.Home.Changed()
}

func (l *Link) Offers() []Offer {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.dropExpiredLocked()
	out := make([]Offer, 0, len(l.offers))
	for _, offer := range l.offers {
		out = append(out, *offer)
	}
	slices.SortFunc(out, func(a, b Offer) int { return a.ExpiresAt.Compare(b.ExpiresAt) })
	return out
}

func (l *Link) dropExpiredLocked() {
	now := l.now()
	for profile, offer := range l.offers {
		if !now.Before(offer.ExpiresAt) {
			delete(l.offers, profile)
		}
	}
}

func (l *Link) Offer(profileID, profileName string) (Offer, error) {
	homeID := l.cfg.Identity.HomeID()
	if homeID == "" {
		status := l.Status()
		return Offer{}, fmt.Errorf("this home has not registered with Attn Cloud yet (%s); a QR code needs its home id", orWaiting(status.Error))
	}
	secret := randomBytes(32)
	offer := &Offer{ProfileID: profileID, PairingID: b64(randomBytes(9)), ExpiresAt: l.now().Add(PairingLifetime), secret: secret}
	qr, _ := json.Marshal(QR{
		V: 1, Cloud: l.cfg.CloudURL, HomeID: homeID, HomeName: l.cfg.HomeName,
		HomeNoisePub: b64(l.cfg.Identity.NoiseKey.Public), ProfileID: profileID, ProfileName: profileName,
		PairingID: offer.PairingID, Secret: b64(secret),
	})
	offer.QR = string(qr)
	l.mu.Lock()
	l.offers[profileID] = offer
	s := l.session
	l.mu.Unlock()
	l.cfg.Home.Changed()
	if s != nil {
		if err := s.allowlistApplied(); err != nil {
			l.mu.Lock()
			if l.offers[profileID] == offer {
				delete(l.offers, profileID)
			}
			l.mu.Unlock()
			l.cfg.Home.Changed()
			return Offer{}, fmt.Errorf("the relay did not open the pairing window: %w", err)
		}
	}
	return *offer, nil
}

func orWaiting(err string) string {
	if err == "" {
		return "still connecting"
	}
	return err
}

func randomBytes(n int) []byte {
	out := make([]byte, n)
	_, _ = rand.Read(out)
	return out
}

func (l *Link) secretFor(claim firstMessage) ([]byte, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.dropExpiredLocked()
	for _, offer := range l.offers {
		if offer.PairingID == claim.PairingID {
			return offer.secret, nil
		}
	}
	return nil, fmt.Errorf("pairing code %s was already used or expired after %s; show a new one", claim.PairingID, PairingLifetime)
}

func (l *Link) spend(pairingID string) (string, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.dropExpiredLocked()
	for profile, offer := range l.offers {
		if offer.PairingID == pairingID {
			delete(l.offers, profile)
			return profile, true
		}
	}
	return "", false
}

func (l *Link) Connected() map[Device]bool {
	l.mu.Lock()
	s := l.session
	l.mu.Unlock()
	connected := map[Device]bool{}
	if s == nil {
		return connected
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, dev := range s.devices {
		if dev.stream != nil {
			connected[dev.device] = true
		}
	}
	return connected
}

func (l *Link) Revoked(device Device) {
	l.closeWhere(func(d Device) bool { return d == device })
}

func (l *Link) ProfileRemoved(profileID string) {
	l.closeWhere(func(d Device) bool { return d.ProfileID == profileID })
}

func (l *Link) closeWhere(match func(Device) bool) {
	l.sendAllowlist()
	l.mu.Lock()
	s := l.session
	l.mu.Unlock()
	if s != nil {
		s.closeDevices(match)
	}
}

func (l *Link) sendAllowlist() {
	l.mu.Lock()
	s := l.session
	l.mu.Unlock()
	if s == nil {
		return
	}
	if err := s.sendAllowlist(); err != nil {
		l.cfg.Home.Logf("cloudlink: sending the allowlist: %v", err)
	}
}

func (l *Link) allowlist() (allowlist, error) {
	pairings, err := l.cfg.Home.Pairings()
	if err != nil {
		return allowlist{}, err
	}
	list := allowlist{Devices: []string{}}
	for _, pairing := range pairings {
		key := b64(pairing.DeviceSignPub)
		if !slices.Contains(list.Devices, key) {
			list.Devices = append(list.Devices, key)
		}
	}
	for _, offer := range l.Offers() {
		list.PairingOpenUntil = max(list.PairingOpenUntil, offer.ExpiresAt.UnixMilli())
	}
	return list, nil
}

func (l *Link) Run(ctx context.Context) {
	wait := firstRetry
	for {
		connected, err := l.connect(ctx)
		if ctx.Err() != nil {
			return
		}
		if connected >= lastRetry {
			wait = firstRetry
		}
		l.cfg.Home.Logf("cloudlink: lost the relay (%v); reconnecting in %s", err, wait)
		l.setStatus(StateConnecting, err)
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return
		}
		wait = min(wait*2, lastRetry)
	}
}

func (l *Link) homeID(ctx context.Context, forceRegister bool) (string, error) {
	if id := l.cfg.Identity.HomeID(); id != "" && !forceRegister {
		return id, nil
	}
	id, err := register(ctx, l.cfg.HTTP, l.cfg.CloudURL, l.cfg.Identity, l.cfg.HomeName)
	if err != nil {
		return "", err
	}
	if err := l.cfg.Identity.saveHomeID(id); err != nil {
		return "", err
	}
	return id, nil
}

func (l *Link) dial(ctx context.Context) (*websocket.Conn, error) {
	ctx, cancel := context.WithTimeout(ctx, connectWithin)
	defer cancel()
	homeID, err := l.homeID(ctx, false)
	if err != nil {
		return nil, err
	}
	conn, err := dialHome(ctx, l.cfg.HTTP, l.cfg.CloudURL, l.cfg.Identity, homeID)
	if errors.Is(err, errUnknownHome) {
		if homeID, err = l.homeID(ctx, true); err != nil {
			return nil, err
		}
		conn, err = dialHome(ctx, l.cfg.HTTP, l.cfg.CloudURL, l.cfg.Identity, homeID)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return nil, fmt.Errorf("the relay at %s did not answer within %s", l.cfg.CloudURL, connectWithin)
	}
	return conn, err
}

func (l *Link) connect(ctx context.Context) (time.Duration, error) {
	conn, err := l.dial(ctx)
	if err != nil {
		return 0, err
	}
	began := time.Now()
	ctx, cancel := context.WithCancelCause(ctx)
	s := &session{link: l, conn: conn, ctx: ctx, lost: cancel, devices: map[uint32]*deviceConn{}, allowChanged: make(chan struct{})}
	defer func() {
		cancel(nil)
		conn.CloseNow()
		l.mu.Lock()
		if l.session == s {
			l.session = nil
		}
		l.mu.Unlock()
		s.closeAll()
	}()
	l.mu.Lock()
	l.session = s
	l.mu.Unlock()
	if err := s.sendAllowlist(); err != nil {
		return time.Since(began), err
	}
	l.setStatus(StateConnected, nil)
	go s.heartbeat()
	err = s.read()
	return time.Since(began), err
}

type session struct {
	link *Link
	conn *websocket.Conn
	ctx  context.Context
	lost context.CancelCauseFunc

	pongMu     sync.Mutex
	pingUnseen bool

	mu      sync.Mutex
	devices map[uint32]*deviceConn
	serving sync.WaitGroup

	allow         sync.Mutex
	allowSent     uint64
	allowAnswered uint64
	allowRefusal  error
	allowChanged  chan struct{}
}

func (s *session) write(kind frameType, connID uint32, payload []byte) error {
	return s.conn.Write(s.ctx, websocket.MessageBinary, encodeFrame(kind, connID, payload))
}

func (s *session) sendAllowlist() error {
	_, err := s.sendAllowlistNumbered()
	return err
}

func (s *session) sendAllowlistNumbered() (uint64, error) {
	s.allow.Lock()
	defer s.allow.Unlock()
	list, err := s.link.allowlist()
	if err != nil {
		return 0, err
	}
	payload, _ := json.Marshal(list)
	if err := s.write(frameAllowlist, 0, payload); err != nil {
		return 0, err
	}
	s.allowSent++
	return s.allowSent, nil
}

func (s *session) allowlistApplied() error {
	seq, err := s.sendAllowlistNumbered()
	if err != nil {
		return err
	}
	timeout := time.NewTimer(connectWithin)
	defer timeout.Stop()
	for {
		s.allow.Lock()
		answered, refusal, changed := s.allowAnswered, s.allowRefusal, s.allowChanged
		s.allow.Unlock()
		if answered >= seq {
			return refusal
		}
		select {
		case <-changed:
		case <-s.ctx.Done():
			return fmt.Errorf("lost the relay before it answered: %w", context.Cause(s.ctx))
		case <-timeout.C:
			return fmt.Errorf("no answer within %s", connectWithin)
		}
	}
}

func (s *session) allowlistAnswered(refusal error) {
	s.allow.Lock()
	s.allowAnswered++
	s.allowRefusal = refusal
	close(s.allowChanged)
	s.allowChanged = make(chan struct{})
	s.allow.Unlock()
}

func (s *session) heartbeat() {
	ticker := time.NewTicker(pingEvery)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
		}
		s.pongMu.Lock()
		unanswered := s.pingUnseen
		s.pingUnseen = true
		s.pongMu.Unlock()
		if unanswered {
			s.lost(fmt.Errorf("the relay did not answer a ping within %s", pingEvery))
			return
		}
		if err := s.conn.Write(s.ctx, websocket.MessageText, []byte("ping")); err != nil {
			s.lost(err)
			return
		}
	}
}

func (s *session) read() error {
	for {
		kind, data, err := s.conn.Read(s.ctx)
		if err != nil {
			if cause := context.Cause(s.ctx); cause != nil && !errors.Is(cause, context.Canceled) {
				return cause
			}
			if websocket.CloseStatus(err) == closeReplaced {
				return errors.New("another attn daemon with this home's identity connected to the relay; two daemons share one data directory's cloud keys")
			}
			return err
		}
		if kind == websocket.MessageText {
			if string(data) == "pong" {
				s.pongMu.Lock()
				s.pingUnseen = false
				s.pongMu.Unlock()
			}
			continue
		}
		f, err := decodeFrame(data)
		if err != nil {
			return err
		}
		s.handle(f)
	}
}

func (s *session) handle(f frame) {
	logf := s.link.cfg.Home.Logf
	switch f.kind {
	case frameOpen:
		s.mu.Lock()
		s.devices[f.connID] = &deviceConn{session: s, connID: f.connID, signPub: bytes.Clone(f.payload)}
		s.mu.Unlock()
	case frameData:
		s.mu.Lock()
		dev := s.devices[f.connID]
		s.mu.Unlock()
		if dev == nil {
			return
		}
		if err := dev.receive(f.payload); err != nil {
			logf("cloudlink: device connection %d: %v", f.connID, err)
			dev.close()
		}
	case frameClose:
		s.mu.Lock()
		dev := s.devices[f.connID]
		s.mu.Unlock()
		if dev != nil {
			dev.end()
		}
	case frameAllowlist:
		s.allowlistAnswered(nil)
	case frameError:
		logf("cloudlink: the relay reported %s", f.payload)
		var reported struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		}
		if json.Unmarshal(f.payload, &reported) == nil && reported.Code == "bad_allowlist" {
			s.allowlistAnswered(errors.New(reported.Message))
		}
	case framePresence:
	default:
		logf("cloudlink: the relay sent frame type %d, which this home does not know", f.kind)
	}
}

func (s *session) closeDevices(match func(Device) bool) {
	s.mu.Lock()
	var matches []*deviceConn
	for _, dev := range s.devices {
		if dev.stream != nil && match(dev.device) {
			matches = append(matches, dev)
		}
	}
	s.mu.Unlock()
	for _, dev := range matches {
		dev.close()
	}
}

func (s *session) closeAll() {
	s.mu.Lock()
	devices := make([]*deviceConn, 0, len(s.devices))
	for _, dev := range s.devices {
		devices = append(devices, dev)
	}
	s.mu.Unlock()
	for _, dev := range devices {
		dev.end()
	}
	s.serving.Wait()
}

type firstMessage struct {
	ProfileID  string `json:"profile_id,omitempty"`
	PairingID  string `json:"pairing_id,omitempty"`
	DeviceName string `json:"device_name,omitempty"`
}

type deviceConn struct {
	session *session
	connID  uint32
	signPub []byte

	answered *answered
	device   Device
	pairing  *Pairing
	stream   *stream
}

func (d *deviceConn) receive(payload []byte) error {
	switch {
	case d.answered == nil:
		return d.answer(payload)
	case d.stream == nil:
		return d.acceptFirstMessage(payload)
	default:
		plaintext, err := d.answered.transport.open(payload)
		if err != nil {
			return fmt.Errorf("decrypting: %w", err)
		}
		d.stream.deliver(plaintext)
		return nil
	}
}

func (d *deviceConn) answer(payload []byte) error {
	if len(payload) < 1 {
		return errors.New("an empty first message")
	}
	link := d.session.link
	static := link.cfg.Identity.NoiseKey
	msg1 := payload[1:]
	var result answered
	var err error
	switch handshakeKind(payload[0]) {
	case handshakePaired:
		result, err = answerPaired(static, d.signPub, msg1, nil, func(claim firstMessage) ([]byte, error) {
			pairing, err := link.findPairing(KeyID(d.signPub), claim.ProfileID)
			if err != nil {
				return nil, err
			}
			d.pairing = &pairing
			return pairing.DeviceNoisePub, nil
		})
	case handshakePair:
		result, err = answerPairing(static, d.signPub, msg1, nil, link.secretFor)
	default:
		return fmt.Errorf("handshake kind %d is not one this home knows (1 paired, 2 pair)", payload[0])
	}
	if err != nil {
		return err
	}
	d.answered = &result
	return d.session.write(frameData, d.connID, result.msg2)
}

func (l *Link) findPairing(deviceID, profileID string) (Pairing, error) {
	pairings, err := l.cfg.Home.Pairings()
	if err != nil {
		return Pairing{}, err
	}
	for _, pairing := range pairings {
		if pairing.DeviceID == deviceID && pairing.ProfileID == profileID {
			return pairing, nil
		}
	}
	return Pairing{}, fmt.Errorf("device %s has no pairing with profile %q on this home; it was revoked or never paired", deviceID, profileID)
}

// The first transport message is the first proof that a pairing device holds the secret.
func (d *deviceConn) acceptFirstMessage(ciphertext []byte) error {
	plaintext, err := d.answered.transport.open(ciphertext)
	if err != nil {
		return fmt.Errorf("decrypting the first message after the handshake: %w", err)
	}
	link := d.session.link
	if d.answered.pairing {
		claim := d.answered.claim
		profileID, live := link.spend(claim.PairingID)
		if !live {
			return fmt.Errorf("pairing code %s was used by another device first or expired; show a new one", claim.PairingID)
		}
		pairing := Pairing{DeviceID: KeyID(d.signPub), ProfileID: profileID, DeviceSignPub: d.signPub, DeviceNoisePub: d.answered.peerStatic, Name: claim.DeviceName}
		if err := link.cfg.Home.AddPairing(pairing); err != nil {
			return err
		}
		d.pairing = &pairing
		link.sendAllowlist()
		link.cfg.Home.Changed()
	}
	st := &stream{dev: d, in: make(chan []byte, 1), done: make(chan struct{})}
	st.in <- plaintext
	d.session.mu.Lock()
	d.device = Device{ID: d.pairing.DeviceID, ProfileID: d.pairing.ProfileID}
	d.stream = st
	d.session.mu.Unlock()
	d.session.serving.Add(1)
	go func() {
		defer d.session.serving.Done()
		link.cfg.Home.Serve(d.device, d.stream)
	}()
	return nil
}

func (d *deviceConn) close() {
	if d.forget() {
		_ = d.session.write(frameClose, d.connID, nil)
	}
	d.endStream()
}

func (d *deviceConn) end() {
	d.forget()
	d.endStream()
}

func (d *deviceConn) forget() bool {
	d.session.mu.Lock()
	defer d.session.mu.Unlock()
	if d.session.devices[d.connID] != d {
		return false
	}
	delete(d.session.devices, d.connID)
	return true
}

func (d *deviceConn) endStream() {
	if d.stream != nil {
		d.stream.end()
	}
}

type stream struct {
	dev    *deviceConn
	sendMu sync.Mutex
	in     chan []byte
	done   chan struct{}
	once   sync.Once
}

func (st *stream) deliver(message []byte) {
	select {
	case st.in <- message:
	case <-st.done:
	}
}

func (st *stream) Recv() ([]byte, error) {
	select {
	case message := <-st.in:
		return message, nil
	case <-st.done:
		return nil, io.EOF
	}
}

func (st *stream) Send(message []byte) error {
	st.sendMu.Lock()
	defer st.sendMu.Unlock()
	select {
	case <-st.done:
		return io.ErrClosedPipe
	default:
	}
	sealed, err := st.dev.answered.transport.seal(message)
	if err != nil {
		return err
	}
	return st.dev.session.write(frameData, st.dev.connID, sealed)
}

func (st *stream) Close() error {
	st.dev.close()
	return nil
}

func (st *stream) end() {
	st.once.Do(func() { close(st.done) })
}
