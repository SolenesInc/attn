package github_test

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/github"
	"github.com/victorarias/attn/internal/github/mockserver"
)

func TestTheClientLogsOneSpendLinePerClosedRateLimitWindow(t *testing.T) {
	gh := mockserver.New()
	t.Cleanup(gh.Close)
	gh.AddPR(mockserver.MockPR{Repo: "acme/shop", Number: 1, Role: "author", ReviewState: "APPROVED"})
	gh.AddPR(mockserver.MockPR{Repo: "acme/shop", Number: 2, DetailsHTTPStatus: 502})

	var mu sync.Mutex
	var lines []string
	logf := func(format string, args ...any) {
		mu.Lock()
		defer mu.Unlock()
		lines = append(lines, fmt.Sprintf(format, args...))
	}
	logged := func() []string {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(lines)
	}
	client, err := github.NewClientForHost(context.Background(), logf, "github.com", gh.URL, "test-token")
	if err != nil {
		t.Fatal(err)
	}

	firstWindowEnd := time.Unix(1_900_000_000, 0)
	gh.StartRateLimitWindow(firstWindowEnd)
	if _, err := client.FetchPRDetails("acme/shop", 1); err != nil {
		t.Fatalf("FetchPRDetails(acme/shop#1): %v", err)
	}
	if _, err := client.FetchPRDetails("acme/shop", 2); err == nil {
		t.Fatal("FetchPRDetails(acme/shop#2) succeeded; want the mock's 502")
	}
	gh.SpendElsewhere("core", 100)
	if _, err := client.SearchAuthoredPRs(); err != nil {
		t.Fatalf("SearchAuthoredPRs: %v", err)
	}
	if _, err := client.RepoVisibility("acme/shop"); err == nil {
		t.Fatal("RepoVisibility(acme/shop) succeeded; want the mock's 404")
	}
	if got := logged(); len(got) != 0 {
		t.Fatalf("the client logged %q while the first window was open; want nothing", got)
	}

	secondWindowEnd := firstWindowEnd.Add(time.Hour)
	gh.StartRateLimitWindow(secondWindowEnd)
	if _, err := client.SearchAuthoredPRs(); err != nil {
		t.Fatalf("SearchAuthoredPRs in the second window: %v", err)
	}
	firstLine := "GitHub spend: host=github.com resource=core window_end=" + firstWindowEnd.Format(time.RFC3339) +
		" account_used=105 attn=5 (pulls 200=1 error=1, reviews 200=1, search 200=1, graphql 0, other error=1)"
	if got := logged(); !slices.Equal(got, []string{firstLine}) {
		t.Fatalf("after the first window closed the client logged\n%q\nwant\n%q", got, []string{firstLine})
	}

	gh.StartRateLimitWindow(secondWindowEnd.Add(time.Hour))
	if _, err := client.SearchAuthoredPRs(); err != nil {
		t.Fatalf("SearchAuthoredPRs in the third window: %v", err)
	}
	secondLine := "GitHub spend: host=github.com resource=core window_end=" + secondWindowEnd.Format(time.RFC3339) +
		" account_used=1 attn=1 (pulls 0, reviews 0, search 200=1, graphql 0, other 0)"
	if got := logged(); !slices.Equal(got, []string{firstLine, secondLine}) {
		t.Fatalf("after the second window closed the client logged\n%q\nwant\n%q", got, []string{firstLine, secondLine})
	}
}
