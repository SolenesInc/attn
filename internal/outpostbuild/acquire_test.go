package outpostbuild_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/victorarias/attn/internal/bridge"
	"github.com/victorarias/attn/internal/outpostbuild"
)

func TestOutpostsUpdatingAtOnceShareOneBuild(t *testing.T) {
	var served atomic.Int32
	firstHeld := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := served.Add(1)
		if n == 1 {
			close(firstHeld)
			<-release
		}
		fmt.Fprintf(w, "%s download %d", r.URL.Path, n)
	}))
	defer server.Close()
	req := outpostbuild.Request{
		Build:         bridge.Build{Version: "0.15.0", SourceFingerprint: "git:abc", ProtocolVersion: "p"},
		Channel:       "release",
		ReleaseSource: server.URL,
		Cache:         t.TempDir(),
		Platform:      "linux/amd64",
	}

	var wait sync.WaitGroup
	results := make([]outpostbuild.Binaries, 2)
	errs := make([]error, 2)
	secondDownloading := make(chan struct{})
	wait.Add(2)
	go func() {
		defer wait.Done()
		results[0], errs[0] = outpostbuild.Acquire(context.Background(), req, func(string) {})
	}()
	<-firstHeld
	go func() {
		defer wait.Done()
		results[1], errs[1] = outpostbuild.Acquire(context.Background(), req, func(phase string) {
			if phase == "downloading" {
				close(secondDownloading)
			}
		})
	}()
	<-secondDownloading
	close(release)
	wait.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("acquire %d: %v", i, err)
		}
	}
	if got := served.Load(); got != 3 {
		t.Errorf("release server served %d files, want the 3 binaries once", got)
	}
	first := readAttnd(t, results[0])
	if second := readAttnd(t, results[1]); first != second {
		t.Errorf("outposts got different attnd builds:\n%q\n%q", first, second)
	}
}

func readAttnd(t *testing.T, binaries outpostbuild.Binaries) string {
	t.Helper()
	data, err := os.ReadFile(binaries.Attnd)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
