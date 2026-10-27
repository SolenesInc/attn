package bridge_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/victorarias/attn/internal/bridge"
	"golang.org/x/crypto/ssh"
)

func TestUpdateRequiresTheOutpostsInstance(t *testing.T) {
	for _, tc := range []struct {
		name, version, instance, reason string
	}{
		{"legacy", "1", "", "install this build by hand once"},
		{"missing", "2", "", "install this build by hand once"},
		{"mismatch", "2", `,"instance":"default"`, `home instance "router-check" cannot use outpost instance "default"`},
		{"matching", "2", `,"instance":"router-check"`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := pair(t)
			answer := `{"bridge":` + tc.version + `,"daemon_id":"d-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","build":{"version":"test","source_fingerprint":"unknown","protocol_version":"test"},"update":1,"target":{"platform":"linux/amd64","executable":"/home/user/.local/bin/attn"}` + tc.instance + `}`
			observed := frozenOutpost(t, w, answer)
			self := home
			self.Instance = "router-check"
			conn, _, err := bridge.DialUpdate(w.ctx, bridge.DialConfig{Address: "outpost", AgentSocket: "agent", Self: self, DialContext: w.dialContext})
			if conn != nil {
				path := filepath.Join(t.TempDir(), "payload")
				if writeErr := os.WriteFile(path, []byte("hello"), 0600); writeErr != nil {
					t.Fatal(writeErr)
				}
				_ = conn.Send(w.ctx, home.Build, path, func(string) {})
				_ = conn.Close()
			}
			channels := <-observed
			if tc.reason == "" {
				if err != nil || len(channels) != 1 || channels[0] != "attn-update" {
					t.Fatalf("matching instance: err=%v channels=%v", err, channels)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.reason) || len(channels) != 0 {
				t.Fatalf("instance refusal: err=%v channels=%v, want %q and no update channel", err, channels, tc.reason)
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
