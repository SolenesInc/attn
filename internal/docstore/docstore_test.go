package docstore

import (
	"encoding/json"
	"strings"
	"testing"
)

func requestsSchema() CollectionSchema {
	return CollectionSchema{
		Namespace:  "app/approval-gate",
		Collection: "requests",
		Fields: []FieldSpec{
			{Name: "status", Type: FieldString},
			{Name: "attempts", Type: FieldNumber},
			{Name: "urgent", Type: FieldBool},
		},
		Table: "doc_12",
	}
}

func mustCompile(t *testing.T, q Query, anchor ...*Document) Compiled {
	t.Helper()
	var after *Document
	if len(anchor) == 1 {
		after = anchor[0]
	}
	c, err := q.Compile(requestsSchema(), after)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	return c
}

func TestPendingRequestsNewestFirstCompiles(t *testing.T) {
	c := mustCompile(t, Query{
		Namespace:  "app/approval-gate",
		Collection: "requests",
		Filters:    []Filter{{Field: "status", Op: OpEq, Value: "pending"}},
		Sort:       &Sort{Field: FieldCreatedAt, Desc: true},
		Limit:      20,
	})
	want := `"f_status" = ?`
	if c.Where != want {
		t.Fatalf("where = %q, want %q", c.Where, want)
	}
	if got := []any{"pending"}; !equalArgs(c.Args, got) {
		t.Fatalf("args = %v, want %v", c.Args, got)
	}
	if c.Table != "doc_12" {
		t.Fatalf("table = %q", c.Table)
	}
	if c.Order != "created_at DESC, id DESC" {
		t.Fatalf("order = %q", c.Order)
	}
	if c.Limit != 20 {
		t.Fatalf("limit = %d", c.Limit)
	}
}

func TestEverySortIsMadeTotalByTheDocumentID(t *testing.T) {
	c := mustCompile(t, Query{
		Namespace:  "app/approval-gate",
		Collection: "requests",
		Sort:       &Sort{Field: "status"},
	})
	if c.Order != `"f_status" ASC, id ASC` {
		t.Fatalf("order = %q", c.Order)
	}
	c = mustCompile(t, Query{
		Namespace:  "app/approval-gate",
		Collection: "requests",
		Sort:       &Sort{Field: "status", Desc: true},
	})
	if c.Order != `"f_status" DESC, id DESC` {
		t.Fatalf("descending order = %q", c.Order)
	}
	if c := mustCompile(t, Query{Namespace: "app/approval-gate", Collection: "requests"}); c.Order != "id ASC" {
		t.Fatalf("default order = %q", c.Order)
	}
}

func TestReservedTimestampsAreQueryableWithoutDeclaration(t *testing.T) {
	c := mustCompile(t, Query{
		Namespace:  "app/approval-gate",
		Collection: "requests",
		Filters:    []Filter{{Field: FieldUpdatedAt, Op: OpGt, Value: "2026-08-03T00:00:00Z"}},
	})
	if !strings.HasSuffix(c.Where, "updated_at > ?") {
		t.Fatalf("where = %q, want a bare column comparison", c.Where)
	}
}

func TestACollectionCannotDeclareAReservedName(t *testing.T) {
	s := requestsSchema()
	s.Fields = append(s.Fields, FieldSpec{Name: FieldCreatedAt, Type: FieldString})
	err := s.Validate()
	if err == nil {
		t.Fatal("declaring created_at was accepted")
	}
	if !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("error does not explain the collision: %v", err)
	}
}

func TestQueryingAnUndeclaredFieldSaysWhatIsQueryable(t *testing.T) {
	_, err := Query{
		Namespace:  "app/approval-gate",
		Collection: "requests",
		Filters:    []Filter{{Field: "priority", Op: OpEq, Value: "high"}},
	}.Compile(requestsSchema(), nil)
	if err == nil {
		t.Fatal("filtering an undeclared field was accepted")
	}
	for _, want := range []string{"priority", "attempts", "status", "urgent", FieldCreatedAt, FieldUpdatedAt} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not mention %q", err, want)
		}
	}
	_, err = Query{
		Namespace:  "app/approval-gate",
		Collection: "requests",
		Sort:       &Sort{Field: "priority"},
	}.Compile(requestsSchema(), nil)
	if err == nil || !strings.Contains(err.Error(), "sort") {
		t.Fatalf("sort on an undeclared field: %v", err)
	}
}

