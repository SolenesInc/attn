package cloudlink

import (
	"crypto/ecdh"
	"crypto/hpke"
	"encoding/json"
	"errors"
	"fmt"
)

// PushInfo binds a push's HPKE context; the phone's Notification Service Extension opens it with the same string.
const PushInfo = "attn push v1"

var ErrLinkDown = errors.New("the home is not connected to Attn Cloud")

type pushFrame struct {
	Device     string `json:"device"`
	Ciphertext string `json:"ciphertext"`
	CollapseID string `json:"collapse_id"`
}

// SealPush encrypts plaintext to a device's Noise static key with HPKE: DHKEM(X25519, HKDF-SHA256), HKDF-SHA256,
// ChaCha20-Poly1305. The result is the 32-byte encapsulated key followed by the ciphertext.
func SealPush(deviceNoisePub, plaintext []byte) ([]byte, error) {
	pub, err := ecdh.X25519().NewPublicKey(deviceNoisePub)
	if err != nil {
		return nil, fmt.Errorf("the device's Noise key: %w", err)
	}
	recipient, err := hpke.NewDHKEMPublicKey(pub)
	if err != nil {
		return nil, err
	}
	return hpke.Seal(recipient, hpke.HKDFSHA256(), hpke.ChaCha20Poly1305(), []byte(PushInfo), plaintext)
}

// Push asks Attn Cloud to deliver plaintext, sealed to the paired device, as a notification. Pushes are best effort:
// when the link is down the push is dropped with ErrLinkDown, and the relay reports its own refusals as error frames.
func (l *Link) Push(p Pairing, plaintext []byte, collapseID string) error {
	sealed, err := SealPush(p.DeviceNoisePub, plaintext)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(pushFrame{Device: b64(p.DeviceSignPub), Ciphertext: b64(sealed), CollapseID: collapseID})
	if err != nil {
		return err
	}
	l.mu.Lock()
	s := l.session
	l.mu.Unlock()
	if s == nil {
		return ErrLinkDown
	}
	return s.write(framePush, 0, payload)
}

func (l *Link) HomeName() string { return l.cfg.HomeName }
