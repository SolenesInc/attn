package daemon

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/github"
	"github.com/victorarias/attn/internal/jobs"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessionmonitor"
	"github.com/victorarias/attn/internal/stepclock"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/testworld"
)

func NewForTesting(socketPath string) *Daemon {
	dataRoot := filepath.Dir(socketPath)
	pidPath := filepath.Join(dataRoot, "attn.pid")
	d := &Daemon{
		recentFileStat:      os.Stat,
		noAgentSockets:      true,
		notebookSyncWatch:   true,
		rootWatch:           openRootWatch,
		clock:               stepclock.Process(),
		recentFilePruneGate: make(chan struct{}, 1),
		ptyRecovered:        make(chan struct{}),
		socketPath:          socketPath,
		pidPath:             pidPath,
		dataRoot:            dataRoot,
		store:               store.New(),
		wsHub:               newWSHub(),
		startedAt:           time.Now(),
		desktopTiles:        newDesktopTileDelivery(),
		tileDocuments:       newTileDocumentWatch(),
		logger:              nil,
		ghRegistry:          github.NewClientRegistry(),
		ptyBackend:          testworld.NewInProcessPTY(nil),

		pendingInitialWS: make(map[*wsClient]struct{}),
		startedCh:        make(chan struct{}),

		plugins:             newPluginRegistry(),
		pluginDir:           pluginDirForSocket(socketPath),
		spawnLocks:          make(map[protocol.SessionID]*spawnLock),
		jobQueue:            jobs.New(jobs.Options{}),
		jobQueueRoleChanged: make(chan struct{}, 1),
	}
	d.wireGitExecution(productionGitExecutorConfig)
	d.ensureEventBus()
	d.loginEnv = newLoginEnv(&d.life, d.logf)
	d.pluginStartEnded = make(chan struct{})
	d.codeNav = d.newCodeNavigation()
	return d
}

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
	if os.Getenv("ATTN_PLUGIN_HELPER") == "1" {
		os.Exit(m.Run())
	}
	sessionmonitor.SubmitDelay = 0
	os.Exit(testworld.Main(m,
		"ATTN_PTY_BACKEND=",
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
