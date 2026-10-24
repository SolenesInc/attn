package bridge_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/victorarias/attn/internal/bridge"
	"golang.org/x/crypto/ssh"
)

type echo struct {
	From string `json:"from"`
	N    int    `json:"n"`
}

func answering(self string) bridge.Handlers {
	return bridge.Handlers{
		"echo": func(_ *bridge.Conn, incoming ssh.NewChannel) {
			go bridge.Answer(incoming, func(request []byte) (any, error) {
				var in echo
				if err := json.Unmarshal(request, &in); err != nil {
					return nil, err
				}
				return echo{From: self, N: in.N}, nil
			})
		},
		"refuse": func(_ *bridge.Conn, incoming ssh.NewChannel) {
			go bridge.Answer(incoming, func([]byte) (any, error) { return nil, errors.New("terminal t-1 is not here") })
		},
	}
}

func bridged(t *testing.T, w *pairWorld, home bridge.Handlers) (*bridge.Conn, *bridge.Conn) {
	t.Helper()
	client, _, err := w.dialWith(home)
	if err != nil {
		t.Fatal(err)
	}
	server := <-w.server
	state(t, client, bridge.StateConnected)
	state(t, server, bridge.StateConnected)
	return client, server
}

func TestBridgeCallsEachWay(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := pair(t)
		w.cfg.Handlers = answering("outpost")
		w.start()
		client, server := bridged(t, w, answering("home"))
		var got echo
		if err := client.Call(w.ctx, "echo", echo{N: 1}, &got); err != nil || got != (echo{From: "outpost", N: 1}) {
			t.Fatalf("home -> outpost: %+v, %v", got, err)
		}
		if err := server.Call(w.ctx, "echo", echo{N: 2}, &got); err != nil || got != (echo{From: "home", N: 2}) {
			t.Fatalf("outpost -> home: %+v, %v", got, err)
		}
	})
}

func TestBridgeConcurrentCalls(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := pair(t)
		w.cfg.Handlers = answering("outpost")
		w.start()
		client, _ := bridged(t, w, nil)
		var calls sync.WaitGroup
		failures := make(chan error, 100)
		for n := range 100 {
			calls.Go(func() {
				var got echo
				if err := client.Call(w.ctx, "echo", echo{N: n}, &got); err != nil || got.N != n {
					failures <- fmt.Errorf("call %d: %+v, %v", n, got, err)
				}
			})
		}
		calls.Wait()
		close(failures)
		for err := range failures {
			t.Error(err)
		}
	})
}

func TestBridgeRejectedCallsCarryTheirReason(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := pair(t)
		w.cfg.Handlers = answering("outpost")
		w.start()
		client, _ := bridged(t, w, nil)
		for kind, reason := range map[string]string{"refuse": "terminal t-1 is not here", "missing": `no bridge handler for "missing"`} {
			err := client.Call(w.ctx, kind, echo{}, nil)
			var rejected *bridge.CallRejected
			if !errors.As(err, &rejected) || rejected.Kind != kind || !strings.Contains(rejected.Reason, reason) {
				t.Fatalf("%s: %v", kind, err)
			}
		}
		var got echo
		if err := client.Call(w.ctx, "echo", echo{N: 3}, &got); err != nil || got.N != 3 {
			t.Fatalf("bridge unusable after rejections: %+v, %v", got, err)
		}
	})
}

func TestBridgeCallRequestLimit(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := pair(t)
		kind := strings.Repeat("k", 64)
		w.cfg.Handlers = bridge.Handlers{kind: func(_ *bridge.Conn, incoming ssh.NewChannel) {
			go bridge.Answer(incoming, func(request []byte) (any, error) { return len(request), nil })
		}}
		w.start()
		client, _ := bridged(t, w, nil)
		largest := make([]byte, bridge.MaxCallRequest-2)
		for i := range largest {
			largest[i] = 'x'
		}
		var size int
		if err := client.Call(w.ctx, kind, string(largest), &size); err != nil || size != bridge.MaxCallRequest {
			t.Fatalf("request at the limit: %d, %v", size, err)
		}
		err := client.Call(w.ctx, kind, string(largest)+"x", &size)
		if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("%d-byte limit", bridge.MaxCallRequest)) {
			t.Fatalf("request over the limit: %v", err)
		}
	})
}

