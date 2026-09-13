package events_test

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/garden/events"
)

const seedID = "s-7k3f9m"

func parties() events.AudienceDef {
	return events.Audience("seed parties").Includes(events.CurrentTender).Includes(events.CoveringWatchers)
}

func ringing() events.BellDef {
	audience := parties()
	return events.Bell("seed activity").Notify(audience).
		Except(events.SessionThatCausedTheEvent).
		Except(events.SessionNotifiedDirectly).
		KeepPendingWhile(events.RecipientStillBelongsTo(audience))
}

func gardenModel(t *testing.T) (*events.Model, events.Vocabulary) {
	t.Helper()
	model, vocabulary, err := events.BuildGardenModel()
	if err != nil {
		t.Fatal(err)
	}
	return model, vocabulary
}

func TestGardenCatalogIsClosedAndEveryEventHasAnExplicitPolicy(t *testing.T) {
	model, _ := gardenModel(t)
	want := []string{
		events.NamePlanted, events.NameTended, events.NameParked, events.NameHarvested,
		events.NameWithered, events.NameReplanted, events.NameBodyEdited, events.NameNoteAdded,
		events.NameArtifactChanged, events.NameResumeIdentityConfigured,
		events.NameResumeIdentityCleared, events.NameEdgeLinked, events.NameEdgeUnlinked,
		events.NameHarvestWhenConfigured, events.NameHarvestWhenCleared,
		events.NameUnblocked, events.NameWorkReady,
	}
	slices.Sort(want)
	if got := model.EventNames(); !slices.Equal(got, want) {
		t.Fatalf("event names = %v, want %v", got, want)
	}
	if got := model.BellNames(); !slices.Equal(got, []string{events.BellSeedActivity}) {
		t.Fatalf("bell names = %v", got)
	}
}

func TestRejectsInvalidModelsIncludingUnselectedBranches(t *testing.T) {
	event := events.Event[events.NoteAddedPayload](events.NameNoteAdded)
	other := events.Event[events.NoteAddedPayload]("garden.seed.other")
	audience := parties()
	watchers := events.Audience("watchers").Includes(events.CoveringWatchers)
	tenders := events.Audience("tender").Includes(events.CurrentTender)
	tests := []struct {
		name     string
		catalog  events.EventCatalog
		policies events.PolicySet
		want     string
	}{
		{"missing policy", events.Catalog(event, other), events.Policies(events.On(event, events.Quiet())), "missing explicit"},
		{"duplicate policy", events.Catalog(event), events.Policies(events.On(event, events.Quiet()), events.On(event, events.Quiet())), "duplicate policy"},
		{"duplicate name", events.Catalog(event, events.Event[events.NoteAddedPayload](events.NameNoteAdded)), events.Policies(), "duplicate event"},
		{"unregistered policy", events.Catalog(event), events.Policies(events.On(other, events.Quiet())), "outside the catalog"},
		{"zero decision", events.Catalog(event), events.Policies(events.On(event, events.Decision{})), "missing or unknown"},
		{"bad boolean", events.Catalog(event), events.Policies(events.On(event, events.Choose(events.BoolField[events.NoteAddedPayload]("note_id"), events.Quiet(), events.Quiet()))), "required boolean"},
		{"wrong payload predicate", events.Catalog(event), events.Policies(events.On(event, events.Choose(events.BoolField[events.UnblockedPayload]("attention_requested"), events.Quiet(), events.Quiet()))), "required boolean"},
		{"invalid hidden branch", events.Catalog(event), events.Policies(events.On(event, events.Choose(events.BoolField[events.NoteAddedPayload]("attention_requested"), events.Quiet(), events.Ring(events.Bell("broken").Notify(tenders).Except(events.SessionThatCausedTheEvent).Except(events.SessionNotifiedDirectly).KeepPendingWhile(events.RecipientStillBelongsTo(watchers)))))), "audiences differ"},
		{"incomplete audience", events.Catalog(event), events.Policies(events.On(event, events.Ring(events.Bell("broken").Notify(watchers).Except(events.SessionThatCausedTheEvent).Except(events.SessionNotifiedDirectly).KeepPendingWhile(events.RecipientStillBelongsTo(watchers))))), "must include"},
		{"missing exclusion", events.Catalog(event), events.Policies(events.On(event, events.Ring(events.Bell("broken").Notify(audience).KeepPendingWhile(events.RecipientStillBelongsTo(audience))))), "must exclude"},
		{"missing retention", events.Catalog(event), events.Policies(events.On(event, events.Ring(events.Bell("broken").Notify(audience).Except(events.SessionThatCausedTheEvent).Except(events.SessionNotifiedDirectly)))), "exactly one"},
		{"duplicate role", events.Catalog(event), events.Policies(events.On(event, events.Ring(events.Bell("broken").Notify(audience.Includes(events.CurrentTender)).Except(events.SessionThatCausedTheEvent).Except(events.SessionNotifiedDirectly).KeepPendingWhile(events.RecipientStillBelongsTo(audience))))), "duplicate role"},
		{"empty catalog", events.Catalog(), events.Policies(), "empty event catalog"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := events.Build(test.catalog, test.policies)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Build error = %v, want %q", err, test.want)
			}
		})
	}
}

