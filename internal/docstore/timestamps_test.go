package docstore

import (
	"sort"
	"strings"
	"testing"
	"time"
)

// A stamp lives in a TEXT column and is ordered and filtered as text, so the
// encoding carries the whole meaning of "before".
func raggedInstants() []time.Time {
	base := time.Date(2026, 8, 5, 10, 0, 0, 0, time.UTC)
	return []time.Time{
		base,
		base.Add(123400 * time.Microsecond),
		base.Add(123450 * time.Microsecond),
		base.Add(500 * time.Millisecond),
		base.Add(time.Second),
	}
}

func TestStoredStampsSortAsTextInTimeOrder(t *testing.T) {
	instants := raggedInstants()
	encoded := make([]string, len(instants))
	for i, at := range instants {
		encoded[i] = at.Format(TimeFormat)
	}
	sorted := append([]string(nil), encoded...)
	sort.Strings(sorted)
	for i := range encoded {
		if sorted[i] != encoded[i] {
			t.Fatalf("text order is %v, but these instants are in time order as %v", sorted, encoded)
		}
	}
}

// Fixed width is what makes the text order hold: a stamp with no fraction ends
// in 'Z', which sorts above '.' and above every digit.
func TestEveryStoredStampIsTheSameWidth(t *testing.T) {
	for _, at := range raggedInstants() {
		got := at.Format(TimeFormat)
		if len(got) != 30 {
			t.Fatalf("%q is %d characters, want the same 30 every stamp has", got, len(got))
		}
		if !strings.HasSuffix(got, "Z") {
			t.Fatalf("%q does not end in Z; stamps are stored in UTC", got)
		}
	}
}

func TestATimestampIsAcceptedInAnyRFC3339Form(t *testing.T) {
	want := time.Date(2026, 8, 5, 10, 0, 0, 500000000, time.UTC)
	for _, form := range []string{
		"2026-08-05T10:00:00.5Z",
		"2026-08-05T10:00:00.500Z",
		"2026-08-05T10:00:00.500000000Z",
		"2026-08-05T12:00:00.5+02:00",
	} {
		got, err := ParseTime(form)
		if err != nil {
			t.Fatalf("%q: %v", form, err)
		}
		if !got.Equal(want) {
			t.Fatalf("%q decoded to %v, want %v", form, got, want)
		}
		if got.Format(TimeFormat) != want.Format(TimeFormat) {
			t.Fatalf("%q re-encoded to %q, want %q", form, got.Format(TimeFormat), want.Format(TimeFormat))
		}
	}
}

func TestATimestampBoundIsReEncodedBeforeItIsCompared(t *testing.T) {
	for _, form := range []string{
		"2026-08-05T10:00:00Z",
		"2026-08-05T10:00:00.000Z",
		"2026-08-05T12:00:00+02:00",
	} {
		c := mustCompile(t, Query{
			Namespace:  "app/approval-gate",
			Collection: "requests",
			Filters:    []Filter{{Field: FieldUpdatedAt, Op: OpGte, Value: form}},
		})
		if len(c.Args) != 1 {
			t.Fatalf("%q compiled to %d arguments, want 1", form, len(c.Args))
		}
		if want := "2026-08-05T10:00:00.000000000Z"; c.Args[0] != want {
			t.Fatalf("%q was bound as %v, want %q", form, c.Args[0], want)
		}
	}
}

func TestATimestampBoundEncodesTheSameFromAStringOrATime(t *testing.T) {
	at := time.Date(2026, 8, 5, 10, 0, 0, 0, time.UTC)
	fromTime := mustCompile(t, Query{
		Namespace:  "app/approval-gate",
		Collection: "requests",
		Filters:    []Filter{{Field: FieldCreatedAt, Op: OpGte, Value: at}},
	})
	fromString := mustCompile(t, Query{
		Namespace:  "app/approval-gate",
		Collection: "requests",
		Filters:    []Filter{{Field: FieldCreatedAt, Op: OpGte, Value: at.Format(time.RFC3339)}},
	})
	if fromTime.Args[0] != fromString.Args[0] {
		t.Fatalf("a time bound is %v and the same instant as a string is %v", fromTime.Args[0], fromString.Args[0])
	}
}

func TestATimestampBoundThatIsNotATimestampIsRefused(t *testing.T) {
	_, err := Query{
		Namespace:  "app/approval-gate",
		Collection: "requests",
		Filters:    []Filter{{Field: FieldUpdatedAt, Op: OpGt, Value: "yesterday"}},
	}.Compile(requestsSchema(), nil)
	if err == nil {
		t.Fatal("a filter on updated_at accepted \"yesterday\"")
	}
	if !strings.Contains(err.Error(), "RFC3339") || !strings.Contains(err.Error(), "yesterday") {
		t.Fatalf("error names neither the expected form nor the value given: %v", err)
	}
}
