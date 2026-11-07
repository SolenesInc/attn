package cloudlink

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"nhooyr.io/websocket"
)

// The wire contract: docs/internal/cloud-relay-contract.md.

const envelopeVersion = 1

type frameType byte

const (
	frameChallenge frameType = 1
	frameAuth      frameType = 2
	frameReady     frameType = 3
	frameOpen      frameType = 4
	frameData      frameType = 5
	frameClose     frameType = 6
	frameAllowlist frameType = 7
	framePresence  frameType = 8
	frameError     frameType = 9
	framePush      frameType = 10
)

const envelopeHeader = 6

// maxRelayFrame is the home's cap on one frame, from the wire contract. It keeps a frame inside one Noise message (65,535 bytes).
const maxCloudRelayFrame = 64 << 10

type frame struct {
	kind    frameType
	connID  uint32
	payload []byte
}

func encodeFrame(kind frameType, connID uint32, payload []byte) []byte {
	out := make([]byte, envelopeHeader+len(payload))
	out[0] = envelopeVersion
	out[1] = byte(kind)
	binary.BigEndian.PutUint32(out[2:6], connID)
	copy(out[envelopeHeader:], payload)
	return out
}

func decodeFrame(data []byte) (frame, error) {
	if len(data) < envelopeHeader {
		return frame{}, fmt.Errorf("the Cloud Relay sent a %d-byte frame; an envelope is at least %d bytes", len(data), envelopeHeader)
	}
	if data[0] != envelopeVersion {
		return frame{}, fmt.Errorf("the Cloud Relay sent envelope version %d; this home speaks %d", data[0], envelopeVersion)
	}
	return frame{kind: frameType(data[1]), connID: binary.BigEndian.Uint32(data[2:6]), payload: data[envelopeHeader:]}, nil
}

type cloudError struct {
	Status  int
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *cloudError) Error() string {
	return fmt.Sprintf("the Cloud answered %d %s: %s", e.Status, e.Code, e.Message)
}

func readCloudError(res *http.Response) error {
	var body struct {
		Error cloudError `json:"error"`
	}
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
	if json.Unmarshal(raw, &body) != nil || body.Error.Code == "" {
		return fmt.Errorf("the Cloud answered %s: %s", res.Status, strings.TrimSpace(string(raw)))
	}
	body.Error.Status = res.StatusCode
	return &body.Error
}

func register(ctx context.Context, client *http.Client, cloudURL string, id Identity, name string) (string, error) {
	pub := b64(id.SignPub())
	sig := ed25519.Sign(id.SignKey, []byte("attn-cloud/v1 register\n"+pub+"\n"+name))
	body, _ := json.Marshal(map[string]string{"home_sign_pub": pub, "name": name, "sig": b64(sig)})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cloudURL+"/v1/homes", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("content-type", "application/json")
	res, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("registering with Attn Cloud at %s: %w", cloudURL, err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", readCloudError(res)
	}
	var out struct {
		HomeID string `json:"home_id"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil || out.HomeID == "" {
		return "", fmt.Errorf("the Cloud at %s answered registration without a home_id", cloudURL)
	}
	return out.HomeID, nil
}

var errUnknownHome = errors.New("the Cloud does not know this home")

func dialHome(ctx context.Context, client *http.Client, cloudURL string, id Identity, homeID string) (*websocket.Conn, error) {
	url := "ws" + strings.TrimPrefix(cloudURL, "http") + "/v1/homes/" + homeID + "/home"
	conn, res, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPClient: client})
	if err != nil {
		if res != nil && res.StatusCode == http.StatusNotFound {
			return nil, errUnknownHome
		}
		return nil, fmt.Errorf("dialing the Cloud Relay at %s: %w", url, err)
	}
	conn.SetReadLimit(maxCloudRelayFrame)
	challenge, err := readFrame(ctx, conn)
	if err != nil {
		conn.CloseNow()
		return nil, err
	}
	if challenge.kind != frameChallenge {
		conn.CloseNow()
		return nil, fmt.Errorf("the Cloud Relay sent frame type %d before its challenge", challenge.kind)
	}
	message := append([]byte("attn-cloud/v1 auth home\n"+homeID+"\n"), challenge.payload...)
	if err := conn.Write(ctx, websocket.MessageBinary, encodeFrame(frameAuth, 0, ed25519.Sign(id.SignKey, message))); err != nil {
		conn.CloseNow()
		return nil, err
	}
	ready, err := readFrame(ctx, conn)
	if err != nil {
		conn.CloseNow()
		return nil, err
	}
	if ready.kind != frameReady {
		conn.CloseNow()
		return nil, fmt.Errorf("the Cloud Relay answered auth with frame type %d, want ready", ready.kind)
	}
	return conn, nil
}

func readFrame(ctx context.Context, conn *websocket.Conn) (frame, error) {
	for {
		kind, data, err := conn.Read(ctx)
		if err != nil {
			return frame{}, err
		}
		if kind == websocket.MessageText {
			continue
		}
		return decodeFrame(data)
	}
}

type allowlist struct {
	Devices  []string        `json:"devices"`
	Pairings []pairingWindow `json:"pairings"`
}

type pairingWindow struct {
	TokenHash string `json:"token_hash"`
	Until     int64  `json:"until"`
}
