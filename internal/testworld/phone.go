package testworld

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/flynn/noise"
	"nhooyr.io/websocket"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol/gateway"
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

type RefusedStatus int

func (r RefusedStatus) Error() string {
	return fmt.Sprintf("the relay refused the device socket with %d", int(r))
}

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
	ctx, cancel := context.WithTimeout(context.Background(), fakeagent.HangGuard)
	defer cancel()
	pub := b64(p.sign.Public().(ed25519.PublicKey))
	conn, res, err := websocket.Dial(ctx, websocketURL(p.cloud.URL)+"/v1/homes/"+qr.HomeID+"/device?device_sign_pub="+pub, nil)
	if err != nil {
		if res != nil && res.StatusCode != http.StatusSwitchingProtocols {
			return nil, RefusedStatus(res.StatusCode)
		}
		return nil, err
	}
	s := &PhoneSocket{t: p.t, phone: p, homeID: qr.HomeID, conn: conn}
	p.t.Cleanup(func() { conn.CloseNow() })
	challenge := s.next(frameChallenge)
	message := append([]byte("attn-cloud/v1 auth device\n"+qr.HomeID+"\n"), challenge.payload...)
	s.write(encodeEnvelope(frameAuth, 0, ed25519.Sign(p.sign, message)))
	s.connID = s.next(frameReady).connID
	return s, nil
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

func (s *PhoneSocket) next(kind byte) envelope {
	s.t.Helper()
	for {
		f, err := s.read()
		if err != nil {
			s.t.Fatalf("device socket: waiting for frame %d: %v", kind, err)
		}
		if f.kind == kind {
			return f
		}
		if f.kind != framePresence {
			s.t.Fatalf("device socket: frame %d while waiting for %d: %s", f.kind, kind, f.payload)
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

func (s *PhoneSocket) Hello() gateway.GatewayWelcome {
	s.t.Helper()
	s.Send(gateway.GatewayHello{Cmd: "hello", API: gateway.APIVersion, DeviceID: s.phone.DeviceID()})
	raw, err := s.Next()
	if err != nil {
		s.t.Fatalf("waiting for welcome: %v", err)
	}
	var welcome gateway.GatewayWelcome
	if err := json.Unmarshal(raw, &welcome); err != nil || welcome.Event != "welcome" {
		s.t.Fatalf("home answered hello with %s", raw)
	}
	return welcome
}
