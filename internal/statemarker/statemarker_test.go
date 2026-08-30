package statemarker

import (
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	cases := map[string]struct {
		state string
		fails bool
	}{
		"nothing to see here":                                        {"", false},
		"done\n\n<!-- attn:state=idle -->":                           {"idle", false},
		"<!--attn:state=waiting_input-->":                            {"waiting_input", false},
		"<!--   attn:state=IDLE   -->":                               {"idle", false},
		"<!-- attn:state=idle --> <!-- attn:state=waiting_input -->": {"waiting_input", false},
		"<!-- attn:state=pending_approval -->":                       {"", true},
		"<!-- attn:state=parked -->":                                 {"", true},
		"<!-- attn:state -->":                                        {"", false},
	}
	for text, want := range cases {
		state, err := Parse(text)
		if (err != nil) != want.fails {
			t.Errorf("Parse(%q) error = %v, want failure %v", text, err, want.fails)
			continue
		}
		if state != want.state {
			t.Errorf("Parse(%q) = %q, want %q", text, state, want.state)
		}
	}
}

func TestParseNamesTheValueAndTheSet(t *testing.T) {
	_, err := Parse("<!-- attn:state=confused -->")
	if err == nil {
		t.Fatal("a marker naming a state no verdict carries must fail")
	}
	for _, want := range []string{`"confused"`, "waiting_input", "idle"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not name %s", err, want)
		}
	}
}