type customPayload events.NoteAddedPayload

func (*customPayload) UnmarshalJSON([]byte) error { return nil }

type customFlag bool

func (customFlag) MarshalJSON() ([]byte, error) { return []byte("true"), nil }

type fieldCodecPayload struct {
	Cause     string     `json:"caused_by_session_id,omitempty"`
	Attention customFlag `json:"attention_requested" required:"true"`
}

func TestPayloadCodecsCannotHideBehaviorOutsideDSL(t *testing.T) {
	for _, event := range []events.EventDefinition{
		events.Event[customPayload]("garden.seed.custom_struct"),
		events.Event[fieldCodecPayload]("garden.seed.custom_field"),
	} {
		if _, err := events.Build(events.Catalog(event), events.Policies()); err == nil || !strings.Contains(err.Error(), "codec") {
			t.Fatalf("hidden codec accepted: %v", err)
		}
	}
}

func TestEmissionRequiresTheExactRegisteredHandleAndPayload(t *testing.T) {
	model, vocabulary := gardenModel(t)
	for _, event := range []events.EventType[events.NoteAddedPayload]{
		events.Event[events.NoteAddedPayload]("garden.seed.hidden"),
		events.Event[events.NoteAddedPayload](events.NameNoteAdded),
		{},
	} {
		if _, err := events.Occur(model, event, seedID, events.NoteAddedPayload{NoteID: "n-one"}); err == nil {
			t.Fatal("unregistered handle accepted")
		}
	}
	if _, err := events.Occur(model, vocabulary.NoteAdded, "", events.NoteAddedPayload{NoteID: "n-one"}); err == nil {
		t.Fatal("empty subject accepted")
	}
	if _, err := events.Occur(model, vocabulary.NoteAdded, " "+seedID, events.NoteAddedPayload{NoteID: "n-one"}); err == nil {
		t.Fatal("non-canonical subject accepted")
	}
	if _, err := events.Occur(model, vocabulary.NoteAdded, " "+seedID, events.NoteAddedPayload{NoteID: "n-one"}); err == nil {
		t.Fatal("untrimmed subject accepted")
	}
	if _, err := events.Occur(model, vocabulary.NoteAdded, seedID, events.NoteAddedPayload{}); err == nil {
		t.Fatal("missing note id accepted")
	}
	if _, err := events.Occur(model, vocabulary.Unblocked, seedID, events.UnblockedPayload{}); err == nil {
		t.Fatal("missing blocker id accepted")
	}
}

func TestPersistedPayloadValidationIsStrict(t *testing.T) {
	model, _ := gardenModel(t)
	for _, payload := range []string{
		`null`,
		`{"note_id":"n-one"}`,
		`{"note_id":"n-one","attention_requested":null}`,
		`{"note_id":"n-one","attention_requested":"true"}`,
		`{"note_id":"n-one","attention_requested":true,"attention_requested":false}`,
		`{"note_id":"n-one","attention_requested":true,"CAUSEd_by_session_id":"alice"}`,
		`{"note_id":"n-one","attention_requested":true} {}`,
	} {
		t.Run(payload, func(t *testing.T) {
			if _, err := model.Interpret(events.NameNoteAdded, seedID, json.RawMessage(payload)); err == nil {
				t.Fatal("invalid payload interpreted")
			}
		})
	}
	if _, err := model.Interpret(events.NameNoteAdded, seedID+" ", []byte(`{"note_id":"n-one","attention_requested":true}`)); err == nil {
		t.Fatal("untrimmed persisted subject interpreted")
	}
}

