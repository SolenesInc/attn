package daemon_test

import (
	"testing"
	"time"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

const (
	autoSettleDefaultArm       = 30 * time.Second
	autoSettleDefaultCountdown = 15 * time.Second
	autoSettleQuietWindow      = 5 * time.Second
)

func TestAutoSettleArmsCountsDownAndSettlesTheTurn(t *testing.T) {
	for _, tc := range []struct {
		name             string
		arm, countdown   string
		armFor, countFor time.Duration
	}{
		{"blank windows mean thirty seconds then fifteen", "", "", autoSettleDefaultArm, autoSettleDefaultCountdown},
		{"configured windows", "5", "3", 5 * time.Second, 3 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inBubble(t, func(t *testing.T, w *world) {
				app, cli := autoSettleOwingSession(t, w)
				setSetting(t, app, "auto_settle_arm_seconds", tc.arm)
				setSetting(t, app, "auto_settle_countdown_seconds", tc.countdown)
				steered := autoSettleSteer(t, w, app, cli, "fix the flaky test")
				deadline := steered.Add(tc.armFor + tc.countFor)

				w.advance(tc.armFor - time.Millisecond)
				if shown := sessionStateLastShown(t, app); shown.AutoSettleFiresAt != nil || !protocol.Deref(shown.TurnOwed) {
					t.Fatalf("during the arm delay the app shows fires_at=%q owed=%v, want no countdown and the turn owed",
						protocol.Deref(shown.AutoSettleFiresAt), protocol.Deref(shown.TurnOwed))
				}

				w.advance(time.Millisecond)
				counting := testworld.AwaitSession(app, "s1", func(s protocol.Session) bool {
					return autoSettleFiresAt(t, s).Equal(deadline)
				})
				if !protocol.Deref(counting.TurnOwed) {
					t.Fatal("the countdown started with the turn already settled")
				}

				w.advance(tc.countFor / 3)
				for range 3 {
					if err := cli.UpdateState("s1", protocol.StateWorking); err != nil {
						t.Fatalf("report working again: %v", err)
					}
				}
				w.advance(0)
				if shown := sessionStateLastShown(t, app); !autoSettleFiresAt(t, shown).Equal(deadline) {
					t.Fatalf("re-reported working moved the deadline to %q, want %s", protocol.Deref(shown.AutoSettleFiresAt), deadline)
				}

				w.advance(time.Until(deadline) - time.Millisecond)
				if shown := sessionStateLastShown(t, app); !protocol.Deref(shown.TurnOwed) {
					t.Fatal("the turn settled before the countdown ended")
				}
				w.advance(time.Millisecond)
				settled := testworld.AwaitSession(app, "s1", func(s protocol.Session) bool {
					return s.State == protocol.SessionStateWorking && !protocol.Deref(s.TurnOwed)
				})
				if settled.AutoSettleFiresAt != nil {
					t.Fatalf("after the settle the app still shows a countdown to %q", *settled.AutoSettleFiresAt)
				}
			})
		})
	}
}

func TestAutoSettleNeverArmsWithoutAnOwedTurnOrWithTheFeatureOff(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, w *world) (*testworld.Peer, *client.Client)
	}{
		{"the feature is off", func(t *testing.T, w *world) (*testworld.Peer, *client.Client) {
			app, cli := autoSettleOwingSession(t, w)
			setSetting(t, app, "auto_settle_enabled", "false")
			return app, cli
		}},
		{"no turn is owed", func(t *testing.T, w *world) (*testworld.Peer, *client.Client) {
			app, cli := w.App(), w.Client()
			setSetting(t, app, "auto_settle_enabled", "true")
			if err := cli.Register("s1", "s1", w.Path("s1")); err != nil {
				t.Fatalf("register: %v", err)
			}
			return app, cli
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inBubble(t, func(t *testing.T, w *world) {
				app, cli := tc.setup(t, w)
				autoSettleSteer(t, w, app, cli, "fix the flaky test")
				w.advance(autoSettleDefaultArm + autoSettleDefaultCountdown)

				for _, s := range sessionUpdatesOf(app, "s1") {
					if s.AutoSettleFiresAt != nil {
						t.Fatalf("the app was shown a countdown to %q", *s.AutoSettleFiresAt)
					}
				}
				if shown := sessionStateLastShown(t, app); shown.State != protocol.SessionStateWorking {
					t.Fatalf("the session is %s, want still working", shown.State)
				}
			})
		})
	}
}

