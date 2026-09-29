package testworld

import (
	"fmt"
	"os"
)

// Forced ready/CLI stalls dumped up to 66,856 bytes on 2026-09-29 (42 goroutines).
// Leave over 15x room, and report any omitted bytes with the limit and original size.
const diagnosticBytes = 1 << 20

func boundedDiagnostic(text string) string {
	if len(text) <= diagnosticBytes {
		return text
	}
	half := diagnosticBytes / 2
	return text[:half] + fmt.Sprintf("\n[diagnostic_bytes=%d, asked for %d; omitted %d middle bytes]\n", diagnosticBytes, len(text), len(text)-diagnosticBytes) + text[len(text)-half:]
}

func (s *Stack) logPressure() {
	for _, resource := range []string{"cpu", "io"} {
		path := "/proc/pressure/" + resource
		if data, err := os.ReadFile(path); err == nil {
			s.T.Logf("%s:\n%s", path, boundedDiagnostic(string(data)))
		}
	}
}
