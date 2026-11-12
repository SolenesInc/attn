use attn_protocol::{SnoozeTurnMessage, StandbySessionMessage};
use attn_ui::Tokens;
use rune::time::LocalTime;
use rune::tok::{space, type_};
use rune::{El, Side, Tx, col, row, text};

use crate::State;

/// The hour a day-named snooze ends at, as in the Tauri app.
const WAKE_HOUR: i64 = 9;
const MINUTE_MS: i64 = 60_000;
const HOUR_MS: i64 = 60 * MINUTE_MS;
const DAY_S: i64 = 86_400;
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

/// Days since 1970-01-01 on the user's calendar at `ms`.
fn local_day(ms: i64) -> i64 {
    (ms.div_euclid(1000) + i64::from(LocalTime::at(ms).offset)).div_euclid(DAY_S)
}

/// 0 for Sunday: 1970-01-01 was a Thursday.
fn weekday(day: i64) -> i64 {
    (day + 4).rem_euclid(7)
}

/// The instant the user's clock reads the wake hour on calendar day `day`.
fn wake_hour_on(day: i64) -> i64 {
    let local_s = day * DAY_S + WAKE_HOUR * 3600;
    let guess = local_s * 1000;
    let first = (local_s - i64::from(LocalTime::at(guess).offset)) * 1000;
    (local_s - i64::from(LocalTime::at(first).offset)) * 1000
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

pub fn clock(ms: i64) -> String {
    let local = LocalTime::at(ms);
    format!("{}:{:02}", local.hour, local.minute)
}

/// `ms` as the Tauri app sends an instant, `Date.toISOString`: UTC to the millisecond.
pub fn iso(ms: i64) -> String {
    let t = LocalTime::at_offset(ms, 0);
    format!(
        "{:04}-{:02}-{:02}T{:02}:{:02}:{:02}.{:03}Z",
        t.year,
        t.month,
        t.day,
        t.hour,
        t.minute,
        t.second,
        ms.rem_euclid(1000)
    )
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
    let until = iso(choice.until(menu.opened_ms));
    let _ = if menu.standby {
        state.daemon.send(&StandbySessionMessage {
            session_id: menu.session,
            until,
            ..StandbySessionMessage::new()
        })
    } else {
        state.daemon.send(&SnoozeTurnMessage {
            session_id: menu.session,
            until,
            ..SnoozeTurnMessage::new()
        })
    };
}

pub const MENU: &str = "snooze.menu";

pub(crate) fn view(state: &State, t: &Tokens) -> Option<El> {
    let menu = state.window.snooze.as_ref()?;
    let title = if menu.standby {
        format!("Standby {} until", menu.name)
    } else {
        format!("Snooze {}", menu.name)
    };
    let item = |key: String, label: &str, detail: Option<String>| {
        row()
            .key(key)
            .role("menuitem")
            .label(label.to_owned())
            .focusable(true)
            .w_full()
            .h(26.0)
            .px(space::S2)
            .gap(space::S3)
            .items_center()
            .radius(4.0)
            .hover(t.bg_button)
            .focus_highlight(t.bg_selected)
            .child(
                text(label.to_owned())
                    .font(type_::BODY)
                    .color(t.text_primary)
                    .grow(1.0),
            )
            .children(detail.map(|d| text(d).font(type_::MONO_CAPTION).color(t.text_tertiary)))
    };
    let mode = if menu.standby { "standby" } else { "snooze" };
    let choices = CHOICES.map(|choice| {
        item(
            format!("{MENU}.{mode}.{}", choice.id()),
            choice.label(),
            Some(choice.detail(menu.opened_ms)),
        )
        .on_press(move |state: &mut State| choose(state, choice))
    });
    let standby = (!menu.standby).then(|| {
        item(format!("{MENU}.standby"), "Standby until…", None).on_press_tx(
            |state: &mut State, tx: &mut Tx| {
                if let Some(menu) = &mut state.window.snooze {
                    menu.standby = true;
                }
                tx.focus(format!("{MENU}.standby.{}", CHOICES[0].id()), true);
            },
        )
    });
    let surface = col()
        .key(format!("{MENU}.{mode}"))
        .role("menu")
        .label(title.clone())
        .w(260.0)
        .padding(space::S1)
        .gap(2.0)
        .radius(6.0)
        .fill(t.bg_elevated)
        .border(1.0, t.border)
        .focus_group()
        .modal_tx(true, |state: &mut State, _: &mut Tx| {
            state.window.snooze = None
        })
        .child(
            text(title)
                .font(type_::SECTION)
                .color(t.text_tertiary)
                .px(space::S2)
                .py(space::S1),
        )
        .children(choices)
        .children(standby)
        .child(
            text(format!("↑ ↓ choose · ↵ {mode} · esc cancel"))
                .font(type_::LABEL)
                .color(t.text_muted)
                .px(space::S2)
                .py(space::S1),
        );
    let surface = match &menu.under {
        Some(under) => surface
            .anchored(under.clone(), Side::Bottom, rune::Align::Start, 4.0)
            .focus_return(under.clone()),
        None => surface,
    };
    Some(match menu.under {
        Some(_) => surface,
        None => row()
            .key(format!("{MENU}.center"))
            .absolute(0.0, 0.0)
            .w_full()
            .h_full()
            .center()
            .child(surface),
    })
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
                let local = LocalTime::at(at);
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
