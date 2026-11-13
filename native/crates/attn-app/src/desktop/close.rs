use attn_protocol::{Desktop, DesktopCloseMessage};
use attn_ui::Tokens;
use rune::tok::{space, type_};
use rune::{El, Tx, col, el, row, text};

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

pub(crate) fn view(state: &State, t: &Tokens) -> Option<El> {
    let closing = state.window.closing_desktop.as_ref()?;
    let title = format!("Close {}?", closing.title);
    let button = |key: &str, label: &str| {
        text(label.to_owned())
            .key(format!("close_desktop.{key}"))
            .role("button")
            .label(label)
            .focusable(true)
            .font(type_::BODY)
            .px(12.0)
            .py(6.0)
            .radius(6.0)
            .border(1.0, t.border)
    };
    let panel = col()
        .key("close_desktop.dialog")
        .role("dialog")
        .label(title.clone())
        .modal_tx(true, cancel)
        .w_full()
        .max_w(420.0)
        .radius(8.0)
        .fill(t.bg_elevated)
        .border(1.0, t.border)
        .padding(space::S6)
        .gap(space::S3)
        .child(text(title).font(type_::TITLE).color(t.text_primary))
        .child(
            text("Close this desktop and everything inside it?")
                .font(type_::BODY)
                .color(t.text_secondary),
        )
        .child(
            row()
                .w_full()
                .gap(space::S2)
                .child(el().grow(1.0))
                .child(
                    button("cancel", "Cancel")
                        .autofocus()
                        .color(t.text_primary)
                        .fill(t.bg_button)
                        .on_press_tx(cancel),
                )
                .child(
                    button("confirm", "Close desktop")
                        .color(t.text_primary)
                        .fill(t.accent)
                        .on_press_tx(confirm),
                ),
        );
    Some(
        el().key("close_desktop")
            .absolute(0.0, 0.0)
            .w_full()
            .h_full()
            .child(
                el().key("close_desktop.scrim")
                    .absolute(0.0, 0.0)
                    .w_full()
                    .h_full()
                    .fill(t.bg_overlay)
                    .a11y_hidden(),
            )
            .child(
                col()
                    .w_full()
                    .h_full()
                    .padding(space::S6)
                    .center()
                    .child(panel),
            ),
    )
}
