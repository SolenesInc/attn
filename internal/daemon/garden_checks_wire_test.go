package daemon_test

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAPlotSharesItsChecksWithEverySeedBelowIt(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		sessions := spawnCheckSessions(w, app, "planner", "builder")
		plot := checksPlot(t, cli, sessions["planner"])
		inner := gardenNudgePlant(t, cli, sessions["planner"], "inner plot", plot.Crown.ID)
		innerLeaf := gardenNudgePlant(t, cli, sessions["planner"], "inner leaf", inner)
		harvested := plot.Children[1].ID
		gardenNudgeMove(t, cli, sessions["builder"], harvested, "harvest")

		daemonChild := checksShow(t, cli, plot.Children[0].ID)
		if got := checkIDsFrom(daemonChild.Checks); !slices.Equal(got, []string{"C1", "C5@" + plot.Crown.ID}) {
			t.Errorf("the daemon child's effective checks = %v, want its own C1 then C5 from the plot", got)
		}
		if got := checkIDsFrom(checksShow(t, cli, innerLeaf).Checks); !slices.Equal(got, []string{"C5@" + plot.Crown.ID}) {
			t.Errorf("a leaf of a nested plot inherits %v, want C5 from the top plot", got)
		}
		if got := checksShow(t, cli, inner).Checks; len(got) != 0 {
			t.Errorf("a child with children of its own inherits %v, want none", checkIDsFrom(got))
		}

		crown := checksShow(t, cli, plot.Crown.ID)
		if got := checkIDsFrom(crown.Checks); !slices.Equal(got, []string{"C6"}) {
			t.Errorf("the plot's own checks = %v, want C6 alone, its shared C5 left out", got)
		}
		if len(crown.SharedChecks) != 1 || crown.SharedChecks[0].ID != "C5" {
			t.Fatalf("the plot's shared checks = %+v, want C5", crown.SharedChecks)
		}
		var rolled []string
		for _, entry := range crown.SharedChecks[0].Seeds {
			rolled = append(rolled, entry.SeedID)
		}
		want := []string{plot.Children[0].ID, harvested, innerLeaf}
		slices.Sort(rolled)
		slices.Sort(want)
		if !slices.Equal(rolled, want) {
			t.Errorf("C5's rollup lists %v, want every inheriting seed, harvested included: %v", rolled, want)
		}
	})
}

