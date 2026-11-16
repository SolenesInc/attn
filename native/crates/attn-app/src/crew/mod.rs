//! Manage crew: the profile's members, and for each its launch settings, charter, letters and
//! seeds, with Start, Close and Apply for its session.

pub(crate) mod autosave;
pub(crate) mod charter;
pub(crate) mod handoffs;
pub(crate) mod launch;
mod seeds;
mod view;

use attn_protocol::{
    CrewApplyMessage, CrewApplyResultMessage, CrewCloseMessage, CrewMember, CrewRenameMessage,
    CrewStartMessage, CrewStartResultMessage, OpenSeedMessage, OpenSeedResultMessage,
};
use rune::{TextEdit, Tx};

use crate::requests::Refused;
use crate::shell::Layer;
use crate::{Action, State};

pub(crate) use seeds::SeedFilter;
pub(crate) use view::panel;

#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub(crate) enum Tab {
    #[default]
    Launch,
    Charter,
    Handoffs,
    Seeds,
}

impl Tab {
    pub(crate) const ALL: [Tab; 4] = [Tab::Launch, Tab::Charter, Tab::Handoffs, Tab::Seeds];

    pub(crate) fn id(self) -> &'static str {
        match self {
            Tab::Launch => "launch",
            Tab::Charter => "charter",
            Tab::Handoffs => "handoffs",
            Tab::Seeds => "seeds",
        }
    }

    pub(crate) fn label(self) -> &'static str {
        match self {
            Tab::Launch => "Launch settings",
            Tab::Charter => "Charter",
            Tab::Handoffs => "Handoffs",
            Tab::Seeds => "Seeds",
        }
    }
}

/// Where the user is in the panel: what a fresh open forgets, and what a seed opened from the
/// panel brings back.
#[derive(Clone, Default)]
pub(crate) struct Place {
    pub(crate) member: Option<String>,
    pub(crate) tab: Tab,
    pub(crate) roster_filter: TextEdit,
    pub(crate) seed_filter: SeedFilter,
    pub(crate) seed_query: TextEdit,
}

/// Where a navigation goes once the charter it leaves has landed.
#[derive(Clone, Debug, PartialEq, Eq)]
pub(crate) enum Nav {
    Member(String),
    Tab(Tab),
    Seed(String),
    Close,
}

pub(crate) struct Rename {
    pub(crate) member: String,
    pub(crate) edit: TextEdit,
    pub(crate) saving: bool,
    pub(crate) error: Option<String>,
}

pub(crate) struct Applied {
    pub(crate) member: String,
    pub(crate) text: String,
    pub(crate) failed: bool,
}

/// The seed tile a seed opened from the panel became, and the place to go back to.
pub(crate) struct Back {
    pub(crate) desktop: String,
    pub(crate) tile: String,
    place: Place,
    arrived: bool,
}

#[derive(Default)]
pub(crate) struct Panel {
    pub(crate) place: Place,
    pub(crate) pending: Option<Nav>,
    pub(crate) rename: Option<Rename>,
    pub(crate) applying: Option<String>,
    pub(crate) applied: Option<Applied>,
    pub(crate) launches: launch::LaunchEdits,
    pub(crate) charters: charter::Charters,
    pub(crate) handoffs: handoffs::Histories,
    pub(crate) back: Option<Back>,
    /// The member whose charter the focus was in at the last frame.
    pub(crate) charter_focus: Option<String>,
    /// The control that opened the panel from a menu, which takes the focus back on close.
    pub(crate) opener: Option<String>,
}

/// Addresses a member by its permanent key; a bare word would be read as a name.
pub(crate) fn member_ref(key: &str) -> String {
    format!("member:{key}")
}

pub(crate) fn is_open(state: &State) -> bool {
    state.window.layer == Some(Layer::Crew)
}

/// The members of the selected profile the panel lists.
pub(crate) fn members(state: &State) -> Vec<&CrewMember> {
    let profile = &state.facts.profiles.selected;
    state
        .facts
        .crew
        .iter()
        .filter(|m| &m.profile_id == profile && !m.retired)
        .collect()
}

