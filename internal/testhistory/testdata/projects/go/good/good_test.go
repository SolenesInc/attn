package good

import (
	"os"
	"testing"
	"time"
)

func TestPasses(t *testing.T) { t.Log("fine") }

func TestFails(t *testing.T) { t.Fatal("broken on purpose") }

func TestSkips(t *testing.T) { t.Skip("needs ClickHouse") }

func TestCases(t *testing.T) {
	t.Run("passes", func(t *testing.T) {})
	t.Run("fails", func(t *testing.T) { t.Error("case broke") })
	t.Run("skips", func(t *testing.T) { t.Skip("not here") })
	t.Run("nested", func(t *testing.T) { t.Run("deep", func(t *testing.T) {}) })
}

func TestSleeps(t *testing.T) {
	if os.Getenv("FIXTURE_SLEEP") == "" {
		t.Skip("set FIXTURE_SLEEP")
	}
	time.Sleep(30 * time.Second)
}