func TestEachCheckMoveChangesItsStateAndWritesOneCheckNote(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		sessions := spawnCheckSessions(w, app, "planner", "builder", "reviewer")
		plot := checksPlot(t, cli, sessions["planner"])
		seed := plot.Children[0].ID

		steps := []struct {
			session string
			move    protocol.SeedCheckMessage
			state   string
			report  bool
			verdict string
			note    string
		}{
			{sessions["builder"], checkMove(seed, "C1", "report", "wire tests pass"), "reported", true, "", "C1 reported: wire tests pass"},
			{sessions["reviewer"], checkMove(seed, "C1", "confirm", ""), "confirmed", true, "confirmed", "C1 confirmed"},
			{sessions["builder"], checkMove(seed, "C1", "report", "a second run"), "reported", true, "", "C1 reported: a second run"},
			{sessions["reviewer"], checkMove(seed, "C1", "reject", "cite the run on the head"), "rejected", true, "rejected", "C1 rejected: cite the run on the head"},
			{sessions["reviewer"], checkMove(seed, "C1", "uncheck", ""), "reported", true, "", "C1 unchecked"},
			{sessions["reviewer"], checkMove(seed, "C1", "reopen", ""), "open", false, "", "C1 reopened"},
			{sessions["reviewer"], checkMove(seed, "C5", "confirm", "seen it myself"), "confirmed", false, "confirmed", "C5 confirmed, no report: seen it myself"},
			{sessions["reviewer"], checkMove(plot.Children[1].ID, "C5", "reject", "show the run"), "rejected", false, "rejected", "C5 rejected, no report: show the run"},
		}
		for _, step := range steps {
			step.move.SourceSessionID = protocol.Ptr(protocol.SessionID(step.session))
			notesBefore := len(lifeNoteBodies(t, cli, step.move.SeedID))
			moved, err := cli.SeedCheck(step.move)
			if err != nil {
				t.Fatalf("%s %s: %v", step.move.Verb, protocol.Deref(step.move.CheckID), err)
			}
			check := checkByID(moved.Seed.Checks, protocol.Deref(step.move.CheckID))
			verdict := ""
			if check.Verdict != nil {
				verdict = map[bool]string{true: "confirmed", false: "rejected"}[check.Verdict.Confirmed]
			}
			if check.State != step.state || (check.Report != nil) != step.report || verdict != step.verdict {
				t.Errorf("after %s %s: state %q, report %t, verdict %q; want %q, %t, %q",
					step.move.Verb, protocol.Deref(step.move.CheckID), check.State, check.Report != nil, verdict, step.state, step.report, step.verdict)
			}
			bodies := lifeNoteBodies(t, cli, step.move.SeedID)
			if len(bodies) != notesBefore+1 || !slices.Contains(bodies, step.note) || moved.Note.Kind != "check" || moved.Note.Body != step.note {
				t.Errorf("after %s %s the log grew %d with %q (kind %s), want one check note %q",
					step.move.Verb, protocol.Deref(step.move.CheckID), len(bodies)-notesBefore, moved.Note.Body, moved.Note.Kind, step.note)
			}
		}

		_, err := cli.SeedCheck(checkMove(seed, "C1", "uncheck", ""))
		lifeRefusal(t, "uncheck with no verdict", err, "C1", "open", "no verdict")
		_, err = cli.SeedCheck(checkMove(seed, "C1", "reopen", ""))
		lifeRefusal(t, "reopen on an open check", err, "C1", "already open")
		_, err = cli.SeedCheck(checkMove(seed, "C1", "reject", ""))
		lifeRefusal(t, "reject with no reason", err, "what would prove C1")
		_, err = cli.SeedCheck(checkMove(seed, "C9", "report", "x"))
		lifeRefusal(t, "a report on a check the seed does not have", err, "C9", "C1, C5")
	})
}

func TestPlotProgressCountsEveryCheckOnceByState(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		sessions := spawnCheckSessions(w, app, "planner")
		plot := checksPlot(t, cli, sessions["planner"])
		daemonChild, appChild := plot.Children[0].ID, plot.Children[1].ID
		checkMoveOK(t, cli, checkMove(daemonChild, "C1", "report", "pass"))
		checkMoveOK(t, cli, checkMove(daemonChild, "C5", "confirm", ""))
		checkMoveOK(t, cli, checkMove(appChild, "C5", "reject", "no run"))

		progress := checksShow(t, cli, plot.Crown.ID).PlotProgress
		want := protocol.SeedCheckTally{Total: 5, Reported: 1, Confirmed: 1, Rejected: 1}
		if progress == nil || progress.Checks == nil || *progress.Checks != want {
			t.Errorf("plot progress = %+v, want checks %+v", progress, want)
		}
		plain, err := cli.SeedPlot(protocol.SessionID(sessions["planner"]), "", protocol.SeedPlotMessage{Title: "no checks", Children: []protocol.SeedPlotChild{{Title: "a"}}})
		if err != nil {
			t.Fatal(err)
		}
		if p := checksShow(t, cli, plain.Crown.ID).PlotProgress; p == nil || p.Checks != nil {
			t.Errorf("a plot without checks has progress %+v, want no check counts", p)
		}
	})
}

