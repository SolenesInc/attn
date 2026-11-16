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
    tx.focus(format!("{MENU}.priority"), true);
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
    let mut kit_menu = rune_kit::Menu::new(MENU)
        .standard()
        .anchor(menu.under.clone())
        .placement(Placement::BottomEnd)
        .gutter(4.0)
        .min_width(200.0)
        .label(format!("Actions for {}", menu.name))
        .open(true)
        .on_close(|state: &mut State| state.window.session_menu = None)
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
    if !chief {
        kit_menu = kit_menu.item(item("close", "×", "Close session").on_select_tx(close));
    }
    let mut layer = Vec::new();
    kit_menu.build(attn_ui::kit(t), &mut layer);
    layer
}