/// The member the panel shows: the one chosen, or the first.
pub(crate) fn selected(state: &State) -> Option<&CrewMember> {
    let members = members(state);
    let chosen = state.window.crew.place.member.as_deref();
    members
        .iter()
        .find(|m| Some(m.id.as_str()) == chosen)
        .or_else(|| members.first())
        .copied()
}

/// Opens the panel afresh, on the launch settings of `member` or the first member.
pub(crate) fn open(state: &mut State, member: Option<String>, tx: &mut Tx) {
    open_on(state, member, Tab::Launch, tx);
}

/// Opens the panel afresh on `tab` of `member`.
pub(crate) fn open_on(state: &mut State, member: Option<String>, tab: Tab, tx: &mut Tx) {
    state.window.crew.place = Place {
        member,
        tab,
        ..Place::default()
    };
    show(state, tx);
}

fn show(state: &mut State, tx: &mut Tx) {
    let panel = &mut state.window.crew;
    panel.pending = None;
    panel.rename = None;
    panel.applied = None;
    panel.opener = None;
    state.window.layer = Some(Layer::Crew);
    launch::follow(state);
    crate::settings::crew_opened(state);
    entered(state, tx);
}

/// Reads what the shown tab needs.
fn entered(state: &mut State, tx: &mut Tx) {
    let Some(member) = selected(state).map(|m| m.id.clone()) else {
        return;
    };
    match state.window.crew.place.tab {
        Tab::Charter if !state.window.crew.charters.edits.writing(&member) => {
            charter::load(state, &member, tx)
        }
        Tab::Handoffs => handoffs::load(state, &member, false, tx),
        Tab::Charter | Tab::Launch | Tab::Seeds => {}
    }
}

/// Goes where the user asked, once a charter being edited has landed.
pub(crate) fn navigate(state: &mut State, nav: Nav, tx: &mut Tx) {
    let member = selected(state).map(|m| m.id.clone());
    if state.window.crew.place.tab == Tab::Charter
        && let Some(member) = member
        && charter::unsettled(state, &member)
    {
        state.window.crew.pending = Some(nav);
        charter::flush(state, &member, tx);
        return;
    }
    state.window.crew.pending = None;
    go(state, nav, tx);
}

/// A charter save landed: a navigation waiting for it goes on when it saved, and stays put
/// when it did not, showing why.
pub(crate) fn settled(state: &mut State, tx: &mut Tx) {
    let Some(member) = selected(state).map(|m| m.id.clone()) else {
        return;
    };
    if state.window.crew.charters.edits.writing(&member) {
        return;
    }
    let Some(nav) = state.window.crew.pending.take() else {
        return;
    };
    let saved = state
        .window
        .crew
        .charters
        .edits
        .get(&member)
        .is_some_and(|e| e.phase == autosave::Phase::Saved);
    if saved {
        go(state, nav, tx);
    }
}

fn go(state: &mut State, nav: Nav, tx: &mut Tx) {
    match nav {
        Nav::Member(id) => {
            state.window.crew.place.member = Some(id);
            state.window.crew.rename = None;
            entered(state, tx);
        }
        Nav::Tab(tab) => {
            state.window.crew.place.tab = tab;
            entered(state, tx);
        }
        Nav::Seed(seed) => open_seed(state, &seed, tx),
        Nav::Close => close_now(state, tx),
    }
}

pub(crate) fn close(state: &mut State, tx: &mut Tx) {
    navigate(state, Nav::Close, tx);
}

fn close_now(state: &mut State, tx: &mut Tx) {
    if !is_open(state) {
        return;
    }
    crate::settings::close_route_picker(state, tx);
    state.window.crew.rename = None;
    state.window.layer = None;
    match state.window.crew.opener.take() {
        Some(opener) => tx.focus(opener, true),
        None => crate::picker::refocus_inline(state, tx),
    }
}