func TestACheckIDNamesOneStatementInAPlotTree(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		sessions := spawnCheckSessions(w, app, "planner")
		plot := checksPlot(t, cli, sessions["planner"])
		appChild := plot.Children[1].ID

		add := checkMove(appChild, "C1", "add", "")
		add.Text = protocol.Ptr("a second C1")
		_, err := cli.SeedCheck(add)
		lifeRefusal(t, "adding an id another seed of the plot uses", err, "C1", plot.Children[0].ID)

		_, err = cli.SeedPlantMessage(protocol.SeedPlantMessage{
			Title: "late child", PartOf: protocol.Ptr(plot.Crown.ID),
			Checks: []protocol.SeedCheckDefinition{{ID: protocol.Ptr("C6"), Text: "again"}},
		})
		lifeRefusal(t, "planting into the plot with a used id", err, "C6", plot.Crown.ID)

		loose, err := cli.SeedPlantMessage(protocol.SeedPlantMessage{
			Title: "loose seed", Checks: []protocol.SeedCheckDefinition{{ID: protocol.Ptr("C2"), Text: "repeats the app child"}},
		})
		if err != nil {
			t.Fatal(err)
		}
		_, err = cli.SeedLink(loose.Seed.ID, "part-of", plot.Crown.ID, false)
		lifeRefusal(t, "linking a seed whose check id the plot uses", err, "C2", appChild)

		checkMoveOK(t, cli, checkMove(appChild, "C2", "report", "frontend test passes"))
		checkMoveOK(t, cli, checkMove(appChild, "C2", "remove", ""))
		readd := checkMove(appChild, "C2", "add", "")
		readd.Text = protocol.Ptr("reused")
		_, err = cli.SeedCheck(readd)
		lifeRefusal(t, "adding a removed id", err, "C2", "restore")
		restored := checkMoveOK(t, cli, checkMove(appChild, "C2", "restore", ""))
		if check := checkByID(restored.Seed.Checks, "C2"); check.Removed || check.State != "reported" || check.Report == nil {
			t.Errorf("restored C2 = %+v, want it back with its report", check)
		}
	})
}

func TestAttnPicksTheNextFreeCheckIDInThePlotTree(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		sessions := spawnCheckSessions(w, app, "planner")
		plot := checksPlot(t, cli, sessions["planner"])
		daemonChild, appChild := plot.Children[0].ID, plot.Children[1].ID

		added := checkMoveOK(t, cli, checkAdd(appChild, "", "Handles empty input"))
		if added.CheckID != "C7" || added.Note.Body != "C7 added: Handles empty input" || checkByID(added.Seed.Checks, "C7").Text != "Handles empty input" {
			t.Errorf("an add without an id = %s with note %q, want C7, one above the plot's C6", added.CheckID, added.Note.Body)
		}
		checkMoveOK(t, cli, checkMove(appChild, "C7", "remove", ""))
		if again := checkMoveOK(t, cli, checkAdd(daemonChild, "", "Survives a restart")); again.CheckID != "C8" {
			t.Errorf("an add after C7 was removed = %s, want C8: a removed id is never reused", again.CheckID)
		}

		_, err := cli.SeedCheck(checkAdd(daemonChild, "C2", "Takes a used id"))
		lifeRefusal(t, "an explicit id another seed uses", err, "C2 is already used by "+appChild, "the next free id is C9", "leave the id out")
		_, err = cli.SeedCheck(checkAdd(daemonChild, "C1", "Takes its own id"))
		lifeRefusal(t, "an explicit id the seed itself uses", err, "already defines C1", "C9")
		if explicit := checkMoveOK(t, cli, checkAdd(daemonChild, "C30", "Named by the caller")); explicit.CheckID != "C30" {
			t.Errorf("an add with a free explicit id = %s, want C30", explicit.CheckID)
		}

		planted, err := cli.SeedPlantMessage(protocol.SeedPlantMessage{
			Title: "late child", PartOf: protocol.Ptr(plot.Crown.ID),
			Checks: []protocol.SeedCheckDefinition{{Text: "Proves C"}, {ID: protocol.Ptr("C40"), Text: "Proves more"}, {Text: "Proves the rest"}},
		})
		if err != nil {
			t.Fatal(err)
		}
		if got := checkIDsFrom(planted.Seed.Checks); !slices.Equal(got, []string{"C41", "C40", "C42", "C5@" + plot.Crown.ID}) {
			t.Errorf("a plant with checks without ids = %v, want C41 and C42 above the plot's highest and the plant's own C40", got)
		}
		_, err = cli.SeedPlantMessage(protocol.SeedPlantMessage{
			Title: "repeating child", PartOf: protocol.Ptr(plot.Crown.ID),
			Checks: []protocol.SeedCheckDefinition{{ID: protocol.Ptr("C6"), Text: "again"}},
		})
		lifeRefusal(t, "a plant with a used id", err, "C6 is already used by "+plot.Crown.ID, "the next free id is C43")

		loose, err := cli.SeedPlantMessage(protocol.SeedPlantMessage{
			Title: "loose seed", Checks: []protocol.SeedCheckDefinition{{Text: "First"}, {Text: "Second"}},
		})
		if err != nil {
			t.Fatal(err)
		}
		if got := checkIDsFrom(loose.Seed.Checks); !slices.Equal(got, []string{"C1", "C2"}) {
			t.Errorf("a plant outside any plot numbers its checks %v, want C1 and C2", got)
		}
		_, err = cli.SeedLink(loose.Seed.ID, "part-of", plot.Crown.ID, false)
		lifeRefusal(t, "a link whose checks collide", err, "check C1", "next free id across both is C43")
	})
}

