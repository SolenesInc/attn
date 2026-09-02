package store

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/launchcontract"
	"github.com/victorarias/attn/internal/protocol"
)

func TestStore_AddAndGet(t *testing.T) {
	s := New()

	session := &protocol.Session{
		ID:         "abc123",
		Label:      "drumstick",
		Directory:  "/home/user/project",
		State:      protocol.SessionStateWorking,
		StateSince: protocol.TimestampNow().String(),
		LastSeen:   protocol.TimestampNow().String(),
	}

	s.Add(session)

	got := s.Get("abc123")
	if got == nil {
		t.Fatal("expected session, got nil")
	}
	if got.Label != "drumstick" {
		t.Errorf("Label = %q, want %q", got.Label, "drumstick")
	}
	if got.Agent != "codex" {
		t.Errorf("Agent = %q, want %q", got.Agent, "codex")
	}
}

func TestStore_MarkModelRequestStartedIsMonotonicAndIndependentOfState(t *testing.T) {
	for _, tc := range []struct {
		name string
		open func(*testing.T) *Store
	}{
		{name: "memory", open: func(t *testing.T) *Store { return New() }},
		{name: "sqlite", open: func(t *testing.T) *Store {
			s, err := NewWithDB(filepath.Join(t.TempDir(), "test.db"))
			if err != nil {
				t.Fatalf("NewWithDB: %v", err)
			}
			return s
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := tc.open(t)
			defer s.Close()
			observedAt := time.Date(2026, 8, 23, 9, 0, 0, 0, time.UTC)
			s.Add(&protocol.Session{
				ID: "session", State: protocol.SessionStateWaitingInput,
				StateSince: string(protocol.NewTimestamp(observedAt)), StateUpdatedAt: string(protocol.NewTimestamp(observedAt)),
			})
			requestAt := observedAt.Add(40 * time.Minute)
			if !s.MarkModelRequestStarted("session", requestAt) {
				t.Fatal("newer request receipt was refused")
			}
			if s.MarkModelRequestStarted("session", observedAt.Add(20*time.Minute)) {
				t.Fatal("older request receipt moved the clock backwards")
			}
			got := s.Get("session")
			if got == nil || !protocol.Timestamp(protocol.Deref(got.LastModelRequestAt)).Time().Equal(requestAt) {
				t.Fatalf("last_model_request_at = %v, want %s", got, requestAt)
			}
			if !protocol.Timestamp(got.StateUpdatedAt).Time().Equal(observedAt) {
				t.Fatalf("state_updated_at = %s, want unchanged %s", got.StateUpdatedAt, observedAt)
			}
		})
	}
}

func TestStore_AddAndGet_PreservesAgent(t *testing.T) {
	s := New()

	session := &protocol.Session{
		ID:         "agent123",
		Label:      "session-with-agent",
		Agent:      "claude",
		Directory:  "/home/user/project",
		State:      protocol.SessionStateWorking,
		StateSince: protocol.TimestampNow().String(),
		LastSeen:   protocol.TimestampNow().String(),
	}

	s.Add(session)

	got := s.Get("agent123")
	if got == nil {
		t.Fatal("expected session, got nil")
	}
	if got.Agent != "claude" {
		t.Errorf("Agent = %q, want %q", got.Agent, "claude")
	}
}

func TestStore_AddAndGet_PreservesShellAgent(t *testing.T) {
	s := New()

	session := &protocol.Session{
		ID:         "shell123",
		Label:      "shell-session",
		Agent:      protocol.SessionAgentShell,
		Directory:  "/home/user/project",
		State:      protocol.SessionStateWorking,
		StateSince: protocol.TimestampNow().String(),
		LastSeen:   protocol.TimestampNow().String(),
	}

	s.Add(session)

	got := s.Get("shell123")
	if got == nil {
		t.Fatal("expected session, got nil")
	}
	if got.Agent != protocol.SessionAgentShell {
		t.Errorf("Agent = %q, want %q", got.Agent, protocol.SessionAgentShell)
	}
}