func TestAFilterBoundMustMatchTheDeclaredType(t *testing.T) {
	for _, tc := range []struct {
		name  string
		field string
		value any
	}{
		{"number field, string bound", "attempts", "5"},
		{"string field, number bound", "status", 5},
		{"bool field, string bound", "urgent", "true"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Query{
				Namespace:  "app/approval-gate",
				Collection: "requests",
				Filters:    []Filter{{Field: tc.field, Op: OpEq, Value: tc.value}},
			}.Compile(requestsSchema(), nil)
			if err == nil {
				t.Fatal("mismatched bound was accepted")
			}
			if !strings.Contains(err.Error(), tc.field) {
				t.Fatalf("error does not name the field: %v", err)
			}
		})
	}
}

func TestBoundsBindTheWayJSONExtractCompares(t *testing.T) {
	c := mustCompile(t, Query{
		Namespace:  "app/approval-gate",
		Collection: "requests",
		Filters: []Filter{
			{Field: "attempts", Op: OpGte, Value: 3},
			{Field: "urgent", Op: OpEq, Value: true},
		},
	})
	if got := c.Args[0]; got != float64(3) {
		t.Fatalf("int bound = %#v, want float64(3)", got)
	}
	if got := c.Args[1]; got != 1 {
		t.Fatalf("true bound = %#v, want 1", got)
	}
}

func TestAQueryRoundTripsThroughJSON(t *testing.T) {
	raw := `{"namespace":"app/approval-gate","collection":"requests",
	         "filters":[{"field":"attempts","op":"gte","value":2}],
	         "sort":{"field":"created_at","desc":true},"limit":5}`
	var q Query
	if err := json.Unmarshal([]byte(raw), &q); err != nil {
		t.Fatal(err)
	}
	c, err := q.Compile(requestsSchema(), nil)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if c.Limit != 5 || c.Order != "created_at DESC, id DESC" {
		t.Fatalf("compiled = %+v", c)
	}
	if got := c.Args[0]; got != float64(2) {
		t.Fatalf("json number bound = %#v", got)
	}
}

func TestLimitDefaultsAndItsCeilingNamesTheAsk(t *testing.T) {
	if c := mustCompile(t, Query{Namespace: "app/approval-gate", Collection: "requests"}); c.Limit != DefaultLimit {
		t.Fatalf("default limit = %d, want %d", c.Limit, DefaultLimit)
	}
	_, err := Query{Namespace: "app/approval-gate", Collection: "requests", Limit: MaxLimit + 1}.Compile(requestsSchema(), nil)
	if err == nil {
		t.Fatal("a limit above the maximum was accepted")
	}
	for _, want := range []string{"1001", "1000"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not carry %s", err, want)
		}
	}
}

