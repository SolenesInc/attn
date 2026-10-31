package mainfail

import (
	"fmt"
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	fmt.Println("setup failed")
	os.Exit(3)
}

func TestNeverRuns(t *testing.T) {}