func TestSimultaneousIDIntroductionsNeverShareAnID(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		cli := w.Client()
		const rounds = 20
		plot, err := cli.SeedPlot("", "", protocol.SeedPlotMessage{
			Title:    "racing plot",
			Children: []protocol.SeedPlotChild{{Title: "left"}, {Title: "right"}},
		})
		if err != nil {
			t.Fatal(err)
		}
		left, right := plot.Children[0].ID, plot.Children[1].ID
		var loose []string
		for i := range rounds {
			planted, err := cli.SeedPlantMessage(protocol.SeedPlantMessage{
				Title:  fmt.Sprintf("loose %d", i),
				Checks: []protocol.SeedCheckDefinition{{ID: protocol.Ptr(fmt.Sprintf("C%d", 900+i%2)), Text: "carried in"}},
			})
			if err != nil {
				t.Fatal(err)
			}
			loose = append(loose, planted.Seed.ID)
		}

		var joinedMu sync.Mutex
		members := []string{plot.Crown.ID, left, right}
		joined := func(id string) {
			joinedMu.Lock()
			members = append(members, id)
			joinedMu.Unlock()
		}
		var moves []func() error
		for i := range rounds {
			moves = append(moves,
				func() error { _, err := cli.SeedCheck(checkAdd(left, "", "left adds")); return err },
				func() error { _, err := cli.SeedCheck(checkAdd(right, "", "right adds")); return err },
				func() error {
					_, err := cli.SeedCheck(checkAdd(left, fmt.Sprintf("C%d", 500+i), "left names"))
					return err
				},
				func() error {
					_, err := cli.SeedCheck(checkAdd(right, fmt.Sprintf("C%d", 500+i), "right names"))
					return err
				},
				func() error {
					planted, err := cli.SeedPlantMessage(protocol.SeedPlantMessage{
						Title: fmt.Sprintf("planted %d", i), PartOf: protocol.Ptr(plot.Crown.ID),
						Checks: []protocol.SeedCheckDefinition{{Text: "planted in"}},
					})
					if err == nil {
						joined(planted.Seed.ID)
					}
					return err
				},
				func() error {
					_, err := cli.SeedLink(loose[i], "part-of", plot.Crown.ID, false)
					if err == nil {
						joined(loose[i])
					}
					return err
				},
			)
		}
		start := make(chan struct{})
		var wg sync.WaitGroup
		for _, move := range moves {
			wg.Go(func() {
				<-start
				if err := move(); err != nil && !strings.Contains(err.Error(), "one id names one statement in a plot tree") {
					t.Errorf("a racing id introduction failed for another reason: %v", err)
				}
			})
		}
		close(start)
		wg.Wait()

		owners := map[string][]string{}
		defined := 0
		for _, id := range members {
			seed := checksShow(t, cli, id)
			for _, check := range seed.Checks {
				if protocol.Deref(check.From) == "" {
					owners[check.ID] = append(owners[check.ID], id)
					defined++
				}
			}
		}
		for checkID, seeds := range owners {
			if len(seeds) > 1 {
				t.Errorf("%s is defined by %v in one plot tree", checkID, seeds)
			}
		}
		if want := 3 * rounds; defined < want {
			t.Errorf("the tree defines %d checks, want at least %d: every add without an id and every plant lands", defined, want)
		}
	})
}

func checkAdd(seedID, checkID, statement string) protocol.SeedCheckMessage {
	msg := checkMove(seedID, checkID, "add", "")
	msg.Text = protocol.Ptr(statement)
	return msg
}

