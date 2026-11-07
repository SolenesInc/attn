package cloudlink

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/flynn/noise"
)

var cipherSuite = noise.NewCipherSuite(noise.DH25519, noise.CipherChaChaPoly, noise.HashSHA256)

const pskPlacement = 2

const noiseTagSize = 16

// MaxMessageBytes is the largest Home Gateway message that fits one Cloud Relay frame once sealed.
const MaxMessageBytes = maxCloudRelayFrame - envelopeHeader - noiseTagSize

func prologue(phoneSignPub []byte) []byte {
	return append([]byte("attn-cloud/v1 noise\n"), phoneSignPub...)
}

type transport struct {
	send *noise.CipherState
	recv *noise.CipherState
}

func (t transport) seal(plaintext []byte) ([]byte, error) {
	if len(plaintext) > MaxMessageBytes {
		return nil, fmt.Errorf("a Home Gateway message of %d bytes does not fit the Cloud Relay's %d-byte frame; at most %d bytes fit after the envelope and the Noise tag", len(plaintext), maxCloudRelayFrame, MaxMessageBytes)
	}
	return t.send.Encrypt(nil, nil, plaintext)
}

func (t transport) open(ciphertext []byte) ([]byte, error) {
	return t.recv.Decrypt(nil, nil, ciphertext)
}

type answered struct {
	msg2       []byte
	peerStatic []byte
	claim      firstMessage
	pairing    bool
	transport  transport
}

func answerPaired(static noise.DHKey, phoneSignPub, msg1 []byte, rng io.Reader, noisePubFor func(firstMessage) ([]byte, error)) (answered, error) {
	hs, err := noise.NewHandshakeState(noise.Config{
		CipherSuite: cipherSuite, Random: rng, Pattern: noise.HandshakeIK,
		Prologue: prologue(phoneSignPub), StaticKeypair: static,
	})
	if err != nil {
		return answered{}, err
	}
	payload, _, _, err := hs.ReadMessage(nil, msg1)
	if err != nil {
		return answered{}, fmt.Errorf("reading a paired phone's handshake: %w", err)
	}
	var claim firstMessage
	if err := json.Unmarshal(payload, &claim); err != nil || claim.ProfileID == "" {
		return answered{}, errors.New("a paired phone's handshake named no profile_id")
	}
	want, err := noisePubFor(claim)
	if err != nil {
		return answered{}, err
	}
	if !bytes.Equal(hs.PeerStatic(), want) {
		return answered{}, errors.New("a paired phone presented a Noise key other than the one it paired with")
	}
	return finish(hs, claim, false)
}

func answerPairing(static noise.DHKey, phoneSignPub, msg1 []byte, rng io.Reader, secretFor func(firstMessage) ([]byte, error)) (answered, error) {
	hs, err := noise.NewHandshakeState(noise.Config{
		CipherSuite: cipherSuite, Random: rng, Pattern: noise.HandshakeIK,
		Prologue: prologue(phoneSignPub), StaticKeypair: static, PresharedKeyPlacement: pskPlacement,
	})
	if err != nil {
		return answered{}, err
	}
	payload, _, _, err := hs.ReadMessage(nil, msg1)
	if err != nil {
		return answered{}, fmt.Errorf("reading a pairing handshake: %w", err)
	}
	var claim firstMessage
	if err := json.Unmarshal(payload, &claim); err != nil || claim.PairingID == "" {
		return answered{}, errors.New("a pairing handshake named no pairing_id; scan the QR code again")
	}
	secret, err := secretFor(claim)
	if err != nil {
		return answered{}, err
	}
	if err := hs.SetPresharedKey(secret); err != nil {
		return answered{}, err
	}
	return finish(hs, claim, true)
}

func finish(hs *noise.HandshakeState, claim firstMessage, pairing bool) (answered, error) {
	msg2, initiatorToResponder, responderToInitiator, err := hs.WriteMessage(nil, nil)
	if err != nil {
		return answered{}, err
	}
	return answered{msg2: msg2, peerStatic: hs.PeerStatic(), claim: claim, pairing: pairing, transport: transport{send: responderToInitiator, recv: initiatorToResponder}}, nil
}