func TestStore_AddAndGet_PreservesExternalPluginAgentAndMetadata(t *testing.T) {
	s := New()
	session := &protocol.Session{
		ID:         "plugin123",
		Label:      "snipe session",
		Agent:      "snipe",
		Directory:  "/home/user/project",
		State:      protocol.SessionStateWorking,
		StateSince: protocol.TimestampNow().String(),
		LastSeen:   protocol.TimestampNow().String(),
	}

	s.Add(session)
	if !s.BeginAgentDriverRun(session.ID, "snipe-plugin", "run-metadata") {
		t.Fatal("BeginAgentDriverRun() = false, want true")
	}
	if !s.ApplyAgentDriverMetadata(session.ID, "run-metadata", 1, `{"snipe_session_id":"abc"}`) {
		t.Fatal("ApplyAgentDriverMetadata() rejected current run sequence")
	}
	session.State = protocol.SessionStateIdle
	s.Add(session)

	got := s.Get(session.ID)
	if got == nil || got.Agent != "snipe" {
		t.Fatalf("Agent = %q, want %q", got.Agent, "snipe")
	}
	if metadata := s.GetAgentMetadata(session.ID); metadata != `{"snipe_session_id":"abc"}` {
		t.Fatalf("metadata = %q, want stored plugin JSON", metadata)
	}
}

func TestStore_AgentDriverMetadataFallbackRetainsOrderedReport(t *testing.T) {
	s := &Store{
		sessions:        make(map[string]*protocol.Session),
		agentDriverRuns: make(map[string]AgentDriverReportCursor),
		agentMetadata:   make(map[string]string),
	}
	s.Add(&protocol.Session{ID: "plugin-fallback", Agent: "snipe"})
	if !s.BeginAgentDriverRun("plugin-fallback", "snipe-plugin", "run-fallback") {
		t.Fatal("BeginAgentDriverRun() = false, want true")
	}
	if !s.ApplyAgentDriverMetadata("plugin-fallback", "run-fallback", 1, `{"native_id":"fallback"}`) {
		t.Fatal("ApplyAgentDriverMetadata() rejected fallback store report")
	}
	if got := s.GetAgentMetadata("plugin-fallback"); got != `{"native_id":"fallback"}` {
		t.Fatalf("GetAgentMetadata()=%q, want retained fallback metadata", got)
	}
}

func TestStore_AgentDriverRunRejectsWrongRunAndStaleSequence(t *testing.T) {
	s := New()
	now := protocol.TimestampNow().String()
	s.Add(&protocol.Session{
		ID:             "plugin-run",
		Label:          "snipe session",
		Agent:          "snipe",
		Directory:      "/home/user/project",
		State:          protocol.SessionStateLaunching,
		StateSince:     now,
		StateUpdatedAt: now,
		LastSeen:       now,
	})

	if !s.BeginAgentDriverRun("plugin-run", "snipe-plugin", "run-a") {
		t.Fatal("BeginAgentDriverRun() = false, want true")
	}
	if cursor := s.GetAgentDriverRun("plugin-run"); cursor.PluginName != "snipe-plugin" || cursor.RunID != "run-a" {
		t.Fatalf("GetAgentDriverRun()=%+v, want snipe-plugin/run-a", cursor)
	}
	if !s.ApplyAgentDriverState("plugin-run", "run-a", 2, protocol.StateWorking, time.Time{}) {
		t.Fatal("ApplyAgentDriverState() rejected current run sequence")
	}
	if s.ApplyAgentDriverState("plugin-run", "run-a", 1, protocol.StateIdle, time.Time{}) {
		t.Fatal("ApplyAgentDriverState() accepted stale sequence")
	}
	if s.ApplyAgentDriverState("plugin-run", "run-b", 3, protocol.StateIdle, time.Time{}) {
		t.Fatal("ApplyAgentDriverState() accepted wrong run")
	}
	if got := s.Get("plugin-run").State; got != protocol.SessionStateWorking {
		t.Fatalf("state=%q, want working", got)
	}

	if ended := s.EndAgentDriverRun("plugin-run"); ended.PluginName != "snipe-plugin" || ended.RunID != "run-a" {
		t.Fatalf("EndAgentDriverRun()=%+v, want snipe-plugin/run-a", ended)
	}
	if s.ApplyAgentDriverState("plugin-run", "run-a", 3, protocol.StateIdle, time.Time{}) {
		t.Fatal("ApplyAgentDriverState() accepted report after run ended")
	}
}

