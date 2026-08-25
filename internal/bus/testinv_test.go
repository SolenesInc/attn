package bus

import (
	"os"
	"testing"

	"github.com/victorarias/attn/internal/testinv"
)

var (
	sawMultiEventBatch = testinv.Sometimes("the log is read forward into a batch holding more than one event")

	sawRedelivery = testinv.Sometimes("a consumer handler is given an event it has already been given")
)

func TestMain(m *testing.M) { os.Exit(testinv.Run(m)) }
