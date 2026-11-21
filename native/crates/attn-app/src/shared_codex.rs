//! The band under the title bar that follows the Codex app server a profile's Codex tiles share:
//! an update to install, the restart that brings it in and what holds it, a restart that
//! failed, and tiles still on an older Codex. Closing the update keeps it closed for that
//! version, and closing the old tiles keeps them closed until more tiles fall behind or the
//! running Codex changes, across launches.

use std::collections::BTreeMap;

use attn_protocol::{
    InstallCodexUpdateMessage, RestartSharedCodexMessage, SharedCodexBlocker, SharedCodexState,
};
use attn_ui::blocks::{Actions, Banner, BannerTone, Block, List};
use attn_ui::system::{Button, ListRow, Look, Type, words};
use attn_ui::{Dialog, Tokens};
use rune::{Cx, El, Tx};
use serde::{Deserialize, Serialize};

use crate::State;
use crate::shell::Layer;

/// The Tauri app's keys, so a dismissal carries over from it.
const UPDATE_PREF: &str = "attn.codex_update_banner.dismissed_version";
const OLD_TILES_PREF: &str = "attn.codex_old_tiles_banner.dismissed";

const BAND: &str = "codex-updates";

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(crate) enum Confirm {
    Restart,
    Reload,
}

#[derive(Default)]
pub(crate) struct SharedCodex {
    installing: bool,
    install_error: Option<String>,
    /// The installed and latest versions the install error was said against.
    versions: Option<(Option<String>, Option<String>)>,
    confirm: Option<Confirm>,
    /// The dismissed update's version and old tiles, by profile; `None` until this window
    /// reads the last launch's.
    dismissed: Option<Dismissed>,
    /// The wall clock the force-stop countdown reads, in milliseconds.
    now_ms: i64,
}

