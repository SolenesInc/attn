package github

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type readinessTransportFunc func(string, map[string]any) ([]byte, error)

func (f readinessTransportFunc) GraphQL(_ context.Context, query string, variables map[string]any) ([]byte, error) {
	return f(query, variables)
}

func TestFetchPullRequestReadinessPaginatesFactsAndSelectedOpinion(t *testing.T) {
	transport := readinessTransportFunc(func(query string, variables map[string]any) ([]byte, error) {
		switch {
		case strings.Contains(query, "query PullRequestReadiness"):
			return []byte(`{"data":{"repository":{"pullRequest":{
				"number":7,"url":"https://ghe.example/acme/widgets/pull/7","title":"Ship","state":"OPEN","isDraft":false,"merged":false,
				"headRefOid":"abc","headRefName":"topic","mergeStateStatus":"HAS_HOOKS","reviewDecision":"APPROVED",
				"reactions":{"nodes":[{"id":"r1","content":"THUMBS_UP","user":{"__typename":"Bot","id":"b1","login":"chatgpt-codex-connector"}}],"pageInfo":{"hasNextPage":true,"endCursor":"r"}},
				"latestOpinionatedReviews":{"nodes":[],"pageInfo":{"hasNextPage":true,"endCursor":"o"}},
				"reviews":{"nodes":[],"pageInfo":{"hasNextPage":true,"endCursor":"h"}},
				"commits":{"nodes":[{"commit":{"statusCheckRollup":{"contexts":{"nodes":[],"pageInfo":{"hasNextPage":true,"endCursor":"c"}}}}}]}
			}}}}`), nil
		case strings.Contains(query, "query PullRequestReactions"):
			return []byte(`{"data":{"repository":{"pullRequest":{"headRefOid":"abc","reactions":{"nodes":[{"id":"r2","content":"EYES","user":{"__typename":"Bot","id":"b1","login":"chatgpt-codex-connector"}}],"pageInfo":{"hasNextPage":false,"endCursor":""}}}}}}`), nil
		case strings.Contains(query, "query PullRequestOpinions"):
			return []byte(`{"data":{"repository":{"pullRequest":{"headRefOid":"abc","latestOpinionatedReviews":{"nodes":[{"id":"v1","state":"APPROVED","bodyText":"looks good","submittedAt":"2026-09-20T12:00:00Z","author":{"__typename":"User","id":"u1","login":"victor"}}],"pageInfo":{"hasNextPage":false,"endCursor":""}}}}}}`), nil
		case strings.Contains(query, "query PullRequestReviews"):
			return []byte(`{"data":{"repository":{"pullRequest":{"headRefOid":"abc","reviews":{"nodes":[{"id":"v1","state":"APPROVED","bodyText":"looks good","submittedAt":"2026-09-20T12:00:00Z","author":{"__typename":"User","id":"u1","login":"victor"}}],"pageInfo":{"hasNextPage":false,"endCursor":""}}}}}}`), nil
		case strings.Contains(query, "query PullRequestChecks"):
			return []byte(`{
				"data":{"repository":{"pullRequest":{
					"headRefOid":"abc",
					"commits":{"nodes":[{"commit":{"statusCheckRollup":{
						"contexts":{"nodes":[{"__typename":"CheckRun","id":"check-1","name":"test","status":"COMPLETED","conclusion":"FAILURE","detailsUrl":"https://ci"}],"pageInfo":{"hasNextPage":false,"endCursor":""}}
					}}}]}
				}}}
			}`), nil
		default:
			return nil, errors.New("unexpected query")
		}
	})

	got, err := FetchPullRequestReadiness(context.Background(), transport, "acme/widgets", 7)
	if err != nil {
		t.Fatal(err)
	}
	if got.MergeStateStatus != "HAS_HOOKS" || !got.CodexThumbsUp || !got.CodexEyes {
		t.Fatalf("observation = %+v", got)
	}
	if len(got.ReviewOpinions) != 1 || got.ReviewOpinions[0].State != "APPROVED" || got.ReviewOpinions[0].Body != "looks good" || len(got.Checks) != 1 || got.Checks[0].State != "failure" {
		t.Fatalf("opinions/checks = %+v / %+v", got.ReviewOpinions, got.Checks)
	}
}

