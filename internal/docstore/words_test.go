package docstore

import (
	"database/sql"
	"reflect"
	"strconv"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func TestWords(t *testing.T) {
	for _, tc := range []struct {
		text  string
		words []string
	}{
		{"Callout disconnect", []string{"callout", "disconnect"}},
		{"TestARefusedDashboard", []string{"testarefuseddashboard", "test", "a", "refused", "dashboard"}},
		{"HTTPServer", []string{"httpserver", "http", "server"}},
		{"branch_changed", []string{"branch", "changed"}},
		{"parse2HTTPServer42", []string{"parse2httpserver42", "parse2", "http", "server42"}},
		{"ÉlanÜber café 東京42", []string{"élanüber", "élan", "über", "café", "東京42"}},
		{"HTTP HTTPServer server server", []string{"http", "httpserver", "server"}},
		{"snake_HTTPServer", []string{"snake", "httpserver", "http", "server"}},
		{"word, words. 123", []string{"word", "words", "123"}},
		{"___ + -", nil},
	} {
		t.Run(tc.text, func(t *testing.T) {
			if got := Words(tc.text); !reflect.DeepEqual(got, tc.words) {
				t.Errorf("Words = %q, want %q", got, tc.words)
			}
		})
	}
}

func TestIndexTextWordsMatchFTS(t *testing.T) {
	texts := []string{
		"Callout disconnect", "TestARefusedDashboard", "HTTPServer", "branch_changed",
		"parse2HTTPServer42", "ÉlanÜber café 東京42", "HTTP HTTPServer server server",
		"snake_HTTPServer", "word, words. 123", "landmine—decision", "say “quoted” here",
		"step →deploy", "C1–C3", "⌘K", "attn’s", "Über alles", "İstanbul visible",
	}
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE VIRTUAL TABLE words USING fts4(text, tokenize=porter)`); err != nil {
		t.Fatal(err)
	}
	for i, text := range texts {
		if _, err := db.Exec(`INSERT INTO words(docid, text) VALUES (?, ?)`, i+1, IndexText(text)); err != nil {
			t.Fatal(err)
		}
		for _, word := range Words(text) {
			var offsets string
			if err := db.QueryRow(`SELECT offsets(words) FROM words WHERE words MATCH ? AND docid = ?`, word, i+1).Scan(&offsets); err != nil {
				t.Fatalf("%q: word %q: %v", text, word, err)
			}
			var spans [][2]int
			parts := strings.Fields(offsets)
			for j := 0; j < len(parts); j += 4 {
				start, err := strconv.Atoi(parts[j+2])
				if err != nil {
					t.Fatal(err)
				}
				size, err := strconv.Atoi(parts[j+3])
				if err != nil {
					t.Fatal(err)
				}
				spans = append(spans, [2]int{start, start + size})
			}
			spans = OriginalSpans(text, spans)
			if len(spans) == 0 {
				t.Fatalf("%q: word %q lost its original span", text, word)
			}
			quoted := false
			for _, span := range spans {
				if span[0] < 0 || span[1] > len(text) || span[0] >= span[1] {
					t.Fatalf("%q: invalid original span %v", text, span)
				}
				quoted = quoted || strings.Contains(strings.ToLower(text[span[0]:span[1]]), word)
			}
			if !quoted {
				t.Errorf("%q: word %q has no original quote in %v", text, word, spans)
			}
		}
	}
	for _, text := range []string{"ordinary text without identifiers", "café 東京42", "word, words. 123"} {
		if got := IndexText(text); got != text {
			t.Errorf("ordinary text changed: %q -> %q", text, got)
		}
	}
}