func TestStore_ListAgentDriverRunsFiltersByOwnerAndIncludesMetadata(t *testing.T) {
	s := New()
	for _, sessionID := range []string{"session-b", "session-a", "other"} {
		s.Add(&protocol.Session{ID: sessionID, Agent: "external"})
	}
	if !s.BeginAgentDriverRun("session-b", "attn-example", "run-b") ||
		!s.BeginAgentDriverRun("session-a", "attn-example", "run-a") ||
		!s.BeginAgentDriverRun("other", "other-plugin", "run-other") {
		t.Fatal("BeginAgentDriverRun failed")
	}
	if !s.ApplyAgentDriverMetadata("session-a", "run-a", 1, `{"native":"one"}`) {
		t.Fatal("ApplyAgentDriverMetadata failed")
	}
	if !s.ApplyAgentDriverState("session-a", "run-a", 4, protocol.StateWorking, time.Time{}) {
		t.Fatal("ApplyAgentDriverState failed")
	}

	// Seq is the run's report cursor: a driver process that replaces the one which opened the
	// run has to continue from it, or every report it makes is discarded as stale.
	if got := s.ListAgentDriverRuns("attn-example"); !reflect.DeepEqual(got, []ActiveAgentDriverRun{
		{SessionID: "session-a", RunID: "run-a", Metadata: `{"native":"one"}`, Seq: 4},
		{SessionID: "session-b", RunID: "run-b"},
	}) {
		t.Fatalf("ListAgentDriverRuns()=%+v", got)
	}
}

func TestStore_ListActiveAgentDriverRunsNamesTheOwnerOfEveryRun(t *testing.T) {
	s, err := NewWithDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewWithDB: %v", err)
	}
	defer s.Close()

	for _, sessionID := range []string{"session-a", "other"} {
		s.Add(&protocol.Session{ID: sessionID, Agent: "external"})
	}
	if !s.BeginAgentDriverRun("session-a", "attn-example", "run-a") ||
		!s.BeginAgentDriverRun("other", "other-plugin", "run-other") {
		t.Fatal("BeginAgentDriverRun failed")
	}

	if got := s.ListActiveAgentDriverRuns(); !reflect.DeepEqual(got, []ActiveAgentDriverRun{
		{SessionID: "other", RunID: "run-other", PluginName: "other-plugin"},
		{SessionID: "session-a", RunID: "run-a", PluginName: "attn-example"},
	}) {
		t.Fatalf("ListActiveAgentDriverRuns()=%+v", got)
	}
}

func TestStore_ListAgentDriverRunsCarriesThePersistedReportCursor(t *testing.T) {
	s, err := NewWithDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewWithDB: %v", err)
	}
	defer s.Close()

	s.Add(&protocol.Session{ID: "session-a", Agent: "external"})
	if !s.BeginAgentDriverRun("session-a", "attn-example", "run-a") {
		t.Fatal("BeginAgentDriverRun failed")
	}
	if !s.ApplyAgentDriverState("session-a", "run-a", 7, protocol.StateWorking, time.Time{}) {
		t.Fatal("ApplyAgentDriverState failed")
	}

	got := s.ListAgentDriverRuns("attn-example")
	if len(got) != 1 || got[0].RunID != "run-a" || got[0].Seq != 7 {
		t.Fatalf("ListAgentDriverRuns()=%+v, want run-a at seq 7", got)
	}
}

func TestStore_AddAndGet_PreservesEndpointID(t *testing.T) {
	s := New()

	session := &protocol.Session{
		ID:         "endpoint123",
		Label:      "session-with-endpoint",
		Directory:  "/home/user/project",
		EndpointID: protocol.Ptr("local"),
		State:      protocol.SessionStateWorking,
		StateSince: protocol.TimestampNow().String(),
		LastSeen:   protocol.TimestampNow().String(),
	}

	s.Add(session)

	got := s.Get("endpoint123")
	if got == nil {
		t.Fatal("expected session, got nil")
	}
	if protocol.Deref(got.EndpointID) != "local" {
		t.Errorf("EndpointID = %q, want %q", protocol.Deref(got.EndpointID), "local")
	}
}

func TestStoreAddCheckedReturnsPersistenceFailure(t *testing.T) {
	s := New()
	if err := s.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	err := s.AddChecked(&protocol.Session{
		ID:        "session-closed-db",
		Label:     "closed",
		Agent:     protocol.SessionAgentCodex,
		Directory: "/tmp",
	})
	if err == nil || !strings.Contains(err.Error(), "insert session") {
		t.Fatalf("AddChecked() error = %v, want insert failure", err)
	}
}

func TestStore_Remove(t *testing.T) {
	s := New()

	session := &protocol.Session{
		ID:    "abc123",
		Label: "drumstick",
	}
	s.Add(session)

	s.Remove("abc123")

	if got := s.Get("abc123"); got != nil {
		t.Errorf("expected nil after remove, got %+v", got)
	}
}

