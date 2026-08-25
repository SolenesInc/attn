package daemon

import (
	"bytes"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
)

// sink keeps the compiler from optimizing the encode away across iterations.
var sink outboundMessage

func benchEncodePtyOutput(b *testing.B, chunk []byte, legacyClient bool) {
	client := &wsClient{}
	if legacyClient {
		client.setIdentity("test", "v", []string{protocol.CapabilityWorkspaceSessions})
	} else {
		client.setIdentity("test", "v", []string{
			protocol.CapabilityWorkspaceSessions,
			protocol.CapabilityBinaryPtyOutput,
		})
	}
	event := ptybackend.OutputEvent{
		Kind: ptybackend.OutputEventKindOutput,
		Data: chunk,
		Seq:  42,
	}

	b.ReportAllocs()
	b.SetBytes(int64(len(chunk)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		outbound, err := encodePtyOutputMessage(client, "sess-1234abcd", event)
		if err != nil {
			b.Fatal(err)
		}
		sink = outbound
	}
}

func BenchmarkEncodePtyOutput_JSON_64B(b *testing.B) {
	benchEncodePtyOutput(b, bytes.Repeat([]byte("abcd"), 16), true)
}
func BenchmarkEncodePtyOutput_JSON_1KB(b *testing.B) {
	benchEncodePtyOutput(b, bytes.Repeat([]byte("abcd"), 256), true)
}
func BenchmarkEncodePtyOutput_JSON_4KB(b *testing.B) {
	benchEncodePtyOutput(b, bytes.Repeat([]byte("abcd"), 1024), true)
}

func BenchmarkEncodePtyOutput_Binary_64B(b *testing.B) {
	benchEncodePtyOutput(b, bytes.Repeat([]byte("abcd"), 16), false)
}
func BenchmarkEncodePtyOutput_Binary_1KB(b *testing.B) {
	benchEncodePtyOutput(b, bytes.Repeat([]byte("abcd"), 256), false)
}
func BenchmarkEncodePtyOutput_Binary_4KB(b *testing.B) {
	benchEncodePtyOutput(b, bytes.Repeat([]byte("abcd"), 1024), false)
}