func TestEvidenceNamesASeedArtifactOrAURL(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		sessions := spawnCheckSessions(w, app, "planner")
		seed := checksPlot(t, cli, sessions["planner"]).Children[0].ID
		if _, err := cli.SeedNote(protocol.SessionID(sessions["planner"]), seed, "", "", "attach", false, &protocol.SeedArtifactReference{
			Kind: "markdown_file", Path: protocol.Ptr("docs/proof.md"),
		}); err != nil {
			t.Fatal(err)
		}

		report := checkMove(seed, "C1", "report", "proof")
		report.Evidence = []string{"verify.tar.gz"}
		_, err := cli.SeedCheck(report)
		lifeRefusal(t, "evidence naming no artifact", err, "verify.tar.gz", "docs/proof.md", "attn seed attach")

		report.Evidence = []string{"docs/proof.md", "https://github.com/victorarias/attn/pull/1"}
		report.Commit = protocol.Ptr("da2b3b9c0ffee")
		moved := checkMoveOK(t, cli, report)
		got := checkByID(moved.Seed.Checks, "C1").Report
		if got == nil || len(got.Evidence) != 2 || protocol.Deref(got.Evidence[0].Artifact) != "docs/proof.md" ||
			protocol.Deref(got.Evidence[1].URL) != "https://github.com/victorarias/attn/pull/1" || protocol.Deref(got.Commit) != "da2b3b9c0ffee" {
			t.Errorf("the report = %+v, want the artifact, the URL and the commit as cited", got)
		}
	})
}

func TestEvidenceReplacedAfterItsReportIsMarkedUntilANewReport(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		seedArtifactsNotebook(t, w)
		sessions := spawnCheckSessions(w, app, "planner")
		seed := checksPlot(t, cli, sessions["planner"]).Children[0].ID
		source := seedArtifactsWrite(t, t.TempDir(), "out.txt", []byte("first run"))
		seedArtifactsTransfer(t, cli, seed, "copy", source, "", "")
		const pr = "https://github.com/victorarias/attn/pull/1"
		report := checkMove(seed, "C1", "report", "proof")
		report.Evidence = []string{"out.txt", pr}
		checkMoveOK(t, cli, report)
		marks := func(when string, stampedSize int, want bool) {
			t.Helper()
			document := seedReaderDocument(app, seed)
			if !document.Success {
				t.Fatalf("%s the seed document read failed: %s", when, protocol.Deref(document.Error))
			}
			for reply, seed := range map[string]protocol.Seed{"show": lifeShow(t, cli, seed).Seed, "seed document": document.Document.Seed} {
				evidence := checkByID(seed.Checks, "C1").Report.Evidence
				artifact, url := evidence[0], evidence[1]
				if protocol.Deref(artifact.Size) != stampedSize || protocol.Deref(artifact.ModifiedAt) == "" {
					t.Errorf("%s the %s reply stamps out.txt with %d bytes at %q, want %d bytes and a time", when, reply, protocol.Deref(artifact.Size), protocol.Deref(artifact.ModifiedAt), stampedSize)
				}
				if got := protocol.Deref(artifact.ChangedSinceReport); got != want {
					t.Errorf("%s the %s reply marks out.txt changed=%t, want %t", when, reply, got, want)
				}
				if url.Size != nil || url.ModifiedAt != nil || url.ChangedSinceReport != nil {
					t.Errorf("%s the %s reply stamps the URL evidence %+v, want no stamp", when, reply, url)
				}
			}
		}
		marks("right after the report", len("first run"), false)

		seedArtifactsWrite(t, filepath.Dir(source), "out.txt", []byte("second run, longer"))
		seedArtifactsTransfer(t, cli, seed, "copy", source, "", "")
		marks("after the file is replaced", len("first run"), true)

		checkMoveOK(t, cli, report)
		marks("after a new report", len("second run, longer"), false)
	})
}

func TestACheckLimitRefusalNamesTheLimitAndTheAsk(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		sessions := spawnCheckSessions(w, app, "planner")
		seed := checksPlot(t, cli, sessions["planner"]).Children[0].ID
		report := checkMove(seed, "C1", "report", strings.Repeat("x", 401))
		_, err := cli.SeedCheck(report)
		lifeRefusal(t, "a 401-character summary", err, "max_check_summary_chars=400", "401")
	})
}

