package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/command"
	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/viewlang"
	"github.com/victorarias/attn/internal/viewlang/catalog"
	"github.com/victorarias/attn/internal/views"
)

// viewStore is the set a kept view of a profile keeps, one collection of the
// document store.
type viewStore struct {
	profileID string
	view      string
	set       string
}

const viewStoresNamespace = "views/"

func (s viewStore) namespace() string  { return viewStoresNamespace + s.profileID }
func (s viewStore) collection() string { return s.view + "_" + s.set }

// storeOfSource is the store a store= source names in a view kept as kept:
// "view/set" names another kept view's, a bare set the view's own.
func storeOfSource(id string, kept keptViewKey, profileID string) (viewStore, error) {
	if kept.name != "" {
		profileID = kept.profileID
	}
	if owner, set, shared := strings.Cut(id, "/"); shared {
		return viewStore{profileID: profileID, view: owner, set: set}, nil
	}
	if kept.name == "" {
		return viewStore{}, errViewNotKept
	}
	return viewStore{profileID: kept.profileID, view: kept.name, set: id}, nil
}

var errViewNotKept = errors.New("only a kept view has a store: keep the view with view_keep, then show it with view_show")

// ownViewStore is the store= source of the view's own store, if it has one.
func ownViewStore(view *protocol.ViewDocument) (protocol.ViewSource, bool) {
	for _, source := range view.Sources {
		if source.Kind == protocol.ViewSourceKindStore && !strings.Contains(source.ID, "/") {
			return source, true
		}
	}
	return protocol.ViewSource{}, false
}

func ownsViewStore(view *protocol.ViewDocument) bool {
	_, owns := ownViewStore(view)
	return owns
}

// withUnkeptStoreProblem is view with the problem a store of its own has
// while document is not a kept view.
func (d *Daemon) withUnkeptStoreProblem(view *protocol.ViewDocument, document tileDocument) *protocol.ViewDocument {
	if view == nil {
		return view
	}
	if !ownsViewStore(view) {
		return view
	}
	if kept, err := d.keptViewOf(document); err != nil || kept.name != "" {
		return view
	}
	line := 0
	eachViewPart(view.Parts, func(part protocol.ViewPart) {
		if part.Data != nil && part.Data.Fields != nil && line == 0 {
			line = part.Line
		}
	})
	filled := *view
	filled.Problems = append(slices.Clone(view.Problems), protocol.ViewProblem{Line: line, Text: "only a kept view has a store", Fix: "Keep the view with view_keep, then show it with view_show."})
	return &filled
}

// heldKeptView is the kept view a shown document is the view file of.
func (d *Daemon) heldKeptView(document tileDocument) keptViewKey {
	delivery := &d.desktopTiles
	delivery.mu.Lock()
	defer delivery.mu.Unlock()
	if held := delivery.documents[document]; held != nil {
		return held.kept
	}
	return keptViewKey{}
}

// keptViewOf is the kept view whose folder document is, or none.
func (d *Daemon) keptViewOf(document tileDocument) (keptViewKey, error) {
	if path.Base(document.path) != views.ViewFile {
		return keptViewKey{}, nil
	}
	holder := d.machineFor(document.host)
	machines := []string{holder.ID()}
	if !holder.IsRemote() {
		machines = append(machines, "")
	}
	view, kept, err := d.store.KeptViewAt(path.Dir(document.path), machines...)
	if err != nil || !kept {
		return keptViewKey{}, err
	}
	return keptViewKey{profileID: view.ProfileID, name: view.Name}, nil
}

// viewStoreFields is the fields the newest version of the store's view
// declares for it.
func (d *Daemon) viewStoreFields(s viewStore) ([]protocol.ViewStoreField, error) {
	view, kept, err := d.store.KeptView(s.profileID, s.view)
	if err != nil {
		return nil, err
	}
	if !kept {
		return nil, command.Refuse(command.CodeNotFound, "this profile keeps no view %s", s.view)
	}
	text, err := d.store.ViewFile(s.profileID, s.view, view.Version, views.ViewFile)
	if err != nil {
		return nil, err
	}
	var fields []protocol.ViewStoreField
	eachViewPart(viewlang.Check(string(text)).Parts, func(part protocol.ViewPart) {
		if data := part.Data; data != nil && data.Fields != nil && protocol.Deref(data.Store) == s.set {
			fields = data.Fields
		}
	})
	if fields == nil {
		return nil, command.Refuse(command.CodeNotFound, "%s keeps no store %s", s.view, s.set)
	}
	return fields, nil
}

