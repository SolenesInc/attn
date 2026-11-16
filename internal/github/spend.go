package github

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

var spendEndpoints = []string{"pulls", "reviews", "search", "graphql", "other"}

var spendStatuses = []string{"200", "304", "error"}

var (
	searchPath  = regexp.MustCompile(`^(/api/v3)?/search/`)
	reviewsPath = regexp.MustCompile(`/repos/[^/]+/[^/]+/pulls/\d+/reviews$`)
	pullsPath   = regexp.MustCompile(`/repos/[^/]+/[^/]+/pulls(/|$)`)
)

type spendWindow struct {
	reset       int64
	accountUsed int
	requests    map[string]map[string]int
}

func newSpendWindow() *spendWindow {
	return &spendWindow{accountUsed: -1, requests: map[string]map[string]int{}}
}

func spendEndpointOf(requestPath string) string {
	switch {
	case strings.HasSuffix(requestPath, "/graphql"):
		return "graphql"
	case searchPath.MatchString(requestPath):
		return "search"
	case reviewsPath.MatchString(requestPath):
		return "reviews"
	case pullsPath.MatchString(requestPath):
		return "pulls"
	default:
		return "other"
	}
}

func spendStatusOf(statusCode int) string {
	switch {
	case statusCode == 304:
		return "304"
	case statusCode < 300:
		return "200"
	default:
		return "error"
	}
}

func (w *spendWindow) count(endpoint, status string) {
	if w.requests[endpoint] == nil {
		w.requests[endpoint] = map[string]int{}
	}
	w.requests[endpoint][status]++
}

func (w *spendWindow) line(host, resource string) string {
	total := 0
	groups := make([]string, 0, len(spendEndpoints))
	for _, endpoint := range spendEndpoints {
		group := endpoint
		for _, status := range spendStatuses {
			if n := w.requests[endpoint][status]; n > 0 {
				group += fmt.Sprintf(" %s=%d", status, n)
				total += n
			}
		}
		if group == endpoint {
			group += " 0"
		}
		groups = append(groups, group)
	}
	accountUsed := "unknown"
	if w.accountUsed >= 0 {
		accountUsed = fmt.Sprint(w.accountUsed)
	}
	return fmt.Sprintf("GitHub spend: host=%s resource=%s window_end=%s account_used=%s attn=%d (%s)",
		host, resource, time.Unix(w.reset, 0).Format(time.RFC3339), accountUsed, total, strings.Join(groups, ", "))
}
