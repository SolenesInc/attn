package events

import (
	"bytes"
	"encoding"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"

	"github.com/victorarias/attn/internal/garden"
)

type Role uint8

const (
	CurrentTender Role = iota + 1
	CoveringWatchers
)

type AudienceDef struct {
	name  string
	roles []Role
}

func Audience(name string) AudienceDef { return AudienceDef{name: name} }

func (a AudienceDef) Includes(role Role) AudienceDef {
	a.roles = append(append([]Role(nil), a.roles...), role)
	return a
}

type Exclusion uint8

const (
	SessionThatCausedTheEvent Exclusion = iota + 1
	SessionNotifiedDirectly
)

type Membership struct{ audience AudienceDef }

func RecipientStillBelongsTo(a AudienceDef) Membership { return Membership{audience: a} }

type BellDef struct {
	name       string
	audience   AudienceDef
	exclusions []Exclusion
	retention  AudienceDef
	notifySet  int
	keepSet    int
}

func Bell(name string) BellDef { return BellDef{name: name} }

func (b BellDef) Notify(a AudienceDef) BellDef {
	b.audience = a
	b.notifySet++
	return b
}

func (b BellDef) Except(e Exclusion) BellDef {
	b.exclusions = append(append([]Exclusion(nil), b.exclusions...), e)
	return b
}

func (b BellDef) KeepPendingWhile(m Membership) BellDef {
	b.retention = m.audience
	b.keepSet++
	return b
}

type definition struct {
	name   string
	typeOf reflect.Type
}

type EventType[T any] struct{ def *definition }

func Event[T any](name string) EventType[T] {
	return EventType[T]{def: &definition{name: name, typeOf: reflect.TypeFor[T]()}}
}

type EventDefinition interface{ eventDefinition() *definition }

func (e EventType[T]) eventDefinition() *definition { return e.def }

type EventCatalog struct{ events []*definition }

func Catalog(events ...EventDefinition) EventCatalog {
	catalog := EventCatalog{}
	for _, event := range events {
		if event == nil || nilInterface(event) {
			catalog.events = append(catalog.events, nil)
			continue
		}
		catalog.events = append(catalog.events, extractDefinition(event))
	}
	return catalog
}

func nilInterface(value any) bool {
	v := reflect.ValueOf(value)
	return v.Kind() == reflect.Pointer && v.IsNil()
}

func extractDefinition(event EventDefinition) (def *definition) {
	defer func() {
		if recover() != nil {
			def = nil
		}
	}()
	return event.eventDefinition()
}

type Condition struct {
	typeOf reflect.Type
	field  string
}

func BoolField[T any](field string) Condition {
	return Condition{typeOf: reflect.TypeFor[T](), field: field}
}

type actionKind uint8

const (
	quietAction actionKind = iota + 1
	ringAction
	chooseAction
)

type Decision struct {
	kind      actionKind
	bell      BellDef
	condition Condition
	yes       *Decision
	no        *Decision
}

func Quiet() Decision         { return Decision{kind: quietAction} }
func Ring(b BellDef) Decision { return Decision{kind: ringAction, bell: b} }

func Choose(condition Condition, yes, no Decision) Decision {
	return Decision{kind: chooseAction, condition: condition, yes: &yes, no: &no}
}

type Rule struct {
	event    *definition
	decision Decision
}

func On[T any](event EventType[T], decision Decision) Rule {
	return Rule{event: event.def, decision: decision}
}

type PolicySet struct{ rules []Rule }

func Policies(rules ...Rule) PolicySet { return PolicySet{rules: append([]Rule(nil), rules...)} }

type field struct {
	kind     reflect.Kind
	required bool
}

type compiledEvent struct {
	def      *definition
	fields   map[string]field
	decision Decision
}

type Model struct{ compiled *compiledModel }

type compiledModel struct {
	validated bool
	byName    map[string]*compiledEvent
	byHandle  map[*definition]*compiledEvent
	bells     map[string]BellDef
}

