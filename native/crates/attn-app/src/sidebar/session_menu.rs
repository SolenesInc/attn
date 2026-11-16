use attn_protocol::{ReloadSessionMessage, Session, UnregisterMessage};
use attn_ui::Tokens;
use rune::tok::type_;
use rune::{El, Tx, text};
use rune_kit::{MenuItem, Placement};

use crate::State;
use crate::requests::Refused;

pub const MENU: &str = "session.menu";

/// An open actions menu: whose, and the button it opened under.
pub struct Menu {
    pub session: String,
    pub name: String,
    pub under: String,
}

pub(crate) fn open(state: &mut State, session: &str, name: String, under: String, tx: &mut Tx) {
    let first = if chief_session(state, session).is_some() {
        "route"
    } else if member_of(state, session).is_some() {
        "member-details"
    } else {
        "priority"
    };
    tx.focus(format!("{MENU}.{first}"), true);
    state.window.session_menu = Some(Menu {
        session: session.to_owned(),
        name,
        under,
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

/// The Chief whose slot shows `session`.
fn chief_session(state: &State, session: &str) -> Option<crate::chief::View> {
    crate::chief::view(state).filter(|c| {
        c.session.as_deref() == Some(session) || member_of(state, session) == Some(c.key.clone())
    })
}

fn member_of(state: &State, session: &str) -> Option<String> {
    state
        .facts
        .sessions
        .iter()
        .find(|s| s.id == session)?
        .crew_member
        .clone()
        .filter(|m| !m.is_empty())
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
    if let Some(menu) = state.window.session_menu.take() {
        super::rename::session(state, &menu.session, menu.under, tx);
    }
}

/// A reload with no terminal of a usable size asks for this one, as the Tauri app does, since
/// the daemon needs a size to restart an agent that has no live worker.
const FALLBACK_SIZE: (u16, u16) = (80, 24);
const MIN_COLS: u16 = 20;
const MIN_ROWS: u16 = 8;

fn reload(state: &mut State, tx: &mut Tx) {
    if let Some(session) = target(state) {
        reload_session(state, &session.id, tx);
    }
}

pub(crate) fn reload_session(state: &mut State, session: &str, tx: &mut Tx) {
    if crate::outpost::refuse_unless_recoverable(state, session, tx) {
        return;
    }
    if let Some(why) = crate::outpost::unreachable(state, session) {
        state
            .window
            .notify(format!("Failed to reload session: {why}"), tx);
        return;
    }
    let (cols, rows) = state
        .terminals
        .size(session)
        .filter(|&(cols, rows)| cols >= MIN_COLS && rows >= MIN_ROWS)
        .unwrap_or(FALLBACK_SIZE);
    let reload = ReloadSessionMessage {
        id: session.into(),
        cols: i64::from(cols),
        rows: i64::from(rows),
        ..ReloadSessionMessage::new()
    };
    state.act_as(
        session.into(),
        &reload,
        Refused::Prefixed("Failed to reload session"),
        |_, _, _| {},
        tx,
    );
}

/// Ends the agent's session, as the Tauri app's Close session does: a crew member is
/// protected and only says so, and the queue moves on from an agent on screen.
fn close(state: &mut State, tx: &mut Tx) {
    let Some(session) = target(state) else {
        return;
    };
    if crate::outpost::refuse_unless_recoverable(state, &session.id, tx) {
        return;
    }
    if let Some(member) = session.crew_member.as_deref().filter(|m| !m.is_empty()) {
        let name = session.crew_member_name.as_deref().unwrap_or(member);
        state.window.notify(
            format!("{name} is protected — ask {name} to close to end its session."),
            tx,
        );
        return;
    }
    let sent = state.daemon.send(&UnregisterMessage {
        id: session.id.clone(),
        ..UnregisterMessage::new()
    });
    if sent.is_err() {
        state.window.notify(
            "Could not close that session: not connected to the daemon".into(),
            tx,
        );
        return;
    }
    super::actions::closing(state, &session.id, tx);
}

pub(crate) fn view(state: &State, t: &Tokens) -> Vec<El> {
    state
        .window
        .session_menu
        .as_ref()
        .map_or_else(Vec::new, |menu| actions(state, menu, t))
}

fn actions(state: &State, menu: &Menu, t: &Tokens) -> Vec<El> {
    let session = state.facts.sessions.iter().find(|s| s.id == menu.session);
    let priority = session.is_some_and(|s| s.priority == Some(true));
    let chief = session.is_some_and(|s| s.chief_of_staff == Some(true));
    let item = |id: &str, glyph: &str, label: &str| {
        MenuItem::new(label).key(format!("{MENU}.{id}")).lead(
            text(glyph.to_owned())
                .font(type_::LABEL)
                .color(t.text_tertiary)
                .w(14.0),
        )
    };
    let id = menu.session.clone();
    let priority_label = if priority {
        "Unmark priority"
    } else {
        "Mark priority"
    };
    let chief_slot = chief_session(state, &menu.session);
    let member = chief_slot
        .as_ref()
        .map(|c| c.key.clone())
        .or_else(|| member_of(state, &menu.session));
    let mut kit_menu = rune_kit::Menu::new(MENU)
        .standard()
        .anchor(menu.under.clone())
        .placement(Placement::BottomEnd)
        .gutter(4.0)
        .min_width(200.0)
        .label(format!("Actions for {}", menu.name))
        .open(true)
        .on_close(|state: &mut State| state.window.session_menu = None);
    if chief_slot.is_some() {
        kit_menu = kit_menu.item(
            MenuItem::new("Harness and model…")
                .key(format!("{MENU}.route"))
                .on_select_tx(|state: &mut State, tx: &mut Tx| {
                    state.window.session_menu = None;
                    crate::chief::edit(state, tx);
                }),
        );
    }
    if let Some(member) = member.clone() {
        let is_chief = chief_slot.is_some();
        let label = if is_chief {
            "Edit charter"
        } else {
            "Member details"
        };
        kit_menu = kit_menu
            .item(
                item("member-details", attn_ui::icon::BOLT, label).on_select_tx(
                    move |state: &mut State, tx: &mut Tx| {
                        state.window.session_menu = None;
                        if is_chief {
                            super::crew::charter(state, &member, tx);
                        } else {
                            super::crew::details(state, &member, tx);
                        }
                    },
                ),
            )
            .separator();
    }
    kit_menu = kit_menu
        .item(
            item("priority", attn_ui::icon::FLAG, priority_label).on_select(
                move |state: &mut State| {
                    state.window.session_menu = None;
                    super::actions::set_priority(state, &id, !priority);
                },
            ),
        )
        .item(item("rename", attn_ui::icon::PENCIL, "Rename session").on_select_tx(rename))
        .separator()
        .item(item("reload", "↻", "Reload session").on_select_tx(reload));
    if let Some(slot) = &chief_slot {
        kit_menu = kit_menu.item(
            item(
                "ask-close",
                attn_ui::icon::MOON,
                &format!("Ask {} to close", slot.name),
            )
            .on_select_tx(|state: &mut State, tx: &mut Tx| {
                state.window.session_menu = None;
                crate::chief::close(state, tx);
            }),
        );
    } else if !chief {
        kit_menu = kit_menu.item(item("close", "×", "Close session").on_select_tx(close));
    }
    let mut layer = Vec::new();
    kit_menu.build(attn_ui::kit(t), &mut layer);
    layer
}
