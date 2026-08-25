package pty

import (
	"os"
	"testing"
)

func TestFailedResizeRemainsRetryable(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	t.Cleanup(func() {
		_ = reader.Close()
		_ = writer.Close()
	})

	session := &Session{cols: 80, rows: 24, ptmx: reader}
	for attempt := 1; attempt <= 2; attempt++ {
		changed, resizeErr := session.resize(100, 30, 0, 0)
		if resizeErr == nil {
			t.Fatalf("attempt %d unexpectedly resized a pipe", attempt)
		}
		if !changed {
			t.Fatalf("attempt %d suppressed geometry whose prior ioctl failed", attempt)
		}
	}
}
