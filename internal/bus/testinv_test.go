package bus

import (
	"os"
	"testing"

	"github.com/victorarias/attn/internal/testinv"
)

var sawMultiEventBatch = testinv.Sometimes("the log is read forward into a batch holding more than one event")

func TestMain(m *testing.M) { os.Exit(testinv.Run(m)) }