func TestAnInterruptedCountdownVanishesAndOnlyASettleClosesTheTurn(t *testing.T) {
	for _, tc := range []struct {
		name      string
		interrupt func(t *testing.T, app *testworld.Peer, cli *client.Client)
		wantState protocol.SessionState
		wantOwed  bool
	}{
		{"the agent asks a question", func(t *testing.T, _ *testworld.Peer, cli *client.Client) {
			if err := cli.UpdateState("s1", protocol.StateWaitingInput); err != nil {
				t.Fatalf("report waiting_input: %v", err)
			}
		}, protocol.SessionStateWaitingInput, true},
		{"the agent asks for approval", func(t *testing.T, _ *testworld.Peer, cli *client.Client) {
			if err := cli.RecordNotification("s1", "permission_prompt", "Allow edit?"); err != nil {
				t.Fatalf("notify: %v", err)
			}
		}, protocol.SessionStatePendingApproval, true},
		{"the user turns auto-settle off", func(t *testing.T, app *testworld.Peer, _ *client.Client) {
			setSetting(t, app, "auto_settle_enabled", "false")
		}, protocol.SessionStateWorking, true},
		{"the user settles the turn", func(_ *testing.T, app *testworld.Peer, _ *client.Client) {
			app.Send(protocol.SettleTurnMessage{Cmd: protocol.CmdSettleTurn, SessionID: "s1"})
		}, protocol.SessionStateWorking, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inBubble(t, func(t *testing.T, w *world) {
				app, cli := autoSettleOwingSession(t, w)
				steered := autoSettleSteer(t, w, app, cli, "fix the flaky test")
				deadline := steered.Add(autoSettleDefaultArm + autoSettleDefaultCountdown)
				w.advance(autoSettleDefaultArm)
				testworld.AwaitSession(app, "s1", func(s protocol.Session) bool { return autoSettleFiresAt(t, s).Equal(deadline) })

				w.advance(autoSettleDefaultCountdown - time.Second)
				tc.interrupt(t, app, cli)
				w.advance(0)
				interrupted := sessionStateLastShown(t, app)
				if interrupted.AutoSettleFiresAt != nil || interrupted.State != tc.wantState {
					t.Fatalf("after the interruption the app shows %s with fires_at=%q, want %s without a countdown",
						interrupted.State, protocol.Deref(interrupted.AutoSettleFiresAt), tc.wantState)
				}

				w.advance(autoSettleDefaultArm + autoSettleDefaultCountdown)
				if owed := protocol.Deref(sessionStateLastShown(t, app).TurnOwed); owed != tc.wantOwed {
					t.Fatalf("past the old deadline the turn is owed=%v, want %v", owed, tc.wantOwed)
				}
			})
		})
	}
}

