package daemon_test

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestThePresenceTierIsTheHighestAcrossConnectedApps(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		cli := w.Client()
		presenceTierIs(t, cli, "with no app connected", "away")

		background := w.App()
		presenceReport(background, protocol.SetClientPresenceMessage{Visible: true, IdleSeconds: protocol.Ptr(600.0)})
		presenceTierIs(t, cli, "with only an app left idle", "away")

		foreground := w.App()
		presenceReport(foreground, protocol.SetClientPresenceMessage{Visible: true, DashboardVisible: true, IdleSeconds: protocol.Ptr(3.5)})
		presenceTierIs(t, cli, "with one app reading the dashboard", "watching")

		foreground.Close()
		synctest.Wait()
		presenceTierIs(t, cli, "after the watching app disconnected", "away")
	})
}

func TestPresenceLapsesToAwayWithoutFreshAttention(t *testing.T) {
	for _, tc := range []struct {
		name   string
		report protocol.SetClientPresenceMessage
		every  time.Duration
		checks []presenceCheck
	}{
		{
			name:   "a client that stops reporting expires past the heartbeat grace",
			report: protocol.SetClientPresenceMessage{Visible: true, DashboardVisible: true, IdleSeconds: protocol.Ptr(0.0)},
			checks: []presenceCheck{{0, "watching"}, {89 * time.Second, "watching"}, {91 * time.Second, "away"}},
		},
		{
			name:   "a window left open untouched drops to away while it keeps reporting",
			report: protocol.SetClientPresenceMessage{Visible: true, DashboardVisible: true},
			every:  time.Minute,
			checks: []presenceCheck{{0, "watching"}, {9 * time.Minute, "watching"}, {11 * time.Minute, "away"}},
		},
		{
			name:   "a report without idle seconds is not fresh input",
			report: protocol.SetClientPresenceMessage{Visible: true},
			checks: []presenceCheck{{0, "away"}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inBubble(t, func(t *testing.T, w *world) {
				cli, app := w.Client(), w.App()
				presenceReport(app, tc.report)
				var elapsed time.Duration
				for _, check := range tc.checks {
					for elapsed < check.at {
						step := check.at - elapsed
						if tc.every > 0 {
							step = min(step, tc.every)
						}
						w.advance(step)
						elapsed += step
						if tc.every > 0 && elapsed%tc.every == 0 {
							presenceReport(app, tc.report)
						}
					}
					presenceTierIs(t, cli, "after "+elapsed.String(), check.tier)
				}
			})
		})
	}
}

func TestUserActionsStampTheLastActivityAgentsSee(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		cli, app := w.Client(), w.App()
		inbox, err := cli.TicketInbox("session-1")
		if err != nil {
			t.Fatal(err)
		}
		if inbox.LastUserActivityAt != nil {
			t.Fatalf("before any user action the ticket inbox says the user was last active at %s", *inbox.LastUserActivityAt)
		}

		stamped := ""
		for _, tc := range []struct {
			name   string
			action any
			stamps bool
		}{
			{"selecting a session", protocol.SessionSelectedMessage{Cmd: protocol.CmdSessionSelected, ID: "session-1"}, true},
			{"reading settings", protocol.GetSettingsMessage{Cmd: protocol.CmdGetSettings}, false},
			{"selecting a workspace", protocol.WorkspaceSelectedMessage{Cmd: protocol.CmdWorkspaceSelected, WorkspaceID: "workspace-1"}, true},
		} {
			w.advance(time.Minute)
			app.Send(tc.action)
			synctest.Wait()
			if tc.stamps {
				stamped = time.Now().UTC().Format(time.RFC3339)
			}
			inbox, err := cli.TicketInbox("session-1")
			if err != nil {
				t.Fatal(err)
			}
			if got := protocol.Deref(inbox.LastUserActivityAt); got != stamped {
				t.Errorf("after %s the ticket inbox says the user was last active at %q, want %q", tc.name, got, stamped)
			}
		}
	})
}

type presenceCheck struct {
	at   time.Duration
	tier string
}

func presenceReport(app *testworld.Peer, report protocol.SetClientPresenceMessage) {
	app.T.Helper()
	report.Cmd = protocol.CmdSetClientPresence
	app.Send(report)
	synctest.Wait()
}

func presenceTierIs(t *testing.T, cli *client.Client, when, want string) {
	t.Helper()
	status, err := cli.ActivityStatus()
	if err != nil {
		t.Fatal(err)
	}
	if status.PresenceTier != want {
		t.Errorf("%s the presence tier is %q, want %q", when, status.PresenceTier, want)
	}
}
