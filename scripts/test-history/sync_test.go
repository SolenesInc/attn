package main

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

const (
	serviceToken       = "sync-test-token"
	checkoutRepository = "acme/attn"
)

type storedReceipt struct {
	repo, id string
	body     []byte
}

type fakeHistoryService struct {
	mu       sync.Mutex
	receipts []storedReceipt
	puts     int
	down     bool
	refuse   map[string]bool
	server   *httptest.Server
}

func newFakeHistoryService(t *testing.T) *fakeHistoryService {
	s := &fakeHistoryService{refuse: map[string]bool{}}
	s.server = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.server.Close)
	return s
}

func (s *fakeHistoryService) add(repo, id string, body []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.receipts = append(s.receipts, storedReceipt{repo, id, bytes.TrimSpace(body)})
}

func (s *fakeHistoryService) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.down {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
		return
	}
	if r.Header.Get("Authorization") != "Bearer "+serviceToken {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	switch {
	case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/v1/receipts/"):
		s.puts++
		rest := strings.TrimPrefix(r.URL.Path, "/v1/receipts/")
		repo, id := rest[:strings.LastIndex(rest, "/")], rest[strings.LastIndex(rest, "/")+1:]
		body, _ := io.ReadAll(r.Body)
		body = bytes.TrimSpace(body)
		if s.refuse[id] {
			http.Error(w, `{"error":"conflict"}`, http.StatusConflict)
			return
		}
		for _, existing := range s.receipts {
			if existing.repo == repo && existing.id == id {
				if bytes.Equal(existing.body, body) {
					w.WriteHeader(http.StatusOK)
				} else {
					w.WriteHeader(http.StatusConflict)
				}
				return
			}
		}
		s.receipts = append(s.receipts, storedReceipt{repo, id, body})
		w.WriteHeader(http.StatusCreated)
	case r.Method == http.MethodGet && r.URL.Path == "/v1/receipts":
		var after int
		fmt.Sscan(r.URL.Query().Get("after"), &after)
		repo := r.URL.Query().Get("repo")
		var page bytes.Buffer
		page.WriteString(`{"receipts":[`)
		next, written, more := after, 0, false
		for i := after; i < len(s.receipts); i++ {
			if s.receipts[i].repo != repo {
				next = i + 1
				continue
			}
			if written == 2 {
				more = true
				break
			}
			if written > 0 {
				page.WriteByte(',')
			}
			fmt.Fprintf(&page, `{"cursor":%d,"id":%q,"receipt":%s}`, i+1, s.receipts[i].id, s.receipts[i].body)
			written++
			next = i + 1
		}
		fmt.Fprintf(&page, `],"next":%d,"more":%t}`, next, more)
		w.Write(page.Bytes())
	default:
		http.NotFound(w, r)
	}
}

func (s *fakeHistoryService) putCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.puts
}

func (s *fakeHistoryService) stored(id string) []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.receipts {
		if r.id == id {
			return r.body
		}
	}
	return nil
}

type syncWorld struct {
	t       *testing.T
	spool   string
	config  string
	service *fakeHistoryService
}

