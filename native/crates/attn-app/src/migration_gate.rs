//! The screen a home still moving from workspaces to profiles opens on. Only the Tauri app's
//! picker finishes the move, so this screen sends the user there and gives way once the daemon
//! says the move is done.

use attn_client::PendingMigration;
use attn_ui::Tokens;
use rune::tok::{space, type_};
use rune::{El, col, text};

use crate::{Action, State};

pub(crate) fn holds(state: &State) -> bool {
    state.facts.pending_migration.is_some()
}

pub(crate) fn lets_through(action: &Action) -> bool {
    matches!(
        action,
        Action::UiIncreaseFontSize | Action::UiDecreaseFontSize | Action::UiResetFontSize
    )
}

pub(crate) fn screen(pending: PendingMigration, t: &Tokens) -> El {
    let (key, step) = match pending {
        PendingMigration::Placement => (
            "migration-gate.placement",
            "This home still keeps its agents in workspaces. Place each workspace's sessions on desktops in the Tauri attn app.",
        ),
        PendingMigration::Launch => (
            "migration-gate.launch",
            "This home has moved its workspaces onto desktops. Choose which desktops launch in the Tauri attn app.",
        ),
    };
    col()
        .key("migration-gate")
        .role("main")
        .label("Finish moving to profiles")
        .max_w(560.0)
        .gap(space::S3)
        .padding(space::S6)
        .child(
            text("Finish moving to profiles")
                .key("migration-gate.title")
                .role("heading")
                .level(1)
                .font(type_::TITLE)
                .color(t.text_primary),
        )
        .child(
            text(step)
                .key(key)
                .font(type_::BODY)
                .color(t.text_secondary)
                .wrap(),
        )
        .child(
            text("This window opens your agents as soon as you finish there.")
                .font(type_::BODY)
                .color(t.text_secondary)
                .wrap(),
        )
}