func Build(catalog EventCatalog, policies PolicySet, retainedBells ...BellDef) (*Model, error) {
	model := &Model{compiled: &compiledModel{
		byName:   map[string]*compiledEvent{},
		byHandle: map[*definition]*compiledEvent{},
		bells:    map[string]BellDef{},
	}}
	for _, bell := range retainedBells {
		if err := model.registerBell(bell); err != nil {
			return nil, err
		}
	}
	if len(catalog.events) == 0 {
		return nil, fmt.Errorf("seed event model: empty event catalog")
	}
	for _, def := range catalog.events {
		if def == nil || !strings.HasPrefix(def.name, "garden.seed.") ||
			strings.TrimSpace(strings.TrimPrefix(def.name, "garden.seed.")) == "" || strings.TrimSpace(def.name) != def.name {
			return nil, fmt.Errorf("seed event model: invalid seed event definition")
		}
		if _, exists := model.compiled.byName[def.name]; exists {
			return nil, fmt.Errorf("%s: duplicate event name", def.name)
		}
		fields, err := payloadFields(def.typeOf)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", def.name, err)
		}
		compiled := &compiledEvent{def: def, fields: fields}
		model.compiled.byName[def.name] = compiled
		model.compiled.byHandle[def] = compiled
	}
	seen := map[*definition]bool{}
	for _, rule := range policies.rules {
		event, exists := model.compiled.byHandle[rule.event]
		if !exists {
			return nil, fmt.Errorf("policy references an event handle outside the catalog")
		}
		if seen[rule.event] {
			return nil, fmt.Errorf("%s: duplicate policy", event.def.name)
		}
		seen[rule.event] = true
		if err := model.validateDecision(event, rule.decision); err != nil {
			return nil, fmt.Errorf("%s: %w", event.def.name, err)
		}
		event.decision = cloneDecision(rule.decision)
	}
	for _, def := range catalog.events {
		if !seen[def] {
			return nil, fmt.Errorf("%s: missing explicit bell policy", def.name)
		}
	}
	model.compiled.validated = true
	return model, nil
}

func payloadFields(payload reflect.Type) (map[string]field, error) {
	if payload == nil || payload.Kind() != reflect.Struct {
		return nil, fmt.Errorf("payload must be a flat struct")
	}
	if customCodec(payload) {
		return nil, fmt.Errorf("payload must be plain data without custom JSON/Text codecs")
	}
	fields := map[string]field{}
	for i := 0; i < payload.NumField(); i++ {
		declared := payload.Field(i)
		tag := strings.Split(declared.Tag.Get("json"), ",")
		name := tag[0]
		if !declared.IsExported() || declared.Anonymous || name == "" || name == "-" {
			return nil, fmt.Errorf("payload field %s requires an explicit flat JSON field", declared.Name)
		}
		if _, exists := fields[name]; exists {
			return nil, fmt.Errorf("duplicate payload field %s", name)
		}
		if declared.Type.Kind() != reflect.String && declared.Type.Kind() != reflect.Bool {
			return nil, fmt.Errorf("payload field %s: unsupported type %s", name, declared.Type)
		}
		if customCodec(declared.Type) {
			return nil, fmt.Errorf("payload field %s: custom JSON/Text codecs are not supported", name)
		}
		required := declared.Tag.Get("required") == "true"
		for _, option := range tag[1:] {
			if option != "omitempty" || required {
				return nil, fmt.Errorf("payload field %s: JSON option %q is not supported for this field", name, option)
			}
		}
		fields[name] = field{kind: declared.Type.Kind(), required: required}
	}
	cause, exists := fields["caused_by_session_id"]
	if !exists || cause.kind != reflect.String || cause.required {
		return nil, fmt.Errorf("payload requires optional string caused_by_session_id")
	}
	return fields, nil
}

func customCodec(value reflect.Type) bool {
	for _, codec := range []reflect.Type{
		reflect.TypeFor[json.Marshaler](), reflect.TypeFor[json.Unmarshaler](),
		reflect.TypeFor[encoding.TextMarshaler](), reflect.TypeFor[encoding.TextUnmarshaler](),
	} {
		if value.Implements(codec) || reflect.PointerTo(value).Implements(codec) {
			return true
		}
	}
	return false
}

