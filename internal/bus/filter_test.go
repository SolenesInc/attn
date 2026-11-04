package bus

import "testing"

func TestFilterMatching(t *testing.T) {
	cases := []struct {
		filter Filter
		name   string
		want   bool
	}{
		{All, "anything.at.all", true},
		{Filter{}, "anything.at.all", true},
		{Filter{"session.*"}, "session.state.changed", true},
		{Filter{"session.*"}, "session.registered", true},
		{Filter{"session.*"}, "sessions.updated", false},
		{Filter{"session.*"}, "session", false},
		{Filter{"ticket.commented"}, "ticket.commented", true},
		{Filter{"ticket.commented"}, "ticket.created", false},
		{Filter{"ticket.*", "pr.updated"}, "pr.updated", true},
		{Filter{"ticket.*", "pr.updated"}, "session.state.changed", false},
		{Filter{"ext.gate.*"}, "ext.gate.approved", true},
		{Filter{"ext.gate.*"}, "ext.other.approved", false},
	}
	for _, tc := range cases {
		if got := tc.filter.Matches(tc.name); got != tc.want {
			t.Errorf("Filter%v.Matches(%q) = %v, want %v", tc.filter, tc.name, got, tc.want)
		}
	}
}

func TestFilterStringJoinsPatterns(t *testing.T) {
	cases := []struct {
		filter Filter
		want   string
	}{
		{Filter{"session.*", "ticket.commented"}, "session.*,ticket.commented"},
		{Filter{"session.*"}, "session.*"},
		{Filter{}, "*"},
		{nil, "*"},
	}
	for _, tc := range cases {
		if got := tc.filter.String(); got != tc.want {
			t.Errorf("Filter%v.String() = %q, want %q", []string(tc.filter), got, tc.want)
		}
	}
}
