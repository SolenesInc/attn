package github

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/victorarias/attn/internal/prreadiness"
)

func readinessPayload(extra string) []byte {
	return []byte(`{"data":{"repository":{"pullRequest":{
    "number":71,"url":"https://github.com/o/r/pull/71","title":"ready","bodyText":"",
    "isDraft":false,"state":"OPEN","merged":false,"mergeStateStatus":"CLEAN",
    "headRefOid":"abcdef1234567890","headRefName":"feature","baseRefOid":"base","baseRefName":"next",
    "author":{"login":"author"},"headRepository":{"nameWithOwner":"o/r"},"baseRepository":{"nameWithOwner":"o/r"},
    "commits":{"nodes":[{"commit":{"committedDate":"2026-09-19T10:00:00Z","statusCheckRollup":{"contexts":{"pageInfo":{"hasNextPage":false},"nodes":[{"__typename":"CheckRun","name":"test","status":"COMPLETED","conclusion":"SUCCESS"}]}}}}]},
    "reviews":{"pageInfo":{"hasNextPage":false},"nodes":[{"id":"review","state":"COMMENTED","bodyText":"Reviewed commit: abcdef1","submittedAt":"2026-09-19T10:01:00Z","author":{"login":"chatgpt-codex-connector"},"commit":{"oid":"abcdef1234567890"},"comments":{"pageInfo":{"hasNextPage":false},"nodes":[]}}]},
    "comments":{"pageInfo":{"hasNextPage":false},"nodes":[]},
    "reactions":{"pageInfo":{"hasNextPage":false},"nodes":[]},
    "reviewThreads":{"pageInfo":{"hasNextPage":false},"nodes":[]}` + extra + `
  }}}}`)
}

func TestParsePullRequestReadinessBuildsExactHeadEvidence(t *testing.T) {
	payload := strings.Replace(string(readinessPayload("")),
		`"comments":{"pageInfo":{"hasNextPage":false},"nodes":[]}`,
		`"comments":{"pageInfo":{"hasNextPage":false},"nodes":[{"id":"inline","bodyText":"Check this guard","path":"watch.go","line":42,"author":{"__typename":"User","login":"human"}}]}`, 1)
	payload = strings.Replace(payload,
		`"reactions":{"pageInfo":{"hasNextPage":false},"nodes":[]}`,
		`"reactions":{"pageInfo":{"hasNextPage":false},"nodes":[{"id":"reaction","content":"THUMBS_UP","createdAt":"2026-09-19T09:00:00Z","user":{"login":"chatgpt-codex-connector"}}]}`, 1)
	result, err := ParsePullRequestReadiness([]byte(payload))
	if err != nil {
		t.Fatalf("parse readiness: %v", err)
	}
	if result.Snapshot.HeadSHA != "abcdef1234567890" || result.Evidence.CheckState != prreadiness.ChecksGreen {
		t.Fatalf("result = %+v", result)
	}
	if got := prreadiness.Evaluate(result.Evidence, "chatgpt-codex-connector[bot]", nil); got.Ready || got.ReviewState != prreadiness.ReviewChangesRequested || len(got.Findings) != 1 {
		t.Fatalf("evaluation = %+v", got)
	}
	if len(result.Evidence.Comments) != 2 || result.Evidence.Comments[0].Kind != prreadiness.CommentReview ||
		result.Evidence.Comments[1].Kind != prreadiness.CommentInline ||
		result.Evidence.Comments[1].Location != "watch.go:42" || result.Evidence.Comments[1].Bot {
		t.Fatalf("human inline evidence lost context: %+v", result.Evidence.Comments)
	}
	if len(result.Evidence.Reactions) != 1 || result.Evidence.Reactions[0].ID != "reaction" {
		t.Fatalf("reaction identity was not retained: %+v", result.Evidence.Reactions)
	}
}