var docstoreFieldTypes = map[protocol.ViewStoreType]docstore.FieldType{
	protocol.ViewStoreTypeText:   docstore.FieldString,
	protocol.ViewStoreTypeNumber: docstore.FieldNumber,
	protocol.ViewStoreTypeYesNo:  docstore.FieldBool,
}

// declaredViewStore declares the store's collection with fields, once they
// differ from what it holds, and answers its schema.
func (d *Daemon) declaredViewStore(s viewStore, fields []protocol.ViewStoreField) (docstore.CollectionSchema, error) {
	want := docstore.CollectionSchema{Namespace: s.namespace(), Collection: s.collection()}
	for _, field := range fields {
		want.Fields = append(want.Fields, docstore.FieldSpec{Name: field.Name, Type: docstoreFieldTypes[field.Type]})
	}
	schema, declared, err := d.store.DocumentCollection(want.Namespace, want.Collection)
	if err != nil {
		return docstore.CollectionSchema{}, err
	}
	if declared && slices.Equal(schema.Fields, want.Fields) {
		return *schema, nil
	}
	if _, err := d.store.DefineDocumentCollection(want, time.Now()); err != nil {
		return docstore.CollectionSchema{}, err
	}
	if schema, _, err = d.store.DocumentCollection(want.Namespace, want.Collection); err != nil {
		return docstore.CollectionSchema{}, err
	}
	return *schema, nil
}