/// Starts a session for `member` and shows it.
pub(crate) fn start(state: &mut State, member: &str, tx: &mut Tx) {
    let name = crate::asks::crew_display_name(member);
    let command_member = member_ref(member);
    state.ask(
        |request_id| CrewStartMessage {
            member: command_member,
            request_id: Some(request_id),
            ..CrewStartMessage::new()
        },
        move |state, answer, tx| {
            let result = answer.parse::<CrewStartResultMessage>();
            match result {
                Some(CrewStartResultMessage {
                    success: true,
                    session_id: Some(session),
                    show_error,
                    ..
                }) => {
                    if let Some(why) = show_error.filter(|e| !e.is_empty()) {
                        state.window.notify(
                            format!("The member's session started, but showing it failed: {why}"),
                            tx,
                        );
                    }
                    close_now(state, tx);
                    crate::dispatch(state, Action::OpenAgent { session }, tx);
                }
                other => {
                    let why = other
                        .and_then(|r| r.error)
                        .filter(|e| !e.is_empty())
                        .unwrap_or_else(|| format!("Failed to start {name}"));
                    state.window.notify(why, tx);
                }
            }
        },
        tx,
    );
}

/// Asks `member`'s session to file its letter and close.
pub(crate) fn ask_to_close(state: &mut State, member: &str, tx: &mut Tx) {
    let command_member = member_ref(member);
    state.act(
        |request_id| CrewCloseMessage {
            member: command_member,
            request_id: Some(request_id),
            ..CrewCloseMessage::new()
        },
        Refused::Bare,
        |_, _, _| {},
        tx,
    );
}

/// Puts `member`'s saved settings into its running session, and says what happened.
pub(crate) fn apply(state: &mut State, member: &str, tx: &mut Tx) {
    let panel = &mut state.window.crew;
    panel.applying = Some(member.to_owned());
    panel.applied = None;
    let member = member.to_owned();
    let command_member = member_ref(&member);
    state.ask(
        |request_id| CrewApplyMessage {
            member: command_member,
            request_id: Some(request_id),
            ..CrewApplyMessage::new()
        },
        move |state, answer, _| {
            let parsed = answer.parse::<CrewApplyResultMessage>();
            let (text, failed) = match parsed {
                Some(CrewApplyResultMessage {
                    success: true,
                    result: Some(result),
                    ..
                }) => (format!("{}.", result.detail), false),
                other => (
                    other
                        .and_then(|r| r.error)
                        .filter(|e| !e.is_empty())
                        .unwrap_or_else(|| match answer {
                            crate::requests::Answer::Reply(_) => {
                                "Applying the settings failed".into()
                            }
                            _ => "not connected to the daemon".into(),
                        }),
                    true,
                ),
            };
            let panel = &mut state.window.crew;
            panel.applying = None;
            panel.applied = Some(Applied {
                member,
                text,
                failed,
            });
        },
        tx,
    );
}

pub(crate) fn begin_rename(state: &mut State, member: &str, tx: &mut Tx) {
    let Some(name) = members(state)
        .iter()
        .find(|m| m.id == member)
        .map(|m| m.name.clone())
    else {
        return;
    };
    let mut edit = TextEdit::new(name.clone());
    edit.set_selection(0, name.len());
    state.window.crew.rename = Some(Rename {
        member: member.to_owned(),
        edit,
        saving: false,
        error: None,
    });
    tx.focus(view::RENAME_FIELD, true);
}

pub(crate) fn cancel_rename(state: &mut State, tx: &mut Tx) {
    if state.window.crew.rename.take().is_some() {
        tx.focus(view::RENAME, false);
    }
}

