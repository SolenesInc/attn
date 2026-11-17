use attn_protocol::{Desktop, DesktopCloseMessage};
use attn_ui::{Dialog, DialogFrame, Tokens};
use rune::{El, Tx, row};
use rune_kit::{Button, ButtonVariant};

use super::layout::Node;
use crate::State;
use crate::requests::Refused;
use crate::shell::Layer;
use crate::sidebar::model;

/// The desktop the close confirmation asks about, as it was when asked.
#[derive(Clone, Debug)]
pub(crate) struct Closing {
    desktop_id: String,
    revision: i64,
    title: String,
}

/// Closes `desktop_id` as the Tauri app does: a desktop holding a crew member's session
/// refuses and names them, an empty one closes at once, and one with tiles asks first.
pub(crate) fn request(state: &mut State, desktop_id: &str, tx: &mut Tx) {
    let Some(desktop) = state.facts.desktops.iter().find(|d| d.id == desktop_id) else {
        return;
    };
    let protected: Vec<String> = desktop
        .panes
        .iter()
        .filter_map(|pane| {
            let session = state
                .facts
                .sessions
                .iter()
                .find(|s| s.id == pane.session_id)?;
            session
                .crew_member
                .as_deref()
                .is_some_and(|m| !m.is_empty())
                .then(|| model::display_name(session))
        })
        .collect();
    let sessions: Vec<String> = desktop.panes.iter().map(|p| p.session_id.clone()).collect();
    if sessions
        .iter()
        .any(|s| crate::outpost::refuse_unless_recoverable(state, s, tx))
    {
        return;
    }
    let Some(desktop) = state.facts.desktops.iter().find(|d| d.id == desktop_id) else {
        return;
    };
    if !protected.is_empty() {
        let names = protected.join(", ");
        state.window.notify(
            format!("Cannot close desktop: move these protected sessions first: {names}."),
            tx,
        );
        return;
    }
    let holds_leaves = Node::parse(&desktop.tree_json).is_some();
    let desktop = desktop.clone();
    if !holds_leaves {
        send(state, desktop.id, desktop.revision, tx);
        return;
    }
    let profile: Vec<Desktop> = state
        .facts
        .desktops
        .iter()
        .filter(|d| d.profile_id == desktop.profile_id)
        .cloned()
        .collect();
    state.window.closing_desktop = Some(Closing {
        title: model::desktop_title(&desktop, &profile),
        desktop_id: desktop.id,
        revision: desktop.revision,
    });
    state.window.layer = Some(Layer::CloseDesktop);
}

fn send(state: &mut State, desktop_id: String, expected_revision: i64, tx: &mut Tx) {
    state.act(
        |request_id| DesktopCloseMessage {
            request_id,
            desktop_id,
            expected_revision,
            ..DesktopCloseMessage::new()
        },
        Refused::Bare,
        |_, _, _| {},
        tx,
    );
}

fn cancel(state: &mut State, tx: &mut Tx) {
    state.window.layer = None;
    state.window.closing_desktop = None;
    crate::picker::refocus_inline(state, tx);
}

fn confirm(state: &mut State, tx: &mut Tx) {
    let Some(closing) = state.window.closing_desktop.take() else {
        return;
    };
    cancel(state, tx);
    send(state, closing.desktop_id, closing.revision, tx);
}

pub(crate) fn view(state: &State, t: &Tokens) -> Vec<El> {
    let Some(closing) = state.window.closing_desktop.as_ref() else {
        return Vec::new();
    };
    let k = attn_ui::kit(t);
    let mut layer = Vec::new();
    Dialog::new(
        "close_desktop",
        format!("Close {}?", closing.title),
        DialogFrame::prompt(t),
    )
    .subtitle("Close this desktop and everything inside it?")
    .on_close_tx(cancel)
    .width(620.0)
    .child(
        row()
            .w_full()
            .gap(10.0)
            .px(24.0)
            .pb(22.0)
            .justify_end()
            .child(
                Button::new("Cancel")
                    .on_press_tx(cancel)
                    .build(k)
                    .key("close_desktop.cancel")
                    .h(PROMPT_BUTTON_HEIGHT)
                    .px(12.0)
                    .autofocus(),
            )
            .child(
                Button::new("Close desktop")
                    .variant(ButtonVariant::Submit)
                    .on_press_tx(confirm)
                    .build(k)
                    .key("close_desktop.confirm")
                    .h(PROMPT_BUTTON_HEIGHT)
                    .px(12.0),
            ),
    )
    .build(t, &mut layer);
    layer
}

/// `.mp-button`: 7 points above and below 13 point text, inside a 1 point edge.
const PROMPT_BUTTON_HEIGHT: f32 = 31.0;