#[derive(Clone, Default)]
struct Dismissed {
    update: Option<String>,
    old_tiles: BTreeMap<String, OldTiles>,
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
struct OldTiles {
    running: String,
    sessions: Vec<String>,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
enum Stage {
    Failed,
    Draining,
    Restarting,
    Pending,
    Installing,
    Available,
    OldTiles,
}

/// The shared Codex of the profile on screen.
fn current(state: &State) -> Option<&SharedCodexState> {
    let shared = state.facts.shared_codex.as_ref().as_ref()?;
    (shared.profile_id == state.facts.profiles.selected)
        .then_some(shared.state.as_ref())
        .flatten()
}

fn semver(version: &str) -> Option<(u64, u64, u64)> {
    let version = version.trim();
    let version = version.strip_prefix('v').unwrap_or(version);
    let core = version.split(['-', '+']).next()?;
    let mut parts = core.split('.').map(|p| p.parse::<u64>().ok());
    let parsed = (parts.next()??, parts.next()??, parts.next()??);
    parts.next().is_none().then_some(parsed)
}

fn update_available(state: &SharedCodexState) -> bool {
    match (
        state.installed_version.as_deref().and_then(semver),
        state.latest_version.as_deref().and_then(semver),
    ) {
        (Some(installed), Some(latest)) => latest > installed,
        _ => false,
    }
}

fn stale(state: &SharedCodexState) -> &[String] {
    state.stale_sessions.as_deref().unwrap_or_default()
}

fn can_restart_now(state: &SharedCodexState) -> bool {
    state.restart == "pending" && state.blockers.as_ref().is_some_and(|b| !b.is_empty())
}

fn dismissed(state: &State) -> Dismissed {
    if let Some(dismissed) = &state.window.shared_codex.dismissed {
        return dismissed.clone();
    }
    Dismissed {
        update: crate::frame::pref_string(UPDATE_PREF).filter(|v| !v.is_empty()),
        old_tiles: crate::frame::pref_string(OLD_TILES_PREF)
            .and_then(|json| serde_json::from_str(&json).ok())
            .unwrap_or_default(),
    }
}

fn old_tiles_dismissed(state: &SharedCodexState, dismissal: Option<&OldTiles>) -> bool {
    let Some(dismissal) = dismissal else {
        return false;
    };
    dismissal.running == state.running_version.clone().unwrap_or_default()
        && stale(state).iter().all(|s| dismissal.sessions.contains(s))
}

fn stage(state: &State, codex: &SharedCodexState) -> Option<Stage> {
    match codex.restart.as_str() {
        "failed" => return Some(Stage::Failed),
        "restarting" if codex.force_at.as_deref().is_some_and(|f| !f.is_empty()) => {
            return Some(Stage::Draining);
        }
        "restarting" => return Some(Stage::Restarting),
        "pending" => return Some(Stage::Pending),
        _ => {}
    }
    let window = &state.window.shared_codex;
    if window.installing {
        return Some(Stage::Installing);
    }
    let dismissed = dismissed(state);
    if update_available(codex)
        && (window.install_error.is_some() || dismissed.update != codex.latest_version)
    {
        return Some(Stage::Available);
    }
    let profile = &state.facts.profiles.selected;
    (!stale(codex).is_empty() && !old_tiles_dismissed(codex, dismissed.old_tiles.get(profile)))
        .then_some(Stage::OldTiles)
}

fn name_of(state: &State, session: &str) -> (String, bool) {
    state
        .facts
        .sessions
        .iter()
        .find(|s| s.id == session)
        .map_or((session.to_owned(), false), |s| {
            (
                crate::sidebar::model::display_name(s),
                s.hidden == Some(true),
            )
        })
}

fn blocker_reason(blocker: &SharedCodexBlocker) -> &'static str {
    match blocker.reason.as_str() {
        "approval" => "waiting on an approval",
        "question" => "asking a question",
        "starting" => "still starting or asking whether to trust its folder",
        _ => "working",
    }
}

fn blocker_loss(blocker: &SharedCodexBlocker) -> &'static str {
    match blocker.reason.as_str() {
        "approval" => "Waiting for your approval. The request is lost.",
        "question" => "Asking you a question. The question is lost.",
        "starting" => "Still starting. Its tile relaunches.",
        _ => "Turn running. Gets up to 60s to finish, then it is stopped.",
    }
}

fn without_final_period(said: &str) -> &str {
    said.trim_end_matches(['.', ' ', '\n', '\t'])
}

fn countdown(force_at: &str, now_ms: i64) -> String {
    let at = crate::calendar::ms_of_rfc3339(force_at).unwrap_or(now_ms);
    let left = ((at - now_ms).max(0) + 999) / 1000;
    format!("{}:{:02}", left / 60, left % 60)
}

/// Follows the daemon's word: an install error goes once the versions move, a confirm closes
/// once it no longer applies, an install the connection dropped is forgotten, and a
/// force-stop countdown wakes the window each second.
pub(crate) fn poll(state: &mut State, tx: &mut Tx) -> bool {
    let codex = current(state).cloned();
    let versions = codex
        .as_ref()
        .map(|c| (c.installed_version.clone(), c.latest_version.clone()));
    let mut changed = false;
    let window = &mut state.window.shared_codex;
    if window.install_error.is_some() && window.versions != versions {
        window.install_error = None;
        changed = true;
    }
    let applies = match (window.confirm, codex.as_ref()) {
        (Some(Confirm::Restart), Some(c)) => can_restart_now(c),
        (Some(Confirm::Reload), Some(c)) => !stale(c).is_empty(),
        (Some(_), None) => false,
        (None, _) => true,
    };
    if !applies {
        window.confirm = None;
        if state.window.layer == Some(Layer::SharedCodex) {
            state.window.layer = None;
        }
        changed = true;
    }
    if let Some(at) = codex
        .as_ref()
        .filter(|c| c.restart == "restarting")
        .and_then(|c| c.force_at.clone())
        .filter(|f| !f.is_empty())
    {
        let now = state.wall_ms();
        if now / 1000 != state.window.shared_codex.now_ms / 1000 {
            state.window.shared_codex.now_ms = now;
            changed = true;
        }
        let due = crate::calendar::ms_of_rfc3339(&at).unwrap_or(0);
        if now < due {
            let next = 1000 - now.rem_euclid(1000);
            tx.wake_at(tx.clock() + next as f64 / 1000.0);
        }
    }
    changed
}