func TestParsePullRequestReadinessUsesCommentIdentityForThreads(t *testing.T) {
	body := strings.Replace(string(readinessPayload("")),
		`"reviewThreads":{"pageInfo":{"hasNextPage":false},"nodes":[]}`,
		`"reviewThreads":{"pageInfo":{"hasNextPage":false},"nodes":[{"id":"thread-id","isResolved":false,"comments":{"nodes":[{"id":"comment-id","bodyText":"finding","createdAt":"2026-09-19T10:02:00Z","path":"watch.go","line":42,"author":{"login":"chatgpt-codex-connector"}}]}}]}`, 1)
	result, err := ParsePullRequestReadiness([]byte(body))
	if err != nil {
		t.Fatalf("parse readiness: %v", err)
	}
	if len(result.Evidence.Threads) != 1 || result.Evidence.Threads[0].ID != "comment-id" {
		t.Fatalf("threads = %+v", result.Evidence.Threads)
	}
}

func TestParsePullRequestReadinessRejectsTruncatedPages(t *testing.T) {
	body := strings.Replace(string(readinessPayload("")),
		`"reviews":{"pageInfo":{"hasNextPage":false}`,
		`"reviews":{"pageInfo":{"hasNextPage":true}`, 1)
	_, err := ParsePullRequestReadiness([]byte(body))
	if !errors.Is(err, ErrReadinessTruncated) {
		t.Fatalf("error = %v, want ErrReadinessTruncated", err)
	}
}

func TestGraphQLURLSupportsDotcomAndEnterprise(t *testing.T) {
	for _, tc := range []struct{ base, want string }{
		{"https://api.github.com", "https://api.github.com/graphql"},
		{"https://ghe.example.test/api/v3", "https://ghe.example.test/api/graphql"},
	} {
		client := &Client{baseURL: tc.base}
		if got := client.graphQLURL(); got != tc.want {
			t.Errorf("graphQLURL(%q) = %q, want %q", tc.base, got, tc.want)
		}
	}
}

func TestFetchPullRequestReadinessPaginatesAndKeepsOneHead(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Variables map[string]any `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		call := calls.Add(1)
		if call == 1 {
			if _, ok := request.Variables["reviewCursor"]; ok {
				t.Fatalf("first request has review cursor: %v", request.Variables)
			}
			body := strings.Replace(string(readinessPayload("")),
				`"reviews":{"pageInfo":{"hasNextPage":false}`,
				`"reviews":{"pageInfo":{"hasNextPage":true,"endCursor":"reviews-1"}`, 1)
			_, _ = w.Write([]byte(body))
			return
		}
		if request.Variables["reviewCursor"] != "reviews-1" {
			t.Fatalf("review cursor = %v", request.Variables["reviewCursor"])
		}
		body := strings.Replace(string(readinessPayload("")), `"id":"review"`, `"id":"second-review"`, 1)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	client, err := NewClient(server.URL, "test-token")
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.FetchPullRequestReadiness("o/r", 71)
	if err != nil {
		t.Fatal(err)
	}
	evaluation := prreadiness.Evaluate(result.Evidence, "chatgpt-codex-connector[bot]", nil)
	if calls.Load() != 2 || len(result.Evidence.Reviews) != 2 || !evaluation.Ready {
		t.Fatalf("calls = %d, reviews = %d, evaluation = %+v", calls.Load(), len(result.Evidence.Reviews), evaluation)
	}
	if result.Evidence.Reviews[0].ID != "review" || result.Evidence.Reviews[1].ID != "second-review" {
		t.Fatalf("reviews from both pages were not retained: %+v", result.Evidence.Reviews)
	}
}

func TestFetchPullRequestReadinessRejectsHeadChangeBetweenPages(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		body := string(readinessPayload(""))
		if calls.Add(1) == 1 {
			body = strings.Replace(body,
				`"reviews":{"pageInfo":{"hasNextPage":false}`,
				`"reviews":{"pageInfo":{"hasNextPage":true,"endCursor":"reviews-1"}`, 1)
		} else {
			body = strings.Replace(body, "abcdef1234567890", "fedcba0987654321", 1)
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	client, err := NewClient(server.URL, "test-token")
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.FetchPullRequestReadiness("o/r", 71)
	if !errors.Is(err, ErrReadinessHeadChanged) {
		t.Fatalf("error = %v, want ErrReadinessHeadChanged", err)
	}
}