func TestFetchPullRequestReadinessRejectsHeadChangeDuringPagination(t *testing.T) {
	transport := readinessTransportFunc(func(query string, _ map[string]any) ([]byte, error) {
		if strings.Contains(query, "query PullRequestReadiness") {
			return []byte(`{"data":{"repository":{"pullRequest":{
				"number":7,"state":"OPEN","headRefOid":"head-a","mergeStateStatus":"CLEAN",
				"reactions":{"nodes":[],"pageInfo":{"hasNextPage":true,"endCursor":"r"}},
				"latestOpinionatedReviews":{"nodes":[],"pageInfo":{}},"reviews":{"nodes":[],"pageInfo":{}},
				"commits":{"nodes":[]}
			}}}}`), nil
		}
		if strings.Contains(query, "query PullRequestReactions") {
			return []byte(`{"data":{"repository":{"pullRequest":{"headRefOid":"head-b","reactions":{"nodes":[],"pageInfo":{}}}}}}`), nil
		}
		return nil, errors.New("unexpected query")
	})
	_, err := FetchPullRequestReadiness(context.Background(), transport, "acme/widgets", 7)
	if !errors.Is(err, ErrReadinessHeadChanged) {
		t.Fatalf("error = %v, want %v", err, ErrReadinessHeadChanged)
	}
}

func TestReadinessPaginationQueriesIncludeHead(t *testing.T) {
	queries := map[string]string{
		"reactions": pullRequestReactionPageQuery,
		"opinions":  pullRequestOpinionPageQuery,
		"reviews":   pullRequestReviewPageQuery,
		"checks":    pullRequestCheckPageQuery,
	}
	for name, query := range queries {
		if !strings.Contains(query, "headRefOid") {
			t.Errorf("%s pagination query does not request headRefOid", name)
		}
	}
}

func TestSelectedOpinionCannotResurrectApprovalAfterDismissal(t *testing.T) {
	candidate := readinessReview{ID: "approved", State: "APPROVED", SubmittedAt: mustTime(t, "2026-09-20T12:00:00Z"), Author: readinessActor{TypeName: "User", Login: "victor"}}
	dismissed := readinessReview{ID: "dismissed", State: "DISMISSED", SubmittedAt: mustTime(t, "2026-09-20T12:01:00Z"), Author: readinessActor{TypeName: "User", Login: "victor"}}
	got := selectReviewOpinion([]readinessReview{candidate}, []readinessReview{candidate, dismissed}, "victor")
	if got.State != "DISMISSED" {
		t.Fatalf("opinion = %+v", got)
	}
}