func TestCancellingTheCountdownStandsForTheStretchItAnswers(t *testing.T) {
	t.Run("a cancelled countdown does not come back until the next steer", func(t *testing.T) {
		inBubble(t, func(t *testing.T, w *world) {
			app, cli := autoSettleOwingSession(t, w)
			steered := autoSettleSteer(t, w, app, cli, "fix the flaky test")
			w.advance(autoSettleDefaultArm)
			testworld.AwaitSession(app, "s1", func(s protocol.Session) bool {
				return autoSettleFiresAt(t, s).Equal(steered.Add(autoSettleDefaultArm + autoSettleDefaultCountdown))
			})

			autoSettleCancel(w, app)
			if shown := sessionStateLastShown(t, app); shown.AutoSettleFiresAt != nil || !protocol.Deref(shown.AutoSettleDismissArmed) {
				t.Fatalf("after the cancel the app shows fires_at=%q dismiss_armed=%v, want no countdown and the dismissal standing",
					protocol.Deref(shown.AutoSettleFiresAt), protocol.Deref(shown.AutoSettleDismissArmed))
			}
			if err := cli.UpdateState("s1", protocol.StateWorking); err != nil {
				t.Fatalf("report working again: %v", err)
			}
			w.advance(2 * (autoSettleDefaultArm + autoSettleDefaultCountdown))
			if shown := sessionStateLastShown(t, app); shown.AutoSettleFiresAt != nil || !protocol.Deref(shown.TurnOwed) {
				t.Fatalf("the cancelled stretch shows fires_at=%q owed=%v, want no countdown and the turn owed",
					protocol.Deref(shown.AutoSettleFiresAt), protocol.Deref(shown.TurnOwed))
			}

			autoSettleWaitsAndSteersAgain(t, w, app, cli)
		})
	})

	t.Run("a cancel before the steer dismisses exactly the next stretch", func(t *testing.T) {
		inBubble(t, func(t *testing.T, w *world) {
			app, cli := autoSettleOwingSession(t, w)
			autoSettleCancel(w, app)
			if err := cli.UpdateState("s1", protocol.StateWaitingInput); err != nil {
				t.Fatalf("report waiting_input again: %v", err)
			}
			w.advance(0)
			if !protocol.Deref(sessionStateLastShown(t, app).AutoSettleDismissArmed) {
				t.Fatal("the dismissal was spent before the stretch it answers began")
			}

			autoSettleSteer(t, w, app, cli, "fix the flaky test")
			w.advance(autoSettleDefaultArm + autoSettleDefaultCountdown)
			if shown := sessionStateLastShown(t, app); shown.AutoSettleFiresAt != nil || !protocol.Deref(shown.TurnOwed) ||
				!protocol.Deref(shown.AutoSettleDismissArmed) {
				t.Fatalf("the dismissed stretch shows fires_at=%q owed=%v dismiss_armed=%v, want no countdown, the turn owed and the dismissal standing",
					protocol.Deref(shown.AutoSettleFiresAt), protocol.Deref(shown.TurnOwed), protocol.Deref(shown.AutoSettleDismissArmed))
			}

			autoSettleWaitsAndSteersAgain(t, w, app, cli)
		})
	})

	t.Run("pressing again disarms the dismissal", func(t *testing.T) {
		inBubble(t, func(t *testing.T, w *world) {
			app, cli := autoSettleOwingSession(t, w)
			autoSettleCancel(w, app)
			autoSettleCancel(w, app)
			if protocol.Deref(sessionStateLastShown(t, app).AutoSettleDismissArmed) {
				t.Fatal("the second press left the dismissal standing")
			}
			steered := autoSettleSteer(t, w, app, cli, "fix the flaky test")
			w.advance(autoSettleDefaultArm)
			testworld.AwaitSession(app, "s1", func(s protocol.Session) bool {
				return autoSettleFiresAt(t, s).Equal(steered.Add(autoSettleDefaultArm + autoSettleDefaultCountdown))
			})
		})
	})

	t.Run("disarming mid-stretch restores the arm delay", func(t *testing.T) {
		inBubble(t, func(t *testing.T, w *world) {
			app, cli := autoSettleOwingSession(t, w)
			autoSettleSteer(t, w, app, cli, "fix the flaky test")
			w.advance(autoSettleDefaultArm)
			autoSettleCancel(w, app)
			if !protocol.Deref(sessionStateLastShown(t, app).AutoSettleDismissArmed) {
				t.Fatal("cancelling a running countdown left no dismissal standing")
			}
			autoSettleCancel(w, app)
			disarmed := time.Now()
			if shown := sessionStateLastShown(t, app); protocol.Deref(shown.AutoSettleDismissArmed) || shown.AutoSettleFiresAt != nil {
				t.Fatalf("after the disarm the app shows dismiss_armed=%v fires_at=%q, want neither",
					protocol.Deref(shown.AutoSettleDismissArmed), protocol.Deref(shown.AutoSettleFiresAt))
			}

			w.advance(autoSettleDefaultArm)
			testworld.AwaitSession(app, "s1", func(s protocol.Session) bool {
				return autoSettleFiresAt(t, s).Equal(disarmed.Add(autoSettleDefaultArm + autoSettleDefaultCountdown))
			})
			w.advance(autoSettleDefaultCountdown)
			autoSettleAwaitSettled(app)
		})
	})

	t.Run("turning auto-settle off clears a standing dismissal and later cancels arm none", func(t *testing.T) {
		inBubble(t, func(t *testing.T, w *world) {
			app, _ := autoSettleOwingSession(t, w)
			autoSettleCancel(w, app)
			testworld.AwaitSession(app, "s1", func(s protocol.Session) bool { return protocol.Deref(s.AutoSettleDismissArmed) })

			setSetting(t, app, "auto_settle_enabled", "false")
			w.advance(0)
			if protocol.Deref(sessionStateLastShown(t, app).AutoSettleDismissArmed) {
				t.Fatal("the dismissal outlived auto-settle being turned off")
			}
			autoSettleCancel(w, app)
			if protocol.Deref(sessionStateLastShown(t, app).AutoSettleDismissArmed) {
				t.Fatal("a cancel armed a dismissal with auto-settle off")
			}
		})
	})
}