func TestARungCheckMoveNamesItsAudienceAndTheMoveInTheInbox(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		sessions := spawnCheckSessions(w, app, "planner", "builder", "reviewer", "watcher")
		plot := checksPlot(t, cli, sessions["planner"])
		seed := plot.Children[0].ID
		gardenNudgeWatch(t, cli, sessions["watcher"], seed, false)

		reject := checkMove(seed, "C1", "reject", "cite the run on the head")
		reject.SourceSessionID = protocol.Ptr(protocol.SessionID(sessions["reviewer"]))
		quiet := checkMoveOK(t, cli, reject)
		w.advance(0)
		gardenNudgeInboxIsEmpty(t, cli, sessions["watcher"], "after a check move without --ring")
		if quiet.RingRecipients != nil {
			t.Errorf("a move without --ring names recipients %v, want none", quiet.RingRecipients)
		}

		gardenNudgeWatch(t, cli, sessions["reviewer"], seed, false)
		reject.Ring = protocol.Ptr(true)
		reject.Member = protocol.Ptr("keel")
		rung := checkMoveOK(t, cli, reject)
		w.advance(0)
		if !slices.Equal(rung.RingRecipients, []string{sessions["watcher"]}) {
			t.Errorf("the rung move's recipients = %v, want the watcher and not the reviewer who made it", rung.RingRecipients)
		}
		gardenNudgeOneBell(t, cli, sessions["watcher"], seed, "check C1 rejected by keel")
		gardenNudgeInboxIsEmpty(t, cli, sessions["reviewer"], "after its own rung move as a member")

		lonely := plot.Children[1].ID
		gardenNudgeMove(t, cli, sessions["builder"], lonely, "harvest")
		lonelyReject := checkMove(lonely, "C2", "reject", "no recording")
		lonelyReject.SourceSessionID = protocol.Ptr(protocol.SessionID(sessions["reviewer"]))
		lonelyReject.Ring = protocol.Ptr(true)
		if got := checkMoveOK(t, cli, lonelyReject).RingRecipients; len(got) != 0 {
			t.Errorf("a rung move on a harvested seed with no watcher names %v, want nobody", got)
		}

		noted, err := cli.SeedNote(protocol.SessionID(sessions["reviewer"]), seed, "please look", "", "", true, nil)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(noted.RingRecipients, []string{sessions["watcher"]}) {
			t.Errorf("a rung note's recipients = %v, want the watcher", noted.RingRecipients)
		}
	})
}

func TestAPlainNoteCitingACheckSaysSo(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		sessions := spawnCheckSessions(w, app, "planner")
		seed := checksPlot(t, cli, sessions["planner"]).Children[0].ID
		cited, err := cli.SeedNote(protocol.SessionID(sessions["planner"]), seed, "C1 done and C5 too; C7 is elsewhere", "", "", false, nil)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(cited.CitedChecks, []string{"C1", "C5"}) {
			t.Errorf("the note cites %v, want the effective C1 and C5", cited.CitedChecks)
		}
		other, err := cli.SeedNote(protocol.SessionID(sessions["planner"]), seed, "C6 is the plot's, not this seed's", "", "", false, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(other.CitedChecks) != 0 {
			t.Errorf("a note citing a check the seed does not prove cites %v, want none", other.CitedChecks)
		}
		_, err = cli.SeedNote(protocol.SessionID(sessions["planner"]), seed, "C1 confirmed", "", "check", false, nil)
		lifeRefusal(t, "seed_note with the check kind", err, "attn seed check")
	})
}