func TestStore_List(t *testing.T) {
	s := New()

	s.Add(&protocol.Session{ID: "1", Label: "one", State: protocol.SessionStateWorking})
	s.Add(&protocol.Session{ID: "2", Label: "two", State: protocol.SessionStateWaitingInput})
	s.Add(&protocol.Session{ID: "3", Label: "three", State: protocol.SessionStateWaitingInput})

	all := s.List("")
	if len(all) != 3 {
		t.Errorf("List() returned %d sessions, want 3", len(all))
	}

	waiting := s.List(string(protocol.SessionStateWaitingInput))
	if len(waiting) != 2 {
		t.Errorf("List(waiting_input) returned %d sessions, want 2", len(waiting))
	}

	working := s.List(string(protocol.SessionStateWorking))
	if len(working) != 1 {
		t.Errorf("List(working) returned %d sessions, want 1", len(working))
	}
}

func TestStore_List_StableOrderForDuplicateLabels(t *testing.T) {
	s := New()

	s.Add(&protocol.Session{ID: "b-id", Label: "dup", State: protocol.SessionStateWorking})
	s.Add(&protocol.Session{ID: "a-id", Label: "dup", State: protocol.SessionStateWorking})
	s.Add(&protocol.Session{ID: "c-id", Label: "zzz", State: protocol.SessionStateWorking})

	all := s.List("")
	if len(all) != 3 {
		t.Fatalf("List() returned %d sessions, want 3", len(all))
	}

	if all[0].ID != "a-id" || all[1].ID != "b-id" || all[2].ID != "c-id" {
		t.Fatalf("unexpected order: got [%s %s %s], want [a-id b-id c-id]", all[0].ID, all[1].ID, all[2].ID)
	}

	all2 := s.List("")
	if all2[0].ID != "a-id" || all2[1].ID != "b-id" || all2[2].ID != "c-id" {
		t.Fatalf("order changed across calls: got [%s %s %s], want [a-id b-id c-id]", all2[0].ID, all2[1].ID, all2[2].ID)
	}
}

func TestStore_UpdateState(t *testing.T) {
	s := New()

	s.Add(&protocol.Session{
		ID:         "abc123",
		State:      protocol.SessionStateWorking,
		StateSince: protocol.NewTimestamp(time.Now().Add(-5 * time.Minute)).String(),
	})

	before := protocol.Timestamp(s.Get("abc123").StateSince).Time()

	s.UpdateState("abc123", string(protocol.SessionStateWaitingInput))

	got := s.Get("abc123")
	if got.State != protocol.SessionStateWaitingInput {
		t.Errorf("State = %q, want %q", got.State, protocol.SessionStateWaitingInput)
	}
	if !protocol.Timestamp(got.StateSince).Time().After(before) {
		t.Error("StateSince should be updated")
	}
}

func TestStore_UpdateTodos(t *testing.T) {
	s := New()

	s.Add(&protocol.Session{
		ID:    "abc123",
		Label: "test",
		Todos: []string{},
	})

	todos := []string{"task 1", "task 2"}
	s.UpdateTodos("abc123", todos)

	got := s.Get("abc123")
	if len(got.Todos) != 2 {
		t.Errorf("Todos length = %d, want 2", len(got.Todos))
	}
	if got.Todos[0] != "task 1" {
		t.Errorf("Todos[0] = %q, want %q", got.Todos[0], "task 1")
	}
}

func TestStore_UpdateSessionLabel(t *testing.T) {
	s, err := NewWithDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewWithDB error: %v", err)
	}
	defer s.Close()

	now := string(protocol.TimestampNow())
	s.Add(&protocol.Session{
		ID: "abc123", Label: "original", Agent: protocol.SessionAgentCodex,
		Directory: "/tmp/project", WorkspaceID: "workspace-1",
		State: protocol.SessionStateIdle, StateSince: now,
		StateUpdatedAt: now, LastSeen: now,
	})

	s.UpdateSessionLabel("abc123", "renamed")

	got := s.Get("abc123")
	if got == nil || got.Label != "renamed" {
		t.Fatalf("label after rename = %+v, want renamed", got)
	}
	if got.Directory != "/tmp/project" || got.State != protocol.SessionStateIdle {
		t.Fatalf("rename mutated unrelated fields: %+v", got)
	}
}