func TestACancelOnAShellArmsNoDismissal(t *testing.T) {
	w := newWorld(t)
	app, cli := w.App(), w.Client()
	setSetting(t, app, "auto_settle_enabled", "true")
	shell := w.Spawn(app, "shell", w.Path("api"))

	app.Send(protocol.CancelCountdownMessage{Cmd: protocol.CmdCancelCountdown, SessionID: shell})
	autoSettleAfterAppCommandsLand(app)

	if armed := queriedSession(t, cli, shell).AutoSettleDismissArmed; armed != nil {
		t.Fatal("a cancel armed a dismissal on a shell, which never auto-settles")
	}
}

func autoSettleAfterAppCommandsLand(app *testworld.Peer) {
	app.T.Helper()
	testworld.Request(app, protocol.GetSettingsMessage{Cmd: protocol.CmdGetSettings}, protocol.EventSettingsUpdated,
		func(m protocol.SettingsUpdatedMessage) bool { return m.RequestID == nil && m.ChangedKey == nil })
}

func TestUserActivityHoldsTheCountdownUntilQuiet(t *testing.T) {
	for _, tc := range []struct {
		name  string
		touch func(app *testworld.Peer)
	}{
		{"typing", func(app *testworld.Peer) {
			app.Send(protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: "s1", Data: "x"})
		}},
		{"pointer movement", func(app *testworld.Peer) {
			app.Send(protocol.TerminalPointerActivityMessage{Cmd: protocol.CmdTerminalPointerActivity, ID: "s1"})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inBubble(t, func(t *testing.T, w *world) {
				app, cli := autoSettleOwingSession(t, w)
				steered := autoSettleSteer(t, w, app, cli, "fix the flaky test")
				w.advance(autoSettleDefaultArm + time.Second)

				tc.touch(app)
				w.advance(0)
				autoSettleHeld(t, app)

				w.advance(3 * time.Second)
				tc.touch(app)
				lastTouch := time.Now()
				w.advance(autoSettleQuietWindow - time.Millisecond)
				autoSettleHeld(t, app)

				w.advance(time.Millisecond)
				resumed := testworld.AwaitSession(app, "s1", func(s protocol.Session) bool {
					return autoSettleFiresAt(t, s).Equal(lastTouch.Add(autoSettleQuietWindow + autoSettleDefaultCountdown))
				})
				if resumed.AutoSettleHeld != nil || resumed.AutoSettleDismissArmed != nil {
					t.Fatalf("the resumed countdown shows held=%v dismiss_armed=%v, want neither",
						protocol.Deref(resumed.AutoSettleHeld), protocol.Deref(resumed.AutoSettleDismissArmed))
				}

				w.advance(autoSettleDefaultCountdown - time.Millisecond)
				tc.touch(app)
				w.advance(time.Millisecond)
				autoSettleHeld(t, app)

				w.advance(autoSettleQuietWindow + autoSettleDefaultCountdown)
				settled := autoSettleAwaitSettled(app)
				if settled.AutoSettleHeld != nil || settled.AutoSettleFiresAt != nil {
					t.Fatal("the settled session still shows a held or running countdown")
				}
				if since := time.Since(steered); since <= autoSettleDefaultArm+autoSettleDefaultCountdown {
					t.Fatalf("the turn settled %s after the steer, inside the undisturbed windows", since)
				}
			})
		})
	}

	t.Run("activity during the arm delay restarts it without showing a hold", func(t *testing.T) {
		inBubble(t, func(t *testing.T, w *world) {
			app, cli := autoSettleOwingSession(t, w)
			autoSettleSteer(t, w, app, cli, "fix the flaky test")
			w.advance(10 * time.Second)
			app.Send(protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: "s1", Data: "x"})
			typed := time.Now()

			w.advance(autoSettleDefaultArm)
			for _, s := range sessionUpdatesOf(app, "s1") {
				if s.AutoSettleHeld != nil || s.AutoSettleFiresAt != nil {
					t.Fatalf("the app was shown held=%v fires_at=%q during the arm delay",
						protocol.Deref(s.AutoSettleHeld), protocol.Deref(s.AutoSettleFiresAt))
				}
			}
			w.advance(autoSettleQuietWindow)
			testworld.AwaitSession(app, "s1", func(s protocol.Session) bool {
				return autoSettleFiresAt(t, s).Equal(typed.Add(autoSettleQuietWindow + autoSettleDefaultArm + autoSettleDefaultCountdown))
			})
		})
	})

	t.Run("leaving working clears a hold and keeps the turn", func(t *testing.T) {
		inBubble(t, func(t *testing.T, w *world) {
			app, cli := autoSettleOwingSession(t, w)
			autoSettleSteer(t, w, app, cli, "fix the flaky test")
			w.advance(autoSettleDefaultArm)
			app.Send(protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: "s1", Data: "x"})
			w.advance(0)
			autoSettleHeld(t, app)

			if err := cli.RecordNotification("s1", "permission_prompt", "Allow edit?"); err != nil {
				t.Fatalf("notify: %v", err)
			}
			asking := testworld.AwaitSession(app, "s1", func(s protocol.Session) bool { return s.State == protocol.SessionStatePendingApproval })
			if asking.AutoSettleHeld != nil || !protocol.Deref(asking.TurnOwed) {
				t.Fatalf("the approval shows held=%v owed=%v, want no hold and the turn owed",
					protocol.Deref(asking.AutoSettleHeld), protocol.Deref(asking.TurnOwed))
			}
		})
	})

	t.Run("input from automation or an attach replay does not hold", func(t *testing.T) {
		inBubble(t, func(t *testing.T, w *world) {
			app, cli := autoSettleOwingSession(t, w)
			steered := autoSettleSteer(t, w, app, cli, "fix the flaky test")
			deadline := steered.Add(autoSettleDefaultArm + autoSettleDefaultCountdown)
			w.advance(autoSettleDefaultArm)
			for _, source := range []string{"automation", "attach_replay"} {
				app.Send(protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: "s1", Data: "x", Source: protocol.Ptr(source)})
				w.advance(time.Second)
				if shown := sessionStateLastShown(t, app); shown.AutoSettleHeld != nil || !autoSettleFiresAt(t, shown).Equal(deadline) {
					t.Fatalf("input from %s shows held=%v fires_at=%q, want the countdown to %s untouched",
						source, protocol.Deref(shown.AutoSettleHeld), protocol.Deref(shown.AutoSettleFiresAt), deadline)
				}
			}
			w.advance(time.Until(deadline))
			autoSettleAwaitSettled(app)
		})
	})
}

