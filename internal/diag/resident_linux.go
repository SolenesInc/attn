package diag

import (
	"fmt"
	"os"
)

func residentBytes() uint64 {
	statm, err := os.ReadFile("/proc/self/statm")
	if err != nil {
		return 0
	}
	var size, resident uint64
	if _, err := fmt.Sscan(string(statm), &size, &resident); err != nil {
		return 0
	}
	return resident * uint64(os.Getpagesize())
}
