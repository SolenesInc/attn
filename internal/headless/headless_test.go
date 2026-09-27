package headless

import "testing"

func TestParseSwitch(t *testing.T) {
	cases := map[string]struct {
		value bool
		ok    bool
	}{
		"on":       {true, true},
		"ON":       {true, true},
		" true ":   {true, true},
		"1":        {true, true},
		"yes":      {true, true},
		"enabled":  {true, true},
		"off":      {false, true},
		"0":        {false, true},
		"false":    {false, true},
		"no":       {false, true},
		"disabled": {false, true},
		"":         {false, false},
		"maybe":    {false, false},
	}
	for raw, want := range cases {
		value, ok := ParseSwitch(raw)
		if value != want.value || ok != want.ok {
			t.Errorf("ParseSwitch(%q) = (%v, %v), want (%v, %v)", raw, value, ok, want.value, want.ok)
		}
	}
}
