package daemon_test

import (
	"strings"
	"testing"
)

func TestATurnThatEndsWhileTheActivityLineIsBeingWrittenGetsALineOfItsOwn(t *testing.T) {
	w, app, _, session, agent := watchedActivityWorld(t)
	activityTurn(t, w, app, agent, session, "run the frontend tests", "The frontend suite is running.")
	writing := w.HeadlessTask()

	activityTurn(t, w, app, agent, session, "update the changelog", "Updated the changelog.")
	writing.Answer("Running the frontend test suite")
	awaitActivity(app, session, "Running the frontend test suite")

	next := w.HeadlessTask()
	if !strings.Contains(next.Prompt, "Updated the changelog.") {
		t.Errorf("after the line in progress landed the next activity prompt was %q, want the turn that ended meanwhile", next.Prompt)
	}
	next.Answer("Updating the changelog")
	awaitActivity(app, session, "Updating the changelog")
}
