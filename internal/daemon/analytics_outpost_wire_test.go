package daemon_test

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/victorarias/attn/internal/analytics"
)

func TestTheHomeReadsNoRepoTraitsFromAnOutpostSessionsFolder(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app := w.App()
		setAnalyticsEnabled(app, "true")
		folder := w.Path("shop")
		gitRepo(t, folder)
		if err := os.WriteFile(filepath.Join(folder, "AGENTS.md"), []byte("be kind\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		const onOutpost = "on-outpost"
		if err := w.InjectSessionOn("far", onOutpost, "far work", folder, "claude"); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()

		secret, err := hex.DecodeString(strings.TrimSpace(readSecret(t, filepath.Join(w.Dir, "analytics-secret"))))
		if err != nil {
			t.Fatal(err)
		}
		subject := analytics.HashSubject(secret, onOutpost)
		var mirrored int
		for _, record := range mirroredRecords(t, app, "") {
			if record.Subject != subject || !strings.HasPrefix(record.Name, "session.") {
				continue
			}
			mirrored++
			var derived analytics.Derived
			if err := json.Unmarshal(record.Props, &derived); err != nil {
				t.Fatalf("%s props %s: %v", record.Name, record.Props, err)
			}
			if derived.Repo != nil {
				t.Errorf("the home's %s for the outpost session carries repo traits %+v read from the folder on its own disk; want none, since that folder is on the outpost", record.Name, *derived.Repo)
			}
		}
		if mirrored == 0 {
			t.Fatal("the home mirrored no session facts for the outpost session")
		}
	})
}
