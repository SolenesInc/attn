package testworld_test

import (
	"testing"

	"github.com/victorarias/attn/internal/cloudlink/relaytest"
	"github.com/victorarias/attn/internal/testworld"
)

func TestTheStandInRelayHonorsTheContract(t *testing.T) {
	t.Setenv("ATTN_TEST_CLOUD_URL", "")
	relaytest.Run(t, testworld.StartCloud(t).URL)
}
