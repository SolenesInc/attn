package daemon

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessionmonitor"
	"github.com/victorarias/attn/internal/testworld"
)

func newDaemonForTest(t *testing.T) *Daemon {
	t.Helper()
	return NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
}

func mdAnchor(startLine, endLine, start int, exact string) *protocol.MarkdownAnnotationAnchor {
	return &protocol.MarkdownAnnotationAnchor{
		BlockID:   "b",
		StartLine: startLine,
		EndLine:   endLine,
		Start:     start,
		End:       start + len(exact),
		Exact:     exact,
	}
}

func fileAnnotationSource(path string) annotationDocumentSource {
	return annotationDocumentSource{kind: annotationSourceFile, path: path}
}

var shippedSessionInputSubmitDelay = sessionmonitor.SubmitDelay

func TestMain(m *testing.M) {
	fakeagent.Main()
	if os.Getenv("ATTN_PLUGIN_HELPER") == "1" {
		os.Exit(m.Run())
	}
	sessionmonitor.SubmitDelay = 0
	os.Exit(testworld.Main(m,
		"ATTN_PTY_BACKEND=embedded",
		"ATTN_PTY_SKIP_STARTUP_PROBE=1",
		"ATTN_CLIENT_TOKEN=daemon-test-client-token",
		"ATTN_MOCK_GH_URL=http://127.0.0.1:1",
		"ATTN_MOCK_GH_TOKEN=",
	))
}

func shortTempDir(t *testing.T) string {
	t.Helper()
	base := "/tmp"
	if _, err := os.Stat(base); err != nil {
		base = ""
	}
	dir, err := os.MkdirTemp(base, "attn-")
	if err != nil {
		t.Fatalf("MkdirTemp() error: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}