pub(crate) fn install(state: &mut State, tx: &mut Tx) {
    let Some(desktop_id) = state.facts.current_desktop().map(|d| d.id.clone()) else {
        return;
    };
    let window = &mut state.window.shared_codex;
    if window.installing {
        return;
    }
    window.installing = true;
    window.install_error = None;
    state.ask(
        |request_id| InstallCodexUpdateMessage {
            desktop_id,
            request_id: Some(request_id),
            ..InstallCodexUpdateMessage::new()
        },
        |state, answer, _| {
            let dropped = matches!(answer, crate::requests::Answer::Dropped);
            let error = answer.outcome().err().filter(|_| !dropped);
            let versions =
                current(state).map(|c| (c.installed_version.clone(), c.latest_version.clone()));
            let window = &mut state.window.shared_codex;
            window.installing = false;
            window.versions = versions;
            window.install_error = error;
        },
        tx,
    );
}

fn restart(state: &mut State, now: bool, tx: &mut Tx) {
    close_confirm(state);
    let profile_id = state.facts.profiles.selected.clone();
    if profile_id.is_empty() {
        return;
    }
    state.ask(
        |request_id| RestartSharedCodexMessage {
            profile_id,
            now,
            request_id: Some(request_id),
            ..RestartSharedCodexMessage::new()
        },
        |state, answer, tx| {
            if let Err(why) = answer.outcome() {
                state
                    .window
                    .notify(format!("Couldn't restart shared Codex: {why}"), tx);
            }
        },
        tx,
    );
}

pub(crate) fn retry(state: &mut State, tx: &mut Tx) {
    restart(state, false, tx);
}

pub(crate) fn open_confirm(state: &mut State, confirm: Confirm) {
    state.window.shared_codex.confirm = Some(confirm);
    state.window.layer = Some(Layer::SharedCodex);
}

fn close_confirm(state: &mut State) {
    state.window.shared_codex.confirm = None;
    if state.window.layer == Some(Layer::SharedCodex) {
        state.window.layer = None;
    }
}

fn reload_old_tiles(state: &mut State, tx: &mut Tx) {
    close_confirm(state);
    let sessions = current(state)
        .map(|c| stale(c).to_vec())
        .unwrap_or_default();
    for session in sessions {
        crate::sidebar::session_menu::reload_session(state, &session, tx);
    }
}

fn dismiss_update(state: &mut State) {
    let Some(latest) = current(state).and_then(|c| c.latest_version.clone()) else {
        return;
    };
    let mut dismissed = dismissed(state);
    crate::frame::set_pref_string(UPDATE_PREF, &latest);
    dismissed.update = Some(latest);
    state.window.shared_codex.dismissed = Some(dismissed);
    state.window.shared_codex.install_error = None;
}

fn dismiss_old_tiles(state: &mut State) {
    let Some(codex) = current(state) else {
        return;
    };
    let tiles = OldTiles {
        running: codex.running_version.clone().unwrap_or_default(),
        sessions: stale(codex).to_vec(),
    };
    let mut dismissed = dismissed(state);
    dismissed
        .old_tiles
        .insert(state.facts.profiles.selected.clone(), tiles);
    if let Ok(json) = serde_json::to_string(&dismissed.old_tiles) {
        crate::frame::set_pref_string(OLD_TILES_PREF, &json);
    }
    state.window.shared_codex.dismissed = Some(dismissed);
}

