package daemon_test

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/victorarias/attn/internal/github/mockserver"
	"github.com/victorarias/attn/internal/protocol"
)

func TestAnUnchangedPullRequestIsReadAgainForFreeAndAChangedOneInFull(t *testing.T) {
	gh := mockserver.New()
	t.Cleanup(gh.Close)
	gh.Config.SetKeepAlivesEnabled(false)
	checkout := mockserver.MockPR{Repo: "acme/shop", Number: 1, Title: "Add checkout", Role: "author"}
	cart := mockserver.MockPR{Repo: "acme/shop", Number: 2, Title: "Fix the cart", Role: "author"}
	landing := mockserver.MockPR{Repo: "acme/shop", Number: 3, Title: "Land the release"}
	gh.AddPR(checkout)
	gh.AddPR(cart)
	gh.AddPR(landing)
	t.Setenv("ATTN_MOCK_GH_URL", gh.URL)
	t.Setenv("ATTN_MOCK_GH_TOKEN", "test-token")
	t.Setenv("ATTN_MOCK_GH_HOST", "github.test")

	inBubble(t, func(t *testing.T, w *world) {
		app := w.App()
		cli := w.Client()
		refreshPRs(t, app)
		dashboard := queryPRs(t, cli, "")
		checkoutID, cartID := prNumbered(t, dashboard, 1).ID, prNumbered(t, dashboard, 2).ID
		fetchPRDetails(t, app, checkoutID)
		fetchPRDetails(t, app, cartID)
		synctest.Wait()

		unchanged := len(gh.Requests())
		fetchPRDetails(t, app, checkoutID)
		fetchPRDetails(t, app, cartID)
		synctest.Wait()
		statuses := pullRequestReadStatuses(gh.Requests()[unchanged:])
		for _, path := range []string{"/repos/acme/shop/pulls/1", "/repos/acme/shop/pulls/1/reviews", "/repos/acme/shop/pulls/2", "/repos/acme/shop/pulls/2/reviews"} {
			if !onlyStatus(statuses[path], http.StatusNotModified) {
				t.Errorf("an unchanged second read of %s answered %v, want only 304", path, statuses[path])
			}
		}
		for _, number := range []int{1, 2} {
			if pr := prNumbered(t, queryPRs(t, cli, ""), number); !hasFetchedDetails(pr) {
				t.Errorf("after reads answered 304, #%d has CI %q, mergeable %q, head %q; want the details GitHub sent first",
					number, protocol.Deref(pr.CIStatus), protocol.Deref(pr.MergeableState), protocol.Deref(pr.HeadSHA))
			}
		}

		gh.SetMergeableState("acme/shop", 1, "unstable")
		changed := len(gh.Requests())
		fetchPRDetails(t, app, checkoutID)
		fetchPRDetails(t, app, cartID)
		synctest.Wait()
		statuses = pullRequestReadStatuses(gh.Requests()[changed:])
		if got := statuses["/repos/acme/shop/pulls/1"]; len(got) == 0 || got[len(got)-1] != http.StatusOK {
			t.Errorf("a read of the changed #1 answered %v, want it to end with a 200", got)
		}
		if got := statuses["/repos/acme/shop/pulls/2"]; !onlyStatus(got, http.StatusNotModified) {
			t.Errorf("reads of the unchanged #2 answered %v, want only 304", got)
		}
		after := queryPRs(t, cli, "")
		if ci := protocol.Deref(prNumbered(t, after, 1).CIStatus); ci != "pending" {
			t.Errorf("#1 after its change has CI %q, want pending", ci)
		}
		if pr := prNumbered(t, after, 2); !hasFetchedDetails(pr) {
			t.Errorf("#2 after #1 changed has CI %q, head %q; want its own details kept", protocol.Deref(pr.CIStatus), protocol.Deref(pr.HeadSHA))
		}

		registerSessions(t, w, cli, "shipper")
		url := "https://github.test/acme/shop/pull/3"
		if err := cli.RecordPullRequestCreated("shipper", url); err != nil {
			t.Fatal(err)
		}
		armed := plantSeedAs(t, cli, "shipper", "ships with the release")
		harvestArm(t, cli, "shipper", armed, url)
		w.advance(protocol.HeatHotInterval)
		beforeMerge := len(gh.Requests())
		w.advance(protocol.HeatHotInterval)
		if got := pullRequestReadStatuses(gh.Requests()[beforeMerge:])["/repos/acme/shop/pulls/3"]; !onlyStatus(got, http.StatusNotModified) {
			t.Fatalf("refreshes of the unchanged session pull request answered %v, want only 304", got)
		}
		if status := lifeShow(t, cli, armed).Seed.Status; status == "harvested" {
			t.Fatal("the seed harvested before its pull request merged")
		}

		gh.Merge("acme/shop", 3)
		w.advance(protocol.HeatHotInterval)
		if got := lifeShow(t, cli, armed).Seed; got.Status != "harvested" || protocol.Deref(got.Reason) != "PR #3 merged: Land the release" {
			t.Errorf("after the merge the armed seed is %s with reason %q, want it harvested by the merge", got.Status, protocol.Deref(got.Reason))
		}
	})
}

