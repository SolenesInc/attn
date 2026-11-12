use attn_protocol::{ReloadSessionMessage, RenameSessionMessage, Session, UnregisterMessage};
use attn_ui::Tokens;
use rune::tok::{space, type_};
use rune::{El, Side, TextEdit, Tx, col, row, text, text_input};

use crate::State;

pub const MENU: &str = "session.menu";
pub const RENAME_FIELD: &str = "session.rename.field";

/// An open actions menu: whose, the button it opened under, and, once the user chose
/// Rename session, the name being typed.
pub struct Menu {
    pub session: String,
    pub name: String,
    pub under: String,
    pub rename: Option<TextEdit>,
    /// Why the typed name was refused.
    pub refused: Option<&'static str>,
}

pub(crate) fn open(state: &mut State, session: &str, name: String, under: String, tx: &mut Tx) {
    tx.focus(format!("{MENU}.priority"), true);
    state.window.session_menu = Some(Menu {
        session: session.to_owned(),
        name,
        under,
        rename: None,
        refused: None,
    });
}

/// Closes the menu once its agent is gone.
pub(crate) fn follow(state: &mut State) {
    let gone = state
        .window
        .session_menu
        .as_ref()
        .is_some_and(|menu| !state.facts.sessions.iter().any(|s| s.id == menu.session));
    if gone {
        state.window.session_menu = None;
    }
}

fn target(state: &mut State) -> Option<Session> {
    let menu = state.window.session_menu.take()?;
    state
        .facts
        .sessions
        .iter()
        .find(|s| s.id == menu.session)
        .cloned()
}

fn rename(state: &mut State, tx: &mut Tx) {
    let Some(menu) = &mut state.window.session_menu else {
        return;
    };
    let current = state
        .facts
        .sessions
        .iter()
        .find(|s| s.id == menu.session)
        .map_or_else(|| menu.name.clone(), |s| s.label.clone());
    let mut edit = TextEdit::new(current);
    edit.anchor = 0;
    menu.rename = Some(edit);
    tx.focus(RENAME_FIELD, true);
}

/// Sends the typed name: an unchanged one only closes, and only a shell may go without one.
fn submit_rename(state: &mut State) {
    let Some(menu) = &mut state.window.session_menu else {
        return;
    };
    let label = menu
        .rename
        .as_ref()
        .map(|edit| edit.text.trim().to_owned())
        .unwrap_or_default();
    let session = state.facts.sessions.iter().find(|s| s.id == menu.session);
    if label.is_empty() && session.is_some_and(|s| s.agent != "shell") {
        menu.refused =
            Some("An agent name cannot be empty; only shell terminals have automatic titles");
        return;
    }
    let unchanged = session.is_some_and(|s| s.label.trim() == label);
    let Some(menu) = state.window.session_menu.take() else {
        return;
    };
    if !unchanged {
        let _ = state.daemon.send(&RenameSessionMessage {
            session_id: menu.session,
            label,
            ..RenameSessionMessage::new()
        });
    }
}

/// A reload with no terminal of a usable size asks for this one, as the Tauri app does, since
/// the daemon needs a size to restart an agent that has no live worker.
const FALLBACK_SIZE: (u16, u16) = (80, 24);
const MIN_COLS: u16 = 20;
const MIN_ROWS: u16 = 8;

fn reload(state: &mut State) {
    let Some(session) = target(state) else {
        return;
    };
    let (cols, rows) = state
        .terminals
        .size(&session.id)
        .filter(|&(cols, rows)| cols >= MIN_COLS && rows >= MIN_ROWS)
        .unwrap_or(FALLBACK_SIZE);
    let _ = state.daemon.send(&ReloadSessionMessage {
        id: session.id,
        cols: i64::from(cols),
        rows: i64::from(rows),
        ..ReloadSessionMessage::new()
    });
}

