//! The band under the title bar that follows an update run: its phase and each outpost's step
//! while it runs, then its outcome, with Retry and Update without the outposts that failed. A
//! run that ends well closes on its own after a countdown that holds while the pointer is over
//! the band; any other ended run stays until dismissed.

use attn_protocol::{StartUpdateMessage, UpdateOutpostProgress, UpdateRun};
use attn_ui::{GlyphButton, Tokens, icon};
use rune::tok::{space, type_};
use rune::{El, Tx, row, text};
use rune_kit::{Button, ControlSize};

use crate::State;
use crate::requests::Refused;
use attn_client::Connection;

const CLOSED_PREF: &str = "update_run.closed";
const CLOSE_AFTER_SECONDS: f64 = 5.0;

#[derive(Default)]
pub(crate) struct Banner {
    /// The run dismissed in this window; before one is, the run dismissed at the last launch.
    closed: Option<String>,
    countdown: Option<Countdown>,
    /// Whether the pointer rests on the band, which holds a countdown that starts under it.
    pointed: bool,
}

/// The seconds left before a run that ended well closes. `since` is the clock reading the
/// count last resumed at, and is `None` while the pointer holds it.
struct Countdown {
    run: String,
    left: f64,
    since: Option<f64>,
    shown: u32,
}

impl Countdown {
    fn remaining(&self, now: f64) -> f64 {
        self.left - self.since.map_or(0.0, |since| now - since)
    }

    fn wake(&self, tx: &mut Tx) {
        if let Some(since) = self.since {
            tx.wake_at(since + self.left - f64::from(self.shown.saturating_sub(1)));
        }
    }
}

fn is_open(run: &UpdateRun) -> bool {
    !matches!(run.phase.as_str(), "done" | "failed")
}

fn failed_outposts(run: &UpdateRun) -> Vec<&UpdateOutpostProgress> {
    if run.phase != "failed" {
        return Vec::new();
    }
    run.outposts
        .iter()
        .filter(|o| o.state == "failed")
        .collect()
}

fn closes_by_itself(run: &UpdateRun) -> bool {
    run.phase == "done"
        && !run
            .outposts
            .iter()
            .any(|o| o.state == "failed" || o.state == "skipped")
}

fn phase_text(phase: &str) -> &str {
    match phase {
        "preparing" => "Building the update.",
        "staging" => "Getting the update ready.",
        "applying" => "Installing the update. attn will restart; your agents keep running.",
        "home_restarted" => "attn restarted.",
        "outposts" => "Finishing up.",
        other => other,
    }
}

fn outpost_text(outpost: &UpdateOutpostProgress) -> String {
    let step = match outpost.state.as_str() {
        "staging" => match outpost.detail.as_deref() {
            Some("connecting") => "Connecting.",
            Some("building") => "Building attn for Linux.",
            Some("downloading") => "Downloading attn for Linux.",
            Some("sending") => "Copying it over.",
            Some("verifying") => "Checking it runs.",
            _ => "Getting ready.",
        },
        "staged" => "Ready.",
        "switching" => "Switching to the new build.",
        "done" => "Up to date.",
        state => outpost.detail.as_deref().unwrap_or(state),
    };
    format!("{}: {step}", outpost.name)
}

/// The run the band shows: the daemon's last, unless this window or the last launch
/// dismissed it.
fn shown(state: &State) -> Option<&UpdateRun> {
    let run = state.facts.update_run.as_ref().as_ref()?;
    let closed = match &state.window.update_run.closed {
        Some(id) => Some(id.clone()),
        None => crate::frame::pref_string(CLOSED_PREF),
    };
    (closed.as_deref() != Some(run.id.as_str())).then_some(run)
}

/// Starts the countdown when the shown run ends well, and drops it when the run changes.
pub(crate) fn follow(state: &mut State, tx: &mut Tx) {
    let due = shown(state)
        .filter(|run| closes_by_itself(run))
        .map(|run| run.id.clone());
    let banner = &mut state.window.update_run;
    match due {
        Some(run) if banner.countdown.as_ref().is_some_and(|c| c.run == run) => {}
        Some(run) => {
            let countdown = Countdown {
                run,
                left: CLOSE_AFTER_SECONDS,
                since: (!banner.pointed).then(|| tx.clock()),
                shown: CLOSE_AFTER_SECONDS as u32,
            };
            countdown.wake(tx);
            banner.countdown = Some(countdown);
        }
        None => banner.countdown = None,
    }
}

