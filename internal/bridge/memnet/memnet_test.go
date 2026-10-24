package memnet_test

import (
	"context"
	"io"
	"testing"
	"testing/synctest"
	"time"

	"github.com/victorarias/attn/internal/bridge/memnet"
)

func TestRateDeliversBytesOnTheClock(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		network := memnet.Listen(memnet.Rate(2.8e6))
		accepted := make(chan io.ReadCloser, 1)
		go func() {
			c, err := network.Accept()
			if err != nil {
				t.Error(err)
			}
			accepted <- c
		}()
		client, err := network.Dial(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		server := <-accepted
		start := time.Now()
		for range 4 {
			if _, err := client.Write(make([]byte, 700_000)); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := io.ReadFull(server, make([]byte, 2_800_000)); err != nil {
			t.Fatal(err)
		}
		if elapsed := time.Since(start); elapsed != time.Second {
			t.Fatalf("2.8 MB at 2.8 MB/s took %s", elapsed)
		}
	})
}
