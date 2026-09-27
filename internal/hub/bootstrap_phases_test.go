package hub

import (
	"testing"
	"time"
)

func TestRemoteReadyBudgetCoversTheBinaryItCarries(t *testing.T) {
	const slowLinkBitsPerSecond = 5_000_000
	const attnBinaryBytes = 58_934_608

	binaryTransfer := time.Duration(float64(attnBinaryBytes*8) / slowLinkBitsPerSecond * float64(time.Second))
	if want := binaryTransfer + remoteDaemonReadyTimeout; remoteReadyBudget <= want {
		t.Errorf("remoteReadyBudget %v does not cover a %d-byte binary at 5 Mbit/s plus the %v readiness wait (%v)",
			remoteReadyBudget, attnBinaryBytes, remoteDaemonReadyTimeout, want)
	}
}
