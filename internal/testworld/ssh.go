package testworld

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

type SSHAgent struct {
	Socket    string
	PublicKey []byte
}

func NewSSHAgent(t testing.TB) *SSHAgent {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pub, err := ssh.NewPublicKey(key.Public())
	if err != nil {
		t.Fatal(err)
	}
	keyring := agent.NewKeyring()
	if err := keyring.Add(agent.AddedKey{PrivateKey: key}); err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp("/tmp", "attn-ssh-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "ssh-agent.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	var connections sync.WaitGroup
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			connections.Go(func() {
				defer conn.Close()
				stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
				defer stop()
				_ = agent.ServeAgent(keyring, conn)
			})
		}
	}()
	t.Cleanup(func() { cancel(); _ = listener.Close(); <-stopped; connections.Wait() })
	return &SSHAgent{Socket: socket, PublicKey: ssh.MarshalAuthorizedKey(pub)}
}

func (a *SSHAgent) Authorize(w *World) {
	w.T.Helper()
	path := filepath.Join(w.Dir, "authorized_keys")
	if err := os.WriteFile(path, a.PublicKey, 0600); err != nil {
		w.T.Fatal(err)
	}
	w.Vars = append(w.Vars, "ATTN_AUTHORIZED_KEYS_PATH="+path, "SSH_AUTH_SOCK="+a.Socket)
}