func (m *Model) validateDecision(event *compiledEvent, decision Decision) error {
	switch decision.kind {
	case quietAction:
		return nil
	case ringAction:
		if err := m.registerBell(decision.bell); err != nil {
			return err
		}
		for _, exclusion := range decision.bell.exclusions {
			fieldName := exclusionField(exclusion)
			if fieldName == "caused_by_session_id" {
				continue
			}
			if declared, exists := event.fields[fieldName]; exists && (declared.kind != reflect.String || declared.required) {
				return fmt.Errorf("exclusion field %s must be an optional string", fieldName)
			}
		}
		return nil
	case chooseAction:
		declared, exists := event.fields[decision.condition.field]
		if decision.condition.typeOf != event.def.typeOf || !exists || declared.kind != reflect.Bool || !declared.required {
			return fmt.Errorf("condition %q must reference a required boolean field of this event payload", decision.condition.field)
		}
		if decision.yes == nil || decision.no == nil {
			return fmt.Errorf("Choose requires both branches")
		}
		if err := m.validateDecision(event, *decision.yes); err != nil {
			return fmt.Errorf("true branch: %w", err)
		}
		if err := m.validateDecision(event, *decision.no); err != nil {
			return fmt.Errorf("false branch: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("missing or unknown decision; declare Quiet explicitly")
	}
}

func (m *Model) registerBell(bell BellDef) error {
	if err := validateBell(bell); err != nil {
		return err
	}
	if previous, exists := m.compiled.bells[bell.name]; exists && !reflect.DeepEqual(previous, bell) {
		return fmt.Errorf("bell %q has conflicting definitions", bell.name)
	}
	m.compiled.bells[bell.name] = cloneBell(bell)
	return nil
}

func roleSet(audience AudienceDef) (map[Role]bool, error) {
	if strings.TrimSpace(audience.name) == "" || len(audience.roles) == 0 {
		return nil, fmt.Errorf("audience needs a name and roles")
	}
	seen := map[Role]bool{}
	for _, role := range audience.roles {
		if role != CurrentTender && role != CoveringWatchers {
			return nil, fmt.Errorf("audience %q: unsupported role %d", audience.name, role)
		}
		if seen[role] {
			return nil, fmt.Errorf("audience %q: duplicate role %d", audience.name, role)
		}
		seen[role] = true
	}
	return seen, nil
}

func validateBell(bell BellDef) error {
	if strings.TrimSpace(bell.name) == "" || bell.notifySet != 1 || bell.keepSet != 1 {
		return fmt.Errorf("bell %q requires exactly one Notify and KeepPendingWhile declaration", bell.name)
	}
	selected, err := roleSet(bell.audience)
	if err != nil {
		return err
	}
	retained, err := roleSet(bell.retention)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(selected, retained) {
		return fmt.Errorf("bell %q: notification and retention audiences differ", bell.name)
	}
	if !selected[CurrentTender] || !selected[CoveringWatchers] {
		return fmt.Errorf("bell %q: seed parties must include current tender and covering watchers", bell.name)
	}
	want := map[Exclusion]bool{
		SessionThatCausedTheEvent: true,
		SessionNotifiedDirectly:   true,
	}
	for _, exclusion := range bell.exclusions {
		if !want[exclusion] {
			return fmt.Errorf("bell %q has unsupported exclusion %d", bell.name, exclusion)
		}
		delete(want, exclusion)
	}
	if len(want) != 0 || len(bell.exclusions) != 2 {
		return fmt.Errorf("bell %q must exclude SessionThatCausedTheEvent and SessionNotifiedDirectly exactly once", bell.name)
	}
	return nil
}

func exclusionField(exclusion Exclusion) string {
	switch exclusion {
	case SessionThatCausedTheEvent:
		return "caused_by_session_id"
	case SessionNotifiedDirectly:
		return "directly_notified_session_id"
	default:
		return ""
	}
}

func cloneBell(bell BellDef) BellDef {
	bell.audience.roles = append([]Role(nil), bell.audience.roles...)
	bell.retention.roles = append([]Role(nil), bell.retention.roles...)
	bell.exclusions = append([]Exclusion(nil), bell.exclusions...)
	return bell
}

func cloneDecision(decision Decision) Decision {
	decision.bell = cloneBell(decision.bell)
	if decision.yes != nil {
		yes := cloneDecision(*decision.yes)
		decision.yes = &yes
	}
	if decision.no != nil {
		no := cloneDecision(*decision.no)
		decision.no = &no
	}
	return decision
}

func (m *Model) EventNames() []string {
	if m == nil || m.compiled == nil {
		return nil
	}
	names := make([]string, 0, len(m.compiled.byName))
	for name := range m.compiled.byName {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (m *Model) BellNames() []string {
	if m == nil || m.compiled == nil {
		return nil
	}
	names := make([]string, 0, len(m.compiled.bells))
	for name := range m.compiled.bells {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (m *Model) ValidatePendingBell(name string) error {
	if m == nil || m.compiled == nil || !m.compiled.validated {
		return fmt.Errorf("pending bell validation requires a validated model")
	}
	if _, exists := m.compiled.bells[name]; !exists {
		return fmt.Errorf("pending bell %q has no retained definition", name)
	}
	return nil
}

type Occurrence struct {
	model   *compiledModel
	event   *compiledEvent
	subject string
	payload []byte
}

func Occur[T any](model *Model, event EventType[T], seedID string, payload T) (Occurrence, error) {
	if model == nil || model.compiled == nil || !model.compiled.validated {
		return Occurrence{}, fmt.Errorf("event requires a validated model")
	}
	compiled, exists := model.compiled.byHandle[event.def]
	if !exists {
		return Occurrence{}, fmt.Errorf("event handle is not registered in this model")
	}
	if err := garden.ValidateID(seedID); err != nil {
		return Occurrence{}, fmt.Errorf("%s: %w", compiled.def.name, err)
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return Occurrence{}, err
	}
	if _, err := compiled.decode(raw); err != nil {
		return Occurrence{}, err
	}
	return Occurrence{model: model.compiled, event: compiled, subject: seedID, payload: raw}, nil
}

type EncodedOccurrence struct {
	Name    string
	Subject string
	Payload []byte
}

func (m *Model) Encode(occurrence Occurrence) (EncodedOccurrence, error) {
	if m == nil || m.compiled == nil || occurrence.model != m.compiled || occurrence.event == nil ||
		m.compiled.byHandle[occurrence.event.def] != occurrence.event {
		return EncodedOccurrence{}, fmt.Errorf("occurrence must be bound to this validated model")
	}
	return EncodedOccurrence{
		Name: occurrence.event.def.name, Subject: occurrence.subject,
		Payload: append([]byte(nil), occurrence.payload...),
	}, nil
}

func (e *compiledEvent) decode(raw []byte) (map[string]any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	opening, err := decoder.Token()
	if err != nil || opening != json.Delim('{') {
		return nil, fmt.Errorf("%s: payload must be a JSON object", e.def.name)
	}
	data := map[string]json.RawMessage{}
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", e.def.name, err)
		}
		name, ok := token.(string)
		if !ok {
			return nil, fmt.Errorf("%s: invalid field name", e.def.name)
		}
		if _, exists := data[name]; exists {
			return nil, fmt.Errorf("%s: duplicate field %s", e.def.name, name)
		}
		if _, exists := e.fields[name]; !exists {
			return nil, fmt.Errorf("%s: unknown field %s", e.def.name, name)
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, fmt.Errorf("%s/%s: %w", e.def.name, name, err)
		}
		data[name] = value
	}
	if _, err := decoder.Token(); err != nil {
		return nil, fmt.Errorf("%s: %w", e.def.name, err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, fmt.Errorf("%s: trailing payload data", e.def.name)
	}
	result := map[string]any{}
	for name, declared := range e.fields {
		rawValue, present := data[name]
		if (declared.required && !present) || (present && bytes.Equal(bytes.TrimSpace(rawValue), []byte("null"))) {
			return nil, fmt.Errorf("%s: missing or null field %s", e.def.name, name)
		}
		switch declared.kind {
		case reflect.String:
			var value string
			if present {
				if err := json.Unmarshal(rawValue, &value); err != nil {
					return nil, fmt.Errorf("%s/%s: %w", e.def.name, name, err)
				}
			}
			if declared.required && strings.TrimSpace(value) == "" {
				return nil, fmt.Errorf("%s: empty required field %s", e.def.name, name)
			}
			result[name] = value
		case reflect.Bool:
			var value bool
			if present {
				if err := json.Unmarshal(rawValue, &value); err != nil {
					return nil, fmt.Errorf("%s/%s: %w", e.def.name, name, err)
				}
			}
			result[name] = value
		}
	}
	return result, nil
}
