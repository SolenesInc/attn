package github

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

func TestNewClient_UsesProvidedToken(t *testing.T) {
	client, err := NewClient("http://example.com", "test-token-from-env")
	if err != nil {
		t.Fatalf("NewClient error: %v", err)
	}

	if client.token != "test-token-from-env" {
		t.Errorf("token = %q, want %q", client.token, "test-token-from-env")
	}
}

func TestGitHTTPSAuthorizationHeader(t *testing.T) {
	client, err := NewClient("http://example.com", "secret-token")
	if err != nil {
		t.Fatal(err)
	}
	header := client.GitHTTPSAuthorizationHeader()
	if header == "" || contains(header, "secret-token") || header != GitHTTPSAuthorizationHeader("secret-token") {
		t.Fatalf("unexpected authorization header %q", header)
	}
}

func TestNewClient_DefaultsToGitHubAPI(t *testing.T) {
	// A real-looking token: "test-token" is blocked when targeting the real
	// GitHub API.
	client, err := NewClient("", "ghp_xxxxxxxxxxxx")
	if err != nil {
		t.Fatalf("NewClient error: %v", err)
	}

	if client.baseURL != "https://api.github.com" {
		t.Errorf("baseURL = %q, want %q", client.baseURL, "https://api.github.com")
	}
}

func TestNewClient_BlocksTestTokenWithRealAPI(t *testing.T) {
	_, err := NewClient("", "test-token")
	if err == nil {
		t.Fatal("NewClient should fail when test-token is used with real GitHub API")
	}

	if !contains(err.Error(), "refusing to use real GitHub API") {
		t.Errorf("Error message = %q, should mention refusing to use real API", err.Error())
	}
}

func TestNewClient_CustomBaseURL(t *testing.T) {
	client, err := NewClient("http://localhost:9999", "test-token")
	if err != nil {
		t.Fatalf("NewClient error: %v", err)
	}

	if client.baseURL != "http://localhost:9999" {
		t.Errorf("baseURL = %q, want %q", client.baseURL, "http://localhost:9999")
	}
}

func TestClient_doRequest_SetsHeaders(t *testing.T) {
	var capturedHeaders http.Header

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedHeaders = r.Header
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer server.Close()

	client, _ := NewClient(server.URL, "test-token-123")
	client.doRequest("GET", "/test", nil)

	if capturedHeaders.Get("Authorization") != "Bearer test-token-123" {
		t.Errorf("Authorization header = %q, want Bearer test-token-123", capturedHeaders.Get("Authorization"))
	}
	if capturedHeaders.Get("Accept") != "application/vnd.github+json" {
		t.Errorf("Accept header = %q, want application/vnd.github+json", capturedHeaders.Get("Accept"))
	}
	if capturedHeaders.Get("X-GitHub-Api-Version") != "2022-11-28" {
		t.Errorf("X-GitHub-Api-Version header missing or wrong")
	}
}

func TestFetchPullRequestSnapshotUsesOneReadOnlyRequest(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != http.MethodGet || r.URL.Path != "/repos/owner/repo/pulls/42" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if r.ContentLength > 0 {
			t.Fatalf("read-only request had body length %d", r.ContentLength)
		}
		_, _ = w.Write([]byte(`{
          "number":42,"html_url":"https://github.com/owner/repo/pull/42",
          "title":"Review me","body":"Provider data","draft":false,"state":"open",
          "user":{"login":"author"},
          "head":{"sha":"0123456789abcdef0123456789abcdef01234567","ref":"topic","repo":{"full_name":"fork/repo"}},
          "base":{"sha":"89abcdef0123456789abcdef0123456789abcdef","ref":"main","repo":{"full_name":"owner/repo"}}
        }`))
	}))
	defer server.Close()
	client, err := NewClient(server.URL, "test-token")
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := client.FetchPullRequestSnapshot("owner/repo", 42)
	if err != nil {
		t.Fatal(err)
	}
	if requests != 1 || snapshot.HeadRepository != "fork/repo" || snapshot.BaseRepository != "owner/repo" || snapshot.HeadSHA != "0123456789abcdef0123456789abcdef01234567" {
		t.Fatalf("requests=%d snapshot=%#v", requests, snapshot)
	}
}