func TestFetchPullRequestFeedbackPaginatesCommentsThreadsAndReplies(t *testing.T) {
	transport := readinessTransportFunc(func(query string, variables map[string]any) ([]byte, error) {
		switch {
		case strings.Contains(query, "query PullRequestFeedback"):
			return []byte(`{"data":{"repository":{"pullRequest":{
				"comments":{"nodes":[{"id":"c1","bodyText":"one","createdAt":"2026-09-20T12:00:00Z","author":{"__typename":"User","login":"a"}}],"pageInfo":{"hasNextPage":true,"endCursor":"c"}},
				"reviews":{"nodes":[{"id":"v1","state":"CHANGES_REQUESTED","bodyText":"fix one","submittedAt":"2026-09-20T12:01:30Z","author":{"__typename":"User","login":"victor"}}],"pageInfo":{"hasNextPage":true,"endCursor":"v"}},
				"reviewThreads":{"nodes":[{"id":"t1","isResolved":true,"comments":{"nodes":[{"id":"i1","bodyText":"inline","createdAt":"2026-09-20T12:01:00Z","path":"x.go","line":4,"author":{"__typename":"User","login":"b"}}],"pageInfo":{"hasNextPage":true,"endCursor":"i"}}}],"pageInfo":{"hasNextPage":true,"endCursor":"t"}}
			}}}}`), nil
		case strings.Contains(query, "query PullRequestComments"):
			return []byte(`{"data":{"repository":{"pullRequest":{"comments":{"nodes":[{"id":"c2","bodyText":"two","createdAt":"2026-09-20T12:02:00Z","author":{"__typename":"User","login":"c"}}],"pageInfo":{"hasNextPage":false,"endCursor":""}}}}}}`), nil
		case strings.Contains(query, "query PullRequestThreads"):
			return []byte(`{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":[{"id":"t2","isResolved":false,"comments":{"nodes":[],"pageInfo":{"hasNextPage":false,"endCursor":""}}}],"pageInfo":{"hasNextPage":false,"endCursor":""}}}}}}`), nil
		case strings.Contains(query, "query PullRequestReviewBodies"):
			return []byte(`{"data":{"repository":{"pullRequest":{"reviews":{"nodes":[{"id":"v2","state":"COMMENTED","bodyText":"note two","submittedAt":"2026-09-20T12:02:30Z","author":{"__typename":"User","login":"d"}}],"pageInfo":{"hasNextPage":false,"endCursor":""}}}}}}`), nil
		case strings.Contains(query, "query PullRequestThreadComments"):
			if variables["id"] != "t1" {
				t.Fatalf("thread id = %v", variables["id"])
			}
			return []byte(`{"data":{"node":{"comments":{"nodes":[{"id":"r1","bodyText":"reply","createdAt":"2026-09-20T12:03:00Z","path":"x.go","line":4,"author":{"__typename":"User","login":"d"}}],"pageInfo":{"hasNextPage":false,"endCursor":""}}}}}`), nil
		default:
			return nil, errors.New("unexpected query")
		}
	})
	feedback, threads, err := FetchPullRequestFeedback(context.Background(), transport, "acme/widgets", 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(feedback) != 6 || feedback[0].Kind != "comment" || feedback[1].Kind != "inline_comment" || feedback[2].Kind != "review" || feedback[2].Body != "fix one" || feedback[4].Kind != "review" || feedback[5].Kind != "reply" {
		t.Fatalf("feedback = %+v", feedback)
	}
	if len(threads) != 2 || threads[0].ID != "t1" || !threads[0].Resolved || threads[1].ID != "t2" {
		t.Fatalf("threads = %+v", threads)
	}
}

func TestFeedbackFailureIsIndependentFromReadiness(t *testing.T) {
	transport := readinessTransportFunc(func(query string, _ map[string]any) ([]byte, error) {
		if strings.Contains(query, "query PullRequestReadiness") {
			return []byte(`{"data":{"repository":{"pullRequest":{"number":1,"state":"OPEN","headRefOid":"h","mergeStateStatus":"CLEAN","reactions":{"pageInfo":{}},"latestOpinionatedReviews":{"pageInfo":{}},"reviews":{"pageInfo":{}},"commits":{"nodes":[]}}}}}`), nil
		}
		return nil, errors.New("comments unavailable")
	})
	if _, err := FetchPullRequestReadiness(context.Background(), transport, "a/b", 1); err != nil {
		t.Fatalf("readiness failed: %v", err)
	}
	if _, _, err := FetchPullRequestFeedback(context.Background(), transport, "a/b", 1); err == nil {
		t.Fatal("feedback unexpectedly succeeded")
	}
}

func TestGraphQLURLRoutesGitHubAndEnterprise(t *testing.T) {
	for _, test := range []struct{ base, want string }{
		{"https://api.github.com", "https://api.github.com/graphql"},
		{"https://ghe.example/api/v3", "https://ghe.example/api/graphql"},
	} {
		client := &Client{baseURL: test.base}
		if got := client.graphQLURL(); got != test.want {
			t.Errorf("graphQLURL(%q) = %q, want %q", test.base, got, test.want)
		}
	}
}

func TestGraphQLRequestsResolvedEndpoint(t *testing.T) {
	for _, test := range []struct{ base, want string }{
		{"https://api.github.com", "https://api.github.com/graphql"},
		{"https://ghe.example/api/v3", "https://ghe.example/api/graphql"},
	} {
		t.Run(test.base, func(t *testing.T) {
			client, err := NewClient(test.base, "token")
			if err != nil {
				t.Fatal(err)
			}
			client.httpClient = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
				if got := request.URL.String(); got != test.want {
					t.Fatalf("request URL = %q, want %q", got, test.want)
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader(`{}`)),
					Request:    request,
				}, nil
			})}
			if _, err := client.GraphQL(context.Background(), "query Test { viewer { id } }", nil); err != nil {
				t.Fatal(err)
			}
		})
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func mustTime(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}
