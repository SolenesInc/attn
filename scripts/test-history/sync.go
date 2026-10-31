package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/testhistory"
)

var serviceRepo = regexp.MustCompile(`^[a-z0-9.-]+/[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

const (
	defaultHistoryURL = "https://attn-test-history.int.caketroll.cloud"
	syncRequestBudget = 2 * time.Minute
)

type syncState struct {
	LastSuccess *time.Time `json:"last_success,omitempty"`
	LastAttempt time.Time  `json:"last_attempt"`
	LastError   string     `json:"last_error,omitempty"`
	Cursor      int64      `json:"cursor"`
}

type historyService struct {
	base   string
	token  string
	client *http.Client
}

type syncReport struct {
	uploaded, alreadyStored, downloaded int
	refused                             []string
}

func syncHistory(args []string) int {
	if len(args) != 0 {
		fmt.Fprint(os.Stderr, usage)
		return 2
	}
	dir, err := spoolDir()
	if err != nil {
		warn("%v", err)
		return 1
	}
	spool := testhistory.Spool{Dir: dir}
	if err := os.MkdirAll(spool.Dir, 0o755); err != nil {
		warn("%v", err)
		return 1
	}
	state, err := readSyncState(spool)
	if err != nil {
		warn("%v", err)
		return 1
	}
	report, err := runSync(spool, &state)
	state.LastAttempt = time.Now().UTC()
	state.LastError = ""
	if err != nil {
		state.LastError = err.Error()
	} else {
		state.LastSuccess = &state.LastAttempt
	}
	if werr := writeSyncState(spool, state); werr != nil && err == nil {
		err = werr
	}
	pending, _ := pendingUploads(spool)
	for _, refusal := range report.refused {
		warn("refused: %s", refusal)
	}
	if err != nil {
		warn("sync failed: %v", err)
		warn("%d receipts wait to upload; %s", len(pending), lastSyncSummary(state))
		return 1
	}
	fmt.Printf("test-history: uploaded %d (%d already stored), downloaded %d; %d receipts wait to upload\n",
		report.uploaded, report.alreadyStored, report.downloaded, len(pending))
	if len(report.refused) > 0 {
		return 1
	}
	return 0
}

func lastSyncSummary(state syncState) string {
	if state.LastSuccess == nil {
		return "no sync has succeeded yet"
	}
	return fmt.Sprintf("last successful sync %s (%s ago)", state.LastSuccess.Format(time.RFC3339), time.Since(*state.LastSuccess).Round(time.Minute))
}

func runSync(spool testhistory.Spool, state *syncState) (syncReport, error) {
	var report syncReport
	service, err := newHistoryService()
	if err != nil {
		return report, err
	}
	pending, err := pendingUploads(spool)
	if err != nil {
		return report, err
	}
	for _, id := range pending {
		body, err := os.ReadFile(spool.ReceiptPath(id))
		if err != nil {
			return report, err
		}
		var envelope struct {
			Repo string `json:"repo"`
		}
		if err := json.Unmarshal(body, &envelope); err != nil {
			report.refused = append(report.refused, fmt.Sprintf("%s: %v", id, err))
			continue
		}
		if !serviceRepo.MatchString(envelope.Repo) {
			report.refused = append(report.refused, fmt.Sprintf("%s: repo %q is not host/owner/name; it was captured outside a checkout with an origin remote", id, envelope.Repo))
			continue
		}
		stored, refusal, err := service.put(envelope.Repo, id, body)
		if err != nil {
			return report, err
		}
		if refusal != "" {
			report.refused = append(report.refused, fmt.Sprintf("%s: %s", id, refusal))
			continue
		}
		if err := markUploaded(spool, id); err != nil {
			return report, err
		}
		if stored {
			report.uploaded++
		} else {
			report.alreadyStored++
		}
	}
	return report, service.download(spool, repository(), state, &report)
}

func newHistoryService() (historyService, error) {
	base := strings.TrimSuffix(os.Getenv("ATTN_TEST_HISTORY_URL"), "/")
	if base == "" {
		base = defaultHistoryURL
	}
	path, err := tokenPath()
	if err != nil {
		return historyService{}, err
	}
	token, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return historyService{}, fmt.Errorf("no service token at %s; docs/internal/test-history.md says how to provision it", path)
	}
	if err != nil {
		return historyService{}, err
	}
	return historyService{base: base, token: strings.TrimSpace(string(token)), client: &http.Client{Timeout: syncRequestBudget}}, nil
}

func tokenPath() (string, error) {
	config := os.Getenv("XDG_CONFIG_HOME")
	if config == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		config = filepath.Join(home, ".config")
	}
	return filepath.Join(config, "attn-test-history", "token"), nil
}

func pendingUploads(spool testhistory.Spool) ([]string, error) {
	paths, err := filepath.Glob(filepath.Join(spool.ReceiptsDir(), "*.json"))
	if err != nil {
		return nil, err
	}
	var pending []string
	for _, path := range paths {
		id := strings.TrimSuffix(filepath.Base(path), ".json")
		if _, err := os.Stat(filepath.Join(spool.UploadedDir(), id)); errors.Is(err, fs.ErrNotExist) {
			pending = append(pending, id)
		} else if err != nil {
			return nil, err
		}
	}
	return pending, nil
}

func markUploaded(spool testhistory.Spool, id string) error {
	if err := os.MkdirAll(spool.UploadedDir(), 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(spool.UploadedDir(), id), nil, 0o644)
}

func (s historyService) request(method, path string, body []byte) (int, []byte, error) {
	req, err := http.NewRequest(method, s.base+path, bytes.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+s.token)
	resp, err := s.client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	reply, err := io.ReadAll(resp.Body)
	return resp.StatusCode, reply, err
}

func (s historyService) put(repo, id string, body []byte) (stored bool, refusal string, err error) {
	status, reply, err := s.request(http.MethodPut, "/v1/receipts/"+repo+"/"+url.PathEscape(id), body)
	if err != nil {
		return false, "", err
	}
	switch {
	case status == http.StatusCreated:
		return true, "", nil
	case status == http.StatusOK:
		return false, "", nil
	case status == http.StatusBadRequest || status == http.StatusConflict || status == http.StatusUnprocessableEntity || status == http.StatusRequestEntityTooLarge:
		return false, fmt.Sprintf("%d %s", status, strings.TrimSpace(string(reply))), nil
	default:
		return false, "", fmt.Errorf("PUT %s: %d %s", id, status, strings.TrimSpace(string(reply)))
	}
}

func (s historyService) download(spool testhistory.Spool, repo string, state *syncState, report *syncReport) error {
	if err := os.MkdirAll(spool.SharedDir(), 0o755); err != nil {
		return err
	}
	for {
		status, reply, err := s.request(http.MethodGet, fmt.Sprintf("/v1/receipts?repo=%s&after=%d", url.QueryEscape(repo), state.Cursor), nil)
		if err != nil {
			return err
		}
		if status != http.StatusOK {
			return fmt.Errorf("GET receipts after %d: %d %s", state.Cursor, status, strings.TrimSpace(string(reply)))
		}
		var page struct {
			Receipts []struct {
				ID      string          `json:"id"`
				Receipt json.RawMessage `json:"receipt"`
			} `json:"receipts"`
			Next int64 `json:"next"`
			More bool  `json:"more"`
		}
		if err := json.Unmarshal(reply, &page); err != nil {
			return fmt.Errorf("GET receipts after %d: %w", state.Cursor, err)
		}
		for _, r := range page.Receipts {
			if err := writeAtomically(filepath.Join(spool.SharedDir(), filepath.Base(r.ID)+".json"), r.Receipt); err != nil {
				return err
			}
			report.downloaded++
		}
		state.Cursor = page.Next
		if err := writeSyncState(spool, *state); err != nil {
			return err
		}
		if !page.More {
			return nil
		}
	}
}

func readSyncState(spool testhistory.Spool) (syncState, error) {
	var state syncState
	data, err := os.ReadFile(spool.SyncStatePath())
	if errors.Is(err, fs.ErrNotExist) {
		return state, nil
	}
	if err != nil {
		return state, err
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return state, fmt.Errorf("%s: %w", spool.SyncStatePath(), err)
	}
	return state, nil
}

func writeSyncState(spool testhistory.Spool, state syncState) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomically(spool.SyncStatePath(), data)
}

func writeAtomically(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), path)
}
