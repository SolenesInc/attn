//! The band under the title bar that follows an update run: its phase and each outpost's step,
//! with outposts at the same step named together, while it runs, then its outcome, with Retry and Update without the outposts that failed. A
//! run that ends well closes on its own after a countdown that holds while the pointer is over
//! the band; any other ended run stays until dismissed.

use attn_protocol::{StartUpdateMessage, UpdateOutpostProgress, UpdateRun};
use attn_ui::Tokens;
use attn_ui::blocks::{self, BannerTone};
use attn_ui::system::Button;
use rune::{El, Tx};

use crate::State;
use crate::closing::{Closing, Tick};
use crate::requests::Refused;
use attn_client::Connection;

const CLOSED_PREF: &str = "update_run.closed";
#[derive(Default)]
pub(crate) struct Banner {
    /// The run dismissed in this window; before one is, the run dismissed at the last launch.
    closed: Option<String>,
    closing: Closing,
}

pub(crate) fn is_open(run: &UpdateRun) -> bool {
    !matches!(run.phase.as_str(), "done" | "failed")
}

pub(crate) fn failed_outposts(run: &UpdateRun) -> Vec<&UpdateOutpostProgress> {
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

/// The step `outpost` is at in a run, in the Tauri app's words.
pub(crate) fn outpost_step(outpost: &UpdateOutpostProgress) -> &str {
    match outpost.state.as_str() {
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
    }
}

/// One note per step, naming every outpost at it, keyed by the first of them.
fn outpost_notes(outposts: &[UpdateOutpostProgress]) -> Vec<(&str, String)> {
    let mut groups: Vec<(&str, Vec<&UpdateOutpostProgress>)> = Vec::new();
    for outpost in outposts {
        let step = outpost_step(outpost);
        match groups.iter_mut().find(|(said, _)| *said == step) {
            Some((_, group)) => group.push(outpost),
            None => groups.push((step, vec![outpost])),
        }
    }
    groups
        .into_iter()
        .map(|(step, group)| {
            let names: Vec<&str> = group.iter().map(|o| o.name.as_str()).collect();
            (
                group[0].id.as_str(),
                format!("{}: {step}", crate::outpost::names(&names)),
            )
        })
        .collect()
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

/// Counts down while the shown run has ended well.
pub(crate) fn follow(state: &mut State, tx: &mut Tx) {
    let due = shown(state)
        .filter(|run| closes_by_itself(run))
        .map(|run| run.id.clone());
    state.window.update_run.closing.follow(due, tx);
}

/// Moves the countdown on, and closes the band when it runs out.
pub(crate) fn poll(state: &mut State, now: f64, tx: &mut Tx) -> bool {
    match state.window.update_run.closing.poll(now, tx) {
        Tick::Same => false,
        Tick::Changed => true,
        Tick::RanOut => {
            close(state);
            true
        }
    }
}

fn hold(state: &mut State, inside: bool, tx: &mut Tx) {
    state.window.update_run.closing.hold(inside, tx);
}

fn close(state: &mut State) {
    let banner = &mut state.window.update_run;
    let Some(run) = state.facts.update_run.as_ref().as_ref() else {
        return;
    };
    banner.closing.stop();
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

fn tone(run: &UpdateRun) -> BannerTone {
    if is_open(run) {
        BannerTone::Calm
    } else if run.phase == "failed" {
        BannerTone::Bad
    } else if closes_by_itself(run) {
        BannerTone::Good
    } else {
        BannerTone::Warn
    }
}

pub(crate) fn banner(state: &State, t: &Tokens) -> Option<El> {
    let run = shown(state)?;
    let connected = state.facts.connection == Connection::Connected;
    let open = is_open(run);
    let says = if open {
        phase_text(&run.phase).to_owned()
    } else {
        run.outcome.clone().unwrap_or_default()
    };
    let mut banner = blocks::Banner::new("update-run", "attn update", tone(run))
        .value(run.phase.clone())
        .says("update-run.text", says)
        .on_hover(hold);
    if open {
        for (id, note) in outpost_notes(&run.outposts) {
            banner = banner.note(format!("update-run.outpost.{id}"), note);
        }
    }
    if run.phase == "failed" {
        banner = banner.action(
            "update-run.retry",
            Button::new("Retry")
                .disabled(!connected)
                .on_press_tx(|state: &mut State, tx: &mut Tx| start(state, None, tx)),
        );
    }
    let failed = failed_outposts(run);
    if !failed.is_empty() {
        let label = match failed.as_slice() {
            [one] => format!("Update without {}", one.name),
            many => format!("Update without {} outposts", many.len()),
        };
        let ids: Vec<String> = failed.iter().map(|o| o.id.clone()).collect();
        banner = banner.action(
            "update-run.without",
            Button::new(label).disabled(!connected).on_press_tx(
                move |state: &mut State, tx: &mut Tx| start(state, Some(ids.clone()), tx),
            ),
        );
    }
    if let Some(seconds) = state.window.update_run.closing.seconds(&run.id) {
        banner = banner.countdown("update-run.countdown", seconds);
    }
    if !open {
        banner = banner.dismiss("update-run.dismiss", "Dismiss update", close);
    }
    Some(banner.build(t))
}
