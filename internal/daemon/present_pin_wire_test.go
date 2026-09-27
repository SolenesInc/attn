package daemon_test

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func TestAPresentationRoundIsPinnedToTheCommitsItsRefsNamedWhenItOpened(t *testing.T) {
	w := newWorld(t)
	app, cli := w.App(), w.Client()
	repo := newRepo(t, "shop")
	base := strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD"))
	runGit(t, repo, "checkout", "-b", "feature")
	head := commitFile(t, repo, "feature.go", "package feature\n")
	manifest := func(repo, base, head string) string {
		return fmt.Sprintf("version: 1\nkind: changes\ntitle: Feature\nframe:\n  repo: %q\n  base: %q\n  head: %q\n", repo, base, head)
	}

	opened, err := cli.PresentOpen("presenter", manifest(repo, "main", "feature"), "")
	if err != nil {
		t.Fatal(err)
	}
	if opened.BaseSHA != base || opened.HeadSHA != head {
		t.Errorf("opening main..feature pinned %s..%s, want %s..%s", opened.BaseSHA, opened.HeadSHA, base, head)
	}
	commitFile(t, repo, "later.go", "package later\n")
	if round := presentationRound(app, opened.PresentationID, 1).Round; round.BaseSHA != base || round.HeadSHA != head {
		t.Errorf("after feature moved on, round 1 reads %s..%s, want it still pinned to %s..%s", round.BaseSHA, round.HeadSHA, base, head)
	}

	for _, refused := range []struct{ repo, base, head, want string }{
		{repo, "does-not-exist", "HEAD", "frame.base"},
		{repo, "HEAD", "does-not-exist", "frame.head"},
		{filepath.Join(repo, "missing"), "HEAD", "HEAD", "does not exist"},
	} {
		if _, err := cli.PresentOpen("presenter", manifest(refused.repo, refused.base, refused.head), ""); err == nil || !strings.Contains(err.Error(), refused.want) {
			t.Errorf("opening %s %s..%s answered %v, want a refusal naming %q", refused.repo, refused.base, refused.head, err, refused.want)
		}
	}
}