/// The palette's shared Codex commands that apply now.
pub(crate) fn commands(state: &State) -> Vec<crate::palette::commands::Command> {
    use crate::action::Action;
    use crate::palette::commands::Command;
    let Some(codex) = current(state) else {
        return Vec::new();
    };
    let installed = codex.installed_version.clone().unwrap_or_default();
    let latest = codex.latest_version.clone().unwrap_or_default();
    let mut commands = Vec::new();
    if codex.restart == "failed" {
        commands.push(
            Command::new(
                "retry-shared-codex-restart",
                "Retry shared Codex restart",
                &[
                    "codex",
                    "shared",
                    "restart",
                    "retry",
                    "update",
                    "app-server",
                ],
                Action::RetrySharedCodex,
            )
            .describe(&format!("Try starting Codex {installed} again"))
            .pin(),
        );
    }
    if update_available(codex) && !state.window.shared_codex.installing {
        commands.push(
            Command::new(
                "install-codex-update",
                "Install Codex update",
                &["codex", "update", "upgrade", "install", "version"],
                Action::InstallCodexUpdate,
            )
            .describe(&format!("Install Codex {latest} in a terminal tile"))
            .pin(),
        );
    }
    if can_restart_now(codex) {
        commands.push(
            Command::new(
                "restart-shared-codex-now",
                "Restart shared Codex now",
                &["codex", "shared", "restart", "update", "now", "app-server"],
                Action::ConfirmSharedCodexRestart,
            )
            .describe(&format!(
                "Restart on {installed} without waiting for idle agents"
            ))
            .pin(),
        );
    }
    let stale = stale(codex).len();
    if stale > 0 {
        let said = if stale == 1 {
            "1 tile still runs an older Codex".to_owned()
        } else {
            format!("{stale} tiles still run an older Codex")
        };
        commands.push(
            Command::new(
                "reload-old-codex-tiles",
                "Reload old Codex tiles",
                &["codex", "reload", "tiles", "old", "update", "restart"],
                Action::ConfirmReloadCodexTiles,
            )
            .describe(&said)
            .pin(),
        );
    }
    commands
}

/// The blockers the restart waits on, one item each, opening its tile, and a Relaunch for one
/// still starting.
fn waiting_on(state: &State, blockers: &[SharedCodexBlocker], banner: Banner) -> Banner {
    let mut banner = banner.note(format!("{BAND}.waiting"), "Waiting on");
    for (i, blocker) in blockers.iter().enumerate() {
        if i > 0 {
            banner = banner.item();
        }
        let (name, _) = name_of(state, &blocker.session_id);
        let id = blocker.session_id.clone();
        let key = format!("{BAND}.blocker.{id}");
        banner = banner.link(
            format!("{key}.go"),
            name.clone(),
            format!("Go to {name}"),
            move |state: &mut State, tx: &mut Tx| crate::sidebar::open_agent(state, &id, tx),
        );
        let end = if i + 1 < blockers.len() { "," } else { "." };
        banner = banner.note(
            format!("{key}.reason"),
            format!("({}){end}", blocker_reason(blocker)),
        );
        if blocker.reason == "starting" {
            let id = blocker.session_id.clone();
            banner = banner.note_button(
                format!("{key}.relaunch"),
                format!("Relaunch {name}"),
                Button::new("Relaunch"),
                move |state: &mut State, tx: &mut Tx| {
                    crate::sidebar::session_menu::reload_session(state, &id, tx)
                },
            );
        }
    }
    banner
}

pub(crate) fn banner(state: &State, t: &Tokens, cx: &Cx<State>) -> Option<El> {
    let banner = said(state)?.details(crate::shell::opens_details(crate::shell::Band::SharedCodex));
    Some(banner.build(t, cx))
}

