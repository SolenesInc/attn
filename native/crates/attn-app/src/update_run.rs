//! The band under the title bar that follows an update run: its phase and each outpost's step,
//! with outposts at the same step named together, while it runs, then its outcome, with Retry and Update without the outposts that failed. A
//! run that ends well closes on its own after a countdown that holds while the pointer is over
//! the band; any other ended run stays until dismissed.

use attn_protocol::{StartUpdateMessage, UpdateOutpostMessage, UpdateOutpostProgress, UpdateRun};
use attn_ui::Tokens;
use attn_ui::blocks::{self, BannerTone};
use attn_ui::system::Button;
use rune::{Cx, El, Tx};

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
        .filter(|o| o.state == "failed" && is_outpost(o))
        .collect()
}

fn is_outpost(row: &UpdateOutpostProgress) -> bool {
    row.role.as_deref().is_none_or(|role| role == "outpost")
}

fn row<'a>(run: &'a UpdateRun, role: &str) -> Option<&'a UpdateOutpostProgress> {
    run.outposts
        .iter()
        .find(|o| o.role.as_deref() == Some(role))
}

fn version(build: &str) -> &str {
    build.split(" (").next().unwrap_or(build)
}

/// What a row offers: Update starts the home's update, UpdateOutpost updates that outpost, Open is the page to
/// open, and Copy is a command the user runs.
#[derive(Clone)]
pub(crate) enum RowAction {
    Update,
    UpdateOutpost(String),
    Open(String),
    Copy(String),
}

impl RowAction {
    pub(crate) fn of(row: &UpdateOutpostProgress) -> Option<RowAction> {
        row.action.as_deref().map(|action| match action {
            "Update" => RowAction::Update,
            "Update outpost" => RowAction::UpdateOutpost(row.id.clone()),
            url if url.starts_with("https://") || url.starts_with("http://") => {
                RowAction::Open(url.to_owned())
            }
            command => RowAction::Copy(command.to_owned()),
        })
    }

    fn needs_daemon(&self) -> bool {
        matches!(self, RowAction::Update | RowAction::UpdateOutpost(_))
    }
}

/// The words for a run this machine keeps for its home: its head, one note and one action.
pub(crate) struct HomeSays {
    pub(crate) tone: BannerTone,
    pub(crate) head: String,
    pub(crate) note: Option<String>,
    pub(crate) action: Option<RowAction>,
}

/// What a run this app machine keeps for its home says, or None for any other run.
pub(crate) fn home_says(run: &UpdateRun) -> Option<HomeSays> {
    let home = row(run, "home")?;
    let this = row(run, "app_machine");
    let builds = || {
        let theirs = version(home.build.as_deref().unwrap_or_default());
        let ours = version(this.and_then(|r| r.build.as_deref()).unwrap_or_default());
        format!("{} {theirs} · this Mac {ours}", home.name)
    };
    if is_open(run) {
        return Some(HomeSays {
            tone: BannerTone::Calm,
            head: format!("Updating {} to {}", home.name, version(&run.target_build)),
            note: None,
            action: None,
        });
    }
    if run.phase == "done" {
        let mut left_behind = run
            .outposts
            .iter()
            .filter(|o| is_outpost(o) && matches!(o.state.as_str(), "failed" | "skipped"));
        let left = left_behind
            .clone()
            .find(|o| RowAction::of(o).is_some())
            .or_else(|| left_behind.next());
        return Some(match left {
            None => HomeSays {
                tone: BannerTone::Good,
                head: format!("Updated {}.", home.name),
                note: None,
                action: None,
            },
            Some(outpost) => HomeSays {
                tone: BannerTone::Warn,
                head: format!(
                    "Updated {}; {} stays on its build.",
                    home.name, outpost.name
                ),
                note: outpost.detail.clone(),
                action: RowAction::of(outpost),
            },
        });
    }
    if let Some(this) = this.filter(|r| r.reason.is_some() || r.state == "skipped") {
        let head = if this.reason.as_deref() == Some("behind") {
            format!("This Mac is behind {}.", home.name)
        } else {
            format!("{} runs another build.", home.name)
        };
        let action = RowAction::of(this);
        let note = match (&action, this.detail.as_deref().filter(|d| !d.is_empty())) {
            (None, Some(detail)) => detail.to_owned(),
            _ => builds(),
        };
        return Some(HomeSays {
            tone: BannerTone::Warn,
            head,
            note: Some(note),
            action,
        });
    }
    let Some(failed) = run.outposts.iter().find(|o| o.reason.is_some()) else {
        return Some(HomeSays {
            tone: BannerTone::Bad,
            head: "Update failed.".to_owned(),
            note: run.outcome.clone().filter(|o| !o.is_empty()),
            action: None,
        });
    };
    Some(HomeSays {
        tone: BannerTone::Bad,
        head: format!(
            "Update failed: {}: {}.",
            failed.name,
            failed
                .reason
                .as_deref()
                .unwrap_or_default()
                .replace('_', " ")
        ),
        note: failed.detail.clone(),
        action: RowAction::of(failed),
    })
}