func pullRequestReadStatuses(requests []mockserver.RequestLog) map[string][]int {
	statuses := map[string][]int{}
	for _, request := range requests {
		if request.Method == http.MethodGet {
			statuses[request.Path] = append(statuses[request.Path], request.Status)
		}
	}
	return statuses
}

func onlyStatus(statuses []int, want int) bool {
	for _, status := range statuses {
		if status != want {
			return false
		}
	}
	return len(statuses) > 0
}

func TestAHostKeepsItsReadsAndSpendWindowAcrossTheHostRefresh(t *testing.T) {
	gh := mockserver.New()
	t.Cleanup(gh.Close)
	gh.Config.SetKeepAlivesEnabled(false)
	gh.AddPR(mockserver.MockPR{Repo: "acme/shop", Number: 1, Title: "Add checkout", Role: "author"})
	t.Setenv("ATTN_MOCK_GH_URL", gh.URL)
	t.Setenv("ATTN_MOCK_GH_TOKEN", "test-token")
	t.Setenv("ATTN_MOCK_GH_HOST", "github.test")

	inBubble(t, func(t *testing.T, w *world) {
		app := w.App()
		firstWindowEnd := time.Now().Add(time.Hour).Truncate(time.Second)
		gh.StartRateLimitWindow(firstWindowEnd)
		refreshPRs(t, app)
		checkoutID := prNumbered(t, queryPRs(t, w.Client(), ""), 1).ID
		fetchPRDetails(t, app, checkoutID)
		synctest.Wait()

		beforeHostRefresh := len(gh.Requests())
		w.advance(5*time.Minute + time.Second)
		gh.StartRateLimitWindow(firstWindowEnd.Add(time.Hour))
		fetchPRDetails(t, app, checkoutID)
		synctest.Wait()

		statuses := pullRequestReadStatuses(gh.Requests()[beforeHostRefresh:])
		for _, path := range []string{"/repos/acme/shop/pulls/1", "/repos/acme/shop/pulls/1/reviews"} {
			if !onlyStatus(statuses[path], http.StatusNotModified) {
				t.Errorf("reads of the unchanged %s after the host refresh answered %v, want only 304", path, statuses[path])
			}
		}
		log, err := os.ReadFile(filepath.Join(w.Dir, "daemon.log"))
		if err != nil {
			t.Fatal(err)
		}
		closed := "GitHub spend: host=github.test resource=core window_end=" + firstWindowEnd.Format(time.RFC3339)
		if !strings.Contains(string(log), closed) {
			t.Errorf("daemon.log has no spend line for the window that closed after the host refresh; want one starting %q", closed)
		}
	})
}