func TestAutoSettleSettingsDefaultAndRefuseOutOfRangeValues(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	for key, want := range map[string]string{
		"auto_settle_enabled":           "false",
		"auto_settle_arm_seconds":       "30",
		"auto_settle_countdown_seconds": "15",
	} {
		if got := app.Initial.Settings[key]; got != want {
			t.Errorf("a fresh daemon reports %s = %v, want %s", key, got, want)
		}
	}

	for _, tc := range []struct {
		key, value string
		accepted   bool
	}{
		{"auto_settle_arm_seconds", "", true},
		{"auto_settle_arm_seconds", "5", true},
		{"auto_settle_arm_seconds", "3600", true},
		{"auto_settle_arm_seconds", "4", false},
		{"auto_settle_arm_seconds", "3601", false},
		{"auto_settle_arm_seconds", "soon", false},
		{"auto_settle_countdown_seconds", "", true},
		{"auto_settle_countdown_seconds", "3", true},
		{"auto_settle_countdown_seconds", "600", true},
		{"auto_settle_countdown_seconds", "2", false},
		{"auto_settle_countdown_seconds", "601", false},
		{"auto_settle_enabled", "true", true},
		{"auto_settle_enabled", "30", false},
	} {
		requestID := tc.key + "=" + tc.value
		result := testworld.Request(app, protocol.SetSettingMessage{Cmd: protocol.CmdSetSetting, Key: tc.key, Value: tc.value, RequestID: protocol.Ptr(requestID)},
			protocol.EventSettingsUpdated, func(m protocol.SettingsUpdatedMessage) bool { return protocol.Deref(m.RequestID) == requestID })
		if accepted := protocol.Deref(result.Success); accepted != tc.accepted {
			t.Errorf("set %s = %q accepted=%v (%s), want %v", tc.key, tc.value, accepted, protocol.Deref(result.Error), tc.accepted)
		}
	}
	for key, want := range map[string]string{
		"auto_settle_enabled":           "true",
		"auto_settle_arm_seconds":       "3600",
		"auto_settle_countdown_seconds": "600",
	} {
		if got := w.App().Initial.Settings[key]; got != want {
			t.Errorf("after the refusals %s = %v, want the last accepted %s", key, got, want)
		}
	}
}

