package events

const (
	NamePlanted                  = "garden.seed.planted"
	NameTended                   = "garden.seed.tended"
	NameParked                   = "garden.seed.parked"
	NameHarvested                = "garden.seed.harvested"
	NameWithered                 = "garden.seed.withered"
	NameReplanted                = "garden.seed.replanted"
	NameBodyEdited               = "garden.seed.body.edited"
	NameNoteAdded                = "garden.seed.note.added"
	NameArtifactChanged          = "garden.seed.artifact.changed"
	NameResumeIdentityConfigured = "garden.seed.resume_identity.configured"
	NameResumeIdentityCleared    = "garden.seed.resume_identity.cleared"
	NameEdgeLinked               = "garden.seed.edge.linked"
	NameEdgeUnlinked             = "garden.seed.edge.unlinked"
	NameHarvestWhenConfigured    = "garden.seed.harvest_when.configured"
	NameHarvestWhenCleared       = "garden.seed.harvest_when.cleared"
	NameUnblocked                = "garden.seed.unblocked"
	NameWorkReady                = "garden.seed.work.ready"
	BellSeedActivity             = "seed activity"
)

type CausePayload struct {
	CausedBySessionID string `json:"caused_by_session_id,omitempty"`
}

type NoteAddedPayload struct {
	NoteID             string `json:"note_id" required:"true"`
	AttentionRequested bool   `json:"attention_requested" required:"true"`
	CausedBySessionID  string `json:"caused_by_session_id,omitempty"`
}

type EdgePayload struct {
	EdgeKind          string `json:"edge_kind" required:"true"`
	TargetSeedID      string `json:"target_seed_id" required:"true"`
	CausedBySessionID string `json:"caused_by_session_id,omitempty"`
}

type HarvestWhenPayload struct {
	PullRequestID     string `json:"pull_request_id" required:"true"`
	CausedBySessionID string `json:"caused_by_session_id,omitempty"`
}

type UnblockedPayload struct {
	BlockerSeedID     string `json:"blocker_seed_id" required:"true"`
	CausedBySessionID string `json:"caused_by_session_id,omitempty"`
}

type WorkReadyPayload struct {
	AutomationRunID   string `json:"automation_run_id" required:"true"`
	CausedBySessionID string `json:"caused_by_session_id,omitempty"`
}

type Vocabulary struct {
	Planted                  EventType[CausePayload]
	Tended                   EventType[CausePayload]
	Parked                   EventType[CausePayload]
	Harvested                EventType[CausePayload]
	Withered                 EventType[CausePayload]
	Replanted                EventType[CausePayload]
	BodyEdited               EventType[CausePayload]
	NoteAdded                EventType[NoteAddedPayload]
	ArtifactChanged          EventType[CausePayload]
	ResumeIdentityConfigured EventType[CausePayload]
	ResumeIdentityCleared    EventType[CausePayload]
	EdgeLinked               EventType[EdgePayload]
	EdgeUnlinked             EventType[EdgePayload]
	HarvestWhenConfigured    EventType[HarvestWhenPayload]
	HarvestWhenCleared       EventType[HarvestWhenPayload]
	Unblocked                EventType[UnblockedPayload]
	WorkReady                EventType[WorkReadyPayload]
	Events                   EventCatalog
	Rules                    PolicySet
	SeedActivity             BellDef
}

func Declarations() Vocabulary {
	seedParties := Audience("seed parties").
		Includes(CurrentTender).
		Includes(CoveringWatchers)
	seedActivity := Bell(BellSeedActivity).
		Notify(seedParties).
		Except(SessionThatCausedTheEvent).
		KeepPendingWhile(RecipientStillBelongsTo(seedParties))

	v := Vocabulary{
		Planted:                  Event[CausePayload](NamePlanted),
		Tended:                   Event[CausePayload](NameTended),
		Parked:                   Event[CausePayload](NameParked),
		Harvested:                Event[CausePayload](NameHarvested),
		Withered:                 Event[CausePayload](NameWithered),
		Replanted:                Event[CausePayload](NameReplanted),
		BodyEdited:               Event[CausePayload](NameBodyEdited),
		NoteAdded:                Event[NoteAddedPayload](NameNoteAdded),
		ArtifactChanged:          Event[CausePayload](NameArtifactChanged),
		ResumeIdentityConfigured: Event[CausePayload](NameResumeIdentityConfigured),
		ResumeIdentityCleared:    Event[CausePayload](NameResumeIdentityCleared),
		EdgeLinked:               Event[EdgePayload](NameEdgeLinked),
		EdgeUnlinked:             Event[EdgePayload](NameEdgeUnlinked),
		HarvestWhenConfigured:    Event[HarvestWhenPayload](NameHarvestWhenConfigured),
		HarvestWhenCleared:       Event[HarvestWhenPayload](NameHarvestWhenCleared),
		Unblocked:                Event[UnblockedPayload](NameUnblocked),
		WorkReady:                Event[WorkReadyPayload](NameWorkReady),
		SeedActivity:             seedActivity,
	}
	v.Events = Catalog(
		v.Planted, v.Tended, v.Parked, v.Harvested, v.Withered, v.Replanted,
		v.BodyEdited, v.NoteAdded, v.ArtifactChanged,
		v.ResumeIdentityConfigured, v.ResumeIdentityCleared,
		v.EdgeLinked, v.EdgeUnlinked,
		v.HarvestWhenConfigured, v.HarvestWhenCleared,
		v.Unblocked, v.WorkReady,
	)
	attentionRequested := BoolField[NoteAddedPayload]("attention_requested")
	v.Rules = Policies(
		On(v.Planted, Quiet()),
		On(v.Tended, Ring(seedActivity)),
		On(v.Parked, Ring(seedActivity)),
		On(v.Harvested, Ring(seedActivity)),
		On(v.Withered, Ring(seedActivity)),
		On(v.Replanted, Ring(seedActivity)),
		On(v.BodyEdited, Quiet()),
		On(v.NoteAdded, Choose(attentionRequested, Ring(seedActivity), Quiet())),
		On(v.ArtifactChanged, Quiet()),
		On(v.ResumeIdentityConfigured, Quiet()),
		On(v.ResumeIdentityCleared, Quiet()),
		On(v.EdgeLinked, Quiet()),
		On(v.EdgeUnlinked, Quiet()),
		On(v.HarvestWhenConfigured, Ring(seedActivity)),
		On(v.HarvestWhenCleared, Ring(seedActivity)),
		On(v.Unblocked, Ring(seedActivity)),
		On(v.WorkReady, Ring(seedActivity)),
	)
	return v
}

func BuildGardenModel() (*Model, Vocabulary, error) {
	vocabulary := Declarations()
	model, err := Build(vocabulary.Events, vocabulary.Rules)
	return model, vocabulary, err
}
