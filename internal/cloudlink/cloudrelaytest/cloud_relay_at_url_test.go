package cloudrelaytest

import (
	"os"
	"testing"
)

func TestCloudRelayAtURLHonorsTheContract(t *testing.T) {
	url := os.Getenv("ATTN_CLOUD_RELAY_URL")
	if url == "" {
		t.Skip("ATTN_CLOUD_RELAY_URL names no Cloud Relay; attn's own stand-in runs this suite in internal/testworld")
	}
	Run(t, url)
}
