package store

import (
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/launchcontract"
	"github.com/victorarias/attn/internal/protocol"
)

func TestStore_MarkModelRequestStartedIsMonotonicAndIndependentOfState(t *testing.T) {
	for _, tc := range []struct {
		name string
		open func(*testing.T) *Store
	}{
		{name: "memory", open: func(t *testing.T) *Store { return New() }},
		{name: "sqlite", open: func(t *testing.T) *Store {
			s, err := newSeededStore(filepath.Join(t.TempDir(), "test.db"))
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

	if got := s.ListAgentDriverRuns("attn-example"); !reflect.DeepEqual(got, []ActiveAgentDriverRun{
		{SessionID: "session-a", RunID: "run-a", Metadata: `{"native":"one"}`, Seq: 4},
		{SessionID: "session-b", RunID: "run-b"},
	}) {
		t.Fatalf("ListAgentDriverRuns()=%+v", got)
	}
}

func TestStore_ListActiveAgentDriverRunsNamesTheOwnerOfEveryRun(t *testing.T) {
	s, err := newSeededStore(filepath.Join(t.TempDir(), "test.db"))
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
	s, err := newSeededStore(filepath.Join(t.TempDir(), "test.db"))
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

func TestSessionIntentionalCloseMark_PersistsAndClears(t *testing.T) {
	dbPath := t.TempDir() + "/test.db"
	s, err := newSeededStore(dbPath)
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

	s2, err := newSeededStore(dbPath)
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

func TestMigration130CarriesLegacyIntentionalCloseMark(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "legacy-close.db")
	s, err := newSeededStore(dbPath)
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

	reopened, err := newSeededStore(dbPath)
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

func TestStore_LaunchIntentRoundTrip(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "attn.db")
	s, err := newSeededStore(dbPath)
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
	s, err = newSeededStore(dbPath)
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