func TestBridgeHandlersSeeCallsInSendOrder(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := pair(t)
		var arrived []int
		release := make(chan struct{})
		w.cfg.Handlers = bridge.Handlers{"write": func(_ *bridge.Conn, incoming ssh.NewChannel) {
			var in echo
			if err := json.Unmarshal(incoming.ExtraData(), &in); err != nil {
				t.Error(err)
			}
			arrived = append(arrived, in.N)
			go bridge.Answer(incoming, func([]byte) (any, error) { <-release; return nil, nil })
		}}
		w.start()
		client, _ := bridged(t, w, nil)
		var calls sync.WaitGroup
		w.network.Cut(true)
		for n := range 20 {
			calls.Go(func() {
				if err := client.Call(w.ctx, "write", echo{N: n}, nil); err != nil {
					t.Error(err)
				}
			})
			synctest.Wait()
		}
		w.network.Cut(false)
		synctest.Wait()
		close(release)
		calls.Wait()
		for i, n := range arrived {
			if i != n {
				t.Fatalf("handlers saw %v, sent 0..19 in order", arrived)
			}
		}
		if len(arrived) != 20 {
			t.Fatalf("handlers saw %v", arrived)
		}
	})
}

func TestBridgeChannelsEndWithTheTransport(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := pair(t)
		served := make(chan ssh.Channel, 1)
		w.cfg.Handlers = bridge.Handlers{
			"stream": func(_ *bridge.Conn, incoming ssh.NewChannel) {
				channel, requests, err := incoming.Accept()
				if err != nil {
					t.Error(err)
					return
				}
				go ssh.DiscardRequests(requests)
				served <- channel
			},
			"hang": func(_ *bridge.Conn, incoming ssh.NewChannel) {
				go bridge.Answer(incoming, func([]byte) (any, error) { <-w.ctx.Done(); return nil, nil })
			},
		}
		w.start()
		client, server := bridged(t, w, nil)
		opened, requests, err := client.Open(w.ctx, "stream", nil)
		if err != nil {
			t.Fatal(err)
		}
		go ssh.DiscardRequests(requests)
		remote := <-served
		pending := make(chan error, 1)
		go func() { pending <- client.Call(context.Background(), "hang", echo{}, nil) }()
		synctest.Wait()
		w.network.Cut(true)
		time.Sleep(bridge.LostAfter)
		synctest.Wait()
		<-client.Done()
		<-server.Done()
		for side, channel := range map[string]ssh.Channel{"home": opened, "outpost": remote} {
			if _, err := channel.Read(make([]byte, 1)); err != io.EOF {
				t.Fatalf("%s channel read after the transport ended: %v", side, err)
			}
		}
		if err := <-pending; err == nil {
			t.Fatal("call in flight survived the transport")
		}
		if _, _, err := client.Open(context.Background(), "stream", nil); err == nil {
			t.Fatal("opened a channel on an ended bridge")
		}
	})
}

func TestBridgeIdleChannelKeepsHeartbeats(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := pair(t)
		w.cfg.Handlers = bridge.Handlers{"stream": func(_ *bridge.Conn, incoming ssh.NewChannel) {
			channel, requests, err := incoming.Accept()
			if err != nil {
				t.Error(err)
				return
			}
			go ssh.DiscardRequests(requests)
			go func() { <-w.ctx.Done(); _ = channel.Close() }()
		}}
		w.start()
		client, server := bridged(t, w, nil)
		channel, requests, err := client.Open(w.ctx, "stream", nil)
		if err != nil {
			t.Fatal(err)
		}
		go ssh.DiscardRequests(requests)
		defer channel.Close()
		time.Sleep(time.Minute)
		synctest.Wait()
		for side, c := range map[string]*bridge.Conn{"home": client, "outpost": server} {
			select {
			case got := <-c.States():
				t.Fatalf("%s state changed to %q with an idle channel open", side, got)
			default:
			}
		}
	})
}