/// The message the banner shows: its stage and the versions it names.
pub(crate) fn identity(state: &State) -> Option<String> {
    let codex = current(state)?;
    let stage = stage(state, codex)?;
    Some(format!(
        "{stage:?}:{:?}:{:?}:{:?}",
        codex.installed_version, codex.latest_version, codex.running_version
    ))
}

pub(crate) fn said(state: &State) -> Option<Banner> {
    let codex = current(state)?;
    let stage = stage(state, codex)?;
    let version = |v: &Option<String>| v.clone().unwrap_or_default();
    let (installed, latest, running) = (
        version(&codex.installed_version),
        version(&codex.latest_version),
        version(&codex.running_version),
    );
    let blockers = codex.blockers.clone().unwrap_or_default();
    let install_error = state.window.shared_codex.install_error.as_deref();
    let tone = match stage {
        Stage::Failed => BannerTone::Bad,
        Stage::Available if install_error.is_some() => BannerTone::Bad,
        Stage::Available | Stage::OldTiles => BannerTone::Warn,
        Stage::Draining | Stage::Restarting | Stage::Pending | Stage::Installing => {
            BannerTone::Calm
        }
    };
    let banner = Banner::new(BAND, "Codex updates", tone);
    let says = |said: String| banner.says(format!("{BAND}.text"), said);
    let banner = match stage {
        Stage::Failed => says(format!(
            "Codex {installed} didn’t start: {}. Still on {running}, nothing was interrupted.",
            without_final_period(codex.error.as_deref().unwrap_or_default())
        ))
        .action(format!("{BAND}.retry"), Button::new("Retry"), retry),
        Stage::Draining => {
            let names: Vec<String> = blockers
                .iter()
                .map(|b| name_of(state, &b.session_id).0)
                .collect();
            let waiting = if names.is_empty() {
                "running turns".to_owned()
            } else {
                names.join(", ")
            };
            says(format!(
                "Restarting Codex now: waiting for {waiting} to finish. Force stop in {}.",
                countdown(
                    codex.force_at.as_deref().unwrap_or_default(),
                    state.window.shared_codex.now_ms
                )
            ))
        }
        Stage::Restarting => says(format!(
            "Restarting Codex on {installed}. Tiles reconnect on their own."
        )),
        Stage::Pending => {
            let said = format!("Codex {installed} installed. Restarts when agents are idle.");
            let banner = says(said);
            if blockers.is_empty() {
                banner
            } else {
                waiting_on(state, &blockers, banner).action(
                    format!("{BAND}.restart"),
                    Button::new("Restart now"),
                    |state: &mut State, _: &mut Tx| open_confirm(state, Confirm::Restart),
                )
            }
        }
        Stage::Installing => says(format!("Installing Codex {latest} in a terminal tile…")),
        Stage::Available => {
            let banner = says(format!("Codex {latest} is available."));
            let banner = match install_error {
                Some(e) => banner.note(
                    format!("{BAND}.install-error"),
                    format!("Install failed: {}.", without_final_period(e)),
                ),
                None => banner,
            };
            banner
                .action(format!("{BAND}.install"), Button::new("Install"), install)
                .dismiss(
                    format!("{BAND}.dismiss-update"),
                    "Dismiss Codex update",
                    dismiss_update,
                )
        }
        Stage::OldTiles => {
            let count = stale(codex).len();
            let tiles = if count == 1 {
                "1 tile still runs".to_owned()
            } else {
                format!("{count} tiles still run")
            };
            says(format!(
                "Codex {running} is running. {tiles} an older Codex."
            ))
            .action(
                format!("{BAND}.reload"),
                Button::new("Reload tiles"),
                |state: &mut State, _: &mut Tx| open_confirm(state, Confirm::Reload),
            )
            .dismiss(
                format!("{BAND}.dismiss-old-tiles"),
                "Dismiss old Codex tiles notice",
                dismiss_old_tiles,
            )
        }
    };
    Some(banner)
}

