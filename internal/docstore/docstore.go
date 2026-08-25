// Design: docs/plans/2026-08-03-ext-a3-doc-store.md
// Design: docs/plans/2026-08-03-ext-a3.1-doc-store-physical-schema.md
package docstore

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Result-set limits. Measured (2026-08-03, production ~/.attn): the lists attn
// pushes whole are tickets 7, sessions 11, notifications 8, workspaces 8.
const (
	DefaultLimit = 100
	MaxLimit     = 1000
)

const (
	FieldCreatedAt = "created_at"
	FieldUpdatedAt = "updated_at"
)

type FieldType string

const (
	FieldString FieldType = "string"
	FieldNumber FieldType = "number"
	FieldBool   FieldType = "bool"
)

type Op string

const (
	OpEq  Op = "eq"
	OpLt  Op = "lt"
	OpLte Op = "lte"
	OpGt  Op = "gt"
	OpGte Op = "gte"
)

var opSQL = map[Op]string{OpEq: "=", OpLt: "<", OpLte: "<=", OpGt: ">", OpGte: ">="}

func FilterOps() []Op { return []Op{OpEq, OpLt, OpLte, OpGt, OpGte} }

type FieldSpec struct {
	Name string    `json:"name"`
	Type FieldType `json:"type"`
}

// Table is minted by the store and filled in on read; Compile refuses a non-minted Table.
type CollectionSchema struct {
	Namespace  string      `json:"namespace"`
	Collection string      `json:"collection"`
	Fields     []FieldSpec `json:"fields"`
	Table      string      `json:"-"`
}

type Filter struct {
	Field string `json:"field"`
	Op    Op     `json:"op"`
	Value any    `json:"value"`
}

type Sort struct {
	Field string `json:"field"`
	Desc  bool   `json:"desc,omitempty"`
}

// A zero Limit means DefaultLimit, never "unbounded".
type Query struct {
	Namespace  string   `json:"namespace"`
	Collection string   `json:"collection"`
	Filters    []Filter `json:"filters,omitempty"`
	Sort       *Sort    `json:"sort,omitempty"`
	Limit      int      `json:"limit,omitempty"`
	After      string   `json:"after,omitempty"`
}