func TestStore_Touch(t *testing.T) {
	s := New()

	now := time.Now()
	s.Add(&protocol.Session{
		ID:       "abc123",
		LastSeen: protocol.NewTimestamp(now.Add(-5 * time.Minute)).String(),
	})

	before := protocol.Timestamp(s.Get("abc123").LastSeen).Time()

	time.Sleep(10 * time.Millisecond)
	s.Touch("abc123")

	got := s.Get("abc123")
	if !protocol.Timestamp(got.LastSeen).Time().After(before) {
		t.Error("LastSeen should be updated after Touch")
	}
}

func TestStore_UpdateStateReportsWhetherSessionWasUpdated(t *testing.T) {
	s := New()
	now := protocol.TimestampNow().String()
	s.Add(&protocol.Session{
		ID:             "state-result",
		State:          protocol.SessionStateIdle,
		StateSince:     now,
		StateUpdatedAt: now,
		LastSeen:       now,
	})

	if !s.UpdateState("state-result", protocol.StateWorking) {
		t.Fatal("UpdateState(existing) = false, want true")
	}
	if got := s.Get("state-result"); got == nil || got.State != protocol.SessionStateWorking {
		t.Fatalf("state after update = %+v, want working", got)
	}
	if s.UpdateState("missing", protocol.StateIdle) {
		t.Fatal("UpdateState(missing) = true, want false")
	}
}

func TestStore_SetAndListPRs(t *testing.T) {
	s := New()

	prs := []*protocol.PR{
		{ID: "github.com:owner/repo#1", State: protocol.PRStateWaiting, Muted: false},
		{ID: "github.com:owner/repo#2", State: protocol.StateWorking, Muted: false},
	}

	s.SetPRs(prs)

	all := s.ListPRs("")
	if len(all) != 2 {
		t.Errorf("ListPRs('') returned %d PRs, want 2", len(all))
	}

	waiting := s.ListPRs(protocol.PRStateWaiting)
	if len(waiting) != 1 {
		t.Errorf("ListPRs(waiting) returned %d PRs, want 1", len(waiting))
	}
}

func TestStore_SetPRs_PreservesMuted(t *testing.T) {
	s := New()

	prs := []*protocol.PR{
		{ID: "github.com:owner/repo#1", State: protocol.PRStateWaiting, Muted: false},
	}
	s.SetPRs(prs)

	s.ToggleMutePR("github.com:owner/repo#1")

	prs2 := []*protocol.PR{
		{ID: "github.com:owner/repo#1", State: protocol.StateWorking, Muted: false},
	}
	s.SetPRs(prs2)

	all := s.ListPRs("")
	if !all[0].Muted {
		t.Error("PR should still be muted after SetPRs")
	}
}

func TestStore_SetPRs_PreservesApprovedByMe(t *testing.T) {
	s := New()

	prs := []*protocol.PR{
		{ID: "github.com:owner/repo#1", State: protocol.PRStateWaiting},
	}
	s.SetPRs(prs)

	s.MarkPRApproved("github.com:owner/repo#1")

	pr := s.GetPR("github.com:owner/repo#1")
	if !pr.ApprovedByMe {
		t.Fatal("PR should be marked as approved")
	}

	prs2 := []*protocol.PR{
		{ID: "github.com:owner/repo#1", State: protocol.PRStateWaiting},
	}
	s.SetPRs(prs2)

	pr = s.GetPR("github.com:owner/repo#1")
	if !pr.ApprovedByMe {
		t.Error("PR should still be approved after SetPRs")
	}
}

func TestStore_SetPRs_PreservesDetailFields(t *testing.T) {
	s := New()

	prs := []*protocol.PR{
		{ID: "github.com:owner/repo#1", State: protocol.PRStateWaiting},
	}
	s.SetPRs(prs)

	mergeable := true
	s.UpdatePRDetails("github.com:owner/repo#1", &mergeable, "clean", "success", "approved", "abc123", "feature-branch")

	pr := s.GetPR("github.com:owner/repo#1")
	if protocol.Deref(pr.CIStatus) != "success" {
		t.Fatalf("CIStatus should be 'success', got '%s'", protocol.Deref(pr.CIStatus))
	}

	prs2 := []*protocol.PR{
		{ID: "github.com:owner/repo#1", State: protocol.StateWorking, LastUpdated: protocol.NewTimestamp(time.Now().Add(time.Hour)).String()},
	}
	s.SetPRs(prs2)

	pr = s.GetPR("github.com:owner/repo#1")
	if protocol.Deref(pr.CIStatus) != "success" {
		t.Errorf("CIStatus should still be 'success' after SetPRs, got '%s'", protocol.Deref(pr.CIStatus))
	}
	if protocol.Deref(pr.ReviewStatus) != "approved" {
		t.Errorf("ReviewStatus should still be 'approved' after SetPRs, got '%s'", protocol.Deref(pr.ReviewStatus))
	}
	if protocol.Deref(pr.MergeableState) != "clean" {
		t.Errorf("MergeableState should still be 'clean' after SetPRs, got '%s'", protocol.Deref(pr.MergeableState))
	}
	if protocol.Deref(pr.HeadSHA) != "abc123" {
		t.Errorf("HeadSHA should still be 'abc123' after SetPRs, got '%s'", protocol.Deref(pr.HeadSHA))
	}
}