/// The confirm before restarting now or reloading the old tiles.
/// A tile the confirmation takes offline: its name, and what it loses under it.
fn affected(session: &str, name: String, loses: Option<&'static str>, t: &Tokens) -> Affected {
    let key = format!("shared-codex-confirm.item.{session}");
    let label = match loses {
        Some(loses) => format!("{name} · {loses}"),
        None => name.clone(),
    };
    let row = ListRow::new(key.clone(), label).still().wrap().title(name);
    let under = loses
        .map(|loses| {
            Block::custom(
                format!("{key}.loses.block"),
                words(loses, Type::Small, t.text_tertiary)
                    .key(format!("{key}.loses"))
                    .wrap()
                    .a11y_hidden(),
            )
        })
        .into_iter()
        .collect();
    (row, under)
}

/// A tile the restart takes offline, and the notes under it.
type Affected = (ListRow, Vec<Block>);

pub(crate) fn confirm(state: &State, t: &Tokens, layers: &mut Vec<El>) {
    let (Some(confirm), Some(codex)) = (state.window.shared_codex.confirm, current(state)) else {
        return;
    };
    let (key, title, said, items, action): (&str, String, String, Vec<Affected>, String) =
        match confirm {
            Confirm::Restart => {
                let installed = codex.installed_version.clone().unwrap_or_default();
                let items = codex
                    .blockers
                    .iter()
                    .flatten()
                    .map(|b| {
                        let (name, hidden) = name_of(state, &b.session_id);
                        let name = if hidden {
                            format!("{name} (hidden)")
                        } else {
                            name
                        };
                        affected(&b.session_id, name, Some(blocker_loss(b)), t)
                    })
                    .collect();
                (
                    "shared-codex-confirm",
                    "Restart shared Codex now?".to_owned(),
                    format!(
                        "Every Codex tile in this profile goes offline until {installed} is up. What you typed stays."
                    ),
                    items,
                    "Restart now".to_owned(),
                )
            }
            Confirm::Reload => {
                let running = codex.running_version.clone().unwrap_or_default();
                let sessions = stale(codex);
                let one = sessions.len() == 1;
                let items = sessions
                    .iter()
                    .map(|s| affected(s, name_of(state, s).0, None, t))
                    .collect();
                (
                    "shared-codex-confirm",
                    if one {
                        "Reload 1 Codex tile?".to_owned()
                    } else {
                        format!("Reload {} Codex tiles?", sessions.len())
                    },
                    format!(
                        "{} its Codex on {running} and resumes its conversation. Text you typed and haven’t sent in {} is lost.",
                        if one {
                            "The tile restarts"
                        } else {
                            "Each tile restarts"
                        },
                        if one { "it" } else { "them" },
                    ),
                    items,
                    if one { "Reload tile" } else { "Reload tiles" }.to_owned(),
                )
            }
        };
    let close = |state: &mut State, _: &mut Tx| close_confirm(state);
    let cancel = Button::new("Cancel")
        .on_press_tx(close)
        .build(t)
        .key(format!("{key}.cancel"));
    let confirm = Button::new(action)
        .look(Look::Primary)
        .on_press_tx(move |state: &mut State, tx: &mut Tx| match confirm {
            Confirm::Restart => restart(state, true, tx),
            Confirm::Reload => reload_old_tiles(state, tx),
        })
        .build(t)
        .key(format!("{key}.confirm"));
    let listed = !items.is_empty();
    Dialog::new(key, title)
        .lead(said)
        .push_all(listed.then(|| {
            items
                .into_iter()
                .fold(
                    List::new("shared-codex-confirm.list", "Affected tiles").card(),
                    |list, (row, under)| list.row_with(row, under),
                )
                .build(t)
        }))
        .actions(
            Actions::new("shared-codex-confirm.actions")
                .then_end()
                .push(El::from(cancel).autofocus())
                .push(confirm),
        )
        .on_close_tx(close)
        .build(t, layers);
}
