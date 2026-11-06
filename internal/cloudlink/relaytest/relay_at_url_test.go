package relaytest

import (
	"os"
	"testing"
)

func TestRelayAtURLHonorsTheContract(t *testing.T) {
	url := os.Getenv("ATTN_RELAY_URL")
	if url == "" {
		t.Skip("ATTN_RELAY_URL names no relay; attn's own stand-in runs this suite in internal/testworld")
	}
	Run(t, url)
}
