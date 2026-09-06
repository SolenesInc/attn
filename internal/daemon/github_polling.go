package daemon

import (
	"fmt"
	"os"
	"strings"

	"github.com/victorarias/attn/internal/config"
)

// GitHubPollingOptInEnv turns real GitHub polling on for a named profile.
const GitHubPollingOptInEnv = "ATTN_GITHUB_POLLING"

// gitHubPollingOffReason is empty when this daemon may poll GitHub with the user's gh
// credentials: the production profile always may, a named profile only when opted in.
func gitHubPollingOffReason() string {
	profile := config.Profile()
	if profile == "" || gitHubPollingOptedIn() {
		return ""
	}
	return fmt.Sprintf("GitHub polling is off for profile %s. Start its daemon with %s=on to poll with your gh credentials.", profile, GitHubPollingOptInEnv)
}

// gitHubPollingOffReasonField keeps the wire unchanged while polling is on.
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
