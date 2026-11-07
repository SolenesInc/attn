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
	Phone      string `json:"device"`
	Ciphertext string `json:"ciphertext"`
	CollapseID string `json:"collapse_id"`
}

// SealPush encrypts plaintext to a phone's Noise static key with HPKE: DHKEM(X25519, HKDF-SHA256), HKDF-SHA256,
// ChaCha20-Poly1305. The result is the 32-byte encapsulated key followed by the ciphertext.
func SealPush(phoneNoisePub, plaintext []byte) ([]byte, error) {
	pub, err := ecdh.X25519().NewPublicKey(phoneNoisePub)
	if err != nil {
		return nil, fmt.Errorf("the phone's Noise key: %w", err)
	}
	recipient, err := hpke.NewDHKEMPublicKey(pub)
	if err != nil {
		return nil, err
	}
	return hpke.Seal(recipient, hpke.HKDFSHA256(), hpke.ChaCha20Poly1305(), []byte(PushInfo), plaintext)
}

// Push asks Attn Cloud to deliver plaintext, sealed to the paired phone, as a notification. It returns ErrLinkDown
// when the frame did not reach the socket; the caller decides whether to wait for NextConnect. The Cloud Relay reports its
// own refusals as error frames.
func (l *Link) Push(p Pairing, plaintext []byte, collapseID string) error {
	sealed, err := SealPush(p.PhoneNoisePub, plaintext)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(pushFrame{Phone: b64(p.PhoneSignPub), Ciphertext: b64(sealed), CollapseID: collapseID})
	if err != nil {
		return err
	}
	l.mu.Lock()
	s := l.session
	l.mu.Unlock()
	if s == nil {
		return ErrLinkDown
	}
	if err := s.write(framePush, 0, payload); err != nil {
		return fmt.Errorf("%w: %w", ErrLinkDown, err)
	}
	return nil
}

func (l *Link) HomeName() string { return l.cfg.HomeName }