func TestStore_ToggleMutePR(t *testing.T) {
	s := New()

	prs := []*protocol.PR{
		{ID: "github.com:owner/repo#1", State: protocol.PRStateWaiting, Muted: false},
	}
	s.SetPRs(prs)

	s.ToggleMutePR("github.com:owner/repo#1")

	all := s.ListPRs("")
	if !all[0].Muted {
		t.Error("PR should be muted after toggle")
	}
}

func TestStore_SQLitePersistence(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := tmpDir + "/test.db"

	s, err := NewWithDB(dbPath)
	if err != nil {
		t.Fatalf("NewWithDB error: %v", err)
	}

	s.Add(&protocol.Session{ID: "sqlite-test", Label: "sqlite-test"})

	s.Close()

	s2, err := NewWithDB(dbPath)
	if err != nil {
		t.Fatalf("NewWithDB reopen error: %v", err)
	}
	defer s2.Close()

	got := s2.Get("sqlite-test")
	if got == nil {
		t.Error("session should persist across store reopens")
	}
	if got != nil && got.Label != "sqlite-test" {
		t.Errorf("Label = %q, want sqlite-test", got.Label)
	}
}

func TestStore_LaunchIntentRoundTrip(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "attn.db")
	s, err := NewWithDB(dbPath)
	if err != nil {
		t.Fatalf("NewWithDB() error = %v", err)
	}
	s.Add(&protocol.Session{ID: "launch-intent", Label: "launch-intent"})
	want := LaunchIntent{
		ChiefOfStaff:  true,
		ApprovalRoute: launchcontract.ApprovalRouteReviewer,
		UnattendedLaunch: launchcontract.UnattendedLaunchSpec{
			Agent: "claude", Model: "sonnet", Effort: "high", Executable: "/opt/claude",
			ApprovalProductMode: launchcontract.ApprovalAuto, ApprovalDriverMode: launchcontract.ApprovalAuto,
			DirectoryTrust: launchcontract.TrustConfiguredDirectory, Recovery: launchcontract.RecoveryAdoptOrRestartFresh,
		},
	}

	s.SetLaunchIntent("launch-intent", want)
	if err := s.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	s, err = NewWithDB(dbPath)
	if err != nil {
		t.Fatalf("reopen NewWithDB() error = %v", err)
	}
	defer s.Close()
	got, ok := s.LaunchIntent("launch-intent")
	if !ok {
		t.Fatal("LaunchIntent() = ok false, want true")
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("LaunchIntent() = %+v, want %+v", got, want)
	}
}

func TestStore_ClearLaunchIntent(t *testing.T) {
	s := New()
	defer s.Close()
	s.Add(&protocol.Session{ID: "clear-launch-intent", Label: "clear-launch-intent"})
	s.SetLaunchIntent("clear-launch-intent", LaunchIntent{Model: "sonnet"})

	s.ClearLaunchIntent("clear-launch-intent")
	if _, ok := s.LaunchIntent("clear-launch-intent"); ok {
		t.Fatal("LaunchIntent() after ClearLaunchIntent() = ok true, want false")
	}
}

func TestStore_LaunchIntentMissingOrEmpty(t *testing.T) {
	s := New()
	defer s.Close()
	s.Add(&protocol.Session{ID: "empty-launch-intent", Label: "empty-launch-intent"})

	if _, ok := s.LaunchIntent("unknown-launch-intent"); ok {
		t.Fatal("LaunchIntent() for an unknown session = ok true, want false")
	}
	if _, ok := s.LaunchIntent("empty-launch-intent"); ok {
		t.Fatal("LaunchIntent() for an empty column = ok true, want false")
	}
}

