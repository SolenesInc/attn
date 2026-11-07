package cloudlink

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"nhooyr.io/websocket"
)

type cloudRelaySigned struct {
	Message   string `json:"message"`
	Signature string `json:"signature"`
}

type cloudRelayVectors struct {
	SignSeed string `json:"sign_seed"`
	SignPub  string `json:"sign_pub"`
	HomeID   string `json:"home_id"`
	Nonce    string `json:"nonce"`
	HomeName string `json:"home_name"`
	Frames   []struct {
		Name    string `json:"name"`
		Type    byte   `json:"type"`
		ConnID  uint32 `json:"conn_id"`
		Payload string `json:"payload"`
		Bytes   string `json:"bytes"`
	} `json:"frames"`
	RelayToken struct {
		QRSecret  string `json:"qr_secret"`
		Token     string `json:"token"`
		TokenHash string `json:"token_hash"`
	} `json:"relay_token"`
	Signatures struct {
		Register cloudRelaySigned `json:"register"`
		AuthHome cloudRelaySigned `json:"auth_home"`
	} `json:"signatures"`
}

func loadCloudRelayVectors(t *testing.T) (cloudRelayVectors, Identity) {
	t.Helper()
	raw, err := os.ReadFile("testdata/cloud_relay_vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var vectors cloudRelayVectors
	if err := json.Unmarshal(raw, &vectors); err != nil {
		t.Fatal(err)
	}
	return vectors, Identity{SignKey: ed25519.NewKeyFromSeed(unhex(t, vectors.SignSeed))}
}

func TestTheHomeSpeaksTheCloudRelayContractVectors(t *testing.T) {
	vectors, identity := loadCloudRelayVectors(t)
	kinds := map[string]frameType{
		"challenge": frameChallenge, "auth": frameAuth, "ready_home": frameReady, "ready_device": frameReady,
		"open": frameOpen, "data": frameData, "close": frameClose, "allowlist": frameAllowlist, "allowlist_applied": frameAllowlist,
		"presence_online": framePresence, "presence_offline": framePresence, "error": frameError, "push": framePush,
	}
	for _, vector := range vectors.Frames {
		t.Run(vector.Name, func(t *testing.T) {
			kind, known := kinds[vector.Name]
			if !known {
				t.Fatalf("no frame type for vector %q", vector.Name)
			}
			wire, payload := unhex(t, vector.Bytes), unhex(t, vector.Payload)
			if got := encodeFrame(kind, vector.ConnID, payload); !bytes.Equal(got, wire) {
				t.Fatalf("encoded %x, want %x", got, wire)
			}
			decoded, err := decodeFrame(wire)
			if err != nil || decoded.kind != frameType(vector.Type) || decoded.connID != vector.ConnID || !bytes.Equal(decoded.payload, payload) {
				t.Fatalf("decoded %+v (%v), want type %d conn %d payload %x", decoded, err, vector.Type, vector.ConnID, payload)
			}
		})
	}

	t.Run("relay token", func(t *testing.T) {
		token := relayToken(unhex(t, vectors.RelayToken.QRSecret))
		if got := hex.EncodeToString(token); got != vectors.RelayToken.Token {
			t.Fatalf("token %s, want %s", got, vectors.RelayToken.Token)
		}
		hash := sha256.Sum256(token)
		if got := hex.EncodeToString(hash[:]); got != vectors.RelayToken.TokenHash {
			t.Fatalf("token hash %s, want %s", got, vectors.RelayToken.TokenHash)
		}
	})

	t.Run("registration signature", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			var body map[string]string
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Errorf("registration body: %v", err)
			}
			if req.URL.Path != "/v1/homes" || body["home_sign_pub"] != vectors.SignPub || body["name"] != vectors.HomeName {
				t.Errorf("registration %s %v", req.URL.Path, body)
			}
			want, _ := hex.DecodeString(vectors.Signatures.Register.Signature)
			if body["sig"] != b64(want) {
				t.Errorf("registration sig %s, want %s", body["sig"], b64(want))
			}
			json.NewEncoder(w).Encode(map[string]string{"home_id": vectors.HomeID})
		}))
		defer server.Close()
		id, err := register(context.Background(), server.Client(), server.URL, identity, vectors.HomeName)
		if err != nil || id != vectors.HomeID {
			t.Fatalf("register = %q, %v", id, err)
		}
	})

	t.Run("home auth signature", func(t *testing.T) {
		wantAuth := "010200000000" + vectors.Signatures.AuthHome.Signature
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if req.URL.Path != "/v1/homes/"+vectors.HomeID+"/home" {
				t.Errorf("home socket path %s", req.URL.Path)
			}
			conn, err := websocket.Accept(w, req, nil)
			if err != nil {
				t.Errorf("accept: %v", err)
				return
			}
			defer conn.CloseNow()
			ctx := req.Context()
			conn.Write(ctx, websocket.MessageBinary, encodeFrame(frameChallenge, 0, unhex(t, vectors.Nonce)))
			_, auth, err := conn.Read(ctx)
			if err != nil || hex.EncodeToString(auth) != wantAuth {
				t.Errorf("auth frame %x (%v), want %s", auth, err, wantAuth)
			}
			conn.Write(ctx, websocket.MessageBinary, encodeFrame(frameReady, 0, nil))
			conn.Read(ctx)
		}))
		defer server.Close()
		conn, err := dialHome(context.Background(), server.Client(), server.URL, identity, vectors.HomeID)
		if err != nil {
			t.Fatal(err)
		}
		conn.CloseNow()
	})
}
