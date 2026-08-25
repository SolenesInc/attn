package jobs

import (
	"os"
	"testing"

	"github.com/victorarias/attn/internal/testinv"
)

// Preconditions rather than outcomes: no assertion says the queue was ever in
// the situation each is about. See internal/testinv.
var (
	sawJobWithheldByItsSchedule = testinv.Sometimes("dispatch withholds a job whose scheduled time has not arrived")

	sawTriggerLandOnARunningJob = testinv.Sometimes("a coalescing trigger lands on a job that is already running")
)

func TestMain(m *testing.M) { os.Exit(testinv.Run(m)) }