/// Ends the agent's session, as the Tauri app's Close session does: a crew member is
/// protected and only says so, and the queue moves on from an agent on screen.
fn close(state: &mut State, tx: &mut Tx) {
    let Some(session) = target(state) else {
        return;
    };
    if let Some(member) = session.crew_member.as_deref().filter(|m| !m.is_empty()) {
        let name = session.crew_member_name.as_deref().unwrap_or(member);
        state.window.notify(
            format!("{name} is protected — ask {name} to close to end its session."),
            tx,
        );
        return;
    }
    let shown = state.window.on_screen(&state.facts).as_deref() == Some(session.id.as_str());
    let _ = state.daemon.send(&UnregisterMessage {
        id: session.id.clone(),
        ..UnregisterMessage::new()
    });
    if shown && session.agent != "shell" {
        super::actions::move_on_from(state, &session.id, tx);
    }
}

pub(crate) fn view(state: &State, t: &Tokens) -> Option<El> {
    let menu = state.window.session_menu.as_ref()?;
    let surface = match &menu.rename {
        Some(edit) => rename_popover(edit, menu.refused, t),
        None => actions(state, menu, t),
    };
    Some(
        surface
            .anchored(menu.under.clone(), Side::Bottom, rune::Align::End, 4.0)
            .focus_return(menu.under.clone()),
    )
}

fn actions(state: &State, menu: &Menu, t: &Tokens) -> El {
    let session = state.facts.sessions.iter().find(|s| s.id == menu.session);
    let priority = session.is_some_and(|s| s.priority == Some(true));
    let chief = session.is_some_and(|s| s.chief_of_staff == Some(true));
    let item = |id: &str, glyph: &str, label: String| {
        row()
            .key(format!("{MENU}.{id}"))
            .role("menuitem")
            .label(label.clone())
            .focusable(true)
            .w_full()
            .h(26.0)
            .px(space::S2)
            .gap(space::S2)
            .items_center()
            .radius(4.0)
            .hover(t.bg_button)
            .focus_highlight(t.bg_selected)
            .child(
                text(glyph.to_owned())
                    .font(type_::LABEL)
                    .color(t.text_tertiary)
                    .w(14.0),
            )
            .child(text(label).font(type_::BODY).color(t.text_primary))
    };
    let divider = || rune::el().w_full().h(1.0).my(2.0).fill(t.border_subtle);
    let id = menu.session.clone();
    col()
        .key(MENU)
        .role("menu")
        .label(format!("Actions for {}", menu.name))
        .w(200.0)
        .padding(space::S1)
        .gap(2.0)
        .radius(6.0)
        .fill(t.bg_elevated)
        .border(1.0, t.border)
        .focus_group()
        .modal_tx(true, |state: &mut State, _: &mut Tx| {
            state.window.session_menu = None
        })
        .child(
            item(
                "priority",
                attn_ui::icon::FLAG,
                if priority {
                    "Unmark priority"
                } else {
                    "Mark priority"
                }
                .into(),
            )
            .on_press(move |state: &mut State| {
                state.window.session_menu = None;
                super::actions::set_priority(state, &id, !priority);
            }),
        )
        .child(item("rename", attn_ui::icon::PENCIL, "Rename session".into()).on_press_tx(rename))
        .child(divider())
        .child(
            item("reload", "↻", "Reload session".into())
                .on_press(|state: &mut State| reload(state)),
        )
        .children((!chief).then(|| item("close", "×", "Close session".into()).on_press_tx(close)))
}

fn rename_popover(edit: &TextEdit, refused: Option<&'static str>, t: &Tokens) -> El {
    col()
        .key("session.rename")
        .role("dialog")
        .label("Rename session")
        .w(240.0)
        .padding(space::S2)
        .gap(space::S1)
        .radius(6.0)
        .fill(t.bg_elevated)
        .border(1.0, t.border)
        .modal_tx(true, |state: &mut State, _: &mut Tx| {
            state.window.session_menu = None
        })
        .child(
            text_input(edit, type_::BODY)
                .key(RENAME_FIELD)
                .single_line()
                .label("Rename session")
                .on_edit(|state: &mut State| {
                    state
                        .window
                        .session_menu
                        .as_mut()
                        .and_then(|m| m.rename.as_mut())
                        .expect("the rename field shows only while renaming")
                })
                .on_submit(|state: &mut State, _: &mut Tx| submit_rename(state))
                .w_full()
                .color(t.text_primary),
        )
        .children(refused.map(|why| {
            text(why)
                .key("session.rename.refused")
                .font(type_::LABEL)
                .color(t.error)
        }))
}