func TestFetchPullRequestSnapshotSeparatesMergedFromClosed(t *testing.T) {
	tests := []struct {
		name      string
		payload   string
		wantState string
		wantMerge bool
	}{
		{"merged", `"state":"closed","merged":true`, "closed", true},
		{"closed unmerged", `"state":"closed","merged":false`, "closed", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(`{"number":42,` + tc.payload + `,
					"mergeable":true,"mergeable_state":"clean","head":{"sha":"abc123","ref":"topic"}}`))
			}))
			defer server.Close()
			client, err := NewClient(server.URL, "test-token")
			if err != nil {
				t.Fatal(err)
			}
			snapshot, err := client.FetchPullRequestSnapshot("owner/repo", 42)
			if err != nil {
				t.Fatal(err)
			}
			if snapshot.State != tc.wantState || snapshot.Merged != tc.wantMerge {
				t.Errorf("state = %q merged = %v, want %q and %v", snapshot.State, snapshot.Merged, tc.wantState, tc.wantMerge)
			}
			if snapshot.MergeableState != "clean" || snapshot.Mergeable == nil || !*snapshot.Mergeable {
				t.Errorf("mergeability = %v/%q, want a clean mergeable pull request", snapshot.Mergeable, snapshot.MergeableState)
			}
		})
	}
}

func TestCIStatusFromMergeableState(t *testing.T) {
	tests := map[string]string{
		"clean": "success", "blocked": "pending", "unstable": "pending",
		"dirty": "failure", "unknown": "none", "": "none",
	}
	for mergeableState, want := range tests {
		if got := CIStatusFromMergeableState(mergeableState); got != want {
			t.Errorf("CIStatusFromMergeableState(%q) = %q, want %q", mergeableState, got, want)
		}
	}
}

func TestFetchPullRequestReviewStatusReportsFailureInsteadOfNone(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !contains(r.URL.Path, "/reviews") {
			t.Fatalf("path = %q, want the reviews endpoint", r.URL.Path)
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	client, _ := NewClient(server.URL, "test-token")

	if status, err := client.FetchPullRequestReviewStatus("owner/repo", 42); err == nil {
		t.Fatalf("status = %q with no error, want the failure reported so a caller can keep what it has", status)
	}
}

func TestClient_SearchAuthoredPRs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search/issues" {
			t.Errorf("Path = %q, want /search/issues", r.URL.Path)
		}

		q := r.URL.Query().Get("q")
		if !containsAll(q, "is:pr", "is:open", "author:@me") {
			t.Errorf("Query = %q, missing required qualifiers", q)
		}

		w.WriteHeader(http.StatusOK)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"total_count": 2,
			"items": [
				{
					"number": 123,
					"title": "Test PR 1",
					"html_url": "https://github.com/owner/repo/pull/123",
					"draft": false,
					"repository_url": "https://api.github.com/repos/owner/repo"
				},
				{
					"number": 456,
					"title": "Draft PR",
					"html_url": "https://github.com/owner/repo/pull/456",
					"draft": true,
					"repository_url": "https://api.github.com/repos/owner/repo"
				}
			]
		}`))
	}))
	defer server.Close()

	client, _ := NewClient(server.URL, "test-token")
	prs, err := client.SearchAuthoredPRs()
	if err != nil {
		t.Fatalf("SearchAuthoredPRs error: %v", err)
	}

	if len(prs) != 1 {
		t.Fatalf("got %d PRs, want 1 (draft should be filtered)", len(prs))
	}

	if prs[0].Number != 123 {
		t.Errorf("PR number = %d, want 123", prs[0].Number)
	}
	if prs[0].Repo != "owner/repo" {
		t.Errorf("PR repo = %q, want owner/repo", prs[0].Repo)
	}
	expectedHost := hostFromAPIURL(server.URL)
	if prs[0].Host != expectedHost {
		t.Errorf("PR host = %q, want %q", prs[0].Host, expectedHost)
	}
	expectedID := protocol.FormatPRID(expectedHost, "owner/repo", 123)
	if prs[0].ID != expectedID {
		t.Errorf("PR id = %q, want %q", prs[0].ID, expectedID)
	}
}

func containsAll(s string, parts ...string) bool {
	for _, part := range parts {
		if !contains(s, part) {
			return false
		}
	}
	return true
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) && containsInner(s, substr))
}

func containsInner(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestClient_SearchReviewRequestedPRs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		if !contains(q, "review-requested:@me") {
			t.Errorf("Query = %q, missing review-requested:@me", q)
		}

		w.WriteHeader(http.StatusOK)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"total_count": 1,
			"items": [
				{
					"number": 789,
					"title": "Needs Review",
					"html_url": "https://github.com/other/repo/pull/789",
					"draft": false,
					"repository_url": "https://api.github.com/repos/other/repo"
				}
			]
		}`))
	}))
	defer server.Close()

	client, _ := NewClient(server.URL, "test-token")
	prs, err := client.SearchReviewRequestedPRs()
	if err != nil {
		t.Fatalf("SearchReviewRequestedPRs error: %v", err)
	}

	if len(prs) != 1 {
		t.Fatalf("got %d PRs, want 1", len(prs))
	}
	if prs[0].Role != "reviewer" {
		t.Errorf("Role = %q, want %q", prs[0].Role, "reviewer")
	}
	if prs[0].Reason != "review_needed" {
		t.Errorf("Reason = %q, want %q", prs[0].Reason, "review_needed")
	}
}

