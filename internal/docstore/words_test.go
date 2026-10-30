package docstore

import (
	"reflect"
	"testing"
)

func TestWordsAndIndexText(t *testing.T) {
	for _, tc := range []struct {
		text    string
		words   []string
		indexed string
	}{
		{"Callout disconnect", []string{"callout", "disconnect"}, "Callout disconnect"},
		{"TestARefusedDashboard", []string{"testarefuseddashboard", "test", "a", "refused", "dashboard"}, "TestARefusedDashboard\nTest A Refused Dashboard"},
		{"HTTPServer", []string{"httpserver", "http", "server"}, "HTTPServer\nHTTP Server"},
		{"branch_changed", []string{"branch", "changed"}, "branch_changed\nbranch changed"},
		{"parse2HTTPServer42", []string{"parse2httpserver42", "parse2", "http", "server42"}, "parse2HTTPServer42\nparse2 HTTP Server42"},
		{"ÉlanÜber café 東京42", []string{"élanüber", "élan", "über", "café", "東京42"}, "ÉlanÜber café 東京42\nÉlan Über"},
		{"HTTP HTTPServer server server", []string{"http", "httpserver", "server"}, "HTTP HTTPServer server server\nHTTP Server"},
		{"snake_HTTPServer", []string{"snake", "httpserver", "http", "server"}, "snake_HTTPServer\nsnake HTTP Server"},
		{"word, words. 123", []string{"word", "words", "123"}, "word, words. 123"},
		{"___ + -", nil, "___ + -"},
	} {
		t.Run(tc.text, func(t *testing.T) {
			if got := Words(tc.text); !reflect.DeepEqual(got, tc.words) {
				t.Errorf("Words = %q, want %q", got, tc.words)
			}
			if got := IndexText(tc.text); got != tc.indexed {
				t.Errorf("IndexText = %q, want %q", got, tc.indexed)
			}
		})
	}
}