func TestNamespaceShapeIsTwoParts(t *testing.T) {
	for _, ok := range []string{"app/approval-gate", "core/tickets", "app/a"} {
		if err := ValidateNamespace(ok); err != nil {
			t.Fatalf("%q rejected: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "approval-gate", "app/", "/name", "App/Name", "app/a/b", "ext name/x"} {
		if err := ValidateNamespace(bad); err == nil {
			t.Fatalf("%q accepted", bad)
		}
	}
}

func TestFieldNamesAreIdentifiersSoThePathNeedsNoQuoting(t *testing.T) {
	for _, bad := range []string{"has space", "with'quote", "a.b", "$x", ""} {
		s := requestsSchema()
		s.Fields = []FieldSpec{{Name: bad, Type: FieldString}}
		if err := s.Validate(); err == nil {
			t.Fatalf("field name %q accepted", bad)
		}
	}
}

func TestBodyMustBeAJSONObject(t *testing.T) {
	if err := ValidateBody([]byte(`{"status":"pending"}`)); err != nil {
		t.Fatalf("object rejected: %v", err)
	}
	for _, bad := range []string{``, `[1,2]`, `"text"`, `7`, `{oops}`} {
		if err := ValidateBody([]byte(bad)); err == nil {
			t.Fatalf("body %q accepted", bad)
		}
	}
}

func TestAQueryWillNotCompileAgainstAnotherCollectionsDeclaration(t *testing.T) {
	_, err := Query{Namespace: "app/other", Collection: "requests"}.Compile(requestsSchema(), nil)
	if err == nil {
		t.Fatal("mismatched declaration was accepted")
	}
}

func equalArgs(got, want []any) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func TestTheAfterCursorComparesTheWholeOrderingTuple(t *testing.T) {
	anchor := &Document{ID: "b", Body: []byte(`{"attempts":7}`)}
	c := mustCompile(t, Query{
		Namespace:  "app/approval-gate",
		Collection: "requests",
		Sort:       &Sort{Field: "attempts"},
		After:      "b",
	}, anchor)
	value := `(SELECT "f_attempts" FROM doc_12 WHERE id = ?)`
	want := `("f_attempts" > ` + value + ` OR ("f_attempts" = ` + value + ` AND id > ?))`
	if !strings.HasSuffix(c.Where, want) {
		t.Fatalf("where = %q, want it to end with %q", c.Where, want)
	}
	if got := c.Args[len(c.Args)-3:]; !equalArgs(got, []any{"b", "b", "b"}) {
		t.Fatalf("cursor args = %v", got)
	}

	c = mustCompile(t, Query{
		Namespace:  "app/approval-gate",
		Collection: "requests",
		Sort:       &Sort{Field: "attempts", Desc: true},
		After:      "b",
	}, anchor)
	want = `("f_attempts" IS NULL OR "f_attempts" < ` + value + ` OR ("f_attempts" = ` + value + ` AND id < ?))`
	if !strings.HasSuffix(c.Where, want) {
		t.Fatalf("descending where = %q, want it to end with %q", c.Where, want)
	}
}

func TestTheAfterCursorOfAnUnsortedQueryIsTheID(t *testing.T) {
	c := mustCompile(t, Query{
		Namespace:  "app/approval-gate",
		Collection: "requests",
		After:      "b",
	}, &Document{ID: "b", Body: []byte(`{}`)})
	if !strings.HasSuffix(c.Where, "id > ?") {
		t.Fatalf("where = %q", c.Where)
	}
}

func TestAnAfterCursorWithNoDocumentIsAnError(t *testing.T) {
	_, err := Query{
		Namespace:  "app/approval-gate",
		Collection: "requests",
		Sort:       &Sort{Field: "attempts"},
		After:      "vanished",
	}.Compile(requestsSchema(), nil)
	if err == nil {
		t.Fatal("a cursor to a missing document was accepted")
	}
	for _, want := range []string{"vanished", "no longer exists"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not mention %q", err, want)
		}
	}
	_, err = Query{
		Namespace:  "app/approval-gate",
		Collection: "requests",
		After:      "b",
	}.Compile(requestsSchema(), &Document{ID: "c", Body: []byte(`{}`)})
	if err == nil || !strings.Contains(err.Error(), "\"c\"") {
		t.Fatalf("mismatched anchor: %v", err)
	}
}

func TestTheLimitCeilingPointsAtTheCursor(t *testing.T) {
	_, err := Query{
		Namespace:  "app/approval-gate",
		Collection: "requests",
		Limit:      MaxLimit + 1,
	}.Compile(requestsSchema(), nil)
	if err == nil || !strings.Contains(err.Error(), "after cursor") {
		t.Fatalf("over-limit error = %v", err)
	}
}

func TestAQueryWillNotCompileWithoutAMintedTable(t *testing.T) {
	bare := requestsSchema()
	bare.Table = ""
	if _, err := (Query{Namespace: "app/approval-gate", Collection: "requests"}).Compile(bare, nil); err == nil {
		t.Fatal("a declaration with no table compiled")
	}
	for _, forged := range []string{
		"documents",
		"doc_1; DROP TABLE sessions",
		"doc_0x1",
		"doc_",
		`doc_1" --`,
	} {
		s := requestsSchema()
		s.Table = forged
		if _, err := (Query{Namespace: "app/approval-gate", Collection: "requests"}).Compile(s, nil); err == nil {
			t.Fatalf("table name %q compiled", forged)
		}
	}
}

func TestPhysicalNamesAreDerivedFromCheckedInput(t *testing.T) {
	if got := TableName(12); got != "doc_12" {
		t.Fatalf("TableName(12) = %q", got)
	}
	if err := ValidateTableName(TableName(12)); err != nil {
		t.Fatalf("a minted name was rejected: %v", err)
	}
	if got := FieldColumn("status"); got != "f_status" {
		t.Fatalf("FieldColumn = %q", got)
	}
	if FieldColumn("id") == "id" || FieldColumn("body") == "body" {
		t.Fatal("a declared field can shadow a stored column")
	}
	if got := FieldExpression("status"); got != "json_extract(body, '$.status')" {
		t.Fatalf("FieldExpression = %q", got)
	}
	for _, tc := range []struct {
		typ  FieldType
		want string
	}{
		{FieldString, "TEXT"},
		{FieldNumber, "NUMERIC"},
		{FieldBool, "INTEGER"},
	} {
		if got := ColumnAffinity(tc.typ); got != tc.want {
			t.Fatalf("ColumnAffinity(%s) = %q, want %q", tc.typ, got, tc.want)
		}
	}
}
