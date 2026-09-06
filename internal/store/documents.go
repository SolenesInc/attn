package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/docstore"
)

// Every identifier spliced into SQL here comes from docstore — derived from an integer or
// a validated field name, never caller text.

const documentColumns = `id, body, rev, created_at, updated_at`

func (s *Store) DefineDocumentCollection(schema docstore.CollectionSchema, now time.Time) (bool, error) {
	if s.db == nil {
		return false, fmt.Errorf("store: no database")
	}
	if err := schema.Validate(); err != nil {
		return false, err
	}
	fields, err := json.Marshal(schema.Fields)
	if err != nil {
		return false, fmt.Errorf("store: encoding fields for %s/%s: %w", schema.Namespace, schema.Collection, err)
	}
	ts := now.UTC().Format(docstore.TimeFormat)

	tx, err := s.db.Begin()
	if err != nil {
		return false, fmt.Errorf("store: defining %s/%s: %w", schema.Namespace, schema.Collection, err)
	}
	defer func() { _ = tx.Rollback() }()

	existing, table, found, err := readCollectionTx(tx, schema.Namespace, schema.Collection)
	if err != nil {
		return false, err
	}

	if !found {
		res, err := tx.Exec(
			`INSERT INTO document_collections (namespace, collection, fields_json, updated_at) VALUES (?, ?, ?, ?)`,
			schema.Namespace, schema.Collection, string(fields), ts)
		if err != nil {
			return false, fmt.Errorf("store: defining %s/%s: %w", schema.Namespace, schema.Collection, err)
		}
		id, err := res.LastInsertId()
		if err != nil {
			return false, fmt.Errorf("store: defining %s/%s: %w", schema.Namespace, schema.Collection, err)
		}
		table = docstore.TableName(id)
		if err := createCollectionTable(tx, table, schema.Fields); err != nil {
			return false, fmt.Errorf("store: creating storage for %s/%s: %w", schema.Namespace, schema.Collection, err)
		}
	} else {
		if err := alterCollectionTable(tx, table, existing.Fields, schema.Fields); err != nil {
			return false, fmt.Errorf("store: redeclaring %s/%s: %w", schema.Namespace, schema.Collection, err)
		}
		if _, err := tx.Exec(
			`UPDATE document_collections SET fields_json = ?, updated_at = ? WHERE namespace = ? AND collection = ?`,
			string(fields), ts, schema.Namespace, schema.Collection); err != nil {
			return false, fmt.Errorf("store: redeclaring %s/%s: %w", schema.Namespace, schema.Collection, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("store: committing declaration of %s/%s: %w", schema.Namespace, schema.Collection, err)
	}
	return found, nil
}

func (s *Store) DocumentCollection(namespace, collection string) (*docstore.CollectionSchema, bool, error) {
	if s.db == nil {
		return nil, false, fmt.Errorf("store: no database")
	}
	schema, table, found, err := readCollection(s.db, namespace, collection)
	if err != nil || !found {
		return nil, false, err
	}
	schema.Table = table
	return &schema, true, nil
}

func (s *Store) ListDocumentCollections() ([]docstore.CollectionSchema, error) {
	if s.db == nil {
		return nil, fmt.Errorf("store: no database")
	}
	rows, err := s.db.Query(
		`SELECT id, namespace, collection, fields_json FROM document_collections ORDER BY namespace, collection`)
	if err != nil {
		return nil, fmt.Errorf("store: listing document collections: %w", err)
	}
	defer rows.Close()
	var out []docstore.CollectionSchema
	for rows.Next() {
		var (
			id     int64
			schema docstore.CollectionSchema
			fields string
		)
		if err := rows.Scan(&id, &schema.Namespace, &schema.Collection, &fields); err != nil {
			return nil, fmt.Errorf("store: scanning document collection: %w", err)
		}
		if err := json.Unmarshal([]byte(fields), &schema.Fields); err != nil {
			return nil, fmt.Errorf("store: decoding fields for %s/%s: %w", schema.Namespace, schema.Collection, err)
		}
		schema.Table = docstore.TableName(id)
		out = append(out, schema)
	}
	return out, rows.Err()
}

func (s *Store) DeleteDocumentCollection(namespace, collection string) (int, error) {
	if s.db == nil {
		return 0, fmt.Errorf("store: no database")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("store: deleting %s/%s: %w", namespace, collection, err)
	}
	defer func() { _ = tx.Rollback() }()

	_, table, found, err := readCollectionTx(tx, namespace, collection)
	if err != nil {
		return 0, err
	}
	if !found {
		return 0, nil
	}

	var n int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
		return 0, fmt.Errorf("store: counting %s/%s: %w", namespace, collection, err)
	}
	if _, err := tx.Exec(`DROP TABLE ` + table); err != nil {
		return 0, fmt.Errorf("store: dropping storage for %s/%s: %w", namespace, collection, err)
	}
	if _, err := tx.Exec(
		`DELETE FROM document_collections WHERE namespace = ? AND collection = ?`, namespace, collection); err != nil {
		return 0, fmt.Errorf("store: deleting declaration for %s/%s: %w", namespace, collection, err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("store: committing deletion of %s/%s: %w", namespace, collection, err)
	}
	return n, nil
}

func (s *Store) PutDocument(schema docstore.CollectionSchema, id string, body []byte, now time.Time, expected *int64) (int64, error) {
	table, err := s.documentTable(schema)
	if err != nil {
		return 0, err
	}
	return putDocumentWith(s.db, schema, table, id, body, now, expected)
}

func putDocumentWith(q rowQuerier, schema docstore.CollectionSchema, table, id string, body []byte, now time.Time, expected *int64) (int64, error) {
	ts := now.UTC().Format(docstore.TimeFormat)

	var (
		stmt string
		args []any
	)
	switch {
	case expected == nil:
		stmt = `INSERT INTO ` + table + ` (id, body, rev, created_at, updated_at)
		        VALUES (?, ?, ?, ?, ?)
		        ON CONFLICT(id) DO UPDATE SET
		          body=excluded.body,
		          rev=` + table + `.rev + 1,
		          updated_at=excluded.updated_at
		        RETURNING rev`
		args = []any{id, string(body), docstore.FirstRev, ts, ts}
	case *expected == docstore.ExpectAbsent:
		stmt = `INSERT INTO ` + table + ` (id, body, rev, created_at, updated_at)
		        VALUES (?, ?, ?, ?, ?)
		        ON CONFLICT(id) DO NOTHING
		        RETURNING rev`
		args = []any{id, string(body), docstore.FirstRev, ts, ts}
	default:
		stmt = `UPDATE ` + table + ` SET body = ?, rev = rev + 1, updated_at = ?
		        WHERE id = ? AND rev = ?
		        RETURNING rev`
		args = []any{string(body), ts, id, *expected}
	}

	var rev int64
	err := q.QueryRow(stmt, args...).Scan(&rev)
	if err == sql.ErrNoRows && expected != nil {
		return 0, documentConflictWith(q, schema, table, id, *expected)
	}
	if err != nil {
		return 0, fmt.Errorf("store: writing %s/%s/%s: %w", schema.Namespace, schema.Collection, id, err)
	}
	return rev, nil
}

func (s *Store) GetDocument(schema docstore.CollectionSchema, id string) (*docstore.Document, bool, error) {
	table, err := s.documentTable(schema)
	if err != nil {
		return nil, false, err
	}
	return getDocumentWith(s.db, schema.Namespace, schema.Collection, table, id)
}

func getDocumentWith(q rowQuerier, namespace, collection, table, id string) (*docstore.Document, bool, error) {
	row := q.QueryRow(`SELECT `+documentColumns+` FROM `+table+` WHERE id = ?`, id)
	doc, err := scanDocument(row)
	if err == sql.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("store: reading %s/%s/%s: %w", namespace, collection, id, err)
	}
	return doc, true, nil
}

func (s *Store) DeleteDocument(schema docstore.CollectionSchema, id string, expected *int64) (bool, error) {
	table, err := s.documentTable(schema)
	if err != nil {
		return false, err
	}
	return deleteDocumentWith(s.db, schema, table, id, expected)
}

func deleteDocumentWith(x execQuerier, schema docstore.CollectionSchema, table, id string, expected *int64) (bool, error) {
	if expected != nil && *expected == docstore.ExpectAbsent {
		// Treating "expect absent" as unconditional would delete a document the
		// caller was trying to protect.
		return false, fmt.Errorf("store: deleting %s/%s/%s: rev %d means the document must not exist, which a delete cannot expect; pass the revision you read, or none to delete unconditionally",
			schema.Namespace, schema.Collection, id, docstore.ExpectAbsent)
	}

	stmt := `DELETE FROM ` + table + ` WHERE id = ?`
	args := []any{id}
	if expected != nil {
		stmt += ` AND rev = ?`
		args = append(args, *expected)
	}
	res, err := x.Exec(stmt, args...)
	if err != nil {
		return false, fmt.Errorf("store: deleting %s/%s/%s: %w", schema.Namespace, schema.Collection, id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if n == 0 && expected != nil {
		return false, documentConflictWith(x, schema, table, id, *expected)
	}
	return n > 0, nil
}

// documentConflictWith describes a refused write, re-reading to name the revision that won.
// The re-read must run on whatever refused the write, or it reads a state it never saw.
func documentConflictWith(q rowQuerier, schema docstore.CollectionSchema, table, id string, expected int64) error {
	conflict := &docstore.ConflictError{
		Namespace: schema.Namespace, Collection: schema.Collection, ID: id, Expected: expected,
	}
	var rev int64
	switch err := q.QueryRow(`SELECT rev FROM `+table+` WHERE id = ?`, id).Scan(&rev); {
	case err == nil:
		conflict.Found = true
		conflict.Actual = rev
	case err != sql.ErrNoRows:
		return fmt.Errorf("store: %s/%s/%s was refused, and re-reading it to say why also failed: %w",
			schema.Namespace, schema.Collection, id, err)
	}
	return conflict
}

type DocumentWrite struct {
	Schema   docstore.CollectionSchema
	ID       string
	Body     []byte
	Delete   bool
	Expected *int64
}

type DocumentWriteResult struct {
	Rev     int64
	Seq     int64
	Changed bool
}

type DocumentCommit struct {
	Write DocumentWrite
	Fact  BusEvent
}

func (s *Store) CommitDocumentWrite(w DocumentWrite, fact BusEvent, now time.Time) (DocumentWriteResult, error) {
	results, err := s.commitDocumentWrites([]DocumentCommit{{Write: w, Fact: fact}}, now, true, nil)
	if err != nil {
		return DocumentWriteResult{}, err
	}
	return results[0], nil
}

func (s *Store) CommitDocumentWrites(commits []DocumentCommit, now time.Time) ([]DocumentWriteResult, error) {
	return s.commitDocumentWrites(commits, now, false, nil)
}

// CommitGardenDispatchWrites persists a new binding and its ordinary watch together.
// Callers use the committed binding as the receipt and skip this on replay.
func (s *Store) CommitGardenDispatchWrites(commits []DocumentCommit, watch GardenSeedWatch, now time.Time) ([]DocumentWriteResult, error) {
	return s.commitDocumentWrites(commits, now, false, &watch)
}

func (s *Store) commitDocumentWrites(commits []DocumentCommit, now time.Time, single bool, watch *GardenSeedWatch) ([]DocumentWriteResult, error) {
	if len(commits) == 0 {
		return []DocumentWriteResult{}, nil
	}
	tables := make([]string, len(commits))
	for i, commit := range commits {
		table, err := s.documentTable(commit.Write.Schema)
		if err != nil {
			return nil, err
		}
		tables[i] = table
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		w := commits[0].Write
		return nil, fmt.Errorf("store: writing %s/%s/%s: %w",
			w.Schema.Namespace, w.Schema.Collection, w.ID, err)
	}
	defer func() { _ = tx.Rollback() }()

	results, changed, err := commitDocumentWritesWith(tx, commits, tables, now)
	if err != nil {
		return nil, err
	}
	if watch != nil && watch.WatcherSessionID != "" && watch.SeedID != "" {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO garden_seed_watches(watcher_session_id, seed_id, created_at) VALUES (?, ?, ?)`, watch.WatcherSessionID, watch.SeedID, now.UTC().Format(sortableTimeFormat)); err != nil {
			return nil, fmt.Errorf("subscribe delegation dispatcher: %w", err)
		}
		changed = true
	}
	if !changed {
		return results, nil
	}

	if err := tx.Commit(); err != nil {
		if single {
			w := commits[0].Write
			return nil, fmt.Errorf("store: committing the write to %s/%s/%s: %w",
				w.Schema.Namespace, w.Schema.Collection, w.ID, err)
		}
		return nil, fmt.Errorf("store: committing %d document writes: %w", len(commits), err)
	}
	return results, nil
}

func commitDocumentWritesWith(tx *sql.Tx, commits []DocumentCommit, tables []string, now time.Time) ([]DocumentWriteResult, bool, error) {
	results := make([]DocumentWriteResult, len(commits))
	changed := false
	for i, commit := range commits {
		w := commit.Write
		out := &results[i]
		if w.Delete {
			existed, err := deleteDocumentWith(tx, w.Schema, tables[i], w.ID, w.Expected)
			if err != nil {
				return nil, false, err
			}
			out.Changed = existed
		} else {
			rev, err := putDocumentWith(tx, w.Schema, tables[i], w.ID, w.Body, now, w.Expected)
			if err != nil {
				return nil, false, err
			}
			out.Rev = rev
			out.Changed = true
		}

		if !out.Changed {
			continue
		}
		changed = true
		seq, err := appendBusEventWith(tx, commit.Fact, now)
		if err != nil {
			return nil, false, fmt.Errorf("store: announcing the write to %s/%s/%s: %w",
				w.Schema.Namespace, w.Schema.Collection, w.ID, err)
		}
		out.Seq = seq
	}
	return results, changed, nil
}

func (s *Store) queryDocuments(c docstore.Compiled) ([]docstore.Document, error) {
	if s.db == nil {
		return nil, fmt.Errorf("store: no database")
	}
	return queryDocumentsWith(s.db, c)
}

func queryDocumentsWith(q rowsQuerier, c docstore.Compiled) ([]docstore.Document, error) {
	if err := docstore.ValidateTableName(c.Table); err != nil {
		return nil, err
	}
	stmt := `SELECT ` + documentColumns + ` FROM ` + c.Table
	if c.Where != "" {
		stmt += ` WHERE ` + c.Where
	}
	stmt += ` ORDER BY ` + c.Order + ` LIMIT ?`

	rows, err := q.Query(stmt, append(append([]any{}, c.Args...), c.Limit)...)
	if err != nil {
		return nil, fmt.Errorf("store: querying documents: %w", err)
	}
	defer rows.Close()
	out := []docstore.Document{}
	for rows.Next() {
		doc, err := scanDocument(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scanning document: %w", err)
		}
		out = append(out, *doc)
	}
	return out, rows.Err()
}

type QueryRead struct {
	Schema    docstore.CollectionSchema
	Documents []docstore.Document
	AsOfSeq   int64
}

// readAsOfSeq is the log position an answer was true at, read inside the same transaction
// as the rows — outside it the number names a state the rows were never in.
func readAsOfSeq(q rowQuerier) (int64, error) {
	var seq int64
	if err := q.QueryRow(`SELECT COALESCE(MAX(seq), 0) FROM bus_events`).Scan(&seq); err != nil {
		return 0, fmt.Errorf("store: reading the log position of this answer: %w", err)
	}
	return seq, nil
}

type DocumentRead struct {
	Document *docstore.Document
	Found    bool
	AsOfSeq  int64
}

func (s *Store) ReadDocument(namespace, collection, id string) (DocumentRead, bool, error) {
	if s.db == nil {
		return DocumentRead{}, false, fmt.Errorf("store: no database")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return DocumentRead{}, false, fmt.Errorf("store: reading %s/%s/%s: %w", namespace, collection, id, err)
	}
	defer func() { _ = tx.Rollback() }()

	schema, table, found, err := readCollectionTx(tx, namespace, collection)
	if err != nil || !found {
		return DocumentRead{}, false, err
	}
	doc, ok, err := getDocumentWith(tx, schema.Namespace, schema.Collection, table, id)
	if err != nil {
		return DocumentRead{}, false, err
	}
	asOf, err := readAsOfSeq(tx)
	if err != nil {
		return DocumentRead{}, false, err
	}
	return DocumentRead{Document: doc, Found: ok, AsOfSeq: asOf}, true, nil
}

type CountRead struct {
	Schema  docstore.CollectionSchema
	Count   int
	AsOfSeq int64
}

func (s *Store) CountQuery(q docstore.Query) (CountRead, bool, error) {
	if s.db == nil {
		return CountRead{}, false, fmt.Errorf("store: no database")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return CountRead{}, false, fmt.Errorf("store: counting %s/%s: %w", q.Namespace, q.Collection, err)
	}
	defer func() { _ = tx.Rollback() }()

	schema, table, found, err := readCollectionTx(tx, q.Namespace, q.Collection)
	if err != nil || !found {
		return CountRead{}, false, err
	}
	var anchor *docstore.Document
	if q.After != "" {
		doc, ok, err := getDocumentWith(tx, schema.Namespace, schema.Collection, table, q.After)
		if err != nil {
			return CountRead{}, false, err
		}
		if ok {
			anchor = doc
		}
	}
	compiled, err := q.Compile(schema, anchor)
	if err != nil {
		return CountRead{}, false, err
	}
	if err := docstore.ValidateTableName(compiled.Table); err != nil {
		return CountRead{}, false, err
	}
	stmt := `SELECT COUNT(*) FROM ` + compiled.Table
	if compiled.Where != "" {
		stmt += ` WHERE ` + compiled.Where
	}
	var n int
	if err := tx.QueryRow(stmt, compiled.Args...).Scan(&n); err != nil {
		return CountRead{}, false, fmt.Errorf("store: counting %s/%s: %w", q.Namespace, q.Collection, err)
	}
	asOf, err := readAsOfSeq(tx)
	if err != nil {
		return CountRead{}, false, err
	}
	return CountRead{Schema: schema, Count: n, AsOfSeq: asOf}, true, nil
}

// ReadQuery answers a query in a single read transaction. Split apart, a statement can
// compile against one state and execute against another, silently returning wrong pages.
func (s *Store) ReadQuery(q docstore.Query) (QueryRead, bool, error) {
	if s.db == nil {
		return QueryRead{}, false, fmt.Errorf("store: no database")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return QueryRead{}, false, fmt.Errorf("store: reading %s/%s: %w", q.Namespace, q.Collection, err)
	}
	defer func() { _ = tx.Rollback() }()

	schema, table, found, err := readCollectionTx(tx, q.Namespace, q.Collection)
	if err != nil || !found {
		return QueryRead{}, false, err
	}

	var anchor *docstore.Document
	if q.After != "" {
		doc, ok, err := getDocumentWith(tx, schema.Namespace, schema.Collection, table, q.After)
		if err != nil {
			return QueryRead{}, false, err
		}
		if ok {
			anchor = doc
		}
	}

	compiled, err := q.Compile(schema, anchor)
	if err != nil {
		return QueryRead{}, false, err
	}
	docs, err := queryDocumentsWith(tx, compiled)
	if err != nil {
		return QueryRead{}, false, err
	}
	asOf, err := readAsOfSeq(tx)
	if err != nil {
		return QueryRead{}, false, err
	}
	return QueryRead{Schema: schema, Documents: docs, AsOfSeq: asOf}, true, nil
}

func (s *Store) CountDocuments(schema docstore.CollectionSchema) (int, error) {
	table, err := s.documentTable(schema)
	if err != nil {
		return 0, err
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
		return 0, fmt.Errorf("store: counting %s/%s: %w", schema.Namespace, schema.Collection, err)
	}
	return n, nil
}

func (s *Store) QueryPlan(c docstore.Compiled) ([]string, error) {
	if s.db == nil {
		return nil, fmt.Errorf("store: no database")
	}
	if err := docstore.ValidateTableName(c.Table); err != nil {
		return nil, err
	}
	stmt := `SELECT ` + documentColumns + ` FROM ` + c.Table
	if c.Where != "" {
		stmt += ` WHERE ` + c.Where
	}
	stmt += ` ORDER BY ` + c.Order + ` LIMIT ?`

	rows, err := s.db.Query(`EXPLAIN QUERY PLAN `+stmt, append(append([]any{}, c.Args...), c.Limit)...)
	if err != nil {
		return nil, fmt.Errorf("store: explaining query: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id, parent, notUsed int
		var detail string
		if err := rows.Scan(&id, &parent, &notUsed, &detail); err != nil {
			return nil, fmt.Errorf("store: scanning query plan: %w", err)
		}
		out = append(out, detail)
	}
	return out, rows.Err()
}

func (s *Store) documentTable(schema docstore.CollectionSchema) (string, error) {
	if s.db == nil {
		return "", fmt.Errorf("store: no database")
	}
	if err := docstore.ValidateTableName(schema.Table); err != nil {
		return "", fmt.Errorf("store: %s/%s: %w", schema.Namespace, schema.Collection, err)
	}
	return schema.Table, nil
}

func createCollectionTable(tx *sql.Tx, table string, fields []docstore.FieldSpec) error {
	if err := docstore.ValidateTableName(table); err != nil {
		return err
	}
	if _, err := tx.Exec(fmt.Sprintf(`CREATE TABLE %s (
    id         TEXT NOT NULL PRIMARY KEY,
    body       TEXT NOT NULL,
    rev        INTEGER NOT NULL DEFAULT %d,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
) WITHOUT ROWID`, table, docstore.FirstRev)); err != nil {
		return err
	}
	for _, col := range []string{docstore.FieldCreatedAt, docstore.FieldUpdatedAt} {
		if err := createFieldIndex(tx, table, col); err != nil {
			return err
		}
	}
	for _, f := range fields {
		if err := addFieldColumn(tx, table, f); err != nil {
			return err
		}
	}
	return nil
}

func alterCollectionTable(tx *sql.Tx, table string, before, after []docstore.FieldSpec) error {
	if err := docstore.ValidateTableName(table); err != nil {
		return err
	}
	old := make(map[string]docstore.FieldSpec, len(before))
	for _, f := range before {
		old[f.Name] = f
	}
	want := make(map[string]docstore.FieldSpec, len(after))
	for _, f := range after {
		want[f.Name] = f
	}

	for _, f := range before {
		next, kept := want[f.Name]
		if kept && next.Type == f.Type {
			continue
		}
		if err := dropFieldColumn(tx, table, f); err != nil {
			return err
		}
	}
	for _, f := range after {
		prev, existed := old[f.Name]
		if existed && prev.Type == f.Type {
			continue
		}
		if err := addFieldColumn(tx, table, f); err != nil {
			return err
		}
	}
	return nil
}

func addFieldColumn(tx *sql.Tx, table string, f docstore.FieldSpec) error {
	col := quoteIdent(docstore.FieldColumn(f.Name))
	// VIRTUAL, not STORED: the index already materialises the compared values,
	// and SQLite refuses to add a STORED column to an existing table.
	_, err := tx.Exec(fmt.Sprintf(`ALTER TABLE %s ADD COLUMN %s %s GENERATED ALWAYS AS (%s) VIRTUAL`,
		table, col, docstore.ColumnAffinity(f.Type), docstore.FieldExpression(f.Name)))
	if err != nil {
		return err
	}
	return createFieldIndex(tx, table, docstore.FieldColumn(f.Name))
}

func dropFieldColumn(tx *sql.Tx, table string, f docstore.FieldSpec) error {
	column := docstore.FieldColumn(f.Name)
	// The index goes first: SQLite refuses to drop an indexed column.
	if _, err := tx.Exec(`DROP INDEX IF EXISTS ` + quoteIdent(fieldIndexName(table, column))); err != nil {
		return err
	}
	_, err := tx.Exec(fmt.Sprintf(`ALTER TABLE %s DROP COLUMN %s`, table, quoteIdent(column)))
	return err
}

func createFieldIndex(tx *sql.Tx, table, column string) error {
	_, err := tx.Exec(fmt.Sprintf(`CREATE INDEX %s ON %s (%s, id)`,
		quoteIdent(fieldIndexName(table, column)), table, quoteIdent(column)))
	return err
}

func fieldIndexName(table, column string) string {
	return table + "_" + column
}

func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

type rowQuerier interface {
	QueryRow(query string, args ...any) *sql.Row
}

type rowsQuerier interface {
	Query(query string, args ...any) (*sql.Rows, error)
}

type execQuerier interface {
	execer
	rowQuerier
}

func readCollection(q rowQuerier, namespace, collection string) (docstore.CollectionSchema, string, bool, error) {
	schema := docstore.CollectionSchema{Namespace: namespace, Collection: collection}
	var (
		id     int64
		fields string
	)
	err := q.QueryRow(
		`SELECT id, fields_json FROM document_collections WHERE namespace = ? AND collection = ?`,
		namespace, collection).Scan(&id, &fields)
	switch {
	case err == sql.ErrNoRows:
		return schema, "", false, nil
	case err != nil:
		return schema, "", false, fmt.Errorf("store: reading %s/%s: %w", namespace, collection, err)
	}
	if err := json.Unmarshal([]byte(fields), &schema.Fields); err != nil {
		return schema, "", false, fmt.Errorf("store: decoding fields for %s/%s: %w", namespace, collection, err)
	}
	table := docstore.TableName(id)
	schema.Table = table
	return schema, table, true, nil
}

func readCollectionTx(tx *sql.Tx, namespace, collection string) (docstore.CollectionSchema, string, bool, error) {
	return readCollection(tx, namespace, collection)
}

func scanDocument(sc rowScanner) (*docstore.Document, error) {
	var (
		doc                    docstore.Document
		body                   string
		createdStr, updatedStr string
	)
	if err := sc.Scan(&doc.ID, &body, &doc.Rev, &createdStr, &updatedStr); err != nil {
		return nil, err
	}
	doc.Body = json.RawMessage(body)
	doc.CreatedAt = parseStoreTime(createdStr)
	doc.UpdatedAt = parseStoreTime(updatedStr)
	return &doc, nil
}