type Document struct {
	ID        string          `json:"id"`
	Body      json.RawMessage `json:"body"`
	Rev       int64           `json:"rev"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

// int64: an int32 overflow (~7 years at 10 writes/s to one document) would silently make a stale check pass.
const (
	FirstRev     int64 = 1
	ExpectAbsent int64 = 0
)

type ConflictError struct {
	Namespace  string
	Collection string
	ID         string
	Expected   int64
	// Actual is meaningless when Found is false.
	Found  bool
	Actual int64
}

func (e *ConflictError) Error() string {
	addr := Address(e.Namespace, e.Collection, e.ID)
	switch {
	case e.Expected == ExpectAbsent:
		return fmt.Sprintf("docstore: %s already exists at rev %d, and this write expected it not to exist yet", addr, e.Actual)
	case !e.Found:
		return fmt.Sprintf("docstore: %s expected rev %d but no document is there; it was removed since you read it", addr, e.Expected)
	default:
		return fmt.Sprintf("docstore: %s expected rev %d but is at rev %d; re-read it and apply your change to that version", addr, e.Expected, e.Actual)
	}
}

func IsConflict(err error) bool {
	var conflict *ConflictError
	return errors.As(err, &conflict)
}

type UndeclaredCollectionError struct {
	Namespace  string
	Collection string
}

func (e *UndeclaredCollectionError) Error() string {
	return fmt.Sprintf("docstore: %s/%s is not declared; declare it with `attn doc define` before reading or writing it",
		e.Namespace, e.Collection)
}

func IsUndeclaredCollection(err error) bool {
	var undeclared *UndeclaredCollectionError
	return errors.As(err, &undeclared)
}

type InvalidQueryError struct{ Err error }

func (e *InvalidQueryError) Error() string { return e.Err.Error() }
func (e *InvalidQueryError) Unwrap() error { return e.Err }

func IsInvalidQuery(err error) bool {
	var invalid *InvalidQueryError
	return errors.As(err, &invalid)
}

func InvalidQuery(err error) error {
	if err == nil {
		return nil
	}
	if IsInvalidQuery(err) {
		return err
	}
	return &InvalidQueryError{Err: err}
}

type Compiled struct {
	Table string
	Where string
	Args  []any
	Order string
	Limit int
}

var (
	namePart      = `[a-z0-9][a-z0-9_-]*`
	namespaceRe   = regexp.MustCompile(`^` + namePart + `/` + namePart + `$`)
	collectionRe  = regexp.MustCompile(`^` + namePart + `$`)
	documentIDRe  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	fieldNameRe   = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	tableNameRe   = regexp.MustCompile(`^doc_[1-9][0-9]*$`)
	reservedField = map[string]bool{FieldCreatedAt: true, FieldUpdatedAt: true}
)

// These names are spliced into SQL as identifiers; each must derive from an integer row id or a fieldNameRe-checked name.
const (
	tablePrefix = "doc_"
	// Keeps a declared field (`id`, `body`) from shadowing the columns the store owns.
	fieldColumnPrefix = "f_"
)

func TableName(id int64) string {
	return fmt.Sprintf("%s%d", tablePrefix, id)
}

func ValidateTableName(name string) error {
	if name == "" {
		return fmt.Errorf("docstore: collection has no table; a declaration must be read from the store before it can be queried")
	}
	if !tableNameRe.MatchString(name) {
		return fmt.Errorf("docstore: %q is not a minted table name", name)
	}
	return nil
}

func FieldColumn(field string) string {
	return fieldColumnPrefix + field
}

func FieldExpression(field string) string {
	return "json_extract(body, '$." + field + "')"
}

func ColumnAffinity(t FieldType) string {
	switch t {
	case FieldNumber:
		return "NUMERIC"
	case FieldBool:
		return "INTEGER"
	default:
		return "TEXT"
	}
}

func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func ValidateNamespace(ns string) error {
	if ns == "" {
		return fmt.Errorf("docstore: namespace is required, as owner/name (for example app/approval-gate)")
	}
	if !namespaceRe.MatchString(ns) {
		return fmt.Errorf("docstore: namespace %q is not owner/name, where each part is lowercase letters, digits, - or _ (for example app/approval-gate)", ns)
	}
	return nil
}

func ValidateCollection(name string) error {
	if name == "" {
		return fmt.Errorf("docstore: collection is required")
	}
	if !collectionRe.MatchString(name) {
		return fmt.Errorf("docstore: collection %q must be lowercase letters, digits, - or _", name)
	}
	return nil
}

func ValidateDocumentID(id string) error {
	if id == "" {
		return fmt.Errorf("docstore: document id is required")
	}
	if !documentIDRe.MatchString(id) {
		return fmt.Errorf("docstore: document id %q must start alphanumeric and contain only letters, digits, ., - or _", id)
	}
	return nil
}

// Field names must be plain identifiers: a declared field becomes both a JSON path and an executed column name.
func (s CollectionSchema) Validate() error {
	if err := ValidateNamespace(s.Namespace); err != nil {
		return err
	}
	if err := ValidateCollection(s.Collection); err != nil {
		return err
	}
	seen := make(map[string]bool, len(s.Fields))
	for _, f := range s.Fields {
		switch {
		case f.Name == "":
			return fmt.Errorf("docstore: %s/%s declares a field with no name", s.Namespace, s.Collection)
		case reservedField[f.Name]:
			return fmt.Errorf("docstore: %s/%s declares %q, which is reserved — %s and %s are always queryable and must not be declared",
				s.Namespace, s.Collection, f.Name, FieldCreatedAt, FieldUpdatedAt)
		case !fieldNameRe.MatchString(f.Name):
			return fmt.Errorf("docstore: %s/%s declares field %q, which must start with a letter or _ and contain only letters, digits or _",
				s.Namespace, s.Collection, f.Name)
		case seen[f.Name]:
			return fmt.Errorf("docstore: %s/%s declares field %q twice", s.Namespace, s.Collection, f.Name)
		}
		switch f.Type {
		case FieldString, FieldNumber, FieldBool:
		default:
			return fmt.Errorf("docstore: %s/%s declares field %q with type %q; use %s, %s or %s",
				s.Namespace, s.Collection, f.Name, f.Type, FieldString, FieldNumber, FieldBool)
		}
		seen[f.Name] = true
	}
	return nil
}

func (s CollectionSchema) field(name string) (FieldSpec, bool) {
	for _, f := range s.Fields {
		if f.Name == name {
			return f, true
		}
	}
	return FieldSpec{}, false
}

func (s CollectionSchema) declaredNames() string {
	names := make([]string, 0, len(s.Fields)+2)
	for _, f := range s.Fields {
		names = append(names, f.Name)
	}
	sort.Strings(names)
	names = append(names, FieldCreatedAt, FieldUpdatedAt)
	return strings.Join(names, ", ")
}

// anchor is the document q.After names; nil with q.After set is an error, not an empty page.
func (q Query) Compile(schema CollectionSchema, anchor *Document) (Compiled, error) {
	compiled, err := q.compile(schema, anchor)
	if err != nil {
		return Compiled{}, InvalidQuery(err)
	}
	return compiled, nil
}

func (q Query) compile(schema CollectionSchema, anchor *Document) (Compiled, error) {
	if err := ValidateNamespace(q.Namespace); err != nil {
		return Compiled{}, err
	}
	if err := ValidateCollection(q.Collection); err != nil {
		return Compiled{}, err
	}
	if schema.Namespace != q.Namespace || schema.Collection != q.Collection {
		return Compiled{}, fmt.Errorf("docstore: query targets %s/%s but was compiled against the declaration for %s/%s",
			q.Namespace, q.Collection, schema.Namespace, schema.Collection)
	}
	if err := ValidateTableName(schema.Table); err != nil {
		return Compiled{}, err
	}

	// No namespace/collection predicate: the table IS the collection, so the isolation is structural.
	var where []string
	var args []any

	for _, f := range q.Filters {
		op, ok := opSQL[f.Op]
		if !ok {
			return Compiled{}, fmt.Errorf("docstore: %s/%s filter on %q uses operator %q; use %s, %s, %s, %s or %s",
				q.Namespace, q.Collection, f.Field, f.Op, OpEq, OpLt, OpLte, OpGt, OpGte)
		}
		expr, spec, err := q.fieldExpr(schema, f.Field, "filter")
		if err != nil {
			return Compiled{}, err
		}
		val, err := q.bindValue(f, spec)
		if err != nil {
			return Compiled{}, err
		}
		where = append(where, expr+" "+op+" ?")
		args = append(args, val)
	}

	sortExpr := ""
	desc := false
	order := "id ASC"
	if q.Sort != nil {
		expr, _, err := q.fieldExpr(schema, q.Sort.Field, "sort")
		if err != nil {
			return Compiled{}, err
		}
		sortExpr, desc = expr, q.Sort.Desc
		dir := "ASC"
		if desc {
			dir = "DESC"
		}
		order = expr + " " + dir + ", id " + dir
	}

	if q.After != "" || anchor != nil {
		clause, cursorArgs, err := q.afterTuple(schema.Table, sortExpr, desc, anchor)
		if err != nil {
			return Compiled{}, err
		}
		where = append(where, clause)
		args = append(args, cursorArgs...)
	}

	limit := q.Limit
	switch {
	case limit == 0:
		limit = DefaultLimit
	case limit < 0:
		return Compiled{}, fmt.Errorf("docstore: %s/%s limit is %d; a limit must be positive", q.Namespace, q.Collection, limit)
	case limit > MaxLimit:
		return Compiled{}, fmt.Errorf("docstore: %s/%s limit is %d, above the maximum of %d; page instead, passing the last document's id as the query's after cursor",
			q.Namespace, q.Collection, limit, MaxLimit)
	}

	return Compiled{
		Table: schema.Table,
		Where: strings.Join(where, " AND "),
		Args:  args,
		Order: order,
		Limit: limit,
	}, nil
}

// NULL compares as nothing, so a missing or JSON-null sort value is branched on rather than bound; SQLite sorts NULL first.
func (q Query) afterTuple(table, sortExpr string, desc bool, anchor *Document) (string, []any, error) {
	if q.After == "" {
		return "", nil, fmt.Errorf("docstore: %s/%s was compiled with a cursor document but no after id", q.Namespace, q.Collection)
	}
	if err := ValidateDocumentID(q.After); err != nil {
		return "", nil, err
	}
	if anchor == nil {
		return "", nil, fmt.Errorf("docstore: %s/%s cannot page after %q, which no longer exists; page again from the start, or use the id of a document that is still stored",
			q.Namespace, q.Collection, q.After)
	}
	if anchor.ID != q.After {
		return "", nil, fmt.Errorf("docstore: %s/%s was compiled to page after %q but given document %q", q.Namespace, q.Collection, q.After, anchor.ID)
	}

	cmp := ">"
	if desc {
		cmp = "<"
	}
	if sortExpr == "" {
		return "id " + cmp + " ?", []any{q.After}, nil
	}

	isNull, err := q.anchorSortIsNull(anchor)
	if err != nil {
		return "", nil, err
	}
	if isNull {
		if desc {
			return "(" + sortExpr + " IS NULL AND id < ?)", []any{q.After}, nil
		}
		return "(" + sortExpr + " IS NOT NULL OR id > ?)", []any{q.After}, nil
	}

	// Read the anchor's sort value back through the ORDER BY column, not bound from Go: the column's affinity must govern the comparison.
	value := "(SELECT " + sortExpr + " FROM " + table + " WHERE id = ?)"
	valueArgs := []any{q.After}

	clause := "(" + sortExpr + " > " + value + " OR (" + sortExpr + " = " + value + " AND id > ?))"
	if desc {
		// Descending puts NULLs last, so they are past any non-NULL anchor.
		clause = "(" + sortExpr + " IS NULL OR " + sortExpr + " < " + value + " OR (" + sortExpr + " = " + value + " AND id < ?))"
	}
	args := make([]any, 0, len(valueArgs)*2+1)
	args = append(args, valueArgs...)
	args = append(args, valueArgs...)
	args = append(args, q.After)
	return clause, args, nil
}

func (q Query) anchorSortIsNull(anchor *Document) (bool, error) {
	if reservedField[q.Sort.Field] {
		return false, nil
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(anchor.Body, &body); err != nil {
		return false, fmt.Errorf("docstore: %s/%s cannot page after %q: its body is not a JSON object (%w)", q.Namespace, q.Collection, anchor.ID, err)
	}
	raw, ok := body[q.Sort.Field]
	if !ok {
		return true, nil
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return false, fmt.Errorf("docstore: %s/%s cannot page after %q: its %q is not valid JSON (%w)", q.Namespace, q.Collection, anchor.ID, q.Sort.Field, err)
	}
	return value == nil, nil
}

// fieldExpr resolves a field reference to SQL: a reserved name literally,
func (q Query) fieldExpr(schema CollectionSchema, name, use string) (string, FieldSpec, error) {
	if name == "" {
		return "", FieldSpec{}, fmt.Errorf("docstore: %s/%s has a %s with no field name", q.Namespace, q.Collection, use)
	}
	if reservedField[name] {
		return name, FieldSpec{Name: name, Type: FieldString}, nil
	}
	spec, ok := schema.field(name)
	if !ok {
		return "", FieldSpec{}, fmt.Errorf("docstore: %s/%s cannot %s on %q, which the collection does not declare (queryable: %s)",
			q.Namespace, q.Collection, use, name, schema.declaredNames())
	}
	return quoteIdent(FieldColumn(name)), spec, nil
}

// A number field against a string bound would silently match nothing.
func (q Query) bindValue(f Filter, spec FieldSpec) (any, error) {
	mismatch := func(want string) error {
		return fmt.Errorf("docstore: %s/%s filter on %q needs a %s value, got %T (%v)",
			q.Namespace, q.Collection, f.Field, want, f.Value, f.Value)
	}
	if f.Value == nil {
		return nil, fmt.Errorf("docstore: %s/%s filter on %q has no value", q.Namespace, q.Collection, f.Field)
	}
	// Re-encode to TimeFormat: a raw "…T10:00:00Z" bound sorts above every stamp in that second.
	if reservedField[f.Field] {
		switch v := f.Value.(type) {
		case string:
			t, err := ParseTime(v)
			if err != nil {
				return nil, fmt.Errorf("docstore: %s/%s filter on %q needs an RFC3339 timestamp, got %q",
					q.Namespace, q.Collection, f.Field, v)
			}
			return t.Format(TimeFormat), nil
		case time.Time:
			return v.UTC().Format(TimeFormat), nil
		default:
			return nil, mismatch("timestamp or string")
		}
	}
	switch spec.Type {
	case FieldString:
		s, ok := f.Value.(string)
		if !ok {
			return nil, mismatch("string")
		}
		return s, nil
	case FieldNumber:
		switch v := f.Value.(type) {
		case float64:
			return v, nil
		case float32:
			return float64(v), nil
		case int:
			return float64(v), nil
		case int64:
			return float64(v), nil
		case json.Number:
			n, err := v.Float64()
			if err != nil {
				return nil, mismatch("number")
			}
			return n, nil
		default:
			return nil, mismatch("number")
		}
	case FieldBool:
		b, ok := f.Value.(bool)
		if !ok {
			return nil, mismatch("bool")
		}
		// json_extract yields 1/0 for JSON booleans.
		if b {
			return 1, nil
		}
		return 0, nil
	}
	return nil, fmt.Errorf("docstore: %s/%s filter on %q has undeclared type %q", q.Namespace, q.Collection, f.Field, spec.Type)
}

// Stamps are ordered as text, so the fraction is fixed-width (nine digits, always present) and the zone always "Z"; migration 91 rewrote the stored stamps.
const TimeFormat = "2006-01-02T15:04:05.000000000Z07:00"

// Accepts any RFC3339 form, including the pre-migration-91 trailing-zero-stripped stamps this store once handed out.
func ParseTime(s string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, err
	}
	return t.UTC(), nil
}

func Target(namespace, collection string) string {
	return namespace + "/" + collection
}

func (q Query) Target() string { return Target(q.Namespace, q.Collection) }

func Address(namespace, collection, id string) string {
	return Target(namespace, collection) + "/" + id
}

func ValidateBody(body []byte) error {
	if len(body) == 0 {
		return fmt.Errorf("docstore: document body is required")
	}
	var probe any
	if err := json.Unmarshal(body, &probe); err != nil {
		return fmt.Errorf("docstore: document body is not valid JSON: %w", err)
	}
	if _, ok := probe.(map[string]any); !ok {
		return fmt.Errorf("docstore: document body must be a JSON object, got %T", probe)
	}
	return nil
}
