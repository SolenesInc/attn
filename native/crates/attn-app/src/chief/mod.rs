//! The Chief's slot at the top of the crew: the phase its member is in, the setup that picks
//! its harness, model and effort, and Start and Ask to close with the request each has on its
//! way.

pub(crate) mod setup;

use attn_protocol::{
    CrewCloseMessage, CrewMember, CrewSetMessage, CrewSetResultMessage, CrewStartMessage,
    CrewStartResultMessage,
};
use rune::Tx;

use crate::crew::member_ref;
use crate::requests::Answer;
use crate::settings::Route;
use crate::{Action, State};

#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash)]
pub(crate) enum Phase {
    Unconfigured,
    Configuring,
    Starting,
    InSession,
    Closing,
    BetweenSessions,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
enum Pending {
    Configuring,
    Starting,
    Closing,
}

/// What the window holds of the Chief: the request on its way, the setup's last refusal,
/// whether the user opened or dismissed the setup, and the setup's draft while it shows.
#[derive(Default)]
pub(crate) struct Chief {
    pending: Option<(String, Pending)>,
    error: Option<(String, String)>,
    editor: Option<(String, bool)>,
    automatic: Option<String>,
    pub(crate) draft: Option<setup::Draft>,
    seen: Option<(String, Phase)>,
}

/// The Chief as its slot shows it.
#[derive(Clone, Debug, PartialEq, Eq, Hash)]
pub(crate) struct View {
    pub(crate) key: String,
    pub(crate) name: String,
    pub(crate) agent: Option<String>,
    pub(crate) phase: Phase,
    pub(crate) busy: bool,
    pub(crate) session: Option<String>,
}

/// The selected profile's Chief.
pub(crate) fn member(state: &State) -> Option<&CrewMember> {
    let profile = &state.facts.profiles.selected;
    state
        .facts
        .crew
        .iter()
        .find(|m| m.chief == Some(true) && &m.profile_id == profile)
}

fn configured(member: &CrewMember) -> bool {
    member.agent.as_deref().is_some_and(|a| !a.is_empty())
}

fn pending(state: &State, key: &str) -> Option<Pending> {
    state
        .window
        .chief
        .pending
        .as_ref()
        .filter(|(k, _)| k == key)
        .map(|(_, p)| *p)
}

pub(crate) fn view(state: &State) -> Option<View> {
    let member = member(state)?;
    let pending = pending(state, &member.id);
    let session = member.binding_session.clone().filter(|s| !s.is_empty());
    let phase = match (pending, &session) {
        (Some(Pending::Configuring), _) => Phase::Configuring,
        (Some(Pending::Starting), _) => Phase::Starting,
        (Some(Pending::Closing), _) => Phase::Closing,
        _ if !configured(member) => Phase::Unconfigured,
        (None, None) => Phase::BetweenSessions,
        (None, Some(id)) if state.facts.sessions.iter().any(|s| &s.id == id) => Phase::InSession,
        (None, Some(_)) => Phase::Starting,
    };
    Some(View {
        key: member.id.clone(),
        name: member.name.clone(),
        agent: member.agent.clone().filter(|a| !a.is_empty()),
        phase,
        busy: pending.is_some(),
        session,
    })
}

fn is_current(state: &State, key: &str) -> bool {
    member(state).is_some_and(|m| m.id == key)
}

fn settle(state: &mut State, key: &str) {
    if state
        .window
        .chief
        .pending
        .as_ref()
        .is_some_and(|(k, _)| k == key)
    {
        state.window.chief.pending = None;
    }
}

/// Whether the setup would show: the user opened it, or the Chief still needs one and the user
/// has not dismissed it.
fn setup_wanted(state: &State) -> Option<(String, bool)> {
    let member = member(state)?;
    let chief = &state.window.chief;
    let by_user = chief
        .editor
        .as_ref()
        .is_some_and(|(k, open)| *k == member.id && *open);
    let wanted = match &chief.editor {
        Some((k, open)) if *k == member.id => *open,
        _ => !configured(member),
    };
    wanted.then(|| (member.id.clone(), by_user))
}

/// Whether the setup shows over the slot.
pub(crate) fn setup_shows(state: &State) -> bool {
    setup_wanted(state).is_some_and(|(key, by_user)| {
        by_user || state.window.chief.automatic.as_deref() == Some(key.as_str())
    })
}

/// The setup's refusal for the Chief shown now.
pub(crate) fn setup_error(state: &State) -> Option<&str> {
    let member = member(state)?;
    state
        .window
        .chief
        .error
        .as_ref()
        .filter(|(k, _)| *k == member.id)
        .map(|(_, e)| e.as_str())
}

/// Opens the setup over the slot.
pub(crate) fn edit(state: &mut State, tx: &mut Tx) {
    let Some(key) = member(state).map(|m| m.id.clone()) else {
        return;
    };
    state.window.chief.error = None;
    state.window.chief.editor = Some((key, true));
    setup::poll(state, tx);
}

/// Opens the setup from somewhere the sidebar may be hidden: the palettes.
pub(crate) fn open_setup(state: &mut State, tx: &mut Tx) {
    if state.window.sidebar_hidden {
        crate::dispatch(state, Action::SessionToggleSidebar, tx);
    }
    edit(state, tx);
}

/// Closes the setup; `return_focus` puts the focus back on the slot.
pub(crate) fn dismiss(state: &mut State, return_focus: bool, tx: &mut Tx) {
    let Some(key) = member(state).map(|m| m.id.clone()) else {
        return;
    };
    state.window.chief.editor = Some((key, false));
    state.window.chief.draft = None;
    if return_focus {
        focus_row(state, tx);
    }
}

/// Puts the focus on the slot: in queue flow on a Chief in session, the queue list's cursor on
/// its row.
fn focus_row(state: &mut State, tx: &mut Tx) {
    let Some(row) = row_key(state) else { return };
    let session = view(state).and_then(|v| v.session);
    match session {
        Some(session) if row != SLOT && crate::sidebar::actions::queue_flow(state) => {
            crate::sidebar::actions::focus_row(state, &row, &session, tx);
        }
        _ => tx.focus(row, true),
    }
}

/// The key of the slot's row as it is drawn now.
pub(crate) fn row_key(state: &State) -> Option<String> {
    let view = view(state)?;
    Some(match (&view.phase, &view.session) {
        (Phase::InSession | Phase::Closing, Some(session))
            if state.facts.sessions.iter().any(|s| &s.id == session) =>
        {
            format!("queue-chief.{session}")
        }
        _ => SLOT.to_owned(),
    })
}

/// The slot's row while its Chief has no session to open.
pub(crate) const SLOT: &str = "chief-slot";

/// Starts the Chief's session.
pub(crate) fn start(state: &mut State, tx: &mut Tx) {
    let Some(member) = member(state) else { return };
    if !configured(member) || pending(state, &member.id).is_some() {
        return;
    }
    let key = member.id.clone();
    start_chief(state, key, tx);
}

fn start_chief(state: &mut State, key: String, tx: &mut Tx) {
    state.window.chief.pending = Some((key.clone(), Pending::Starting));
    let command_member = member_ref(&key);
    state.ask(
        |request_id| CrewStartMessage {
            member: command_member,
            request_id: Some(request_id),
            ..CrewStartMessage::new()
        },
        move |state, answer, tx| {
            settle(state, &key);
            if !is_current(state, &key) {
                return;
            }
            match answer.parse::<CrewStartResultMessage>() {
                Some(CrewStartResultMessage {
                    success: true,
                    session_id: Some(session),
                    ..
                }) => {
                    crate::dispatch(state, Action::OpenAgent { session }, tx);
                }
                other => {
                    let why = other
                        .and_then(|r| r.error)
                        .filter(|e| !e.is_empty())
                        .unwrap_or_else(|| refusal(&answer, "Starting the Chief failed"));
                    state.window.notify(why, tx);
                }
            }
        },
        tx,
    );
}

fn refusal(answer: &Answer, failed: &str) -> String {
    match answer {
        Answer::Reply(_) => failed.to_owned(),
        Answer::NotSent => "not connected to the daemon".to_owned(),
        Answer::Dropped => "the daemon did not answer".to_owned(),
    }
}

/// Asks the Chief's session to file its letter and close.
pub(crate) fn close(state: &mut State, tx: &mut Tx) {
    let Some(member) = member(state) else { return };
    if member.binding_session.as_deref().is_none_or(str::is_empty)
        || pending(state, &member.id).is_some()
    {
        return;
    }
    let key = member.id.clone();
    state.window.chief.pending = Some((key.clone(), Pending::Closing));
    let command_member = member_ref(&key);
    state.ask(
        |request_id| CrewCloseMessage {
            member: command_member,
            request_id: Some(request_id),
            ..CrewCloseMessage::new()
        },
        move |state, answer, tx| {
            settle(state, &key);
            if let Err(why) = answer.outcome()
                && is_current(state, &key)
            {
                state.window.notify(why, tx);
            }
        },
        tx,
    );
}

/// Saves the route the setup shows, and on a first setup starts the Chief once it is saved.
/// `return_focus` puts the focus back on the slot after a save that closes the setup.
pub(crate) fn set_up(
    state: &mut State,
    route: Route,
    notebook_root: Option<String>,
    return_focus: bool,
    tx: &mut Tx,
) {
    let Some(member) = member(state) else { return };
    if pending(state, &member.id).is_some() {
        return;
    }
    let key = member.id.clone();
    let first_run = !configured(member);
    let revision = member.revision;
    let profile_id = Some(member.profile_id.clone()).filter(|p| !p.is_empty());
    let chief = &mut state.window.chief;
    chief.error = None;
    chief.editor = Some((key.clone(), true));
    chief.pending = Some((key.clone(), Pending::Configuring));
    let command_member = member_ref(&key);
    state.ask(
        |request_id| CrewSetMessage {
            member: command_member,
            expected_revision: Some(revision),
            agent: Some(route.harness.clone()),
            model: Some(route.stored_model()),
            effort: Some(route.effort.clone()),
            notebook_root,
            profile_id,
            request_id: Some(request_id),
            ..CrewSetMessage::new()
        },
        move |state, answer, tx| {
            let refused = match answer.parse::<CrewSetResultMessage>() {
                Some(r) if r.success => None,
                Some(r) => Some(
                    r.error
                        .filter(|e| !e.is_empty())
                        .unwrap_or_else(|| "The launch settings were not saved.".into()),
                ),
                None => Some(refusal(&answer, "The launch settings were not saved.")),
            };
            if let Some(why) = refused {
                if is_current(state, &key) {
                    state.window.chief.error = Some((key.clone(), why));
                }
                settle(state, &key);
                return;
            }
            let chief = &mut state.window.chief;
            if chief.editor.as_ref().is_some_and(|(k, _)| *k == key) {
                chief.editor = Some((key.clone(), false));
                chief.draft = None;
            }
            if first_run && is_current(state, &key) {
                start_chief(state, key, tx);
                return;
            }
            settle(state, &key);
            let still_here = tx
                .focused()
                .is_none_or(|f| f == setup::POP || f.starts_with(&format!("{}.", setup::POP)));
            if return_focus && still_here && is_current(state, &key) {
                focus_row(state, tx);
            }
        },
        tx,
    );
}

/// Before each frame: opens the first setup on its own only while nothing else needs the user,
/// keeps the setup's draft and its model discovery going while it shows, and hands the focus
/// back to the slot once its row is drawn; whether anything changed.
pub(crate) fn poll(state: &mut State, tx: &mut Tx) -> bool {
    let mut changed = false;
    let seen = view(state).map(|v| (v.key, v.phase));
    if seen != state.window.chief.seen {
        if seen
            .as_ref()
            .is_some_and(|(_, p)| *p == Phase::Unconfigured)
            && state.window.sidebar_hidden
        {
            crate::dispatch(state, Action::SessionToggleSidebar, tx);
        }
        state.window.chief.seen = seen;
        changed = true;
    }
    if let Some((key, by_user)) = setup_wanted(state)
        && !by_user
        && state.window.chief.automatic.as_deref() != Some(key.as_str())
    {
        let quiet =
            state.facts.desktops.iter().all(|d| d.panes.is_empty()) && tx.focused().is_none();
        if quiet {
            state.window.chief.automatic = Some(key);
        } else {
            state.window.chief.editor = Some((key, false));
        }
        changed = true;
    }
    changed | setup::poll(state, tx)
}