type roleResolver struct{ roles map[events.Role][]string }

func (r roleResolver) ResolveSeedRole(_ string, role events.Role) ([]string, error) {
	return append([]string(nil), r.roles[role]...), nil
}

func TestSelectionAndRetentionShareTheAudienceResolver(t *testing.T) {
	model, vocabulary := gardenModel(t)
	occurrence, err := events.Occur(model, vocabulary.Unblocked, seedID, events.UnblockedPayload{
		BlockerSeedID: "s-9k3f9m", CausedBySessionID: "alice",
	})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := model.Encode(occurrence)
	if err != nil {
		t.Fatal(err)
	}
	decision, err := model.Interpret(encoded.Name, encoded.Subject, encoded.Payload)
	if err != nil {
		t.Fatal(err)
	}
	roles := roleResolver{roles: map[events.Role][]string{
		events.CurrentTender:    {"alice"},
		events.CoveringWatchers: {"alice", "bob", "ancestor"},
	}}
	if got, err := model.Recipients(seedID, decision, roles); err != nil || !reflect.DeepEqual(got, []string{"ancestor", "bob"}) {
		t.Fatalf("Recipients = %v, %v", got, err)
	}
	if eligible, err := model.RecipientEligible(decision.BellName(), seedID, "alice", roles); err != nil || !eligible {
		t.Fatalf("cause remains a current party: eligible=%v err=%v", eligible, err)
	}
	roles.roles[events.CurrentTender] = []string{"new-tender"}
	roles.roles[events.CoveringWatchers] = []string{"bob"}
	if eligible, err := model.RecipientEligible(decision.BellName(), seedID, "alice", roles); err != nil || eligible {
		t.Fatalf("lost party stayed eligible=%v err=%v", eligible, err)
	}
}

func TestDeclaredExclusionsCoverTheActorAndADirectlyNotifiedSession(t *testing.T) {
	model, vocabulary := gardenModel(t)
	occurrence, err := events.Occur(model, vocabulary.Tended, seedID, events.CausePayload{
		CausedBySessionID: "source", DirectlyNotifiedSessionID: "destination",
	})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := model.Encode(occurrence)
	if err != nil {
		t.Fatal(err)
	}
	decision, err := model.Interpret(encoded.Name, encoded.Subject, encoded.Payload)
	if err != nil {
		t.Fatal(err)
	}
	roles := roleResolver{roles: map[events.Role][]string{
		events.CurrentTender:    {"destination"},
		events.CoveringWatchers: {"source", "observer"},
	}}
	if got, err := model.Recipients(seedID, decision, roles); err != nil || !reflect.DeepEqual(got, []string{"observer"}) {
		t.Fatalf("Recipients = %v, %v", got, err)
	}
}

func TestNotePolicyIsConditional(t *testing.T) {
	model, _ := gardenModel(t)
	for _, test := range []struct {
		payload string
		quiet   bool
	}{
		{`{"note_id":"n-one","attention_requested":false}`, true},
		{`{"note_id":"n-one","attention_requested":true}`, false},
	} {
		decision, err := model.Interpret(events.NameNoteAdded, seedID, []byte(test.payload))
		if err != nil {
			t.Fatal(err)
		}
		if decision.Quiet() != test.quiet {
			t.Fatalf("payload %s quiet=%v, want %v", test.payload, decision.Quiet(), test.quiet)
		}
	}
}