func TestStore_LaunchIntentRejectsCorruptJSON(t *testing.T) {
	s := New()
	defer s.Close()
	s.Add(&protocol.Session{ID: "corrupt-launch-intent", Label: "corrupt-launch-intent"})
	if _, err := s.db.Exec("UPDATE sessions SET launch_intent = ? WHERE id = ?", "{not-json", "corrupt-launch-intent"); err != nil {
		t.Fatalf("seed corrupt launch intent: %v", err)
	}

	if _, ok := s.LaunchIntent("corrupt-launch-intent"); ok {
		t.Fatal("LaunchIntent() for corrupt JSON = ok true, want false")
	}
}

func TestStore_RepoState(t *testing.T) {
	s := New()

	state := s.GetRepoState("owner/repo")
	if state != nil {
		t.Error("expected nil for unknown repo")
	}

	s.ToggleMuteRepo("owner/repo")
	state = s.GetRepoState("owner/repo")
	if state == nil {
		t.Fatal("expected repo state after toggle")
	}
	if !state.Muted {
		t.Error("repo should be muted")
	}

	s.ToggleMuteRepo("owner/repo")
	state = s.GetRepoState("owner/repo")
	if state.Muted {
		t.Error("repo should be unmuted")
	}

	s.SetRepoCollapsed("owner/repo", true)
	state = s.GetRepoState("owner/repo")
	if !state.Collapsed {
		t.Error("repo should be collapsed")
	}
}

func TestStore_ListRepoStates(t *testing.T) {
	s := New()

	s.ToggleMuteRepo("repo-a")
	s.SetRepoCollapsed("repo-b", true)

	states := s.ListRepoStates()
	if len(states) != 2 {
		t.Errorf("expected 2 repo states, got %d", len(states))
	}
}

func TestStore_AuthorState(t *testing.T) {
	s := New()

	states := s.ListAuthorStates()
	if len(states) != 0 {
		t.Errorf("expected 0 author states, got %d", len(states))
	}

	s.ToggleMuteAuthor("dependabot")
	states = s.ListAuthorStates()
	if len(states) != 1 {
		t.Fatalf("expected 1 author state, got %d", len(states))
	}
	if !states[0].Muted {
		t.Error("author should be muted")
	}

	s.ToggleMuteAuthor("dependabot")
	states = s.ListAuthorStates()
	if states[0].Muted {
		t.Error("author should be unmuted")
	}
}

func TestStore_ListAuthorStates(t *testing.T) {
	s := New()

	s.ToggleMuteAuthor("dependabot")
	s.ToggleMuteAuthor("renovate")

	states := s.ListAuthorStates()
	if len(states) != 2 {
		t.Errorf("expected 2 author states, got %d", len(states))
	}
}

func TestHasSessionInDirectoryIgnoresIdleSessions(t *testing.T) {
	for _, persistent := range []bool{false, true} {
		name := "memory"
		var s *Store
		if persistent {
			name = "sqlite"
			var err error
			s, err = NewWithDB(t.TempDir() + "/test.db")
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
		} else {
			s = New()
		}
		t.Run(name, func(t *testing.T) {
			s.Add(&protocol.Session{ID: "idle", Directory: "/tmp/worktree", State: protocol.SessionStateIdle})
			if s.HasSessionInDirectory("/tmp/worktree") {
				t.Fatal("idle session should not reserve the worktree")
			}
			s.Add(&protocol.Session{ID: "working", Directory: "/tmp/worktree", State: protocol.SessionStateWorking})
			if !s.HasSessionInDirectory("/tmp/worktree") {
				t.Fatal("working session should reserve the worktree")
			}
		})
	}
}

func TestSessionIntentionalCloseMark_PersistsAndClears(t *testing.T) {
	dbPath := t.TempDir() + "/test.db"
	s, err := NewWithDB(dbPath)
	if err != nil {
		t.Fatalf("NewWithDB error: %v", err)
	}
	s.Add(&protocol.Session{ID: "sess-1", Label: "sess-1"})

	if s.SessionCloseIntentional("sess-1") {
		t.Fatal("fresh session should carry no intentional-close mark")
	}
	if !s.BeginAgentDriverRun("sess-1", "test-plugin", "run-1") {
		t.Fatal("begin driver run")
	}
	run, err := s.PrepareSessionTeardown("sess-1", time.Now())
	if err != nil {
		t.Fatalf("prepare intentional close: %v", err)
	}
	if run.PluginName != "test-plugin" || run.RunID != "run-1" {
		t.Fatalf("prepared driver run = %+v", run)
	}
	if !s.SessionCloseIntentional("sess-1") {
		t.Fatal("mark not readable after MarkSessionIntentionalClose")
	}
	s.Remove("sess-1")
	s.Close()

	s2, err := NewWithDB(dbPath)
	if err != nil {
		t.Fatalf("NewWithDB reopen error: %v", err)
	}
	defer s2.Close()
	if !s2.SessionCloseIntentional("sess-1") {
		t.Fatal("intentional-close mark must survive session removal and a store reopen")
	}
	recoveredRun, err := s2.PrepareSessionTeardown("sess-1", time.Now())
	if err != nil {
		t.Fatalf("recover teardown owner: %v", err)
	}
	if recoveredRun.PluginName != "test-plugin" || recoveredRun.RunID != "run-1" {
		t.Fatalf("recovered driver run = %+v", recoveredRun)
	}
	s2.ClearSessionIntentionalClose("sess-1")
	if s2.SessionCloseIntentional("sess-1") {
		t.Fatal("mark should be gone after ClearSessionIntentionalClose")
	}
}

