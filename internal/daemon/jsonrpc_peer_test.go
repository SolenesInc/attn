package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

func TestJSONRPCRequestWriteDeadlineClosesAStalledStreamAndReleasesTheGate(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server, client := net.Pipe()
		defer client.Close()
		peer := newJSONRPCPeer(server, bufio.NewReader(server))
		plugin := &pluginConnection{jsonrpcPeer: peer, name: "stalled"}
		ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
		defer cancel()
		deadline, _ := ctx.Deadline()

		result := make(chan error, 1)
		go func() {
			result <- plugin.request(ctx, "driver.spawn", struct{}{}, nil)
		}()
		one := make([]byte, 1)
		if _, err := client.Read(one); err != nil {
			t.Fatalf("read first request byte: %v", err)
		}
		err := <-result
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("stalled write error=%v, want context deadline exceeded", err)
		}
		for _, want := range []string{`plugin "stalled"`, "driver.spawn", "write deadline " + deadline.Format(time.RFC3339Nano)} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("stalled write error=%q, want %q", err, want)
			}
		}

		if err := plugin.request(context.Background(), "driver.resume", struct{}{}, nil); err == nil {
			t.Fatal("request after damaged stream succeeded")
		}
	})
}

func TestJSONRPCRequestExpiresWhileWaitingForTheWriteGate(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server, client := net.Pipe()
		defer server.Close()
		defer client.Close()
		peer := newJSONRPCPeer(server, bufio.NewReader(server))
		plugin := &pluginConnection{jsonrpcPeer: peer, name: "busy"}

		firstCtx, firstCancel := context.WithTimeout(context.Background(), 2*time.Hour)
		defer firstCancel()
		first := make(chan error, 1)
		go func() {
			first <- plugin.request(firstCtx, "driver.spawn", struct{}{}, nil)
		}()
		synctest.Wait()

		secondCtx, secondCancel := context.WithTimeout(context.Background(), time.Hour)
		defer secondCancel()
		secondDeadline, _ := secondCtx.Deadline()
		second := make(chan error, 1)
		go func() {
			second <- plugin.request(secondCtx, "driver.resume", struct{}{}, nil)
		}()
		if err := <-second; !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "write deadline "+secondDeadline.Format(time.RFC3339Nano)) {
			t.Fatalf("waiting write error=%q, want its deadline", err)
		}

		firstCancel()
		if err := <-first; !errors.Is(err, context.Canceled) {
			t.Fatalf("active write error=%v, want context canceled", err)
		}
	})
}

func TestJSONRPCRequestClearsACompletedWriteDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server, client := net.Pipe()
		defer server.Close()
		defer client.Close()
		peer := newJSONRPCPeer(server, bufio.NewReader(server))
		plugin := &pluginConnection{jsonrpcPeer: peer, name: "healthy"}

		request := func(ctx context.Context, method string) {
			done := make(chan error, 1)
			go func() {
				done <- plugin.request(ctx, method, struct{}{}, &struct{}{})
			}()
			frame := decodeJSONRPCMessage(t, client)
			result, err := json.Marshal(struct{}{})
			if err != nil {
				t.Fatalf("marshal response: %v", err)
			}
			peer.routeResponse(jsonRPCMessage{JSONRPC: "2.0", ID: frame.ID, Result: result})
			if err := <-done; err != nil {
				t.Fatalf("%s request: %v", method, err)
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
		defer cancel()
		request(ctx, "driver.spawn")
		<-ctx.Done()

		request(context.Background(), "driver.resume")
	})
}
