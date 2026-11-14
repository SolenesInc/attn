use attn_protocol::{SnoozeTurnMessage, StandbySessionMessage};
use attn_ui::Tokens;
use rune::{El, Tx, col};
use rune_kit::{MenuItem, Placement};

use crate::State;
use crate::calendar::{clock, iso};
use crate::calendar::{local_day, local_instant, weekday};

/// The hour a day-named snooze ends at, as in the Tauri app.
const WAKE_HOUR: i64 = 9;
const MINUTE_MS: i64 = 60_000;
const HOUR_MS: i64 = 60 * MINUTE_MS;
const SATURDAY: i64 = 6;
const MONDAY: i64 = 1;

#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash)]
pub enum Choice {
    Minutes30,
    Hour1,
    Hours2,
    Hours4,
    Hours8,
    Tomorrow,
    Saturday,
    Monday,
}

pub const CHOICES: [Choice; 8] = [
    Choice::Minutes30,
    Choice::Hour1,
    Choice::Hours2,
    Choice::Hours4,
    Choice::Hours8,
    Choice::Tomorrow,
    Choice::Saturday,
    Choice::Monday,
];

impl Choice {
    pub fn id(self) -> &'static str {
        match self {
            Choice::Minutes30 => "30m",
            Choice::Hour1 => "1h",
            Choice::Hours2 => "2h",
            Choice::Hours4 => "4h",
            Choice::Hours8 => "8h",
            Choice::Tomorrow => "tomorrow",
            Choice::Saturday => "saturday",
            Choice::Monday => "monday",
        }
    }

    pub fn label(self) -> &'static str {
        match self {
            Choice::Minutes30 => "For 30 minutes",
            Choice::Hour1 => "For an hour",
            Choice::Hours2 => "For 2 hours",
            Choice::Hours4 => "For 4 hours",
            Choice::Hours8 => "For 8 hours",
            Choice::Tomorrow => "Until tomorrow",
            Choice::Saturday => "Until Saturday",
            Choice::Monday => "Until Monday",
        }
    }

    /// When the choice ends, pressed at `now_ms`, in the user's time zone: a day choice ends at
    /// the wake hour on the calendar day, across a daylight saving change too, and always after
    /// `now_ms`.
    pub fn until(self, now_ms: i64) -> i64 {
        let plus = |hours: i64| now_ms + hours * HOUR_MS;
        match self {
            Choice::Minutes30 => now_ms + 30 * MINUTE_MS,
            Choice::Hour1 => plus(1),
            Choice::Hours2 => plus(2),
            Choice::Hours4 => plus(4),
            Choice::Hours8 => plus(8),
            Choice::Tomorrow => wake_hour_on(local_day(now_ms) + 1),
            Choice::Saturday => next_weekday(now_ms, SATURDAY),
            Choice::Monday => next_weekday(now_ms, MONDAY),
        }
    }

    /// The time the choice resolves to, beside its label: the clock for an hour choice, the
    /// weekday and clock for a day choice.
    pub fn detail(self, now_ms: i64) -> String {
        let at = self.until(now_ms);
        let clock = clock(at);
        match self {
            Choice::Tomorrow | Choice::Saturday | Choice::Monday => {
                format!("{} {clock}", WEEKDAYS[weekday(local_day(at)) as usize])
            }
            _ => clock,
        }
    }
}

const WEEKDAYS: [&str; 7] = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"];

/// The instant the user's clock reads the wake hour on calendar day `day`.
fn wake_hour_on(day: i64) -> i64 {
    local_instant(day, WAKE_HOUR * 3600)
}

/// The wake hour on the next `target` weekday strictly after `now_ms`.
fn next_weekday(now_ms: i64, target: i64) -> i64 {
    let today = local_day(now_ms);
    let ahead = (target - weekday(today)).rem_euclid(7);
    let at = wake_hour_on(today + ahead);
    if at > now_ms {
        at
    } else {
        wake_hour_on(today + ahead + 7)
    }
}

/// An open snooze menu: whose, where it opened, and the moment its times were read, so the
/// time it shows is the time it sends.
#[derive(Clone, Debug, PartialEq, Eq, Hash)]
pub struct Menu {
    pub session: String,
    pub name: String,
    /// The key of what it opened under; none opens it in the middle of the window.
    pub under: Option<String>,
    pub standby: bool,
    pub opened_ms: i64,
}

/// Opens the menu with its first choice focused, as the Tauri app's does.
pub(crate) fn open(
    state: &mut State,
    session: &str,
    name: String,
    under: Option<String>,
    tx: &mut Tx,
) {
    tx.focus(format!("{MENU}.snooze.{}", CHOICES[0].id()), true);
    state.tick(tx);
    state.window.snooze = Some(Menu {
        session: session.to_owned(),
        name,
        under,
        standby: false,
        opened_ms: state.window.now_ms,
    });
}