func autoSettleOwingSession(t *testing.T, w *world) (*testworld.Peer, *client.Client) {
	t.Helper()
	app, cli := w.App(), w.Client()
	setSetting(t, app, "auto_settle_enabled", "true")
	if err := cli.Register("s1", "s1", w.Path("s1")); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := cli.UpdateState("s1", protocol.StateWaitingInput); err != nil {
		t.Fatalf("report waiting_input: %v", err)
	}
	testworld.AwaitSession(app, "s1", func(s protocol.Session) bool { return protocol.Deref(s.TurnOwed) })
	return app, cli
}

func autoSettleSteer(t *testing.T, w *world, app *testworld.Peer, cli *client.Client, prompt string) time.Time {
	t.Helper()
	app.Send(protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: "s1", Data: prompt + "\r"})
	w.advance(0)
	if err := cli.UpdateStateFromHookEvidence("s1", protocol.StateWorking, "", "user_prompt_submit", prompt); err != nil {
		t.Fatalf("report the prompt taken: %v", err)
	}
	w.advance(0)
	if state := sessionStateLastShown(t, app).State; state != protocol.SessionStateWorking {
		t.Fatalf("after the steer the session is %s, want working", state)
	}
	return time.Now()
}

func autoSettleWaitsAndSteersAgain(t *testing.T, w *world, app *testworld.Peer, cli *client.Client) {
	t.Helper()
	if err := cli.UpdateState("s1", protocol.StateWaitingInput); err != nil {
		t.Fatalf("report waiting_input: %v", err)
	}
	w.advance(0)
	if shown := sessionStateLastShown(t, app); shown.AutoSettleDismissArmed != nil || !protocol.Deref(shown.TurnOwed) {
		t.Fatalf("back at the prompt the app shows dismiss_armed=%v owed=%v, want the dismissal spent and the turn owed",
			protocol.Deref(shown.AutoSettleDismissArmed), protocol.Deref(shown.TurnOwed))
	}
	steered := autoSettleSteer(t, w, app, cli, "now the other one")
	w.advance(autoSettleDefaultArm)
	testworld.AwaitSession(app, "s1", func(s protocol.Session) bool {
		return autoSettleFiresAt(t, s).Equal(steered.Add(autoSettleDefaultArm + autoSettleDefaultCountdown))
	})
}

func autoSettleAwaitSettled(app *testworld.Peer) protocol.Session {
	return testworld.AwaitSession(app, "s1", func(s protocol.Session) bool {
		return s.State == protocol.SessionStateWorking && !protocol.Deref(s.TurnOwed)
	})
}

func autoSettleCancel(w *world, app *testworld.Peer) {
	app.Send(protocol.CancelCountdownMessage{Cmd: protocol.CmdCancelCountdown, SessionID: "s1"})
	w.advance(0)
}

func autoSettleHeld(t *testing.T, app *testworld.Peer) {
	t.Helper()
	shown := sessionStateLastShown(t, app)
	if !protocol.Deref(shown.AutoSettleHeld) || shown.AutoSettleFiresAt != nil || !protocol.Deref(shown.TurnOwed) {
		t.Fatalf("the app shows held=%v fires_at=%q owed=%v, want the countdown frozen and the turn owed",
			protocol.Deref(shown.AutoSettleHeld), protocol.Deref(shown.AutoSettleFiresAt), protocol.Deref(shown.TurnOwed))
	}
}

func sessionStateLastShown(t *testing.T, app *testworld.Peer) protocol.Session {
	t.Helper()
	updates := sessionUpdatesOf(app, "s1")
	if len(updates) == 0 {
		t.Fatal("the app has not been shown s1")
	}
	return updates[len(updates)-1]
}

func autoSettleFiresAt(t *testing.T, s protocol.Session) time.Time {
	t.Helper()
	if s.AutoSettleFiresAt == nil {
		return time.Time{}
	}
	at, err := time.Parse(time.RFC3339Nano, *s.AutoSettleFiresAt)
	if err != nil {
		t.Fatalf("auto_settle_fires_at %q: %v", *s.AutoSettleFiresAt, err)
	}
	return at
}