func TestClient_FetchAll(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		q := r.URL.Query().Get("q")

		var items []map[string]interface{}
		if contains(q, "author:@me") {
			items = []map[string]interface{}{
				{
					"number":         1,
					"title":          "My PR",
					"html_url":       "https://github.com/a/b/pull/1",
					"draft":          false,
					"repository_url": "https://api.github.com/repos/a/b",
				},
			}
		} else if contains(q, "review-requested:@me") {
			items = []map[string]interface{}{
				{
					"number":         2,
					"title":          "Review This",
					"html_url":       "https://github.com/c/d/pull/2",
					"draft":          false,
					"repository_url": "https://api.github.com/repos/c/d",
				},
			}
		}

		w.WriteHeader(http.StatusOK)
		w.Header().Set("Content-Type", "application/json")
		responseData := map[string]interface{}{
			"total_count": len(items),
			"items":       items,
		}

		jsonBytes, _ := json.Marshal(responseData)
		w.Write(jsonBytes)
	}))
	defer server.Close()

	client, _ := NewClient(server.URL, "test-token")
	prs, err := client.FetchAll()
	if err != nil {
		t.Fatalf("FetchAll error: %v", err)
	}

	if len(prs) != 2 {
		t.Fatalf("got %d PRs, want 2", len(prs))
	}
	if callCount != 3 {
		t.Errorf("API called %d times, want 3", callCount)
	}
}

func TestClient_FetchPRDetails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch {
		case contains(r.URL.Path, "/pulls/42") && !contains(r.URL.Path, "/reviews"):
			json.NewEncoder(w).Encode(map[string]interface{}{
				"mergeable":       true,
				"mergeable_state": "clean",
				"head":            map[string]string{"sha": "abc123"},
			})
		case contains(r.URL.Path, "/check-runs"):
			json.NewEncoder(w).Encode(map[string]interface{}{
				"check_runs": []map[string]interface{}{
					{"conclusion": "success"},
					{"conclusion": "success"},
				},
			})
		case contains(r.URL.Path, "/reviews"):
			json.NewEncoder(w).Encode([]map[string]interface{}{
				{"state": "APPROVED"},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client, _ := NewClient(server.URL, "test-token")
	details, err := client.FetchPRDetails("owner/repo", 42)
	if err != nil {
		t.Fatalf("FetchPRDetails error: %v", err)
	}

	if details.Mergeable == nil || *details.Mergeable != true {
		t.Error("Mergeable should be true")
	}
	if details.MergeableState != "clean" {
		t.Errorf("MergeableState = %q, want clean", details.MergeableState)
	}
	if details.CIStatus != "success" {
		t.Errorf("CIStatus = %q, want success", details.CIStatus)
	}
	if details.ReviewStatus != "approved" {
		t.Errorf("ReviewStatus = %q, want approved", details.ReviewStatus)
	}
}

func TestClient_ApprovePR(t *testing.T) {
	var capturedMethod, capturedPath string
	var capturedBody map[string]interface{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedMethod = r.Method
		capturedPath = r.URL.Path

		json.NewDecoder(r.Body).Decode(&capturedBody)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"id":    12345,
			"state": "APPROVED",
		})
	}))
	defer server.Close()

	client, _ := NewClient(server.URL, "test-token")
	err := client.ApprovePR("owner/repo", 42)
	if err != nil {
		t.Fatalf("ApprovePR error: %v", err)
	}

	if capturedMethod != "POST" {
		t.Errorf("HTTP method = %q, want POST", capturedMethod)
	}

	expectedPath := "/repos/owner/repo/pulls/42/reviews"
	if capturedPath != expectedPath {
		t.Errorf("Path = %q, want %q", capturedPath, expectedPath)
	}

	if capturedBody["event"] != "APPROVE" {
		t.Errorf("Request body event = %v, want APPROVE", capturedBody["event"])
	}
}

