package testworld_test

import (
	"testing"

	"github.com/victorarias/attn/internal/cloudlink/cloudrelaytest"
	"github.com/victorarias/attn/internal/testworld"
)

func TestTheStandInCloudRelayHonorsTheContract(t *testing.T) {
	t.Setenv("ATTN_TEST_CLOUD_URL", "")
	cloudrelaytest.Run(t, testworld.StartCloud(t).URL)
}
