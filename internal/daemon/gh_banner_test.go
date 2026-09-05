package daemon

import (
	"errors"
	"os/exec"
	"strings"
	"testing"

	attngithub "github.com/victorarias/attn/internal/github"
)

func TestGHVersionWarningSplit(t *testing.T) {
	// Warning keys are read by the frontend; only the text may change.
	missing := errors.Join(exec.ErrNotFound, errors.New("sentinel"))
	code, msg := ghVersionWarning(missing)
	if code != warnGHNotInstalled {
		t.Fatalf("missing gh code = %q, want %q", code, warnGHNotInstalled)
	}
	if !strings.Contains(msg, "not installed") || !strings.Contains(msg, attngithub.InstallHint()) {
		t.Fatalf("missing gh message = %q, want absence plus install hint", msg)
	}

	outdated := &attngithub.VersionTooOldError{Have: "2.45.0", Want: "2.81.0"}
	code, msg = ghVersionWarning(outdated)
	if code != warnGHVersionTooOld {
		t.Fatalf("outdated gh code = %q, want %q", code, warnGHVersionTooOld)
	}
	for _, want := range []string{"2.45.0", "2.81.0", attngithub.UpgradeHint()} {
		if !strings.Contains(msg, want) {
			t.Fatalf("outdated gh message = %q, want %q", msg, want)
		}
	}

	code, msg = ghVersionWarning(errors.New("unparsable gh output"))
	if code != warnGHVersionTooOld {
		t.Fatalf("opaque gh failure code = %q, want %q", code, warnGHVersionTooOld)
	}
	if !strings.Contains(msg, attngithub.UpgradeHint()) {
		t.Fatalf("opaque gh failure message = %q, want upgrade hint", msg)
	}
}
