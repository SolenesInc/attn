package order

import (
	"fmt"
	"testing"
)

func TestLogsAroundSubtests(t *testing.T) {
	t.Log("before the first subtest")
	t.Run("first", func(t *testing.T) { t.Error("first failed") })
	t.Log("between subtests")
	t.Run("second", func(t *testing.T) {
		t.Run("deep", func(t *testing.T) { t.Error("deep failed") })
		t.Log("after deep")
	})
	t.Error("parent failed")
}

func TestPrintsAndPasses(t *testing.T) {
	fmt.Println("stdout of a passing test")
	t.Log("log of a passing test")
}