/// The run the app shows. While the home is away, that is this machine's own update of the home
/// while it goes or after it failed; otherwise it is the home's run.
pub(crate) fn current(state: &State) -> Option<&UpdateRun> {
    match &state.facts.connection {
        Connection::HomeUnreachable(_) => state
            .facts
            .machine
            .update_run
            .as_deref()
            .filter(|run| run.install_kind == "home" && run.phase != "done"),
        _ => state.facts.update_run.as_ref().as_ref(),
    }
}

/// Whether the daemon that keeps the shown run answers now.
fn reachable(state: &State) -> bool {
    match state.facts.connection {
        Connection::HomeUnreachable(_) => state.facts.machine.connected,
        ref connection => *connection == Connection::Connected,
    }
}

/// Runs `action`: Update starts the update on the daemon that keeps the shown run, Copy puts the
/// command on the clipboard.
pub(crate) fn run_action(action: &RowAction, state: &mut State, tx: &mut Tx) {
    match action {
        RowAction::Update if matches!(state.facts.connection, Connection::HomeUnreachable(_)) => {
            let id = state.next_request_id();
            let command = StartUpdateMessage {
                request_id: Some(id.clone()),
                ..StartUpdateMessage::new()
            };
            state.act_on(
                attn_client::Reach::Machine,
                id,
                &command,
                Refused::Bare,
                |_, _, _| {},
                tx,
            );
        }
        RowAction::Update => start(state, None, tx),
        RowAction::UpdateOutpost(id) => {
            let _ = state.daemon.send(&UpdateOutpostMessage {
                id: id.clone(),
                ..UpdateOutpostMessage::new()
            });
        }
        RowAction::Open(url) => {
            if let Err(why) = tx.open_url(url) {
                eprintln!("attn-app: could not open {url}: {why}");
            }
        }
        RowAction::Copy(command) => tx.copy(command.clone()),
    }
}

pub(crate) fn action_text(action: &RowAction) -> &'static str {
    match action {
        RowAction::Update => "Update",
        RowAction::UpdateOutpost(_) => "Update outpost",
        RowAction::Open(_) => "Open release",
        RowAction::Copy(_) => "Copy command",
    }
}

pub(crate) fn action_button(action: &RowAction) -> Button {
    Button::new(action_text(action))
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

/// The words for the step `outpost` is at in a run.
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
    let run = current(state)?;
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
    let Some(id) = current(state).map(|run| run.id.clone()) else {
        return;
    };
    let banner = &mut state.window.update_run;
    banner.closing.stop();
    crate::frame::set_pref_string(CLOSED_PREF, &id);
    banner.closed = Some(id);
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

pub(crate) fn banner(state: &State, t: &Tokens, cx: &Cx<State>) -> Option<El> {
    let banner = said(state)?.details(crate::shell::opens_details(crate::shell::Band::UpdateRun));
    Some(banner.build(t, cx))
}

/// The message the banner shows: its run and the run's phase.
pub(crate) fn identity(state: &State) -> Option<String> {
    shown(state).map(|run| format!("{}:{}", run.id, run.phase))
}

pub(crate) fn said(state: &State) -> Option<blocks::Banner> {
    let run = shown(state)?;
    let connected = state.facts.connection == Connection::Connected;
    let open = is_open(run);
    if let Some(home) = home_says(run) {
        return Some(home_banner(state, run, home, reachable(state)));
    }
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
            banner = banner.item().note(format!("update-run.outpost.{id}"), note);
        }
    }
    if run.phase == "failed" {
        banner = banner.action(
            "update-run.retry",
            Button::new("Retry").disabled(!connected),
            |state: &mut State, tx: &mut Tx| start(state, None, tx),
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
            Button::new(label).disabled(!connected),
            move |state: &mut State, tx: &mut Tx| start(state, Some(ids.clone()), tx),
        );
    }
    if let Some(seconds) = state.window.update_run.closing.seconds(&run.id) {
        banner = banner.countdown("update-run.countdown", seconds);
    }
    if !open {
        banner = banner.dismiss("update-run.dismiss", "Dismiss update", close);
    }
    Some(banner)
}

fn home_banner(state: &State, run: &UpdateRun, home: HomeSays, connected: bool) -> blocks::Banner {
    let mut banner = blocks::Banner::new("update-run", "attn update", home.tone)
        .value(run.phase.clone())
        .says("update-run.text", home.head)
        .on_hover(hold);
    if is_open(run) {
        for (id, note) in outpost_notes(&run.outposts) {
            banner = banner.item().note(format!("update-run.outpost.{id}"), note);
        }
    }
    if let Some(note) = home.note {
        banner = banner.item().note("update-run.note", note);
    }
    if let Some(action) = home.action {
        let button = action_button(&action).disabled(action.needs_daemon() && !connected);
        banner = banner.action(
            "update-run.action",
            button,
            move |state: &mut State, tx: &mut Tx| run_action(&action, state, tx),
        );
    }
    if let Some(seconds) = state.window.update_run.closing.seconds(&run.id) {
        banner = banner.countdown("update-run.countdown", seconds);
    }
    if !is_open(run) {
        banner = banner.dismiss("update-run.dismiss", "Dismiss update", close);
    }
    banner
}
