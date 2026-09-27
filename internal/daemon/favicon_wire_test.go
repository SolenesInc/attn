package daemon_test

import (
	"io"
	"net/http"
	"testing"
)

func TestTheFaviconIsAnUncachedEmptyAnswerRatherThanA404(t *testing.T) {
	w := newWorld(t)
	resp, err := http.Get("http://" + w.WSAddr + "/favicon.ico")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusNoContent || len(body) != 0 || resp.Header.Get("Cache-Control") != "no-store, max-age=0" {
		t.Fatalf("GET /favicon.ico = %d with %d bytes and Cache-Control %q, want 204, empty, no-store, max-age=0",
			resp.StatusCode, len(body), resp.Header.Get("Cache-Control"))
	}
}
