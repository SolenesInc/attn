package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/victorarias/attn/internal/bridge"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/ptybackend"
	"golang.org/x/crypto/ssh"
)

type transfer struct {
	Bytes int64
	Raw   bool
}

type zeros struct{}

func (zeros) Read(p []byte) (int, error) { clear(p); return len(p), nil }

func main() {
	serve := flag.Bool("serve", false, "serve the isolated bridge and real shell")
	address := flag.String("address", "", "dedicated test bridge address")
	data := flag.String("data", "", "isolated instance data directory")
	key := flag.String("keys", "", "test server's authorized_keys file")
	size := flag.Int64("bytes", 1_000_000_000, "total duplex payload bytes")
	raw := flag.Bool("ssh-only", false, "use only SSH's channel window")
	flag.Parse()
	if *address == "" || *data == "" {
		fmt.Fprintln(os.Stderr, "--address and --data must name an isolated test instance")
		os.Exit(1)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	self := bridge.Identity{DaemonID: "bulk-proof-home", Build: bridge.Build{Version: "probe", SourceFingerprint: "bulk-proof", ProtocolVersion: "bulk-proof"}}
	var err error
	if *serve {
		self.DaemonID = "bulk-proof-outpost"
		err = server(ctx, *address, *data, *key, self)
	} else {
		err = measure(ctx, *address, self, transfer{Bytes: *size / 2, Raw: *raw})
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func server(ctx context.Context, address, data, keys string, self bridge.Identity) error {
	if err := os.MkdirAll(data, 0700); err != nil {
		return err
	}
	backend := ptybackend.NewEmbedded(nil)
	defer backend.Shutdown(context.Background())
	handlers := bridge.Handlers{
		"spawn": func(c *bridge.Conn, in ssh.NewChannel) {
			go bridge.Answer(in, func(ctx context.Context, _ []byte) (any, error) {
				return nil, backend.Spawn(ctx, ptybackend.SpawnOptions{ID: "echo", Agent: "shell", Executable: "/bin/bash", CWD: data, Cols: 100, Rows: 30})
			})
		},
		"input": func(c *bridge.Conn, in ssh.NewChannel) {
			go bridge.Answer(in, func(ctx context.Context, raw []byte) (any, error) {
				var input string
				if err := json.Unmarshal(raw, &input); err != nil {
					return nil, err
				}
				return nil, backend.Input(ctx, "echo", []byte(input))
			})
		},
		bridge.AttachChannel: func(c *bridge.Conn, in ssh.NewChannel) {
			go c.ServeAttach(ctx, in, func(r bridge.AttachRequest) (ptybackend.AttachInfo, ptybackend.Stream, error) {
				return backend.Attach(ctx, r.Terminal, r.Subscriber, r.Options)
			})
		},
		"stats": func(c *bridge.Conn, in ssh.NewChannel) {
			go bridge.Answer(in, func(context.Context, []byte) (any, error) { return c.MaxHeartbeatGap().Seconds(), nil })
		},
		"bulk": func(c *bridge.Conn, in ssh.NewChannel) {
			go func() {
				var request transfer
				if json.Unmarshal(in.ExtraData(), &request) != nil {
					_ = in.Reject(ssh.Prohibited, "invalid transfer")
					return
				}
				channel, requests, err := in.Accept()
				if err != nil {
					return
				}
				defer channel.Close()
				var stream interface {
					io.ReadWriteCloser
					CloseWrite() error
				} = channel
				if request.Raw {
					go ssh.DiscardRequests(requests)
				} else {
					stream = c.Bulk(ctx, channel, requests)
					defer stream.Close()
				}
				written := make(chan error, 1)
				go func() {
					_, err := io.CopyN(stream, zeros{}, request.Bytes)
					if err == nil {
						err = stream.CloseWrite()
					}
					written <- err
				}()
				_, _ = io.CopyN(io.Discard, stream, request.Bytes)
				<-written
			}()
		},
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	fmt.Println("ready", listener.Addr())
	return bridge.Serve(ctx, listener, bridge.ServerConfig{Self: self, HostKeyPath: filepath.Join(data, "bridge_host_key"), AuthorizedKeys: func() ([]byte, error) { return os.ReadFile(keys) }, Handlers: handlers, OnError: func(err error) { fmt.Fprintln(os.Stderr, err) }}, func(c *bridge.Conn) {
		go func() { <-c.Done(); _ = backend.Remove(context.Background(), "echo") }()
	})
}

func measure(ctx context.Context, address string, self bridge.Identity, request transfer) error {
	c, _, err := bridge.Dial(ctx, bridge.DialConfig{Address: address, Self: self, AgentSocket: os.Getenv("SSH_AUTH_SOCK")})
	if err != nil {
		return err
	}
	defer c.Close()
	if err := c.Call(ctx, "spawn", struct{}{}, nil); err != nil {
		return err
	}
	_, output, err := c.Attach(ctx, bridge.AttachRequest{Terminal: harness.TerminalID("echo"), Subscriber: "bulk-proof"})
	if err != nil {
		return err
	}
	defer output.Close()
	echoes := make(chan int, 1)
	ready := make(chan struct{})
	var once sync.Once
	go func() {
		defer close(echoes)
		var pending []byte
		for event := range output.Events() {
			pending = append(pending, event.Data...)
			if bytes.Contains(pending, []byte("echo_READY")) {
				once.Do(func() { close(ready) })
			}
			for {
				start := bytes.Index(pending, []byte("<E"))
				if start < 0 || len(pending) < start+10 {
					break
				}
				n, err := strconv.Atoi(string(pending[start+2 : start+8]))
				pending = pending[start+10:]
				if err == nil {
					echoes <- n
				}
			}
			output.(interface{ Acknowledge() }).Acknowledge()
		}
	}()
	command := "stty raw -echo; python3 -u -c 'import os,sys; print(\"echo_\"+\"READY\",flush=True); [os.write(1,b\"<\"+line+b\">\") for line in sys.stdin.buffer]'\r"
	if err := c.Call(ctx, "input", command, nil); err != nil {
		return err
	}
	select {
	case <-ready:
	case <-c.Done():
		return c.Err()
	}
	payload, _ := json.Marshal(request)
	channel, requests, err := c.Open(ctx, "bulk", payload)
	if err != nil {
		return err
	}
	defer channel.Close()
	var stream interface {
		io.ReadWriteCloser
		CloseWrite() error
	} = channel
	if request.Raw {
		go ssh.DiscardRequests(requests)
	} else {
		stream = c.Bulk(ctx, channel, requests)
		defer stream.Close()
	}
	began := time.Now()
	finished := make(chan struct{})
	results := make(chan error, 2)
	go func() { _, err := io.CopyN(stream, zeros{}, request.Bytes); _ = stream.CloseWrite(); results <- err }()
	go func() { _, err := io.CopyN(io.Discard, stream, request.Bytes); results <- err; close(finished) }()
	var delays []time.Duration
	for n := 0; ; n++ {
		start := time.Now()
		if err := c.Call(ctx, "input", fmt.Sprintf("E%06d\n", n), nil); err != nil {
			return err
		}
		select {
		case got, open := <-echoes:
			if !open {
				return fmt.Errorf("echo stream ended")
			}
			if got != n {
				return fmt.Errorf("echo=%d, wanted %d", got, n)
			}
			delays = append(delays, time.Since(start))
		case <-c.Done():
			return c.Err()
		}
		select {
		case <-finished:
			goto drained
		case <-time.After(30 * time.Millisecond):
		}
	}
drained:
	for range 2 {
		if err := <-results; err != nil {
			return err
		}
	}
	seconds := time.Since(began).Seconds()
	var remoteGap float64
	if err := c.Call(ctx, "stats", struct{}{}, &remoteGap); err != nil {
		return err
	}
	slices.Sort(delays)
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"bytes_each_direction": request.Bytes, "ssh_only": request.Raw, "bulk_window": bridge.BulkCredit, "seconds": seconds, "MB_s_each_direction": float64(request.Bytes) / 1e6 / seconds, "echo_samples": len(delays), "echo_p50_ms": float64(delays[len(delays)/2]) / 1e6, "echo_p99_ms": float64(delays[(len(delays)*99-1)/100]) / 1e6, "heartbeat_gap_home_s": c.MaxHeartbeatGap().Seconds(), "heartbeat_gap_outpost_s": remoteGap, "address": strings.TrimSpace(address)})
}
