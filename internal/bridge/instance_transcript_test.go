package bridge_test

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/victorarias/attn/internal/bridge"
	"strings"
	"sync"
	"testing"

	"golang.org/x/crypto/ssh"
)

func TestBridgeHelloTranscript(t *testing.T) {
	for _, tc := range []struct{ name, version, instance, reason string }{
		{"legacy", "1", "", "choose Update"},
		{"missing-instance", "2", "", "choose Update"},
		{"other-instance", "2", `,"instance":"other"`, `home instance "default" cannot use outpost instance "other"`},
		{"matching", "2", `,"instance":"default"`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := pair(t)
			answer := `{"bridge":` + tc.version + `,"daemon_id":"d-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","name":"outpost","build":{"version":"test","source_fingerprint":"unknown","protocol_version":"test"}` + tc.instance + `}`
			observed := frozenOutpost(t, w, answer)
			conn, _, err := bridge.Dial(w.ctx, bridge.DialConfig{Conn: dialPipe(t, w), Self: home})
			if conn != nil {
				conn.Close()
			}
			<-observed
			if tc.reason == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.reason) {
				t.Fatalf("refusal=%v, want %s", err, tc.reason)
			}
		})
	}
}

func frozenOutpost(t *testing.T, w *pairWorld, answer string) <-chan []string {
	t.Helper()
	signer, err := ssh.NewSignerFromKey(testKey(t))
	if err != nil {
		t.Fatal(err)
	}
	observed := make(chan []string, 1)
	w.workers.Go(func() {
		var opened []string
		defer func() { observed <- opened }()
		raw, err := w.network.Accept()
		if err != nil {
			t.Error(err)
			return
		}
		defer raw.Close()
		stop := context.AfterFunc(w.ctx, func() { _ = raw.Close() })
		defer stop()
		cfg := &ssh.ServerConfig{NoClientAuth: true}
		cfg.AddHostKey(signer)
		conn, channels, requests, err := ssh.NewServerConn(raw, cfg)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		var drains sync.WaitGroup
		drains.Go(func() { ssh.DiscardRequests(requests) })
		defer drains.Wait()
		defer conn.Close()
		for incoming := range channels {
			if incoming.ChannelType() != "attn-control" {
				opened = append(opened, incoming.ChannelType())
				_ = incoming.Reject(ssh.Prohibited, "frozen peer records updates without installing them")
				continue
			}
			control, requests, err := incoming.Accept()
			if err != nil {
				t.Error(err)
				return
			}
			drains.Go(func() { ssh.DiscardRequests(requests) })
			var greeting json.RawMessage
			if err := json.NewDecoder(control).Decode(&greeting); err != nil {
				t.Error(err)
				return
			}
			const expected = `{"bridge":2,"daemon_id":"d-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","name":"home","instance":"default","build":{"version":"test","source_fingerprint":"unknown","protocol_version":"test"}}`
			if string(greeting) != expected {
				t.Errorf("hello transcript = %s, want %s", greeting, expected)
			}
			if _, err := fmt.Fprintln(control, answer); err != nil {
				t.Error(err)
				return
			}
		}
	})
	t.Cleanup(func() {
		w.cancel()
		_ = w.network.Close()
		w.workers.Wait()
	})
	return observed
}