/// Closes the menu once its agent is gone.
pub(crate) fn follow(state: &mut State) {
    let gone = state
        .window
        .snooze
        .as_ref()
        .is_some_and(|menu| !state.facts.sessions.iter().any(|s| s.id == menu.session));
    if gone {
        state.window.snooze = None;
    }
}

fn choose(state: &mut State, choice: Choice) {
    let Some(menu) = state.window.snooze.take() else {
        return;
    };
    if menu.standby {
        let _ = state.daemon.send(&StandbySessionMessage {
            session_id: menu.session,
            until: iso(choice.until(menu.opened_ms)),
            ..StandbySessionMessage::new()
        });
    } else {
        snooze_turn(state, &menu.session, choice, menu.opened_ms);
    }
}

/// Snoozes the agent's turn until `choice` ends, counted from `opened_ms`.
pub(crate) fn snooze_turn(state: &State, session: &str, choice: Choice, opened_ms: i64) {
    let _ = state.daemon.send(&SnoozeTurnMessage {
        session_id: session.into(),
        until: iso(choice.until(opened_ms)),
        ..SnoozeTurnMessage::new()
    });
}

pub const MENU: &str = "snooze.menu";

pub(crate) fn view(state: &State, t: &Tokens) -> Vec<El> {
    let Some(menu) = state.window.snooze.as_ref() else {
        return Vec::new();
    };
    let title = if menu.standby {
        format!("Standby {} until", menu.name)
    } else {
        format!("Snooze {}", menu.name)
    };
    let mode = if menu.standby { "standby" } else { "snooze" };
    let mut kit_menu = rune_kit::Menu::new(format!("{MENU}.{mode}"))
        .standard()
        .gutter(4.0)
        .min_width(260.0)
        .label(title.clone())
        .open(true)
        .on_close(|state: &mut State| state.window.snooze = None)
        .group_label(title);
    for choice in CHOICES {
        kit_menu = kit_menu.item(
            MenuItem::new(choice.label())
                .key(format!("{MENU}.{mode}.{}", choice.id()))
                .badge(choice.detail(menu.opened_ms))
                .on_select(move |state: &mut State| choose(state, choice)),
        );
    }
    if !menu.standby {
        kit_menu = kit_menu.item(
            MenuItem::new("Standby until…")
                .key(format!("{MENU}.standby"))
                .on_select_tx(|state: &mut State, tx: &mut Tx| {
                    if let Some(menu) = &mut state.window.snooze {
                        menu.standby = true;
                    }
                    tx.focus(format!("{MENU}.standby.{}", CHOICES[0].id()), true);
                }),
        );
    }
    let kit_menu = kit_menu.group_label(format!("↑ ↓ choose · ↵ {mode} · esc cancel"));
    let mut layer = Vec::new();
    match &menu.under {
        Some(under) => {
            kit_menu
                .anchor(under.clone())
                .build(attn_ui::kit(t), &mut layer);
        }
        None => {
            let spot = kit_menu
                .placement(Placement::Bottom)
                .build(attn_ui::kit(t), &mut layer);
            layer.insert(
                0,
                col()
                    .key(format!("{MENU}.center"))
                    .absolute(0.0, 0.0)
                    .w_full()
                    .h_full()
                    .items_center()
                    .child(rune::el().grow(1.0))
                    .child(spot)
                    .child(rune::el().grow(2.0)),
            );
        }
    }
    layer
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn every_choice_ends_after_it_was_pressed_and_a_day_choice_ends_at_the_wake_hour_of_its_day() {
        let start = 1_767_225_600_000;
        for step in 0..(2 * 366 * 24 * 60 / 37) {
            let now = start + step * 37 * MINUTE_MS;
            for choice in CHOICES {
                let at = choice.until(now);
                assert!(at > now, "{choice:?} at {now}");
                let local = rune::time::LocalTime::at(at);
                let day = local_day(at);
                let days_ahead = day - local_day(now);
                match choice {
                    Choice::Tomorrow => assert_eq!(days_ahead, 1, "{choice:?} at {now}"),
                    Choice::Saturday => assert_eq!(weekday(day), SATURDAY),
                    Choice::Monday => assert_eq!(weekday(day), MONDAY),
                    _ => continue,
                }
                assert_eq!((local.hour, local.minute), (9, 0), "{choice:?} at {now}");
                assert!((1..=7).contains(&days_ahead) || (days_ahead == 0 && at > now));
            }
        }
    }

    #[test]
    fn an_instant_reads_as_date_to_iso_string_writes_it() {
        assert_eq!(iso(1_794_474_000_000), "2026-11-12T09:00:00.000Z");
        assert_eq!(iso(1_794_474_000_123), "2026-11-12T09:00:00.123Z");
        assert_eq!(iso(0), "1970-01-01T00:00:00.000Z");
    }
}