func newSyncWorld(t *testing.T) *syncWorld {
	w := &syncWorld{t: t, spool: t.TempDir(), config: t.TempDir(), service: newFakeHistoryService(t)}
	if err := os.MkdirAll(filepath.Join(w.config, "attn-test-history"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(w.config, "attn-test-history", "token"), []byte(serviceToken+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return w
}

func (w *syncWorld) captureReceipt() string {
	w.t.Helper()
	before := map[string]bool{}
	for _, r := range sealedReceipts(w.t, w.spool) {
		before[r.InvocationID] = true
	}
	cmd := historyCommand(w.t, w.spool, fixtureModule(w.t), "capture", "go", "--", "-count=1", "-run", "TestPasses", "./good")
	cmd.Env = append(cmd.Env, "GITHUB_REPOSITORY="+checkoutRepository)
	if out, err := cmd.CombinedOutput(); err != nil {
		w.t.Fatalf("capture: %v\n%s", err, out)
	}
	for _, r := range sealedReceipts(w.t, w.spool) {
		if !before[r.InvocationID] {
			return r.InvocationID
		}
	}
	w.t.Fatal("capture sealed no receipt")
	return ""
}

func (w *syncWorld) sync() (stdout, stderr string, code int) {
	w.t.Helper()
	cmd := historyCommand(w.t, w.spool, fixtureModule(w.t), "sync")
	cmd.Env = append(cmd.Env, "ATTN_TEST_HISTORY_URL="+w.service.server.URL, "XDG_CONFIG_HOME="+w.config, "GITHUB_REPOSITORY="+checkoutRepository)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	code = exitCode(cmd.Run())
	return out.String(), errOut.String(), code
}

func (w *syncWorld) receiptRepo(id string) string {
	w.t.Helper()
	for _, r := range sealedReceipts(w.t, w.spool) {
		if r.InvocationID == id {
			return r.Repo
		}
	}
	w.t.Fatalf("no sealed receipt %s", id)
	return ""
}

func (w *syncWorld) shared(id string) []byte {
	data, err := os.ReadFile(filepath.Join(w.spool, "shared", id+".json"))
	if err != nil {
		return nil
	}
	return data
}

func TestSyncUploadsEachReceiptOnceAndDownloadsTheSharedHistory(t *testing.T) {
	w := newSyncWorld(t)
	mine := w.captureReceipt()
	repo := w.receiptRepo(mine)
	for i := range 3 {
		w.service.add(repo, fmt.Sprintf("ci-%d", i), []byte(fmt.Sprintf(`{"invocation_id":"ci-%d"}`, i)))
	}
	w.service.add("github.com/other/repo", "elsewhere", []byte(`{"invocation_id":"elsewhere"}`))

	out, errOut, code := w.sync()
	if code != 0 || !strings.Contains(out, "uploaded 1 (0 already stored), downloaded 4; 0 receipts wait to upload") {
		t.Fatalf("first sync exited %d\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	sealed, _ := os.ReadFile(filepath.Join(w.spool, "receipts", mine+".json"))
	if !bytes.Equal(w.service.stored(mine), bytes.TrimSpace(sealed)) {
		t.Fatal("the service did not receive the sealed receipt's bytes")
	}
	for _, id := range []string{"ci-0", "ci-1", "ci-2", mine} {
		if w.shared(id) == nil {
			t.Errorf("shared history is missing %s", id)
		}
	}
	if w.shared("elsewhere") != nil {
		t.Error("downloaded another repository's receipt")
	}

	puts := w.service.putCount()
	out, errOut, code = w.sync()
	if code != 0 || !strings.Contains(out, "uploaded 0 (0 already stored), downloaded 0") || w.service.putCount() != puts {
		t.Fatalf("second sync exited %d, %d new uploads\nstdout: %s\nstderr: %s", code, w.service.putCount()-puts, out, errOut)
	}
	w.service.add(repo, "ci-3", []byte(`{"invocation_id":"ci-3"}`))
	if out, _, _ = w.sync(); !strings.Contains(out, "downloaded 1;") || w.shared("ci-3") == nil {
		t.Fatalf("third sync: %s", out)
	}
}

func TestAFailedSyncKeepsReceiptsAndSaysWhenSyncLastSucceeded(t *testing.T) {
	w := newSyncWorld(t)
	w.captureReceipt()
	if _, errOut, code := w.sync(); code != 0 {
		t.Fatalf("first sync: %s", errOut)
	}
	later := w.captureReceipt()
	w.service.mu.Lock()
	w.service.down = true
	w.service.mu.Unlock()

	_, errOut, code := w.sync()
	if code != 1 || !strings.Contains(errOut, "sync failed") || !strings.Contains(errOut, "1 receipts wait to upload; last successful sync ") {
		t.Fatalf("sync against an unavailable service exited %d\nstderr: %s", code, errOut)
	}
	if _, err := os.Stat(filepath.Join(w.spool, "receipts", later+".json")); err != nil {
		t.Fatalf("failed sync lost the receipt: %v", err)
	}

	w.service.mu.Lock()
	w.service.down = false
	w.service.mu.Unlock()
	if out, errOut, code := w.sync(); code != 0 || !strings.Contains(out, "uploaded 1 ") || w.service.stored(later) == nil {
		t.Fatalf("sync after recovery exited %d\nstdout: %s\nstderr: %s", code, out, errOut)
	}
}

func TestARefusedReceiptIsReportedAndOfferedAgain(t *testing.T) {
	w := newSyncWorld(t)
	id := w.captureReceipt()
	w.service.mu.Lock()
	w.service.refuse[id] = true
	w.service.mu.Unlock()
	_, errOut, code := w.sync()
	if code != 1 || !strings.Contains(errOut, "refused: "+id+": 409") {
		t.Fatalf("refused sync exited %d\nstderr: %s", code, errOut)
	}
	w.service.mu.Lock()
	w.service.refuse[id] = false
	w.service.mu.Unlock()
	if out, errOut, code := w.sync(); code != 0 || !strings.Contains(out, "uploaded 1 ") {
		t.Fatalf("sync after the refusal cleared exited %d\nstdout: %s\nstderr: %s", code, out, errOut)
	}
}

func TestSyncWithoutATokenNamesWhereItLooked(t *testing.T) {
	w := newSyncWorld(t)
	w.captureReceipt()
	w.config = t.TempDir()
	_, errOut, code := w.sync()
	want := filepath.Join(w.config, "attn-test-history", "token")
	if code != 1 || !strings.Contains(errOut, "no service token at "+want) || !strings.Contains(errOut, "1 receipts wait to upload; no sync has succeeded yet") {
		t.Fatalf("sync without a token exited %d\nstderr: %s", code, errOut)
	}
}

func TestAReceiptWithoutARepositoryDoesNotStopTheSync(t *testing.T) {
	w := newSyncWorld(t)
	module := t.TempDir()
	for name, content := range map[string]string{
		"go.mod":          "module outside\n\ngo 1.22\n",
		"outside_test.go": "package outside\n\nimport \"testing\"\n\nfunc TestPasses(t *testing.T) {}\n",
	} {
		if err := os.WriteFile(filepath.Join(module, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cmd := historyCommand(t, w.spool, module, "capture", "go", "--", "-count=1", ".")
	cmd.Env = append(cmd.Env, "GITHUB_REPOSITORY=", "GIT_CEILING_DIRECTORIES="+filepath.Dir(module))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("capture outside a checkout: %v\n%s", err, out)
	}
	orphan := sealedReceipts(t, w.spool)[0]
	if orphan.Repo != "unknown" {
		t.Fatalf("receipt captured outside a checkout has repo %q", orphan.Repo)
	}
	mine := w.captureReceipt()
	w.service.add(w.receiptRepo(mine), "ci-0", []byte(`{"invocation_id":"ci-0"}`))

	out, errOut, code := w.sync()
	if code != 1 || !strings.Contains(errOut, "refused: "+orphan.InvocationID+`: repo "unknown"`) {
		t.Fatalf("sync exited %d\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	if w.service.stored(mine) == nil || w.shared("ci-0") == nil {
		t.Fatalf("the unknown-repo receipt stopped the rest of the sync\nstdout: %s\nstderr: %s", out, errOut)
	}
}
