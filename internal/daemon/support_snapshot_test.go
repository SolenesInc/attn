package daemon

import (
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

func BenchmarkSupportInputTrace(b *testing.B) {
	d := &Daemon{}
	message := &protocol.PtyInputMessage{
		ID: "runtime-1", Data: "x", Source: protocol.Ptr("user"), TraceID: protocol.Ptr("trace-1"),
	}
	receivedAt := time.UnixMilli(1)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		d.recordSupportInputTrace(message, receivedAt, 50*time.Microsecond, nil)
	}
}

func BenchmarkSupportInputTraceAbsent(b *testing.B) {
	d := &Daemon{}
	message := &protocol.PtyInputMessage{ID: "runtime-1", Data: "x", Source: protocol.Ptr("user")}
	receivedAt := time.UnixMilli(1)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		d.recordSupportInputTrace(message, receivedAt, 50*time.Microsecond, nil)
	}
}
