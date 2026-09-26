package daemon_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

func TestAttachingFilesToATicketCopiesThemIntoTheNotebook(t *testing.T) {
	notebook := t.TempDir()
	inBubble(t, func(t *testing.T, w *world) {
		w.finishStartupWork()
		app, cli := w.App(), w.Client()
		setSetting(t, app, "notebook.root", notebook)
		createTicket(t, cli, "planner", "Storage choice", "storage")
		if _, err := cli.TakeTicket("owner", "storage", false); err != nil {
			t.Fatal(err)
		}
		inboxLines(t, cli, "owner")
		inboxLines(t, cli, "planner")
		nested := writeAttachment(t, "source.html", "<!doctype html><title>prototype</title>")
		nested.Filename = "nested/prototype.html"
		files := []protocol.TicketAttachFile{writeAttachment(t, "design.md", "the design"), nested, writeAttachment(t, "results.json", `{"ok":true}`)}

		receipt, err := cli.AttachTicket("owner", files, "", string(protocol.DispatchWorkStateReadyForReview), "Storage choice is confirmed.")
		if err != nil {
			t.Fatal(err)
		}
		if receipt.TicketID != "storage" || receipt.State != protocol.TicketStatusInReview || !receipt.Applied || receipt.EventSeq == 0 {
			t.Fatalf("receipt = %+v, want storage moved to review", receipt)
		}
		var names []string
		for _, artifact := range receipt.Artifacts {
			names = append(names, artifact.Filename)
			if !strings.HasPrefix(artifact.NotebookPath, "tickets/storage/") {
				t.Errorf("%s landed at %q, want it under tickets/storage/", artifact.Filename, artifact.NotebookPath)
			}
			if _, err := os.Stat(artifact.Path); err != nil {
				t.Errorf("%s is not on disk: %v", artifact.Filename, err)
			}
		}
		if !slices.Equal(names, []string{"design.md", "prototype.html", "results.json"}) {
			t.Errorf("receipt artifacts = %q, want the three files under their visible basenames", names)
		}
		shown := showTicket(t, cli, "storage")
		if shown.Status != protocol.TicketStatusInReview || !slices.ContainsFunc(shown.Activity, func(a protocol.TicketActivity) bool {
			comment := protocol.Deref(a.Comment)
			return a.Kind == protocol.TicketActivityKindAttach && strings.Contains(comment, "design.md, prototype.html, results.json") && strings.Contains(comment, "Storage choice is confirmed.")
		}) {
			t.Errorf("after the attach the ticket is %s with activity %q, want in review with the attach naming the files and the comment", shown.Status, activityLines(shown))
		}
		if told := inboxLines(t, cli, "planner"); !slices.ContainsFunc(told, func(line string) bool { return strings.HasPrefix(line, "storage attach_submitted") }) {
			t.Errorf("the creator was told %q, want the attach", told)
		}
		if told := inboxLines(t, cli, "owner"); len(told) != 0 {
			t.Errorf("the attaching owner was told %q about its own attach", told)
		}

		retry, err := cli.AttachTicket("owner", files, "", string(protocol.DispatchWorkStateReadyForReview), "Storage choice is confirmed.")
		if err != nil {
			t.Fatal(err)
		}
		if !retry.Deduplicated || retry.Fingerprint != receipt.Fingerprint || retry.EventSeq != receipt.EventSeq {
			t.Errorf("retrying the same attach = %+v, want the first receipt marked deduplicated", retry)
		}

		link := filepath.Join(t.TempDir(), "linked.txt")
		if err := os.Symlink(files[0].SourcePath, link); err != nil {
			t.Fatal(err)
		}
		if _, err := cli.AttachTicket("owner", []protocol.TicketAttachFile{{SourcePath: link, Filename: "linked.txt"}}, "", "", ""); err == nil || !strings.Contains(err.Error(), "not a regular file") {
			t.Errorf("attaching a symlink = %v, want it refused as not a regular file", err)
		}
		if _, err := cli.AttachTicket("owner", []protocol.TicketAttachFile{writeAttachment(t, "design.md", "a different design")}, "", "", ""); err == nil || !strings.Contains(err.Error(), "different contents") {
			t.Errorf("attaching over design.md with other bytes = %v, want it refused", err)
		}
		if kept, _ := os.ReadFile(filepath.Join(notebook, "tickets", "storage", "design.md")); string(kept) != "the design" {
			t.Errorf("design.md now holds %q, want the original left untouched", kept)
		}

		commentOnTicket(t, cli, "reviewer", "storage", "new decision")
		decision := []protocol.TicketAttachFile{writeAttachment(t, "decision.md", "decision")}
		held, err := cli.AttachTicket("owner", decision, "", "", "")
		if err != nil {
			t.Fatal(err)
		}
		installed := filepath.Join(notebook, "tickets", "storage", "decision.md")
		if held.Applied || held.CatchUp == nil || len(held.CatchUp.Events) != 1 || protocol.Deref(held.CatchUp.Events[0].Comment) != "new decision" {
			t.Fatalf("an attach behind unread activity = %+v, want the catch-up instead", held)
		}
		if _, err := os.Stat(installed); !os.IsNotExist(err) {
			t.Errorf("the held attach left decision.md behind: %v", err)
		}
		if applied, err := cli.AttachTicket("owner", decision, "", "", ""); err != nil || !applied.Applied || applied.CatchUp != nil {
			t.Fatalf("the caught-up retry = %+v, %v", applied, err)
		}
		if _, err := os.Stat(installed); err != nil {
			t.Errorf("the applied retry did not install decision.md: %v", err)
		}

		createTicket(t, cli, "planner", "Other", "other-ticket")
		if other, err := cli.AttachTicket("peer", []protocol.TicketAttachFile{writeAttachment(t, "plan.md", "plan")}, "other-ticket", "", ""); err != nil || other.TicketID != "other-ticket" {
			t.Errorf("attaching to a named ticket the peer does not own = %+v, %v", other, err)
		}
	})
}