func TestDeclarationsAloneChangeBehaviorAndExtendTheCatalog(t *testing.T) {
	example := events.Event[events.CausePayload]("garden.seed.example")
	quiet, err := events.Build(events.Catalog(example), events.Policies(events.On(example, events.Quiet())))
	if err != nil {
		t.Fatal(err)
	}
	occurrence, err := events.Occur(quiet, example, seedID, events.CausePayload{})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := quiet.Encode(occurrence)
	if err != nil {
		t.Fatal(err)
	}
	decision, err := quiet.Interpret(encoded.Name, encoded.Subject, encoded.Payload)
	if err != nil || !decision.Quiet() {
		t.Fatalf("quiet declaration decision=%+v err=%v", decision, err)
	}

	ringingModel, err := events.Build(events.Catalog(example), events.Policies(events.On(example, events.Ring(ringing()))))
	if err != nil {
		t.Fatal(err)
	}
	ringingOccurrence, err := events.Occur(ringingModel, example, seedID, events.CausePayload{})
	if err != nil {
		t.Fatal(err)
	}
	ringingEncoded, err := ringingModel.Encode(ringingOccurrence)
	if err != nil {
		t.Fatal(err)
	}
	decision, err = ringingModel.Interpret(ringingEncoded.Name, ringingEncoded.Subject, ringingEncoded.Payload)
	if err != nil || decision.Quiet() || decision.BellName() != events.BellSeedActivity {
		t.Fatalf("ring declaration decision=%+v err=%v", decision, err)
	}
}

func TestNamedBellDefinitionCanOutliveItsEventsPolicy(t *testing.T) {
	event := events.Event[events.CausePayload]("garden.seed.formerly_ringing")
	model, err := events.Build(
		events.Catalog(event), events.Policies(events.On(event, events.Quiet())), ringing(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := model.ValidatePendingBell(events.BellSeedActivity); err != nil {
		t.Fatalf("retained pending bell definition was lost: %v", err)
	}
	occurrence, err := events.Occur(model, event, seedID, events.CausePayload{})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := model.Encode(occurrence)
	if err != nil {
		t.Fatal(err)
	}
	decision, err := model.Interpret(encoded.Name, encoded.Subject, encoded.Payload)
	if err != nil || !decision.Quiet() {
		t.Fatalf("quiet current policy decision=%+v err=%v", decision, err)
	}
}

type namedString string
type namedBool bool

type namedScalarPayload struct {
	Value              namedString `json:"value" required:"true"`
	AttentionRequested namedBool   `json:"attention_requested" required:"true"`
	CausedBySessionID  namedString `json:"caused_by_session_id,omitempty"`
}

func TestNamedScalarPayloadsAndBuilderBranchesStayPlainAndImmutable(t *testing.T) {
	event := events.Event[namedScalarPayload]("garden.seed.named")
	base := events.Audience("seed parties").Includes(events.CurrentTender)
	parties := base.Includes(events.CoveringWatchers)
	bell := events.Bell(events.BellSeedActivity).Notify(parties).
		Except(events.SessionThatCausedTheEvent).
		Except(events.SessionNotifiedDirectly).
		KeepPendingWhile(events.RecipientStillBelongsTo(parties))
	model, err := events.Build(events.Catalog(event), events.Policies(events.On(event,
		events.Choose(events.BoolField[namedScalarPayload]("attention_requested"), events.Ring(bell), events.Quiet()),
	)))
	if err != nil {
		t.Fatal(err)
	}
	occurrence, err := events.Occur(model, event, seedID, namedScalarPayload{
		Value: "plain", AttentionRequested: true, CausedBySessionID: "writer",
	})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := model.Encode(occurrence)
	if err != nil {
		t.Fatal(err)
	}
	decision, err := model.Interpret(encoded.Name, encoded.Subject, encoded.Payload)
	if err != nil || decision.Quiet() || decision.CausedBySessionID() != "writer" {
		t.Fatalf("named scalar decision=%+v err=%v", decision, err)
	}
	broken := events.Bell("broken").Notify(base).
		Except(events.SessionThatCausedTheEvent).
		Except(events.SessionNotifiedDirectly).
		KeepPendingWhile(events.RecipientStillBelongsTo(base))
	if _, err := events.Build(events.Catalog(event), events.Policies(events.On(event, events.Ring(broken)))); err == nil || !strings.Contains(err.Error(), "must include") {
		t.Fatalf("base audience was mutated by a derived branch: %v", err)
	}
}
