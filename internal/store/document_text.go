package store

import (
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/victorarias/attn/internal/docstore"
)

func createTextIndex(tx *sql.Tx, table string, schema docstore.CollectionSchema) error {
	columns := []string{"id"}
	for _, field := range schema.Text {
		columns = append(columns, quoteIdent(docstore.FieldColumn(field)))
	}
	if _, err := tx.Exec(`CREATE VIRTUAL TABLE ` + table + `_text USING fts4(` + strings.Join(columns, ", ") + `, created_at, fields_json, notindexed=id, notindexed=created_at, notindexed=fields_json, tokenize=porter)`); err != nil {
		return err
	}
	rows, err := tx.Query(`SELECT id, body, created_at FROM ` + table)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, createdAt string
		var body []byte
		if err := rows.Scan(&id, &body, &createdAt); err != nil {
			return err
		}
		if err := insertTextIndex(tx, table, schema, id, body, createdAt); err != nil {
			return err
		}
	}
	return rows.Err()
}

func putTextIndex(q execer, table string, schema docstore.CollectionSchema, id string, body []byte, createdAt string) error {
	if _, err := q.Exec(`DELETE FROM `+table+`_text WHERE id = ?`, id); err != nil {
		return err
	}
	return insertTextIndex(q, table, schema, id, body, createdAt)
}

func insertTextIndex(q execer, table string, schema docstore.CollectionSchema, id string, body []byte, createdAt string) error {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(body, &object); err != nil {
		return err
	}
	args := []any{id}
	for _, field := range schema.Text {
		var value string
		if raw := object[field]; raw != nil {
			if err := json.Unmarshal(raw, &value); err != nil {
				return fmt.Errorf("store: text field %q in %s/%s must be a string: %w", field, table, id, err)
			}
		}
		args = append(args, docstore.IndexText(value))
	}
	metadata := map[string]json.RawMessage{}
	for _, field := range schema.Fields {
		if field.Type == docstore.FieldString {
			if raw := object[field.Name]; raw != nil {
				metadata[field.Name] = raw
			}
		}
	}
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	args = append(args, createdAt, string(encoded))

	_, err = q.Exec(`INSERT INTO `+table+`_text VALUES (`+strings.TrimSuffix(strings.Repeat("?,", len(args)), ",")+`)`, args...)
	return err
}

func (s *Store) textCollection(schema docstore.CollectionSchema) (*docstore.CollectionSchema, error) {
	stored, found, err := s.DocumentCollection(schema.Namespace, schema.Collection)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, &docstore.UndeclaredCollectionError{Namespace: schema.Namespace, Collection: schema.Collection}
	}
	if len(stored.Text) == 0 {
		return nil, fmt.Errorf("store: %s has no text fields", docstore.Target(schema.Namespace, schema.Collection))
	}
	return stored, nil
}

func textExpression(words []string) string {
	terms := make([]string, len(words))
	for i, word := range words {
		terms[i] = word + "*"
	}
	return strings.Join(terms, " OR ")
}

func (s *Store) MatchText(schema docstore.CollectionSchema, words []string, key string) ([]docstore.TextMatch, error) {
	stored, err := s.textCollection(schema)
	if err != nil {
		return nil, err
	}
	if len(words) == 0 {
		return nil, nil
	}
	keyExpr := `''`
	if key != "" {
		found := false
		for _, field := range stored.Fields {
			if field.Name == key && field.Type == docstore.FieldString {
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("store: %s has no string key field %q", docstore.Target(schema.Namespace, schema.Collection), key)
		}
		keyExpr = `json_extract(fields_json, '$.` + key + `')`
	}
	table := stored.Table + `_text`
	rows, err := s.db.Query(`SELECT id, `+keyExpr+`, created_at, matchinfo(`+table+`, 'pcnalx') FROM `+table+
		` WHERE `+table+` MATCH ?`, textExpression(words))
	if err != nil {
		return nil, fmt.Errorf("store: matching %s text: %w", docstore.Target(schema.Namespace, schema.Collection), err)
	}
	defer rows.Close()
	var out []docstore.TextMatch
	for rows.Next() {
		var hit docstore.TextMatch
		var created string
		var info []byte
		if err := rows.Scan(&hit.ID, &hit.Key, &created, &info); err != nil {
			return nil, err
		}
		hit.CreatedAt = parseStoreTime(created)
		hit.Score, hit.Carries = textScores(info, len(stored.Text))
		out = append(out, hit)
	}
	return out, rows.Err()
}

func textScores(info []byte, fields int) ([]float64, [][]bool) {
	value := func(i int) float64 { return float64(binary.NativeEndian.Uint32(info[4*i:])) }
	phrases, columns, n := int(value(0)), int(value(1)), value(2)
	scores := make([]float64, fields)
	carries := make([][]bool, fields)
	for col := 1; col <= fields; col++ {
		carries[col-1] = make([]bool, phrases)
		avg, length := value(3+col), value(3+columns+col)
		for phrase := 0; phrase < phrases; phrase++ {
			at := 3 + 2*columns + 3*(phrase*columns+col)
			tf, df := value(at), value(at+2)
			if tf == 0 {
				continue
			}
			carries[col-1][phrase] = true
			idf := math.Log(1 + (n-df+0.5)/(df+0.5))
			scores[col-1] += idf * tf * 2.2 / (tf + 1.2*(0.25+0.75*length/math.Max(avg, 1)))
		}
	}
	return scores, carries
}

func (s *Store) TextSpans(schema docstore.CollectionSchema, id string, words []string) ([][][2]int, error) {
	stored, err := s.textCollection(schema)
	if err != nil {
		return nil, err
	}
	out := make([][][2]int, len(stored.Text))
	if len(words) == 0 {
		return out, nil
	}
	table := stored.Table + `_text`
	var offsets string
	var body []byte
	err = s.db.QueryRow(`SELECT offsets(`+table+`), d.body FROM `+table+` JOIN `+stored.Table+
		` d ON d.id = `+table+`.id WHERE `+table+` MATCH ? AND `+table+`.docid = (SELECT docid FROM `+table+` WHERE id = ?)`, textExpression(words), id).Scan(&offsets, &body)
	if err == sql.ErrNoRows {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	parts := strings.Fields(offsets)
	for i := 0; i < len(parts); i += 4 {
		col, err := strconv.Atoi(parts[i])
		if err != nil {
			return nil, err
		}
		start, err := strconv.Atoi(parts[i+2])
		if err != nil {
			return nil, err
		}
		size, err := strconv.Atoi(parts[i+3])
		if err != nil {
			return nil, err
		}
		out[col-1] = append(out[col-1], [2]int{start, start + size})
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(body, &object); err != nil {
		return nil, err
	}
	for i, field := range stored.Text {
		var value string
		if raw := object[field]; raw != nil {
			if err := json.Unmarshal(raw, &value); err != nil {
				return nil, err
			}
		}
		out[i] = docstore.OriginalSpans(value, out[i])
	}
	return out, nil
}
