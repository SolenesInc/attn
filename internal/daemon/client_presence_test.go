package daemon

import (
	"testing"
	"time"
)

func reported(visible, dashboard bool, idle float64, at time.Time) clientPresence {
	return clientPresence{
		Visible:          visible,
		DashboardVisible: dashboard,
		IdleSeconds:      idle,
		ReportedAt:       at,
	}
}

func TestPresenceTierFromOneClientsReport(t *testing.T) {
	now := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	const idleLimit = 90 * time.Second

	cases := []struct {
		name    string
		report  clientPresence
		want    PresenceTier
		because string
	}{
		{
			name:    "dashboard on screen",
			report:  reported(true, true, 0, now),
			want:    PresenceWatching,
			because: "the line is actually being read",
		},
		{
			name:    "in the app, dashboard not showing, input recent",
			report:  reported(true, false, 10, now),
			want:    PresencePresent,
			because: "the user is inside a session and may glance back",
		},
		{
			name:    "in the app, dashboard not showing, input long ago",
			report:  reported(true, false, 600, now),
			want:    PresenceAway,
			because: "a window left open is not attention",
		},
		{
			name:    "app hidden even with the dashboard mounted",
			report:  reported(false, true, 0, now),
			want:    PresenceAway,
			because: "a rendered view nobody can see is still nobody looking",
		},
		{
			name:    "in the app, no input observed yet this connection",
			report:  reported(true, false, -1, now),
			want:    PresenceAway,
			because: "unknown idleness is not recent input",
		},
		{
			name:    "watching home just inside the watching idle limit",
			report:  reported(true, true, presenceWatchingIdleLimit.Seconds()-60, now),
			want:    PresenceWatching,
			because: "reading home is a thing people do without touching anything",
		},
		{
			name:    "watching home past the watching idle limit",
			report:  reported(true, true, presenceWatchingIdleLimit.Seconds()+60, now),
			want:    PresenceAway,
			because: "a dashboard nobody has touched for that long is not being read",
		},
		{
			name: "watching with no input, window opened a minute ago",
			report: clientPresence{
				Visible: true, DashboardVisible: true, IdleSeconds: -1,
				ReportedAt: now, FirstReportAt: now.Add(-time.Minute),
			},
			want:    PresenceWatching,
			because: "without input, watching is measured from the first report",
		},
		{
			name: "watching with no input, window open eight untouched hours",
			report: clientPresence{
				Visible: true, DashboardVisible: true, IdleSeconds: -1,
				ReportedAt: now, FirstReportAt: now.Add(-8 * time.Hour),
			},
			want:    PresenceAway,
			because: "without input, watching is measured from the first report",
		},
		{
			name:    "a client that never reported",
			report:  clientPresence{},
			want:    PresenceAway,
			because: "a fresh connection has said nothing yet",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.report.tier(now, idleLimit); got != tc.want {
				t.Errorf("tier = %s, want %s — %s", got, tc.want, tc.because)
			}
		})
	}
}