func TestClient_ApprovePR_Error(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"message": "Resource not accessible by integration"}`))
	}))
	defer server.Close()

	client, _ := NewClient(server.URL, "test-token")
	err := client.ApprovePR("owner/repo", 42)
	if err == nil {
		t.Fatal("ApprovePR should return error on 403 response")
	}

	if !contains(err.Error(), "403") {
		t.Errorf("Error message = %q, should contain 403", err.Error())
	}
}

func TestClient_MergePR(t *testing.T) {
	var capturedMethod, capturedPath string
	var capturedBody map[string]interface{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedMethod = r.Method
		capturedPath = r.URL.Path

		json.NewDecoder(r.Body).Decode(&capturedBody)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"sha":     "abc123",
			"merged":  true,
			"message": "Pull Request successfully merged",
		})
	}))
	defer server.Close()

	client, _ := NewClient(server.URL, "test-token")
	err := client.MergePR("owner/repo", 42, "squash")
	if err != nil {
		t.Fatalf("MergePR error: %v", err)
	}

	if capturedMethod != "PUT" {
		t.Errorf("HTTP method = %q, want PUT", capturedMethod)
	}

	expectedPath := "/repos/owner/repo/pulls/42/merge"
	if capturedPath != expectedPath {
		t.Errorf("Path = %q, want %q", capturedPath, expectedPath)
	}

	if capturedBody["merge_method"] != "squash" {
		t.Errorf("Request body merge_method = %v, want squash", capturedBody["merge_method"])
	}
}

func TestClient_MergePR_InvalidMethod(t *testing.T) {
	// A mock URL, because test-token against the real API is blocked.
	client, _ := NewClient("http://localhost:9999", "test-token")
	err := client.MergePR("owner/repo", 42, "invalid")
	if err == nil {
		t.Fatal("MergePR should return error for invalid merge method")
	}

	if !contains(err.Error(), "invalid") || !contains(err.Error(), "merge") {
		t.Errorf("Error message = %q, should mention invalid merge method", err.Error())
	}
}

func TestClient_FetchPRState(t *testing.T) {
	var capturedPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"state":  "closed",
			"merged": true,
			"title":  "Fix the thing",
		})
	}))
	defer server.Close()

	client, _ := NewClient(server.URL, "test-token")
	state, merged, title, err := client.FetchPRState("owner/repo", 462)
	if err != nil {
		t.Fatalf("FetchPRState error: %v", err)
	}
	if capturedPath != "/repos/owner/repo/pulls/462" {
		t.Errorf("Path = %q, want /repos/owner/repo/pulls/462", capturedPath)
	}
	if state != "closed" || !merged || title != "Fix the thing" {
		t.Errorf("FetchPRState = (%q, %v, %q), want (closed, true, Fix the thing)", state, merged, title)
	}
}

func TestClient_FetchPRState_HTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client, _ := NewClient(server.URL, "test-token")
	if _, _, _, err := client.FetchPRState("owner/repo", 462); err == nil {
		t.Fatal("FetchPRState should return error on 404")
	}
}
