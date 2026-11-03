package cloudlink

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"flag"
	"os"
	"testing"

	"github.com/flynn/noise"
)

var update = flag.Bool("update", false, "rewrite testdata/noise_vectors.json")

const vectorsFile = "testdata/noise_vectors.json"

// noiseVector is one handshake and its first transport messages, as the phone's initiator must produce and read them.
type noiseVector struct {
	Name                  string `json:"name"`
	Kind                  int    `json:"kind"`
	HomeStaticPrivate     string `json:"home_static_private"`
	HomeStaticPublic      string `json:"home_static_public"`
	HomeEphemeralPrivate  string `json:"home_ephemeral_private"`
	DeviceStaticPrivate   string `json:"device_static_private"`
	DeviceEphemeralPriv   string `json:"device_ephemeral_private"`
	DeviceSignPub         string `json:"device_sign_pub"`
	Prologue              string `json:"prologue"`
	PSK                   string `json:"psk,omitempty"`
	FirstPayload          string `json:"first_payload"`
	DeviceFirstFrame      string `json:"device_first_frame"`
	HomeReply             string `json:"home_reply"`
	DeviceToHomePlaintext string `json:"device_to_home_plaintext"`
	DeviceToHome          string `json:"device_to_home"`
	HomeToDevicePlaintext string `json:"home_to_device_plaintext"`
	HomeToDevice          string `json:"home_to_device"`
}

func fill(b byte) []byte { return bytes.Repeat([]byte{b}, 32) }

func mustKey(t *testing.T, private []byte) noise.DHKey {
	t.Helper()
	key, err := noiseKeyFromPrivate(private)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func unhex(t *testing.T, s string) []byte {
	t.Helper()
	out, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func generateVector(t *testing.T, name string, kind handshakeKind, first firstMessage) noiseVector {
	t.Helper()
	home, device := mustKey(t, fill(0x11)), mustKey(t, fill(0x22))
	homeEphemeral, deviceEphemeral := fill(0x33), fill(0x44)
	signPub, psk := fill(0x55), fill(0x66)
	cfg := noise.Config{
		CipherSuite: cipherSuite, Random: bytes.NewReader(deviceEphemeral), Pattern: noise.HandshakeIK, Initiator: true,
		Prologue: prologue(signPub), StaticKeypair: device, PeerStatic: home.Public,
	}
	if kind == handshakePair {
		cfg.PresharedKey, cfg.PresharedKeyPlacement = psk, pskPlacement
	}
	initiator, err := noise.NewHandshakeState(cfg)
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(first)
	msg1, _, _, err := initiator.WriteMessage(nil, payload)
	if err != nil {
		t.Fatal(err)
	}
	reply := respond(t, kind, home.Private, homeEphemeral, signPub, device.Public, psk, msg1)
	_, deviceSend, deviceRecv, err := initiator.ReadMessage(nil, reply.msg2)
	if err != nil {
		t.Fatal(err)
	}
	up, down := []byte(`{"cmd":"hello","api":1}`), []byte(`{"event":"welcome","api":1}`)
	sealedUp, _ := deviceSend.Encrypt(nil, nil, up)
	sealedDown, _ := reply.transport.seal(down)
	if got, _ := deviceRecv.Decrypt(nil, nil, sealedDown); !bytes.Equal(got, down) {
		t.Fatalf("device could not read the home's first message")
	}
	v := noiseVector{
		Name: name, Kind: int(kind),
		HomeStaticPrivate: hex.EncodeToString(home.Private), HomeStaticPublic: hex.EncodeToString(home.Public),
		HomeEphemeralPrivate: hex.EncodeToString(homeEphemeral), DeviceStaticPrivate: hex.EncodeToString(device.Private),
		DeviceEphemeralPriv: hex.EncodeToString(deviceEphemeral), DeviceSignPub: hex.EncodeToString(signPub),
		Prologue: hex.EncodeToString(prologue(signPub)), FirstPayload: string(payload),
		DeviceFirstFrame: hex.EncodeToString(append([]byte{byte(kind)}, msg1...)), HomeReply: hex.EncodeToString(reply.msg2),
		DeviceToHomePlaintext: string(up), DeviceToHome: hex.EncodeToString(sealedUp),
		HomeToDevicePlaintext: string(down), HomeToDevice: hex.EncodeToString(sealedDown),
	}
	if kind == handshakePair {
		v.PSK = hex.EncodeToString(psk)
	}
	return v
}

func respond(t *testing.T, kind handshakeKind, homePrivate, homeEphemeral, signPub, deviceNoisePub, psk, msg1 []byte) answered {
	t.Helper()
	home := mustKey(t, homePrivate)
	rng := bytes.NewReader(homeEphemeral)
	var reply answered
	var err error
	if kind == handshakePair {
		reply, err = answerPairing(home, signPub, msg1, rng, func(firstMessage) ([]byte, error) { return psk, nil })
	} else {
		reply, err = answerPaired(home, signPub, msg1, rng, func(firstMessage) ([]byte, error) { return deviceNoisePub, nil })
	}
	if err != nil {
		t.Fatal(err)
	}
	return reply
}

func TestNoiseVectors(t *testing.T) {
	if *update {
		vectors := []noiseVector{
			generateVector(t, "pair: IKpsk2 with a pairing secret", handshakePair, firstMessage{PairingID: "pairing-1", DeviceName: "Test iPhone"}),
			generateVector(t, "paired: IK with the paired Noise key", handshakePaired, firstMessage{ProfileID: "default"}),
		}
		data, _ := json.MarshalIndent(vectors, "", "  ")
		if err := os.WriteFile(vectorsFile, append(data, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(vectorsFile)
	if err != nil {
		t.Fatal(err)
	}
	var vectors []noiseVector
	if err := json.Unmarshal(data, &vectors); err != nil {
		t.Fatal(err)
	}
	for _, v := range vectors {
		t.Run(v.Name, func(t *testing.T) {
			frame := unhex(t, v.DeviceFirstFrame)
			deviceNoisePub := mustKey(t, unhex(t, v.DeviceStaticPrivate)).Public
			reply := respond(t, handshakeKind(frame[0]), unhex(t, v.HomeStaticPrivate), unhex(t, v.HomeEphemeralPrivate), unhex(t, v.DeviceSignPub), deviceNoisePub, unhex(t, v.PSK), frame[1:])
			if got := hex.EncodeToString(reply.msg2); got != v.HomeReply {
				t.Fatalf("home reply = %s, want %s", got, v.HomeReply)
			}
			if got, _ := json.Marshal(reply.claim); string(got) != v.FirstPayload {
				t.Fatalf("first payload = %s, want %s", got, v.FirstPayload)
			}
			up, err := reply.transport.open(unhex(t, v.DeviceToHome))
			if err != nil || string(up) != v.DeviceToHomePlaintext {
				t.Fatalf("device to home = %q, %v", up, err)
			}
			down, _ := reply.transport.seal([]byte(v.HomeToDevicePlaintext))
			if got := hex.EncodeToString(down); got != v.HomeToDevice {
				t.Fatalf("home to device = %s, want %s", got, v.HomeToDevice)
			}
		})
	}
}