func TestAReportReachesTheAppWithTheSeedAndItsPlot(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		sessions := spawnCheckSessions(w, app, "planner")
		plot := checksPlot(t, cli, sessions["planner"])
		bystander := plantSeedAs(t, cli, sessions["planner"], "no checks here")
		viewer := w.App()
		before := map[string]protocol.Seed{}
		for _, seed := range viewer.Initial.Seeds {
			before[seed.ID] = seed
		}
		if seed := before[bystander]; seed.Checks != nil || seed.SharedChecks != nil {
			t.Errorf("a seed without checks arrives with checks %+v and shared %+v, want neither", seed.Checks, seed.SharedChecks)
		}
		raw, err := json.Marshal(before[bystander])
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), `checks":`) {
			t.Errorf("a seed without checks encodes %s, want no checks keys", raw)
		}

		checkMoveOK(t, cli, checkMove(plot.Children[0].ID, "C5", "report", "green"))
		changed := testworld.Await(viewer, protocol.EventGardenSeedsChanged, func(m protocol.GardenSeedsChangedMessage) bool {
			return len(m.Seeds) > 0
		})
		seeds := map[string]protocol.Seed{}
		for _, seed := range changed.Seeds {
			seeds[seed.ID] = seed
		}
		if _, ok := seeds[bystander]; ok || len(seeds) != 2 {
			t.Errorf("the push after a report carries %v, want the reported seed and its plot", lifeSeedIDs(changed.Seeds))
		}
		if check := checkByID(seeds[plot.Children[0].ID].Checks, "C5"); check.State != "reported" {
			t.Errorf("the reported seed arrives with C5 %q, want reported", check.State)
		}
		crown := seeds[plot.Crown.ID]
		if crown.PlotProgress == nil || crown.PlotProgress.Checks == nil || crown.PlotProgress.Checks.Reported != 1 {
			t.Errorf("the plot arrives with progress %+v, want one reported check", crown.PlotProgress)
		}
		if len(crown.SharedChecks) != 1 || rollupState(crown.SharedChecks[0], plot.Children[0].ID) != "reported" {
			t.Errorf("the plot arrives with shared checks %+v, want C5 reported by the child", crown.SharedChecks)
		}
	})
}

func TestHarvestIgnoresOpenChecksAndCarriesThem(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		sessions := spawnCheckSessions(w, app, "planner", "builder")
		seed := checksPlot(t, cli, sessions["planner"]).Children[0].ID
		harvested := gardenNudgeMove(t, cli, sessions["builder"], seed, "harvest")
		if harvested.Seed.Status != "harvested" || len(harvested.Seed.Checks) != 2 || checkByID(harvested.Seed.Checks, "C1").State != "open" {
			t.Errorf("harvest with open checks = %s with checks %+v, want harvested carrying both open checks", harvested.Seed.Status, harvested.Seed.Checks)
		}
	})
}

func spawnCheckSessions(w *world, app *testworld.Peer, names ...string) map[string]string {
	sessions := map[string]string{}
	for _, name := range names {
		sessions[name] = w.Spawn(app, fakeagent.Claude, w.Path(name))
	}
	return sessions
}

func checksPlot(t *testing.T, cli *client.Client, planner string) *protocol.SeedPlotResult {
	t.Helper()
	planted, err := cli.SeedPlot(protocol.SessionID(planner), "", protocol.SeedPlotMessage{
		Title: "search moves to the daemon",
		Checks: []protocol.SeedCheckDefinition{
			{ID: protocol.Ptr("C5"), Text: "The pull request is green on CI.", Shared: protocol.Ptr(true)},
			{ID: protocol.Ptr("C6"), Text: "Search behaves the same in the packaged app."},
		},
		Children: []protocol.SeedPlotChild{
			{Title: "daemon search endpoint", Checks: []protocol.SeedCheckDefinition{{ID: protocol.Ptr("C1"), Text: "Wire tests cover the queries."}}},
			{Title: "app calls the endpoint", Checks: []protocol.SeedCheckDefinition{{ID: protocol.Ptr("C2"), Text: "No stale results."}}},
		},
	})
	if err != nil {
		t.Fatalf("plot with checks: %v", err)
	}
	return planted
}

func checkMove(seedID, checkID, verb, summary string) protocol.SeedCheckMessage {
	msg := protocol.SeedCheckMessage{SeedID: seedID, Verb: verb}
	if checkID != "" {
		msg.CheckID = protocol.Ptr(checkID)
	}
	if summary != "" {
		msg.Summary = protocol.Ptr(summary)
	}
	return msg
}

func checkMoveOK(t *testing.T, cli *client.Client, msg protocol.SeedCheckMessage) *protocol.SeedCheckResult {
	t.Helper()
	moved, err := cli.SeedCheck(msg)
	if err != nil {
		t.Fatalf("%s %s on %s: %v", msg.Verb, protocol.Deref(msg.CheckID), msg.SeedID, err)
	}
	return moved
}

