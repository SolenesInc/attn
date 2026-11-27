//! Settings → Updates: how this attn was installed, the build it runs, the release the daemon
//! last found, and Update attn for an install the daemon can update.

use std::collections::BTreeMap;

use attn_protocol::{StartUpdateMessage, UpdateRun};
use attn_ui::Tokens;
use attn_ui::blocks::{Actions, Block, Facts, Notice};
use attn_ui::system::{Button, Look, Said};
use rune::Tx;

use crate::State;
use crate::update_run;

const UPDATABLE_KINDS: &[&str] = &["release_app", "release_binary", "source", "named_instance"];

/// Why the last Update attn did not start.
#[derive(Default)]
pub(crate) struct Updates {
    error: Option<String>,
}

/// Forgets the last refusal when the page comes into view.
pub(super) fn arrive(state: &mut State) {
    if super::is_open(state) && state.window.settings.section == super::Section::Updates {
        state.window.settings.updates.error = None;
    }
}

fn install_label(kind: &str) -> Said {
    match kind {
        "release_app" => "Release app".into(),
        "release_binary" => "Release binary".into(),
        "source" => "Built from source".into(),
        "named_instance" => "Named instance".into(),
        "outpost" => "Outpost".into(),
        "unknown" => "Unknown".into(),
        other => other.to_owned().into(),
    }
}

fn availability(settings: &BTreeMap<String, String>) -> String {
    let set = |key: &str| settings.get(key).filter(|v| !v.is_empty());
    if let Some(version) = set("update_available_version") {
        return version.clone();
    }
    if let Some(why) = set("update_check_error") {
        return format!("Unknown. The last check failed: {why}");
    }
    if set("update_checked_at").is_some() {
        return "Up to date".into();
    }
    if set("update_install_kind").map(String::as_str) == Some("source") {
        "Source installs do not check for releases".into()
    } else {
        "Not checked".into()
    }
}

/// Whether the daemon can update this install of attn in place.
pub(crate) fn updatable(settings: &BTreeMap<String, String>) -> bool {
    settings
        .get("update_install_kind")
        .is_some_and(|kind| UPDATABLE_KINDS.contains(&kind.as_str()))
}

fn running(state: &State) -> bool {
    state
        .facts
        .update_run
        .as_ref()
        .as_ref()
        .is_some_and(|run| !matches!(run.phase.as_str(), "done" | "failed"))
}

fn start(state: &mut State, tx: &mut Tx) {
    state.window.settings.updates.error = None;
    state.ask(
        |request_id| StartUpdateMessage {
            request_id: Some(request_id),
            ..StartUpdateMessage::new()
        },
        |state, answer, _tx: &mut Tx| {
            state.window.settings.updates.error = answer.outcome().err();
        },
        tx,
    );
}

/// The rows a run this app machine keeps for its home: the home and this Mac, each with its
/// build or why it is not on the run's, and the one action the run offers.
fn machines(run: &UpdateRun, t: &Tokens) -> Vec<Block> {
    let Some(says) = update_run::home_says(run) else {
        return Vec::new();
    };
    let mut facts = Facts::new("settings.updates.machines");
    for row in &run.outposts {
        let label = match row.role.as_deref() {
            Some("app_machine") => "This Mac".to_owned(),
            Some("home") => format!("{} · home", row.name),
            _ => row.name.clone(),
        };
        let build = row.build.as_deref().unwrap_or_default();
        let status = match (&row.reason, row.detail.as_deref().filter(|d| !d.is_empty())) {
            (Some(reason), Some(detail)) => format!("{}: {detail}", reason.replace('_', " ")),
            (Some(reason), None) => reason.replace('_', " "),
            (None, Some(detail)) if row.state != "done" => detail.to_owned(),
            _ => build.split(" (").next().unwrap_or(build).to_owned(),
        };
        facts = facts.fact(
            format!("settings.updates.machine.{}", row.id),
            label,
            status,
        );
    }
    let mut blocks = vec![facts.build(t)];
    if let Some(action) = says.action {
        blocks.push(
            Actions::new("settings.updates.machine-actions")
                .push(
                    update_run::action_button(&action)
                        .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                            update_run::run_action(&action, state, tx)
                        })
                        .build(t)
                        .key("settings.updates.machine-action"),
                )
                .build(),
        );
    }
    blocks
}

pub(super) fn page(state: &State, t: &Tokens) -> Vec<Block> {
    let settings = &state.facts.settings;
    let get = |key: &str| settings.get(key).map(String::as_str).unwrap_or_default();
    let kind = get("update_install_kind");
    let commit: String = get("update_current_commit").chars().take(9).collect();
    let mut page = vec![
        Facts::new("settings.updates")
            .fact(
                "settings.updates.install",
                "Installed as",
                install_label(kind),
            )
            .code(
                "settings.updates.build",
                "Build",
                format!("{} ({commit})", get("update_current_version")),
            )
            .fact(
                "settings.updates.available",
                "Available",
                availability(settings),
            )
            .build(t),
    ];
    if let Some(run) = state.facts.update_run.as_ref().as_ref() {
        page.extend(machines(run, t));
    }
    if updatable(settings) {
        let running = running(state);
        page.push(
            Actions::new("settings.updates.actions")
                .push(
                    Button::new(if running {
                        "Updating…"
                    } else {
                        "Update attn"
                    })
                    .look(Look::Primary)
                    .disabled(running)
                    .on_press_tx(start)
                    .build(t)
                    .key("settings.updates.start"),
                )
                .build(),
        );
        if let Some(why) = &state.window.settings.updates.error {
            page.push(Notice::error("settings.updates.error", why.clone()).build(t));
        }
    }
    page
}