func TestSessionIntentionalCloseMark_UnknownSessionFalse(t *testing.T) {
	s := New()
	if s.SessionCloseIntentional("nope") {
		t.Fatal("unknown session must not read as intentionally closed")
	}
}

func TestMigration130CarriesLegacyIntentionalCloseMark(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "legacy-close.db")
	s, err := NewWithDB(dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	s.Add(&protocol.Session{ID: "legacy-close", Label: "legacy-close"})
	if _, err := s.db.Exec(`UPDATE sessions SET closed_intentionally_at = '2026-09-01T12:00:00Z' WHERE id = 'legacy-close';
		DROP TABLE session_teardown_tombstones;
		DELETE FROM schema_migrations WHERE version = 130`); err != nil {
		t.Fatalf("restore pre-130 schema: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close pre-130 store: %v", err)
	}

	reopened, err := NewWithDB(dbPath)
	if err != nil {
		t.Fatalf("reopen with migration 130: %v", err)
	}
	defer reopened.Close()
	if !reopened.SessionCloseIntentional("legacy-close") {
		t.Fatal("migration 130 did not carry the legacy close mark into the tombstone")
	}
}

func TestSessionTeardownDriverRunCanOnlyBeClaimedOnce(t *testing.T) {
	s := New()
	s.Add(&protocol.Session{ID: "claim-once", Label: "claim-once"})
	if !s.BeginAgentDriverRun("claim-once", "test-plugin", "run-once") {
		t.Fatal("begin driver run")
	}
	run, err := s.PrepareSessionTeardown("claim-once", time.Now())
	if err != nil {
		t.Fatalf("prepare teardown: %v", err)
	}
	first, err := s.ClaimSessionTeardownDriverRun("claim-once", run.RunID)
	if err != nil {
		t.Fatalf("first claim: %v", err)
	}
	second, err := s.ClaimSessionTeardownDriverRun("claim-once", run.RunID)
	if err != nil {
		t.Fatalf("second claim: %v", err)
	}
	if !first || second {
		t.Fatalf("claims = (%v, %v), want (true, false)", first, second)
	}
}

func TestPrepareExistingSessionTeardownDoesNotRecreateClearedIntent(t *testing.T) {
	s := New()
	if err := s.MarkSessionIntentionalClose("reused", time.Now()); err != nil {
		t.Fatalf("mark close: %v", err)
	}
	s.ClearSessionIntentionalClose("reused")
	s.Add(&protocol.Session{ID: "reused", Label: "fresh"})
	if !s.BeginAgentDriverRun("reused", "test-plugin", "fresh-run") {
		t.Fatal("begin fresh driver run")
	}
	run, found, err := s.PrepareExistingSessionTeardown("reused", time.Now())
	if err != nil || found || run.RunID != "" {
		t.Fatalf("prepare existing after clear = run %+v found %v err %v", run, found, err)
	}
	if got := s.GetAgentDriverRun("reused"); got.RunID != "fresh-run" {
		t.Fatalf("fresh driver run was claimed: %+v", got)
	}
}

func TestAddCheckedUnlessTeardownRefusesClosingSession(t *testing.T) {
	s := New()
	if err := s.MarkSessionIntentionalClose("closing", time.Now()); err != nil {
		t.Fatalf("mark close: %v", err)
	}
	if err := s.AddCheckedUnlessTeardown(&protocol.Session{ID: "closing", Label: "late"}); err == nil {
		t.Fatal("late session insert succeeded despite teardown tombstone")
	}
	if s.Get("closing") != nil {
		t.Fatal("late session insert recreated the closing session")
	}
}
