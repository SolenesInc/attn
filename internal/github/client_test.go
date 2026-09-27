package github

import (
	"strings"
	"testing"
)

func TestATestTokenIsNeverSentToTheRealGitHubAPI(t *testing.T) {
	_, err := NewClient("", "test-token")
	if err == nil || !strings.Contains(err.Error(), "refusing to use real GitHub API") {
		t.Fatalf("NewClient with the test token and the real API = %v, want a refusal", err)
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
