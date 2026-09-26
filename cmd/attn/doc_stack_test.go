package main_test

import (
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/testworld"
)

type docDelivery struct {
	Documents []struct {
		ID string `json:"id"`
	} `json:"documents"`
	Changed []string `json:"changed"`
}

func queriedIDs(t *testing.T, s *testworld.Stack, args ...string) []string {
	t.Helper()
	var docs []struct {
		ID string `json:"id"`
	}
	result := s.Attn(append([]string{"doc", "query", "app/test", "requests"}, append(args, "--json")...)...)
	if result.Code != 0 {
		t.Fatalf("doc query %q exited %d: %s", args, result.Code, result.Stderr)
	}
	result.JSON(t, &docs)
	ids := []string{}
	for _, doc := range docs {
		ids = append(ids, doc.ID)
	}
	return ids
}

func putRequest(t *testing.T, s *testworld.Stack, id, body string, flags ...string) testworld.Result {
	t.Helper()
	return s.Attn(append([]string{"doc", "put", "app/test", "requests", id, body}, flags...)...)
}

func TestDocQueryPutAndWatchHonourTheirFlags(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t)

	for _, tc := range []struct {
		args []string
		want string
	}{
		{args: []string{"query", "app/test", "requests", "--resume"}, want: "--resume is only for watch"},
		{args: []string{"query", "app/test", "requests", "--desc"}, want: "--desc needs --sort <field>"},
		{args: []string{"count", "app/test", "requests", "--where", "status"}, want: "needs one of = < <= > >="},
	} {
		if refused := s.Attn(append([]string{"doc"}, tc.args...)...); refused.Code == 0 || !strings.Contains(refused.Stderr, tc.want) {
			t.Errorf("doc %q exited %d with %q, want a refusal saying %q", tc.args, refused.Code, refused.Stderr, tc.want)
		}
	}

	s.Start()
	if defined := s.Attn("doc", "define", "app/test", "requests", "status:string", "attempts:number"); defined.Code != 0 {
		t.Fatalf("doc define exited %d: %s", defined.Code, defined.Stderr)
	}
	for _, doc := range []struct {
		id, status string
		attempts   int
	}{
		{"a", "pending", 1}, {"b", "pending", 5}, {"c", "done", 3}, {"d", "pending", 4},
	} {
		if put := putRequest(t, s, doc.id, fmt.Sprintf(`{"status":%q,"attempts":%d}`, doc.status, doc.attempts)); put.Code != 0 {
			t.Fatalf("doc put %s exited %d: %s", doc.id, put.Code, put.Stderr)
		}
	}

	for _, tc := range []struct {
		args []string
		want []string
	}{
		{args: []string{"--where", "status=pending", "--sort", "attempts", "--desc", "--limit", "2"}, want: []string{"b", "d"}},
		{args: []string{"--desc", "--sort", "attempts"}, want: []string{"b", "d", "c", "a"}},
		{args: []string{"--where", "status=pending", "--where", "attempts>=4", "--sort", "attempts"}, want: []string{"d", "b"}},
		{args: []string{"--sort", "attempts", "--after", "c"}, want: []string{"d", "b"}},
	} {
		if got := queriedIDs(t, s, tc.args...); !slices.Equal(got, tc.want) {
			t.Errorf("doc query %q = %q, want %q", tc.args, got, tc.want)
		}
	}

	if put := putRequest(t, s, "a", `{"status":"done","attempts":1}`, "--expect", "1"); put.Code != 0 || !strings.Contains(put.Stdout, "rev 2") {
		t.Errorf("a put expecting the current revision exited %d with %q, want rev 2", put.Code, put.Stdout+put.Stderr)
	}
	if stale := putRequest(t, s, "a", `{"status":"lost"}`, "--expect", "1"); stale.Code == 0 || !strings.Contains(stale.Stderr, "expected rev 1 but is at rev 2") {
		t.Errorf("a put expecting a moved revision exited %d with %q, want a refusal naming revisions 1 and 2", stale.Code, stale.Stderr)
	}
	if taken := putRequest(t, s, "a", `{"status":"lost"}`, "--expect", "absent"); taken.Code == 0 || !strings.Contains(taken.Stderr, "already exists at rev 2") {
		t.Errorf("--expect absent on an existing document exited %d with %q, want a refusal naming its revision", taken.Code, taken.Stdout+taken.Stderr)
	}
	if fresh := putRequest(t, s, "e", `{"status":"pending","attempts":2}`, "--expect", "absent"); fresh.Code != 0 || !strings.Contains(fresh.Stdout, "rev 1") {
		t.Errorf("--expect absent on a new document exited %d with %q, want rev 1", fresh.Code, fresh.Stdout+fresh.Stderr)
	}
	if forced := putRequest(t, s, "a", `{"status":"forced","attempts":1}`); forced.Code != 0 || !strings.Contains(forced.Stdout, "rev 3") {
		t.Errorf("a put without --expect exited %d with %q, want it to win at rev 3", forced.Code, forced.Stdout+forced.Stderr)
	}
	if got := s.Attn("doc", "get", "app/test", "requests", "a").Stdout; !strings.Contains(got, "forced") {
		t.Errorf("doc get a = %q, want the unconditional write", got)
	}

	watch := s.Launch(testworld.Invocation{Args: []string{"doc", "watch", "app/test", "requests", "--where", "status=pending", "--json", "--resume"}})
	watch.AwaitStdout(`"e"`)
	s.Stop()
	s.Start()
	if put := putRequest(t, s, "f", `{"status":"pending","attempts":6}`); put.Code != 0 {
		t.Fatalf("doc put f after the restart exited %d: %s", put.Code, put.Stderr)
	}
	watch.AwaitStdout(`"f"`)
	if undefined := s.Attn("doc", "undefine", "app/test", "requests"); undefined.Code != 0 {
		t.Fatalf("doc undefine exited %d: %s", undefined.Code, undefined.Stderr)
	}
	ended := watch.Wait()
	if ended.Code == 0 {
		t.Errorf("a watch whose collection was removed exited 0; it must not look like a watch that finished")
	}
	decoder := json.NewDecoder(strings.NewReader(ended.Stdout))
	for {
		var delivery docDelivery
		if err := decoder.Decode(&delivery); err == io.EOF {
			t.Fatalf("no delivery after the restart carried f:\n%s", ended.Stdout)
		} else if err != nil {
			t.Fatalf("decode the watch output: %v\n%s", err, ended.Stdout)
		}
		var ids []string
		for _, doc := range delivery.Documents {
			ids = append(ids, doc.ID)
		}
		if !slices.Contains(ids, "f") {
			continue
		}
		slices.Sort(ids)
		if !slices.Equal(ids, []string{"b", "d", "e", "f"}) {
			t.Errorf("the window after the restart holds %q, want the pending b, d, e and f", ids)
		}
		if !slices.Equal(delivery.Changed, []string{"f"}) {
			t.Errorf("the resumed watch was sent %q, want only f, the one body it did not already hold", delivery.Changed)
		}
		break
	}
}