/// Moves the countdown on, and closes the band when it runs out.
pub(crate) fn poll(state: &mut State, now: f64, tx: &mut Tx) -> bool {
    let Some(countdown) = &mut state.window.update_run.countdown else {
        return false;
    };
    if countdown.since.is_none() {
        return false;
    }
    let left = countdown.remaining(now);
    if left <= 0.0 {
        close(state);
        return true;
    }
    let shown = left.ceil() as u32;
    let changed = shown != countdown.shown;
    countdown.shown = shown;
    countdown.wake(tx);
    changed
}

fn hold(state: &mut State, inside: bool, tx: &mut Tx) {
    let banner = &mut state.window.update_run;
    banner.pointed = inside;
    let Some(countdown) = &mut banner.countdown else {
        tx.live_only();
        return;
    };
    let now = tx.clock();
    match (inside, countdown.since) {
        (true, Some(_)) => {
            countdown.left = countdown.remaining(now);
            countdown.since = None;
        }
        (false, None) => {
            countdown.since = Some(now);
            countdown.wake(tx);
        }
        _ => tx.live_only(),
    }
}

fn close(state: &mut State) {
    let banner = &mut state.window.update_run;
    let Some(run) = state.facts.update_run.as_ref().as_ref() else {
        return;
    };
    banner.countdown = None;
    banner.pointed = false;
    banner.closed = Some(run.id.clone());
    crate::frame::set_pref_string(CLOSED_PREF, &run.id);
}

/// Asks the daemon to update attn, leaving out `without` outposts; a refusal shows as a
/// notice.
pub(crate) fn start(state: &mut State, without: Option<Vec<String>>, tx: &mut Tx) {
    state.act(
        |request_id| StartUpdateMessage {
            request_id: Some(request_id),
            without_outposts: without,
            ..StartUpdateMessage::new()
        },
        Refused::Bare,
        |_, _, _| {},
        tx,
    );
}

pub(crate) fn banner(state: &State, t: &Tokens) -> Option<El> {
    let run = shown(state)?;
    let kit = attn_ui::kit(t);
    let connected = state.facts.connection == Connection::Connected;
    let open = is_open(run);
    let says = if open {
        phase_text(&run.phase).to_owned()
    } else {
        run.outcome.clone().unwrap_or_default()
    };
    let outposts = open.then(|| {
        run.outposts.iter().map(|outpost| {
            text(outpost_text(outpost))
                .key(format!("update-run.outpost.{}", outpost.id))
                .font(type_::BODY)
                .color(t.text_secondary)
        })
    });
    let retry = (run.phase == "failed").then(|| {
        Button::new("Retry")
            .control_size(ControlSize::Small)
            .disabled(!connected)
            .on_press_tx(|state: &mut State, tx: &mut Tx| start(state, None, tx))
            .build(kit)
            .key("update-run.retry")
    });
    let failed = failed_outposts(run);
    let without = (!failed.is_empty()).then(|| {
        let label = match failed.as_slice() {
            [one] => format!("Update without {}", one.name),
            many => format!("Update without {} outposts", many.len()),
        };
        let ids: Vec<String> = failed.iter().map(|o| o.id.clone()).collect();
        Button::new(label)
            .control_size(ControlSize::Small)
            .disabled(!connected)
            .on_press_tx(move |state: &mut State, tx: &mut Tx| start(state, Some(ids.clone()), tx))
            .build(kit)
            .key("update-run.without")
    });
    let countdown = state
        .window
        .update_run
        .countdown
        .as_ref()
        .filter(|c| c.run == run.id)
        .map(|c| {
            text(format!("Closing in {}s", c.shown))
                .key("update-run.countdown")
                .font(type_::BODY)
                .color(t.text_tertiary)
        });
    let dismiss = (!open).then(|| {
        GlyphButton::new(icon::CLOSE, "Dismiss update")
            .help("Dismiss")
            .on_press(close)
            .build(kit)
            .key("update-run.dismiss")
    });
    Some(
        row()
            .key("update-run")
            .role("region")
            .label("attn update")
            .a11y_value(run.phase.clone())
            .w_full()
            .px(space::S3)
            .py(space::S1)
            .gap(space::S2)
            .items_center()
            .fill(t.bg_panel)
            .border_sides([0.0, 0.0, 1.0, 0.0], t.border)
            .on_hover_change(hold)
            .child(
                text(says)
                    .key("update-run.text")
                    .font(type_::BODY)
                    .color(t.text_primary)
                    .wrap()
                    .min_w(0.0),
            )
            .children(outposts.into_iter().flatten())
            .child(row().grow(1.0))
            .children(retry)
            .children(without)
            .children(countdown)
            .children(dismiss),
    )
}
