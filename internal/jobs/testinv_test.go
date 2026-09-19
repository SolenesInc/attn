package jobs

import (
	"os"
	"testing"

	"github.com/victorarias/attn/internal/testinv"
)

var (
	sawJobWithheldByItsSchedule = testinv.Sometimes("dispatch withholds a job whose scheduled time has not arrived")

	sawTriggerLandOnARunningJob = testinv.Sometimes("a coalescing trigger lands on a job that is already running")
)

func TestMain(m *testing.M) { os.Exit(testinv.Run(m)) }
