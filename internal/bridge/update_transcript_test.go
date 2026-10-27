package bridge_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/victorarias/attn/internal/bridge"
	"golang.org/x/crypto/ssh"
)

const frozenUpdateHello = `{"bridge":2,"daemon_id":"d-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","name":"home","instance":"default","build":{"version":"next","source_fingerprint":"git:next","protocol_version":"next"},"update":1}`
const frozenUpdateHeader = `{"update":1,"build":{"version":"next","source_fingerprint":"git:next","protocol_version":"next"},"platform":"linux/amd64","size":5,"sha256":"2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824","encoding":"zstd"}`

func TestOutpostUpdateFrozenTranscript(t *testing.T) {
	for _, mode := range []string{"install", "wrong-platform", "disconnect"} {
		t.Run(mode, func(t *testing.T) {
			wrongPlatform := mode == "wrong-platform"
			cancelled := make(chan struct{}, 1)
			w := pair(t)
			w.cfg.Self.Name, w.cfg.Self.Instance = "outpost", "default"
			restarted := make(chan struct{}, 1)
			w.cfg.Update = &bridge.UpdateServer{Target: bridge.UpdateTarget{Platform: "linux/amd64", Executable: "/home/user/.local/bin/attn"}, Receive: func(ctx context.Context, header bridge.UpdateHeader, compressed io.Reader, verifying func()) error {
				raw, _ := json.Marshal(header)
				if string(raw) != frozenUpdateHeader {
					return fmt.Errorf("header changed: %s", raw)
				}
				reader, err := zstd.NewReader(compressed)
				if err != nil {
					return err
				}
				defer reader.Close()
				data, err := io.ReadAll(reader)
				if err != nil {
					return err
				}
				if string(data) != "hello" {
					return fmt.Errorf("payload changed: %q", data)
				}
				verifying()
				if mode == "disconnect" {
					<-ctx.Done()
					cancelled <- struct{}{}
					return ctx.Err()
				}
				return nil
			}, Restart: func() { restarted <- struct{}{} }}
			w.start()
			raw, err := w.network.Dial(w.ctx)
			if err != nil {
				t.Fatal(err)
			}
			signers, err := w.keyring.Signers()
			if err != nil {
				t.Fatal(err)
			}
			conn, channels, requests, err := ssh.NewClientConn(raw, "outpost", &ssh.ClientConfig{User: "attn", Auth: []ssh.AuthMethod{ssh.PublicKeys(signers...)}, HostKeyCallback: ssh.InsecureIgnoreHostKey()})
			if err != nil {
				t.Fatal(err)
			}
			client := ssh.NewClient(conn, channels, requests)
			defer client.Close()
			control, requests, err := client.OpenChannel("attn-control", nil)
			if err != nil {
				t.Fatal(err)
			}
			go ssh.DiscardRequests(requests)
			if _, err := fmt.Fprintln(control, frozenUpdateHello); err != nil {
				t.Fatal(err)
			}
			var answer struct {
				Bridge   int    `json:"bridge"`
				Update   int    `json:"update"`
				DaemonID string `json:"daemon_id"`
				Name     string `json:"name"`
				Instance string `json:"instance"`
				Target   struct {
					Platform   string `json:"platform"`
					Executable string `json:"executable"`
				} `json:"target"`
				Refused json.RawMessage `json:"refused"`
			}
			if err := json.NewDecoder(control).Decode(&answer); err != nil {
				t.Fatal(err)
			}
			if answer.Bridge != 2 || answer.Update != 1 || answer.DaemonID != outpost.DaemonID || answer.Name != "outpost" || answer.Instance != "default" || answer.Target.Platform != "linux/amd64" || answer.Target.Executable != "/home/user/.local/bin/attn" || answer.Refused != nil {
				t.Fatalf("update hello answer changed: %+v", answer)
			}
			header := frozenUpdateHeader
			if wrongPlatform {
				header = strings.ReplaceAll(header, "linux/amd64", "darwin/arm64")
			}
			channel, requests, err := client.OpenChannel("attn-update", []byte(header))
			if err != nil {
				t.Fatal(err)
			}
			go ssh.DiscardRequests(requests)
			if !wrongPlatform {
				var compressed bytes.Buffer
				writer, _ := zstd.NewWriter(&compressed)
				_, _ = writer.Write([]byte("hello"))
				_ = writer.Close()
				if _, err := channel.Write(compressed.Bytes()); err != nil {
					t.Fatal(err)
				}
				_ = channel.CloseWrite()
			}
			decoder := json.NewDecoder(channel)
			var reply struct {
				Status string `json:"status"`
				Error  string `json:"error"`
			}
			if err := decoder.Decode(&reply); err != nil {
				t.Fatal(err)
			}
			if wrongPlatform {
				if reply.Status != "refused" || !strings.Contains(reply.Error, "darwin/arm64") || !strings.Contains(reply.Error, "linux/amd64") {
					t.Fatalf("wrong platform refusal: %+v", reply)
				}
				return
			}
			if reply.Status != "verifying" {
				t.Fatalf("verification reply changed: %+v", reply)
			}
			if mode == "disconnect" {
				_ = client.Close()
				<-cancelled
				return
			}
			if err := decoder.Decode(&reply); err != nil {
				t.Fatal(err)
			}
			if reply.Status != "restarting" || reply.Error != "" {
				t.Fatalf("restart reply changed: %+v", reply)
			}
			<-restarted
		})
	}
}
