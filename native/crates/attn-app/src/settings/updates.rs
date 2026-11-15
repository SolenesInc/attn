//! Settings → Updates: how this attn was installed, the build it runs, the release the daemon
//! last found, and Update attn for an install the daemon can update.

use std::collections::BTreeMap;

use attn_protocol::StartUpdateMessage;
use attn_ui::Tokens;
use rune::tok::{space, type_};
use rune::{El, Tx, col, row, text};
use rune_kit::Button;

use super::pages::CAPTION;
use crate::State;

const UPDATABLE_KINDS: &[&str] = &["release_app", "release_binary", "source", "named_instance"];
const LABEL_W: f32 = 110.0;

/// Why the last Update attn did not start.
#[derive(Default)]
pub(crate) struct Updates {
    error: Option<String>,
}

/// Forgets the last refusal when the page comes into view, as the Tauri page starts afresh.
pub(super) fn arrive(state: &mut State) {
    if super::is_open(state) && state.window.settings.section == super::Section::Updates {
        state.window.settings.updates.error = None;
    }
}

fn install_label(kind: &str) -> &str {
    match kind {
        "release_app" => "Release app",
        "release_binary" => "Release binary",
        "source" => "Built from source",
        "named_instance" => "Named instance",
        "outpost" => "Outpost",
        "unknown" => "Unknown",
        other => other,
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

pub(super) fn page(state: &State, t: &Tokens) -> Vec<El> {
    let settings = &state.facts.settings;
    let get = |key: &str| settings.get(key).map(String::as_str).unwrap_or_default();
    let kind = get("update_install_kind");
    let commit: String = get("update_current_commit").chars().take(9).collect();
    let facts = [
        ("install", "Installed as", install_label(kind).to_owned()),
        (
            "build",
            "Build",
            format!("{} ({commit})", get("update_current_version")),
        ),
        ("available", "Available", availability(settings)),
    ];
    let mut page =
        vec![
            col()
                .key("settings.updates")
                .gap(space::S2)
                .children(facts.into_iter().map(|(key, label, value)| {
                    row()
                        .gap(space::S3)
                        .child(text(label).font(CAPTION).color(t.text_tertiary).w(LABEL_W))
                        .child(
                            text(value.clone())
                                .key(format!("settings.updates.{key}"))
                                .label(format!("{label} {value}"))
                                .font(type_::BODY)
                                .color(t.text_primary)
                                .wrap()
                                .grow(1.0)
                                .min_w(0.0),
                        )
                })),
        ];
    if updatable(settings) {
        page.push(action(state, t));
    }
    page
}

fn action(state: &State, t: &Tokens) -> El {
    let running = running(state);
    let error = state.window.settings.updates.error.as_ref().map(|why| {
        text(why.clone())
            .key("settings.updates.error")
            .role("alert")
            .label(why.clone())
            .font(CAPTION)
            .color(t.error)
            .wrap()
    });
    col()
        .gap(space::S2)
        .pt(space::S2)
        .child(
            row().child(
                Button::new(if running {
                    "Updating…"
                } else {
                    "Update attn"
                })
                .disabled(running)
                .on_press_tx(start)
                .build(attn_ui::kit(t))
                .key("settings.updates.start"),
            ),
        )
        .children(error)
}
