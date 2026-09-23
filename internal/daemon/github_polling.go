package daemon

import (
	"fmt"
	"os"
	"strings"

	"github.com/victorarias/attn/internal/config"
)

const GitHubPollingOptInEnv = "ATTN_GITHUB_POLLING"

func gitHubPollingOffReason() string {
	instance := config.Instance()
	if instance == "" || gitHubPollingOptedIn() {
		return ""
	}
	return fmt.Sprintf("GitHub polling is off for instance %s. Start its daemon with %s=on to poll with your gh credentials.", instance, GitHubPollingOptInEnv)
}

func gitHubPollingOffReasonField() *string {
	reason := gitHubPollingOffReason()
	if reason == "" {
		return nil
	}
	return &reason
}

func gitHubPollingOptedIn() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(GitHubPollingOptInEnv))) {
	case "1", "on", "true", "yes":
		return true
	}
	return false
}