// viewStoreItems is the store's items, oldest first, each its id and the
// fields the view declares now.
func (d *Daemon) viewStoreItems(s viewStore) ([]any, error) {
	fields, err := d.viewStoreFields(s)
	if err != nil {
		return nil, err
	}
	schema, declared, err := d.store.DocumentCollection(s.namespace(), s.collection())
	if err != nil || !declared {
		return []any{}, err
	}
	documents, err := d.store.ReadAllDocuments(*schema)
	if err != nil {
		return nil, err
	}
	slices.SortFunc(documents, func(a, b docstore.Document) int {
		if c := a.CreatedAt.Compare(b.CreatedAt); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	items := make([]any, 0, len(documents))
	for _, document := range documents {
		items = append(items, shownViewItem(document, fields))
	}
	return items, nil
}

// shownViewItem is the item as a view shows it: its id and each declared
// field, a field it lacks or holds of another type as its type's empty value.
func shownViewItem(document docstore.Document, fields []protocol.ViewStoreField) map[string]any {
	var body map[string]any
	_ = json.Unmarshal(document.Body, &body)
	item := map[string]any{"id": document.ID}
	for _, field := range fields {
		value, ok := body[field.Name]
		if !ok || !ofViewStoreType(value, field.Type) {
			value = emptyViewStoreValue(field.Type)
		}
		item[field.Name] = value
	}
	return item
}

func ofViewStoreType(value any, kind protocol.ViewStoreType) bool {
	switch value.(type) {
	case string:
		return kind == protocol.ViewStoreTypeText
	case float64:
		return kind == protocol.ViewStoreTypeNumber
	case bool:
		return kind == protocol.ViewStoreTypeYesNo
	}
	return false
}

func emptyViewStoreValue(kind protocol.ViewStoreType) any {
	switch kind {
	case protocol.ViewStoreTypeNumber:
		return float64(0)
	case protocol.ViewStoreTypeYesNo:
		return false
	}
	return ""
}

// viewItemFields reads given, the fields an agent wrote for an item, against
// the store's fields.
func viewItemFields(given json.RawMessage, fields []protocol.ViewStoreField) (map[string]any, error) {
	var values map[string]any
	if err := json.Unmarshal(given, &values); err != nil || values == nil {
		return nil, command.Refuse(command.CodeBadItem, "item is not a JSON object of fields")
	}
	for name, value := range values {
		i := slices.IndexFunc(fields, func(f protocol.ViewStoreField) bool { return f.Name == name })
		if i < 0 {
			return nil, command.Refuse(command.CodeBadItem, "the store has no field %q; its fields are %s", name, describeViewStoreFields(fields))
		}
		if !ofViewStoreType(value, fields[i].Type) {
			return nil, command.Refuse(command.CodeBadItem, "%s holds %s, and the item gives it %s", name, viewStoreTypeWord(fields[i].Type), jsonWord(value))
		}
	}
	return values, nil
}

func describeViewStoreFields(fields []protocol.ViewStoreField) string {
	described := make([]string, 0, len(fields))
	for _, field := range fields {
		described = append(described, fmt.Sprintf("%s (%s)", field.Name, field.Type))
	}
	return strings.Join(described, ", ")
}

func viewStoreTypeWord(kind protocol.ViewStoreType) string {
	switch kind {
	case protocol.ViewStoreTypeNumber:
		return "a number"
	case protocol.ViewStoreTypeYesNo:
		return "true or false"
	}
	return "text"
}

func jsonWord(value any) string {
	switch value.(type) {
	case nil:
		return "null"
	case string:
		return "text"
	case float64:
		return "a number"
	case bool:
		return "true or false"
	case []any:
		return "a list"
	}
	return "an object"
}

// viewStoreValue is text, which a control or an entry gives, as the value
// of a field of kind.
func viewStoreValue(text string, field protocol.ViewStoreField) (any, error) {
	switch field.Type {
	case protocol.ViewStoreTypeNumber:
		number, err := strconv.ParseFloat(strings.TrimSpace(text), 64)
		if err != nil {
			return nil, fmt.Errorf("%s holds a number, and %q is not one", field.Name, text)
		}
		return number, nil
	case protocol.ViewStoreTypeYesNo:
		switch strings.ToLower(strings.TrimSpace(text)) {
		case "yes", "true":
			return true, nil
		case "no", "false":
			return false, nil
		}
		return nil, fmt.Errorf("%s holds yes or no, and %q is neither", field.Name, text)
	}
	return text, nil
}

// addViewStoreItem adds an item with values, every other field empty.
func (d *Daemon) addViewStoreItem(s viewStore, values map[string]any) (map[string]any, error) {
	fields, err := d.viewStoreFields(s)
	if err != nil {
		return nil, err
	}
	body := map[string]any{}
	for _, field := range fields {
		body[field.Name] = emptyViewStoreValue(field.Type)
	}
	for name, value := range values {
		body[name] = value
	}
	absent := docstore.ExpectAbsent
	document, err := d.writeViewStoreItem(s, fields, uuid.NewString()[:8], body, &absent)
	if err != nil {
		return nil, err
	}
	return shownViewItem(document, fields), nil
}

// changeViewStoreItem changes the item id with change, which gets its stored
// fields, a field an edit removed among them.
func (d *Daemon) changeViewStoreItem(s viewStore, id string, change func(body map[string]any, fields []protocol.ViewStoreField) error) (map[string]any, error) {
	fields, err := d.viewStoreFields(s)
	if err != nil {
		return nil, err
	}
	read, declared, err := d.store.ReadDocument(s.namespace(), s.collection(), id)
	if err != nil {
		return nil, err
	}
	if !declared || !read.Found {
		return nil, command.Refuse(command.CodeNotFound, "the store %s of %s holds no item %s", s.set, s.view, id)
	}
	body := map[string]any{}
	_ = json.Unmarshal(read.Document.Body, &body)
	if err := change(body, fields); err != nil {
		return nil, err
	}
	document, err := d.writeViewStoreItem(s, fields, id, body, &read.Document.Rev)
	if err != nil {
		return nil, err
	}
	return shownViewItem(document, fields), nil
}

func (d *Daemon) writeViewStoreItem(s viewStore, fields []protocol.ViewStoreField, id string, body map[string]any, expected *int64) (docstore.Document, error) {
	schema, err := d.declaredViewStore(s, fields)
	if err != nil {
		return docstore.Document{}, err
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return docstore.Document{}, err
	}
	now := time.Now()
	fact := documentChangedFact(schema.Namespace, schema.Collection, id, false)
	written, err := d.store.CommitDocumentWrite(store.DocumentWrite{Schema: schema, ID: id, Body: encoded, Expected: expected}, fact, now)
	if docstore.IsConflict(err) {
		return docstore.Document{}, fmt.Errorf("the item %s changed meanwhile; try again", id)
	}
	if err != nil {
		return docstore.Document{}, err
	}
	d.announceCommittedWrite(fact, written.Seq)
	return docstore.Document{ID: id, Body: encoded, Rev: written.Rev}, nil
}

// removeViewStoreItem removes the item id.
func (d *Daemon) removeViewStoreItem(s viewStore, id string) error {
	if _, err := d.viewStoreFields(s); err != nil {
		return err
	}
	schema, declared, err := d.store.DocumentCollection(s.namespace(), s.collection())
	if err != nil {
		return err
	}
	missing := command.Refuse(command.CodeNotFound, "the store %s of %s holds no item %s", s.set, s.view, id)
	if !declared {
		return missing
	}
	fact := documentChangedFact(schema.Namespace, schema.Collection, id, true)
	written, err := d.store.CommitDocumentWrite(store.DocumentWrite{Schema: *schema, ID: id, Delete: true}, fact, time.Now())
	if err != nil {
		return err
	}
	if !written.Changed {
		return missing
	}
	d.announceCommittedWrite(fact, written.Seq)
	return nil
}

// deleteViewStores deletes every store the kept view keeps.
func (d *Daemon) deleteViewStores(view store.KeptView) ([]protocol.ViewStoreRemoved, error) {
	collections, err := d.store.ListDocumentCollections()
	if err != nil {
		return nil, err
	}
	owner := viewStore{profileID: view.ProfileID, view: view.Name}
	removed := []protocol.ViewStoreRemoved{}
	for _, collection := range collections {
		set, ours := strings.CutPrefix(collection.Collection, owner.view+"_")
		if collection.Namespace != owner.namespace() || !ours {
			continue
		}
		items, err := d.store.DeleteDocumentCollection(collection.Namespace, collection.Collection)
		if err != nil {
			return removed, err
		}
		d.coalesceSnapshots(func() { d.publishCollectionRemoved(collection.Namespace, collection.Collection, items) })
		removed = append(removed, protocol.ViewStoreRemoved{Set: set, Items: items})
	}
	return removed, nil
}

// viewStoreRun is the run of the store command msg names, from the part and
// rows it ran from, against the view's own store.
func viewStoreRun(view *protocol.ViewDocument, msg *protocol.ViewRunMessage, kept keptViewKey) (viewCommandRun, error) {
	call, found := viewCallOf(view.Parts, msg)
	if !found {
		return nil, fmt.Errorf("the view has no control for %s here", msg.Command)
	}
	source, owns := ownViewStore(view)
	if !owns {
		return nil, fmt.Errorf("%s changes the view's own store, and this view shows no store of its own", msg.Command)
	}
	s, err := storeOfSource(source.ID, kept, "")
	if err != nil {
		return nil, err
	}
	field := func(d *Daemon) (protocol.ViewStoreField, error) {
		fields, err := d.viewStoreFields(s)
		if err != nil {
			return protocol.ViewStoreField{}, err
		}
		i := slices.IndexFunc(fields, func(f protocol.ViewStoreField) bool { return len(call.Args) > 0 && f.Name == call.Args[0] })
		if i < 0 {
			return protocol.ViewStoreField{}, fmt.Errorf("the store has no field %s", strings.Join(call.Args, " "))
		}
		return fields[i], nil
	}
	if msg.Command == "add" {
		text := strings.TrimSpace(protocol.Deref(msg.Text))
		return func(_ context.Context, d *Daemon, _ tileDocument) error {
			if text == "" {
				return errors.New("type the item first")
			}
			f, err := field(d)
			if err != nil {
				return err
			}
			value, err := viewStoreValue(text, f)
			if err != nil {
				return err
			}
			_, err = d.addViewStoreItem(s, map[string]any{f.Name: value})
			return err
		}, nil
	}
	scope, found := viewCallScope(view.Parts, newViewScope(view), msg.Part, msg.Rows)
	id, _ := scope.field([]string{"id"}).(string)
	if !found || len(msg.Rows) == 0 || id == "" {
		return nil, fmt.Errorf("%s runs on a row of the store", msg.Command)
	}
	return func(_ context.Context, d *Daemon, _ tileDocument) error {
		if msg.Command == "remove" {
			return d.removeViewStoreItem(s, id)
		}
		f, err := field(d)
		if err != nil {
			return err
		}
		_, err = d.changeViewStoreItem(s, id, func(body map[string]any, _ []protocol.ViewStoreField) error {
			if msg.Command == "toggle" {
				on, _ := body[f.Name].(bool)
				body[f.Name] = !on
				return nil
			}
			value, err := viewStoreValue(call.Args[len(call.Args)-1], f)
			body[f.Name] = value
			return err
		})
		return err
	}, nil
}

// viewCallOf is the control of the part on msg's line that runs msg's
// command with its arguments.
func viewCallOf(parts []protocol.ViewPart, msg *protocol.ViewRunMessage) (protocol.ViewCommandCall, bool) {
	var calls []protocol.ViewCommandCall
	eachViewPart(parts, func(part protocol.ViewPart) {
		if part.Line != msg.Part {
			return
		}
		switch {
		case part.List != nil:
			if part.List.Slots.Press != nil {
				calls = append(calls, *part.List.Slots.Press)
			}
			calls = append(calls, part.List.Slots.Controls...)
			calls = append(calls, part.List.Slots.GroupControls...)
		case part.Entry != nil:
			calls = append(calls, part.Entry.Command)
		case part.Controls != nil:
			calls = append(calls, part.Controls.Controls...)
		}
	})
	for _, call := range calls {
		if call.Command == msg.Command && (msg.Args == nil || slices.Equal(call.Args, msg.Args)) {
			return call, true
		}
	}
	return protocol.ViewCommandCall{}, false
}

func isViewStoreCommand(name string) bool {
	return slices.ContainsFunc(catalog.StoreCommands, func(c catalog.StoreCommand) bool { return c.Name == name })
}

func (d *Daemon) viewStoreCommands() map[string]command.Handler {
	return map[string]command.Handler{
		"view data list":   command.Typed(d.runViewDataList),
		"view data add":    command.Typed(d.runViewDataAdd),
		"view data set":    command.Typed(d.runViewDataSet),
		"view data remove": command.Typed(d.runViewDataRemove),
	}
}

// callerViewStore is the store set of the kept view the caller names.
func (d *Daemon) callerViewStore(c command.Caller, name, set string) (viewStore, error) {
	profile, err := d.viewsProfile(c)
	if err != nil {
		return viewStore{}, err
	}
	if views.BuiltIn(name) {
		return viewStore{}, command.Refuse(command.CodeBuiltIn, "%s is one of attn's own views, which keep no store", name)
	}
	return viewStore{profileID: profile.ID, view: name, set: set}, nil
}

func (d *Daemon) runViewDataList(_ context.Context, c command.Caller, a command.ViewDataListArgs) (any, error) {
	s, err := d.callerViewStore(c, a.View, a.Set)
	if err != nil {
		return nil, err
	}
	items, err := d.viewStoreItems(s)
	if err != nil {
		return nil, err
	}
	return protocol.ViewDataListResult{View: s.view, Set: s.set, Items: items}, nil
}

func (d *Daemon) runViewDataAdd(_ context.Context, c command.Caller, a command.ViewDataAddArgs) (any, error) {
	s, err := d.callerViewStore(c, a.View, a.Set)
	if err != nil {
		return nil, err
	}
	fields, err := d.viewStoreFields(s)
	if err != nil {
		return nil, err
	}
	values, err := viewItemFields(a.Item, fields)
	if err != nil {
		return nil, err
	}
	item, err := d.addViewStoreItem(s, values)
	if err != nil {
		return nil, err
	}
	return protocol.ViewDataAddResult{View: s.view, Set: s.set, Item: item}, nil
}

func (d *Daemon) runViewDataSet(_ context.Context, c command.Caller, a command.ViewDataSetArgs) (any, error) {
	s, err := d.callerViewStore(c, a.View, a.Set)
	if err != nil {
		return nil, err
	}
	item, err := d.changeViewStoreItem(s, a.ID, func(body map[string]any, fields []protocol.ViewStoreField) error {
		values, err := viewItemFields(a.Item, fields)
		for name, value := range values {
			body[name] = value
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	return protocol.ViewDataSetResult{View: s.view, Set: s.set, Item: item}, nil
}

func (d *Daemon) runViewDataRemove(_ context.Context, c command.Caller, a command.ViewDataRemoveArgs) (any, error) {
	s, err := d.callerViewStore(c, a.View, a.Set)
	if err != nil {
		return nil, err
	}
	if err := d.removeViewStoreItem(s, a.ID); err != nil {
		return nil, err
	}
	return protocol.ViewDataRemoveResult{View: s.view, Set: s.set, ID: a.ID}, nil
}