func checksShow(t *testing.T, cli *client.Client, seedID string) protocol.Seed {
	t.Helper()
	return lifeShow(t, cli, seedID).Seed
}

func checkByID(checks []protocol.SeedCheck, id string) protocol.SeedCheck {
	for _, check := range checks {
		if check.ID == id {
			return check
		}
	}
	return protocol.SeedCheck{}
}

func checkIDsFrom(checks []protocol.SeedCheck) []string {
	out := make([]string, 0, len(checks))
	for _, check := range checks {
		if from := protocol.Deref(check.From); from != "" {
			out = append(out, check.ID+"@"+from)
		} else {
			out = append(out, check.ID)
		}
	}
	return out
}

func rollupState(shared protocol.SeedSharedCheck, seedID string) string {
	for _, entry := range shared.Seeds {
		if entry.SeedID == seedID {
			return entry.State
		}
	}
	return ""
}

func TestTheAppConfirmsACheckOverTheWebSocket(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		sessions := spawnCheckSessions(w, app, "planner")
		seed := checksPlot(t, cli, sessions["planner"]).Children[0].ID

		confirmed := testworld.Request(app, protocol.SeedCheckMessage{
			Cmd: protocol.CmdSeedCheck, RequestID: protocol.Ptr("confirm"), SeedID: seed, CheckID: protocol.Ptr("C1"), Verb: "confirm",
		}, protocol.EventSeedCheckResult, func(m protocol.SeedCheckResultMessage) bool { return m.RequestID == "confirm" })
		if !confirmed.Success || confirmed.Seed == nil || checkByID(confirmed.Seed.Checks, "C1").State != "confirmed" {
			t.Fatalf("the app's confirm = %+v (error %q), want C1 confirmed on the returned seed", confirmed.Seed, protocol.Deref(confirmed.Error))
		}
		refused := testworld.Request(app, protocol.SeedCheckMessage{
			Cmd: protocol.CmdSeedCheck, RequestID: protocol.Ptr("reopen"), SeedID: seed, CheckID: protocol.Ptr("C5"), Verb: "reopen",
		}, protocol.EventSeedCheckResult, func(m protocol.SeedCheckResultMessage) bool { return m.RequestID == "reopen" })
		if refused.Success || !strings.Contains(protocol.Deref(refused.Error), "already open") {
			t.Errorf("the app's reopen of an open check = success %t, error %q; want a refusal naming its state", refused.Success, protocol.Deref(refused.Error))
		}
	})
}

func TestAnInheritedResultAnswersOnlyThePlotThatDefinedIt(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		sessions := spawnCheckSessions(w, app, "planner")
		first := checksPlot(t, cli, sessions["planner"])
		leaf := first.Children[1].ID
		checkMoveOK(t, cli, checkMove(leaf, "C5", "confirm", "seen it"))

		other, err := cli.SeedPlantMessage(protocol.SeedPlantMessage{
			Title: "another plot", Checks: []protocol.SeedCheckDefinition{{ID: protocol.Ptr("C5"), Text: "A different statement.", Shared: protocol.Ptr(true)}},
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := cli.SeedLink(leaf, "part-of", first.Crown.ID, true); err != nil {
			t.Fatal(err)
		}
		edgeLink(t, cli, leaf, "part-of", other.Seed.ID)
		if check := checkByID(checksShow(t, cli, leaf).Checks, "C5"); check.State != "open" || protocol.Deref(check.From) != other.Seed.ID {
			t.Errorf("under a plot with a different C5 the leaf's C5 = %+v, want it open, from the new plot", check)
		}

		if _, err := cli.SeedLink(leaf, "part-of", other.Seed.ID, true); err != nil {
			t.Fatal(err)
		}
		edgeLink(t, cli, leaf, "part-of", first.Crown.ID)
		if check := checkByID(checksShow(t, cli, leaf).Checks, "C5"); check.State != "confirmed" {
			t.Errorf("back under its first plot the leaf's C5 is %q, want its confirmation back", check.State)
		}
	})
}