pub(crate) fn submit_rename(state: &mut State, tx: &mut Tx) {
    let Some(rename) = &mut state.window.crew.rename else {
        return;
    };
    if rename.saving {
        return;
    }
    let name = rename.edit.text.trim().to_owned();
    let member = rename.member.clone();
    let unchanged = members(state)
        .iter()
        .any(|m| m.id == member && m.name == name);
    if unchanged {
        cancel_rename(state, tx);
        return;
    }
    let Some(rename) = &mut state.window.crew.rename else {
        return;
    };
    rename.saving = true;
    let command_member = member_ref(&member);
    state.ask(
        |request_id| CrewRenameMessage {
            member: command_member,
            name,
            request_id: Some(request_id),
            ..CrewRenameMessage::new()
        },
        move |state, answer, tx| {
            let outcome = answer.outcome();
            let Some(rename) = state
                .window
                .crew
                .rename
                .as_mut()
                .filter(|r| r.member == member && r.saving)
            else {
                return;
            };
            match outcome {
                Ok(_) => cancel_rename(state, tx),
                Err(why) => {
                    rename.saving = false;
                    rename.error = Some(why);
                }
            }
        },
        tx,
    );
}

/// Opens `seed` as a tile: beside the member's session while it has one, standalone when not.
/// The panel closes, and the tile offers the way back to where the user was.
fn open_seed(state: &mut State, seed: &str, tx: &mut Tx) {
    let session = selected(state).and_then(|m| m.binding_session.clone());
    let standalone = session.is_none().then_some(true);
    let place = state.window.crew.place.clone();
    state.act(
        |request_id| OpenSeedMessage {
            seed_id: seed.to_owned(),
            session_id: session,
            standalone,
            request_id: Some(request_id),
            ..OpenSeedMessage::new()
        },
        Refused::Prefixed("Could not open the seed"),
        move |state, reply, tx| {
            let Some(OpenSeedResultMessage {
                desktop_id: Some(desktop),
                tile_id: Some(tile),
                ..
            }) = reply.parse::<OpenSeedResultMessage>()
            else {
                return;
            };
            state.window.crew.back = Some(Back {
                desktop: desktop.clone(),
                tile: tile.clone(),
                place,
                arrived: false,
            });
            close_now(state, tx);
            crate::desktop::reveal_leaf(state, &desktop, &tile, true, tx);
        },
        tx,
    );
}

/// Goes back from the seed tile to the panel, where the user was when they opened it.
pub(crate) fn go_back(state: &mut State, tx: &mut Tx) {
    let Some(back) = state.window.crew.back.take() else {
        return;
    };
    state.window.crew.place = back.place;
    show(state, tx);
}

/// The tile the way back to Crew shows on.
pub(crate) fn back_tile(state: &State) -> Option<(&str, &str)> {
    state
        .window
        .crew
        .back
        .as_ref()
        .map(|b| (b.desktop.as_str(), b.tile.as_str()))
}

/// Takes the roster and the desktops: edits adopt the members' new versions, and the way back
/// goes once its tile, having arrived, leaves.
pub(crate) fn follow(state: &mut State) {
    launch::follow(state);
    let Some(back) = &mut state.window.crew.back else {
        return;
    };
    let shown = state
        .facts
        .desktops
        .iter()
        .any(|d| d.id == back.desktop && d.tree_json.contains(&back.tile));
    if shown {
        back.arrived = true;
    } else if back.arrived {
        state.window.crew.back = None;
    }
}

/// The connection came back: edits it lost write again, and charters and letters read again.
pub(crate) fn reconnected(state: &mut State, tx: &mut Tx) {
    state.window.crew.handoffs.connection += 1;
    launch::reconnected(state, tx);
    charter::reconnected(state, tx);
    if is_open(state) {
        entered(state, tx);
    }
}

pub(crate) fn poll(state: &mut State, now: f64, tx: &mut Tx) -> bool {
    let left = view::leave_charter(state, tx);
    let listed = is_open(state) && crate::settings::crew_poll(state, tx);
    let rows = crate::sidebar::crew::poll(state, now, &crate::sidebar::crew::row_prefixes(), tx);
    charter::poll(state, now, tx) | left | listed | rows
}
