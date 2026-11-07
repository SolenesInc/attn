package testworld

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/hkdf"
	"crypto/hpke"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/flynn/noise"
	"nhooyr.io/websocket"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol/homegateway"
)

type Phone struct {
	t     *testing.T
	cloud *Cloud
	name  string
	sign  ed25519.PrivateKey
	noise noise.DHKey
}

type PhoneQR struct {
	Cloud        string `json:"cloud"`
	HomeID       string `json:"home_id"`
	HomeNoisePub string `json:"home_noise_pub"`
	ProfileID    string `json:"profile_id"`
	PairingID    string `json:"pairing_id"`
	Secret       string `json:"secret"`
}

func ParsePhoneQR(t *testing.T, qr string) PhoneQR {
	t.Helper()
	var parsed PhoneQR
	if err := json.Unmarshal([]byte(qr), &parsed); err != nil {
		t.Fatalf("QR payload %q: %v", qr, err)
	}
	return parsed
}

func NewPhone(t *testing.T, cloud *Cloud, name string) *Phone {
	_, sign, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	key, err := noise.DH25519.GenerateKeypair(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return &Phone{t: t, cloud: cloud, name: name, sign: sign, noise: key}
}

func (p *Phone) DeviceID() string { return keyID(p.sign.Public().(ed25519.PublicKey)) }

// ErrNotAdmitted is the Cloud Relay closing a device socket with 4403. The error's text carries the Cloud Relay's reason.
var ErrNotAdmitted = errors.New("the Cloud Relay closed the device socket with 4403")

type PhoneSocket struct {
	t      *testing.T
	phone  *Phone
	homeID string
	conn   *websocket.Conn
	connID uint32
	send   *noise.CipherState
	recv   *noise.CipherState
}

func (p *Phone) Dial(qr PhoneQR) (*PhoneSocket, error) {
	return p.dial(qr, nil)
}

func (p *Phone) DialToPair(qr PhoneQR) (*PhoneSocket, error) {
	token, err := hkdf.Key(sha256.New, mustUnb64(qr.Secret), nil, "attn-cloud/v1 relay token", sha256.Size)
	if err != nil {
		p.t.Fatal(err)
	}
	return p.dial(qr, token)
}

func (p *Phone) dial(qr PhoneQR, relayToken []byte) (*PhoneSocket, error) {
	ctx, cancel := context.WithTimeout(context.Background(), fakeagent.HangGuard)
	defer cancel()
	pub := b64(p.sign.Public().(ed25519.PublicKey))
	conn, _, err := websocket.Dial(ctx, websocketURL(p.cloud.URL)+"/v1/homes/"+qr.HomeID+"/device?device_sign_pub="+pub, nil)
	if err != nil {
		return nil, err
	}
	s := &PhoneSocket{t: p.t, phone: p, homeID: qr.HomeID, conn: conn}
	p.t.Cleanup(func() { conn.CloseNow() })
	challenge, err := s.read()
	if err = notAdmitted(err); err != nil {
		return nil, err
	}
	if challenge.kind != frameChallenge {
		p.t.Fatalf("device socket: frame %d before the challenge", challenge.kind)
	}
	message := append([]byte("attn-cloud/v1 auth device\n"+qr.HomeID+"\n"), challenge.payload...)
	s.write(encodeEnvelope(frameAuth, 0, append(ed25519.Sign(p.sign, message), relayToken...)))
	for {
		f, err := s.read()
		if err = notAdmitted(err); err != nil {
			return nil, err
		}
		if f.kind == frameReady {
			s.connID = f.connID
			return s, nil
		}
		if f.kind != framePresence {
			p.t.Fatalf("device socket: frame %d while waiting for ready: %s", f.kind, f.payload)
		}
	}
}

func notAdmitted(err error) error {
	var closed websocket.CloseError
	if errors.As(err, &closed) && closed.Code == 4403 {
		return fmt.Errorf("%w: %s", ErrNotAdmitted, closed.Reason)
	}
	return err
}

func websocketURL(httpURL string) string { return "ws" + httpURL[len("http"):] }

func (s *PhoneSocket) write(data []byte) {
	ctx, cancel := context.WithTimeout(context.Background(), fakeagent.HangGuard)
	defer cancel()
	if err := s.conn.Write(ctx, websocket.MessageBinary, data); err != nil {
		s.t.Fatalf("device socket write: %v", err)
	}
}

func (s *PhoneSocket) read() (envelope, error) {
	ctx, cancel := context.WithTimeout(context.Background(), fakeagent.HangGuard)
	defer cancel()
	for {
		kind, data, err := s.conn.Read(ctx)
		if err != nil {
			return envelope{}, err
		}
		if kind == websocket.MessageBinary {
			return decodeEnvelope(data)
		}
	}
}

func (s *PhoneSocket) AwaitPresence(online bool) {
	s.t.Helper()
	want := byte(0)
	if online {
		want = 1
	}
	for {
		f, err := s.read()
		if err != nil {
			s.t.Fatalf("device socket: waiting for presence %d: %v", want, err)
		}
		if f.kind == framePresence && len(f.payload) == 1 && f.payload[0] == want {
			return
		}
	}
}

func (s *PhoneSocket) AwaitClosed() websocket.StatusCode {
	s.t.Helper()
	for {
		_, err := s.read()
		if err == nil {
			continue
		}
		if code := websocket.CloseStatus(err); code != -1 {
			return code
		}
		s.t.Fatalf("device socket ended without a close frame: %v", err)
	}
}

func (s *PhoneSocket) Pair(qr PhoneQR) error {
	return s.handshake(2, qr, map[string]string{"pairing_id": qr.PairingID, "device_name": s.phone.name}, mustUnb64(qr.Secret))
}

func (s *PhoneSocket) Resume(qr PhoneQR) error {
	return s.handshake(1, qr, map[string]string{"profile_id": qr.ProfileID}, nil)
}

func (s *PhoneSocket) handshake(kind byte, qr PhoneQR, first map[string]string, psk []byte) error {
	s.t.Helper()
	cfg := noise.Config{
		CipherSuite: noise.NewCipherSuite(noise.DH25519, noise.CipherChaChaPoly, noise.HashSHA256),
		Pattern:     noise.HandshakeIK, Initiator: true, StaticKeypair: s.phone.noise, PeerStatic: mustUnb64(qr.HomeNoisePub),
		Prologue: append([]byte("attn-cloud/v1 noise\n"), s.phone.sign.Public().(ed25519.PublicKey)...),
	}
	if psk != nil {
		cfg.PresharedKey, cfg.PresharedKeyPlacement = psk, 2
	}
	hs, err := noise.NewHandshakeState(cfg)
	if err != nil {
		s.t.Fatal(err)
	}
	payload, _ := json.Marshal(first)
	msg1, _, _, err := hs.WriteMessage(nil, payload)
	if err != nil {
		s.t.Fatal(err)
	}
	s.write(encodeEnvelope(frameData, s.connID, append([]byte{kind}, msg1...)))
	for {
		reply, err := s.read()
		if err != nil {
			return err
		}
		if reply.kind == framePresence {
			continue
		}
		if reply.kind != frameData {
			s.t.Fatalf("device socket: frame %d while waiting for the handshake reply", reply.kind)
		}
		if _, s.send, s.recv, err = hs.ReadMessage(nil, reply.payload); err != nil {
			s.t.Fatalf("reading the home's handshake reply: %v", err)
		}
		return nil
	}
}

func (s *PhoneSocket) Send(message any) {
	s.t.Helper()
	plaintext, _ := json.Marshal(message)
	sealed, err := s.send.Encrypt(nil, nil, plaintext)
	if err != nil {
		s.t.Fatal(err)
	}
	s.write(encodeEnvelope(frameData, s.connID, sealed))
}

func (s *PhoneSocket) Next() (json.RawMessage, error) {
	for {
		f, err := s.read()
		if err != nil {
			return nil, err
		}
		switch f.kind {
		case framePresence:
			continue
		case frameData:
			plaintext, err := s.recv.Decrypt(nil, nil, f.payload)
			if err != nil {
				return nil, fmt.Errorf("decrypting the home's message: %w", err)
			}
			return plaintext, nil
		default:
			return nil, fmt.Errorf("device socket: unexpected frame %d", f.kind)
		}
	}
}

func (s *PhoneSocket) Hello() homegateway.HomeGatewayWelcome {
	s.t.Helper()
	s.Send(homegateway.HomeGatewayHello{Cmd: "hello", API: homegateway.APIVersion, DeviceID: s.phone.DeviceID()})
	raw, err := s.Next()
	if err != nil {
		s.t.Fatalf("waiting for welcome: %v", err)
	}
	var welcome homegateway.HomeGatewayWelcome
	if err := json.Unmarshal(raw, &welcome); err != nil || welcome.Event != "welcome" {
		s.t.Fatalf("home answered hello with %s", raw)
	}
	return welcome
}

// PushNote is what a phone's Notification Service Extension reads after it opens a push.
type PushNote struct {
	V           int    `json:"v"`
	HomeID      string `json:"home_id"`
	HomeName    string `json:"home_name"`
	ProfileID   string `json:"profile_id"`
	ProfileName string `json:"profile_name"`
	SessionID   string `json:"session_id"`
	Title       string `json:"title"`
	Kind        string `json:"kind"`
	Text        string `json:"text"`
	AskID       string `json:"ask_id"`
}

func (p *Phone) SignPub() string { return b64(p.sign.Public().(ed25519.PublicKey)) }

// RegisterForPush registers an APNs token as the phone does once the home has allowed it. Pushes are visible only
// through the stand-in Cloud Relay, so a test that reads them skips under ATTN_TEST_CLOUD_URL.
func (s *PhoneSocket) RegisterForPush() {
	s.t.Helper()
	cloud := s.phone.cloud
	if cloud.cloudRelay == nil {
		s.t.Skip("pushes are visible only through the fake Cloud Relay; this run uses ATTN_TEST_CLOUD_URL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), fakeagent.HangGuard)
	defer cancel()
	pub := s.phone.SignPub()
	if err := cloud.cloudRelay.awaitAllowed(ctx, s.homeID, pub); err != nil {
		s.t.Fatal(err)
	}
	const environment, token = "sandbox", "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"
	sig := ed25519.Sign(s.phone.sign, []byte("attn-cloud/v1 push-token\n"+s.homeID+"\n"+pub+"\n"+environment+"\n"+token))
	body, _ := json.Marshal(map[string]string{"device_sign_pub": pub, "platform": "apns", "environment": environment, "token": token, "sig": b64(sig)})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cloud.URL+"/v1/homes/"+s.homeID+"/push-token", bytes.NewReader(body))
	if err != nil {
		s.t.Fatal(err)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		s.t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		s.t.Fatalf("registering a push token: the Cloud Relay answered %d", res.StatusCode)
	}
}

// OpenPush decrypts a push addressed to this phone the way the phone app's extension does.
func (p *Phone) OpenPush(push Push) (PushNote, error) {
	sealed, err := base64.RawURLEncoding.DecodeString(push.Ciphertext)
	if err != nil {
		return PushNote{}, err
	}
	private, err := ecdh.X25519().NewPrivateKey(p.noise.Private)
	if err != nil {
		return PushNote{}, err
	}
	key, err := hpke.NewDHKEMPrivateKey(private)
	if err != nil {
		return PushNote{}, err
	}
	plaintext, err := hpke.Open(key, hpke.HKDFSHA256(), hpke.ChaCha20Poly1305(), []byte("attn push v1"), sealed)
	if err != nil {
		return PushNote{}, fmt.Errorf("opening the push: %w", err)
	}
	var note PushNote
	return note, json.Unmarshal(plaintext, &note)
}
