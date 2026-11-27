//! The peek: a look at a tile off screen, from its shelf entry, and a reply to its terminal that
//! leaves the layout as it is.

use std::collections::HashMap;

use attn_protocol::{
    PtyInputMessage, PtyInputProbeResultMessage, TerminalReadTextMessage,
    TerminalReadTextResultMessage,
};
use attn_ui::blocks::{Bar, Screen as ScreenText};
use attn_ui::system::{
    Button, DotShape, DotSize, FieldState, Look, Size, Type, dot, keycaps, line_box, words,
};
use attn_ui::{Frame, Tokens};
use rune::{El, TextEdit, Tx, col};
use rune_kit::TextInput;

use super::layout::{LeafKind, Node};
use super::shelf::Shelf;
use crate::State;
use crate::requests::Answer;
use crate::sidebar::AgentState;

const OPEN_DELAY: f64 = 0.18;
const SWITCH_DELAY: f64 = 0.06;
const CLOSE_DELAY: f64 = 0.16;
const READ_EVERY: f64 = 1.0;
const EDGE_PX: f32 = 8.0;
const ANCHOR_GAP_PX: f32 = 6.0;
const STOPPED: &str = "Agent stopped. Open its tile to resume it; your reply stays there.";

/// The window's peek and the replies typed into peeks, which outlive the peek that took them.
#[derive(Default)]
pub(crate) struct Peeks {
    pub(crate) open: Option<Open>,
    timer: Option<Timer>,
    /// The tile the pointer last came to on the entry it is over.
    arrived: Option<Arrival>,
    /// Replies not yet delivered, by the terminal they go to.
    pub(crate) replies: HashMap<String, Reply>,
    /// The reply field of the open peek.
    pub(crate) field: TextEdit,
}

pub(crate) struct Open {
    pub(crate) desktop: String,
    pub(crate) leaf: String,
    /// The point in the window the panel hangs from: its entry's bottom-left corner.
    anchor: [f32; 2],
    /// The tile's terminal, when it has one.
    runtime: Option<String>,
    screen: Screen,
    /// When the terminal's text is read next; none while a read is out.
    next_read: Option<f64>,
}

#[derive(Default)]
struct Screen {
    text: Option<String>,
    failure: Option<String>,
}

enum Timer {
    Open {
        desktop: String,
        leaf: String,
        entry: String,
        at: f64,
    },
    Close {
        at: f64,
    },
}

#[derive(Clone, Debug, PartialEq, Eq, Hash)]
pub(crate) enum Reply {
    Draft(String),
    Sending(String),
    NotSent {
        text: String,
        reason: Option<String>,
    },
    Typed {
        text: String,
        reason: String,
    },
}

impl Reply {
    fn text(&self) -> &str {
        match self {
            Reply::Draft(text) | Reply::Sending(text) => text,
            Reply::NotSent { text, .. } | Reply::Typed { text, .. } => text,
        }
    }
}

/// Where on the shelf the pointer came to: an entry, by its key, and the tile on it.
#[derive(Clone, Debug, PartialEq)]
struct Arrival {
    entry: String,
    leaf: String,
}

/// The pointer moved over an entry: it comes to the tile the entry goes to, unless it already came
/// to a tile on the entry.
pub(crate) fn hover_entry(state: &mut State, desktop: &str, leaf: &str, entry: &str, tx: &mut Tx) {
    if state
        .window
        .desk
        .peeks
        .arrived
        .as_ref()
        .is_some_and(|a| a.entry == entry)
    {
        tx.live_only();
        return;
    }
    arrive(state, desktop, leaf, entry, tx);
}

/// The pointer came onto the mark of `leaf` on the entry keyed `entry`.
pub(crate) fn hover_member(state: &mut State, desktop: &str, leaf: &str, entry: &str, tx: &mut Tx) {
    let arrival = Arrival {
        entry: entry.to_owned(),
        leaf: leaf.to_owned(),
    };
    if state.window.desk.peeks.arrived.as_ref() != Some(&arrival) {
        arrive(state, desktop, leaf, entry, tx);
    }
}

/// The pointer came to `leaf` on the entry keyed `entry`: its peek opens after a moment, sooner
/// when another peek shows.
fn arrive(state: &mut State, desktop: &str, leaf: &str, entry: &str, tx: &mut Tx) {
    let desk = &mut state.window.desk;
    let peeks = &mut desk.peeks;
    peeks.timer = None;
    peeks.arrived = Some(Arrival {
        entry: entry.to_owned(),
        leaf: leaf.to_owned(),
    });
    if desk.drag.as_ref().is_some_and(|d| d.dragging().is_some()) {
        return;
    }
    if peeks.open.as_ref().is_some_and(|o| o.leaf == leaf) || replying(peeks, tx) {
        return;
    }
    let delay = if peeks.open.is_some() {
        SWITCH_DELAY
    } else {
        OPEN_DELAY
    };
    let at = tx.clock() + delay;
    peeks.timer = Some(Timer::Open {
        desktop: desktop.to_owned(),
        leaf: leaf.to_owned(),
        entry: entry.to_owned(),
        at,
    });
    tx.wake_at(at);
}

/// The pointer came onto the peek, which stays.
fn enter_peek(state: &mut State) {
    state.window.desk.peeks.timer = None;
}

/// The pointer left the entry keyed `entry`. Hover changes come after the move that caused
/// them, so the pointer may already have come to another entry, which then keeps the peek.
pub(crate) fn leave_entry(state: &mut State, entry: &str, tx: &mut Tx) {
    let peeks = &mut state.window.desk.peeks;
    if peeks.arrived.as_ref().is_some_and(|a| a.entry != entry) {
        return;
    }
    peeks.arrived = None;
    close_soon(state, tx);
}

/// The pointer left the peek, unless it came to an entry first.
fn leave_peek(state: &mut State, tx: &mut Tx) {
    if state.window.desk.peeks.arrived.is_none() {
        close_soon(state, tx);
    }
}

/// The peek closes after a moment, unless its reply field holds the keyboard.
fn close_soon(state: &mut State, tx: &mut Tx) {
    let peeks = &mut state.window.desk.peeks;
    peeks.timer = None;
    if peeks.open.is_none() || replying(peeks, tx) {
        return;
    }
    let at = tx.clock() + CLOSE_DELAY;
    peeks.timer = Some(Timer::Close { at });
    tx.wake_at(at);
}

/// The key of the reply field in the peek at `leaf`. Each tile's field is its own, so the
/// keyboard never carries a reply from one agent's peek into another's.
pub(crate) fn field_key(leaf: &str) -> String {
    format!("shelf.peek.{leaf}.field")
}

/// Whether the person is typing a reply in the open peek: it then stays, so what they type
/// keeps going to its agent.
fn replying(peeks: &Peeks, tx: &Tx) -> bool {
    peeks
        .open
        .as_ref()
        .is_some_and(|o| tx.focused() == Some(field_key(&o.leaf).as_str()))
}

/// The open peek's field shows the reply to `runtime` as it now stands, once its delivery
/// moved it on.
fn follow_reply(state: &mut State, runtime: &str) {
    let peeks = &mut state.window.desk.peeks;
    if peeks.open.as_ref().and_then(|o| o.runtime.as_deref()) == Some(runtime) {
        let text = peeks.replies.get(runtime).map_or("", Reply::text);
        peeks.field.replace_text(text);
    }
}

/// Closes the peek, keeping what its reply field holds.
pub(crate) fn dismiss(state: &mut State) -> bool {
    let peeks = &mut state.window.desk.peeks;
    peeks.timer = None;
    peeks.open.take().is_some()
}

/// The shelf shows another row: the peek closes, keeping its reply, and the pointer is on no entry.
pub(crate) fn leave_row(state: &mut State) {
    dismiss(state);
    state.window.desk.peeks.arrived = None;
}

/// Closes the peek and forgets a reply only drafted in it.
pub(crate) fn close(state: &mut State) -> bool {
    let peeks = &mut state.window.desk.peeks;
    if let Some(runtime) = peeks.open.as_ref().and_then(|o| o.runtime.as_ref())
        && matches!(peeks.replies.get(runtime), Some(Reply::Draft(_)))
    {
        peeks.replies.remove(runtime);
    }
    dismiss(state)
}

/// Opens or closes the peek whose moment came, and reads the open terminal's text when due.
/// Returns whether anything changed.
pub(crate) fn expire(state: &mut State, tx: &mut Tx) -> bool {
    let now = tx.clock();
    let peeks = &mut state.window.desk.peeks;
    let due = match &peeks.timer {
        Some(Timer::Open { at, .. } | Timer::Close { at }) => *at <= now,
        None => false,
    };
    let mut changed = false;
    if due {
        changed = true;
        match peeks.timer.take() {
            Some(Timer::Open {
                desktop,
                leaf,
                entry,
                ..
            }) => show(state, desktop, leaf, &entry),
            _ => {
                peeks.open = None;
            }
        }
    }
    let read = state
        .window
        .desk
        .peeks
        .open
        .as_ref()
        .filter(|o| o.next_read.is_some_and(|at| at <= now))
        .and_then(|o| o.runtime.clone());
    if let Some(runtime) = read {
        start_read(state, runtime, tx);
    }
    changed
}

fn show(state: &mut State, desktop: String, leaf: String, entry: &str) {
    let Some(rect) = state
        .window
        .desk
        .rects
        .get(&format!("shelf.entry.{entry}"))
        .copied()
    else {
        return;
    };
    let runtime = state
        .facts
        .desktops
        .iter()
        .find(|d| d.id == desktop)
        .and_then(|d| super::view::pane_of(d, &leaf))
        .map(|p| p.runtime_id.clone())
        .filter(|r| !r.is_empty());
    let anchor = [rect[0], rect[1] + rect[3]];
    let peeks = &mut state.window.desk.peeks;
    let draft = runtime
        .as_ref()
        .and_then(|r| peeks.replies.get(r))
        .map_or("", Reply::text);
    peeks.field = TextEdit::new(draft);
    peeks.open = Some(Open {
        desktop,
        leaf,
        anchor,
        next_read: runtime.as_ref().map(|_| 0.0),
        runtime,
        screen: Screen::default(),
    });
}

fn start_read(state: &mut State, runtime: String, tx: &mut Tx) {
    if let Some(open) = &mut state.window.desk.peeks.open {
        open.next_read = None;
    }
    let id = state.next_request_id();
    let read = TerminalReadTextMessage {
        request_id: Some(id.clone()),
        terminal_id: runtime.clone(),
        mode: "screen".to_owned(),
        lines: 0,
        ..TerminalReadTextMessage::new()
    };
    let via = crate::terminal::rides_runtime(&state.facts, &runtime);
    state.ask_on(
        via,
        id,
        &read,
        move |state: &mut State, answer: Answer, tx: &mut Tx| {
            let Some(open) = state
                .window
                .desk
                .peeks
                .open
                .as_mut()
                .filter(|o| o.runtime.as_deref() == Some(runtime.as_str()))
            else {
                return;
            };
            match answer.parse::<TerminalReadTextResultMessage>() {
                Some(read) if read.success => {
                    open.screen.text = Some(read.text.unwrap_or_default().trim_end().to_owned());
                    open.screen.failure = None;
                }
                read => {
                    open.screen.failure = Some(
                        read.and_then(|r| r.error)
                            .unwrap_or_else(|| "the daemon did not answer".to_owned()),
                    );
                }
            }
            let at = tx.clock() + READ_EVERY;
            open.next_read = Some(at);
            tx.wake_at(at);
        },
        tx,
    );
}

/// Keeps the peek, open or about to open, and where the pointer came to, to a tile still off
/// screen on the current desktop, and moves a reply typed to an agent that stopped back to unsent, for its tile to keep.
pub(crate) fn settle(state: &mut State) {
    let peeks = &state.window.desk.peeks;
    let peeked = match (&peeks.open, &peeks.timer) {
        (Some(open), _) => Some((&open.desktop, &open.leaf)),
        (None, Some(Timer::Open { desktop, leaf, .. })) => Some((desktop, leaf)),
        (None, _) => None,
    };
    let off_screen = |leaf: &str| Shelf::of(state).is_some_and(|s| s.holds(leaf));
    let gone = peeked.is_some_and(|(desktop, leaf)| {
        !(state
            .facts
            .current_desktop()
            .is_some_and(|d| &d.id == desktop)
            && off_screen(leaf))
    });
    let left = peeks.arrived.as_ref().is_some_and(|a| !off_screen(&a.leaf));
    if gone {
        dismiss(state);
    }
    if left {
        state.window.desk.peeks.arrived = None;
    }
    let stopped: Vec<String> = state
        .facts
        .desktops
        .iter()
        .flat_map(|d| d.panes.iter())
        .filter(|p| session_of(state, &p.session_id).is_some_and(has_stopped))
        .map(|p| p.runtime_id.clone())
        .collect();
    for runtime in stopped {
        retain_typed(&mut state.window.desk.peeks.replies, &runtime);
    }
}

fn session_of<'a>(state: &'a State, id: &str) -> Option<&'a attn_protocol::Session> {
    state.facts.sessions.iter().find(|s| s.id == id)
}

fn has_stopped(session: &attn_protocol::Session) -> bool {
    session.terminal_exit.is_some() || session.state_reason.as_deref() == Some("process_exited")
}

fn retain_typed(replies: &mut HashMap<String, Reply>, runtime: &str) {
    if let Some(Reply::Typed { text, reason }) = replies.get(runtime).cloned() {
        replies.insert(
            runtime.to_owned(),
            Reply::NotSent {
                text,
                reason: Some(reason),
            },
        );
    }
}

/// Why the open peek's tile takes no reply now.
fn blocked(state: &State, open: &Open) -> Option<&'static str> {
    let desktop = state.facts.desktops.iter().find(|d| d.id == open.desktop)?;
    let pane = super::view::pane_of(desktop, &open.leaf)?;
    session_of(state, &pane.session_id)
        .filter(|s| has_stopped(s))
        .map(|_| STOPPED)
}

/// The reply field changed: what it holds is the terminal's draft, or the text of a reply that
/// was not sent. A reply on its way, or typed and waiting for Return, keeps its text.
fn edited(state: &mut State) {
    let peeks = &mut state.window.desk.peeks;
    let Some(runtime) = peeks.open.as_ref().and_then(|o| o.runtime.clone()) else {
        return;
    };
    let typed = peeks.field.text.clone();
    match peeks.replies.get_mut(&runtime) {
        Some(Reply::Sending(text) | Reply::Typed { text, .. }) => {
            peeks.field.replace_text(text.clone());
        }
        _ if typed.is_empty() => {
            peeks.replies.remove(&runtime);
        }
        Some(Reply::NotSent { text, .. }) => *text = typed,
        _ => {
            peeks.replies.insert(runtime, Reply::Draft(typed));
        }
    }
}

/// Return in the reply field: an empty field opens the tile; otherwise the reply goes to the
/// terminal and the peek closes.
fn submit(state: &mut State, tx: &mut Tx) {
    let Some(open) = state.window.desk.peeks.open.as_ref() else {
        return;
    };
    let Some(runtime) = open.runtime.clone() else {
        return;
    };
    let blocked = blocked(state, open).is_some();
    let title = title(state, open);
    let peeks = &mut state.window.desk.peeks;
    let reply = peeks.replies.get(&runtime).cloned();
    if matches!(reply, Some(Reply::Sending(_))) {
        return;
    }
    let draft = peeks.field.text.clone();
    let line = draft.trim().to_owned();
    if line.is_empty() {
        open_tile(state, tx);
        return;
    }
    if blocked {
        if matches!(reply, Some(Reply::Draft(_))) {
            peeks.replies.insert(
                runtime,
                Reply::NotSent {
                    text: draft,
                    reason: None,
                },
            );
        }
        return;
    }
    let reply = match reply {
        Some(typed @ Reply::Typed { .. }) => typed,
        _ => Reply::Draft(line),
    };
    deliver(state, runtime, title, reply, tx);
    dismiss(state);
}

fn title(state: &State, open: &Open) -> String {
    state
        .facts
        .desktops
        .iter()
        .find(|d| d.id == open.desktop)
        .and_then(|d| {
            let tree = d.tree()?;
            Some(super::shelf::title(
                d,
                &state.facts,
                Some(&state.window.garden),
                tree,
                &open.leaf,
            ))
        })
        .unwrap_or_default()
}

/// Types the reply into the terminal and then presses Return, each write confirmed by the
/// daemon. A failed write leaves the reply for the peek and the tile to finish.
fn deliver(state: &mut State, runtime: String, title: String, reply: Reply, tx: &mut Tx) {
    let text = reply.text().to_owned();
    state
        .window
        .desk
        .peeks
        .replies
        .insert(runtime.clone(), Reply::Sending(text.clone()));
    if matches!(reply, Reply::Typed { .. }) {
        press_return(state, runtime, title, text, tx);
        return;
    }
    let data = text.clone();
    write(
        state,
        &runtime.clone(),
        data,
        tx,
        move |state, failure, tx| match failure {
            Some(reason) => {
                state.window.desk.peeks.replies.insert(
                    runtime.clone(),
                    Reply::NotSent {
                        text,
                        reason: Some(reason.clone()),
                    },
                );
                follow_reply(state, &runtime);
                state.window.notify(
                format!("Your reply to {title} may not have arrived: {reason}. Its tile keeps the text."),
                tx,
            );
            }
            None => press_return(state, runtime, title, text, tx),
        },
    );
}

fn press_return(state: &mut State, runtime: String, title: String, text: String, tx: &mut Tx) {
    write(
        state,
        &runtime.clone(),
        "\r".to_owned(),
        tx,
        move |state, failure, tx| {
            let replies = &mut state.window.desk.peeks.replies;
            match failure {
                None => {
                    replies.remove(&runtime);
                    follow_reply(state, &runtime);
                }
                Some(reason) => {
                    replies.insert(
                        runtime.clone(),
                        Reply::Typed {
                            text,
                            reason: reason.clone(),
                        },
                    );
                    follow_reply(state, &runtime);
                    state.window.notify(
                    format!("Your reply to {title} was typed but not submitted: {reason}. Hover its entry to finish it."),
                    tx,
                );
                }
            }
        },
    );
}

/// Writes `data` to the terminal as the person's input and calls `then` with why the write
/// failed, or nothing once the daemon confirms it.
fn write(
    state: &mut State,
    runtime: &str,
    data: String,
    tx: &mut Tx,
    then: impl FnOnce(&mut State, Option<String>, &mut Tx) + 'static,
) {
    let probe = state.next_request_id();
    let input = PtyInputMessage {
        id: runtime.to_owned(),
        data,
        source: Some("user".to_owned()),
        probe_id: Some(probe.clone()),
        ..PtyInputMessage::new()
    };
    let via = crate::terminal::rides_runtime(&state.facts, runtime);
    state.ask_on(
        via,
        probe,
        &input,
        move |state: &mut State, answer: Answer, tx: &mut Tx| {
            let failure = match answer.parse::<PtyInputProbeResultMessage>() {
                Some(result) if result.success => None,
                Some(result) => Some(
                    result
                        .error
                        .unwrap_or_else(|| "the write failed".to_owned()),
                ),
                None => Some("the daemon did not answer".to_owned()),
            };
            then(state, failure, tx);
        },
        tx,
    );
}

/// Opens the peek's tile on its desktop. A drafted reply stays, unsent, in the tile.
fn open_tile(state: &mut State, tx: &mut Tx) {
    let Some(open) = state.window.desk.peeks.open.as_ref() else {
        return;
    };
    let (desktop, leaf) = (open.desktop.clone(), open.leaf.clone());
    let blocked = blocked(state, open).is_some();
    if let Some(runtime) = open.runtime.clone() {
        let replies = &mut state.window.desk.peeks.replies;
        match replies.get(&runtime).cloned() {
            Some(Reply::Typed { .. }) if blocked => retain_typed(replies, &runtime),
            Some(Reply::Typed { .. }) => {
                replies.remove(&runtime);
            }
            Some(Reply::Draft(text)) => {
                replies.insert(runtime, Reply::NotSent { text, reason: None });
            }
            _ => {}
        }
    }
    dismiss(state);
    crate::sidebar::open_tile(state, &desktop, &leaf, tx);
}

impl Peeks {
    /// The replies not sent, by terminal, for their tiles to show.
    pub(crate) fn unsent(&self) -> Vec<(String, String)> {
        let mut unsent: Vec<(String, String)> = self
            .replies
            .iter()
            .filter_map(|(runtime, reply)| match reply {
                Reply::NotSent { text, .. } => Some((runtime.clone(), text.clone())),
                _ => None,
            })
            .collect();
        unsent.sort();
        unsent
    }
}

/// The strip under an agent's terminal that keeps a reply the peek could not send.
pub(crate) fn unsent_strip(runtime: &str, text_: &str, t: &Tokens) -> El {
    let (copied, discarded) = (text_.to_owned(), runtime.to_owned());
    Bar::new(format!("tile.unsent.{runtime}"))
        .lead(
            words(
                format!("Unsent reply: {text_}"),
                Type::Small,
                t.text_secondary,
            )
            .single_line()
            .truncate(),
        )
        .push(button(
            format!("tile.unsent.{runtime}.copy"),
            "Copy",
            Keys::None,
            t,
            move |_, tx| tx.copy(copied.clone()),
        ))
        .push(button(
            format!("tile.unsent.{runtime}.discard"),
            "Discard",
            Keys::None,
            t,
            move |state, _| {
                state.window.desk.peeks.replies.remove(&discarded);
            },
        ))
        .build(t)
        .role("status")
        .fill(t.bg_panel)
}

/// The keys a button of the peek shows after its word, or only in its help when the peek is
/// too narrow to show them beside a reply field that holds its placeholder.
#[derive(Clone, Copy)]
enum Keys<'a> {
    None,
    Shown(&'a str),
    Help(&'a str),
}

/// How wide a peek stands at least to show its buttons' keys, measured on a 780-wide peek:
/// Open and Close with their keys, the prompt, the gaps and the line's insets take 214.5, and
/// "Reply without changing the layout" takes 208 in its field, 234 with the field's insets.
const KEYS_SHOWN_W: f32 = 460.0;

/// A button of the peek or the unsent strip: its word, and the key that does the same.
fn button(
    key: String,
    label: &str,
    keys: Keys,
    t: &Tokens,
    press: impl Fn(&mut State, &mut Tx) + 'static,
) -> El {
    let button = Button::new(label.to_owned())
        .look(Look::Quiet)
        .size(Size::Compact)
        .on_press_tx(press);
    let button = match keys {
        Keys::None => button,
        Keys::Shown(kbd) => button.trailing(keycaps([kbd], t)),
        Keys::Help(kbd) => button.shortcut(kbd),
    };
    button.build(t).key(key).into()
}

fn notice(reply: Option<&Reply>, blocked: Option<&str>) -> Option<String> {
    match reply {
        Some(Reply::Sending(_)) => return Some("Sending…".to_owned()),
        _ if blocked.is_some() => return blocked.map(str::to_owned),
        _ => {}
    }
    match reply? {
        Reply::Typed { reason, .. } => Some(format!(
            "The text reached the terminal, but Return did not: {reason}. ↵ sends Return."
        )),
        Reply::NotSent { reason: None, .. } => {
            Some("This reply was not sent. ↵ sends it.".to_owned())
        }
        Reply::NotSent {
            reason: Some(reason),
            ..
        } => Some(format!(
            "This reply may not have arrived: {reason}. Check the screen; ↵ sends it again."
        )),
        _ => None,
    }
}

/// The open peek, hanging below its entry within the tile area.
pub(crate) fn view(state: &State, t: &Tokens) -> Option<El> {
    let peeks = &state.window.desk.peeks;
    let open = peeks.open.as_ref()?;
    let area = state.window.desk.area()?;
    let desktop = state.facts.desktops.iter().find(|d| d.id == open.desktop)?;
    let tree = desktop.tree()?;
    let title = super::shelf::title(
        desktop,
        &state.facts,
        Some(&state.window.garden),
        tree,
        &open.leaf,
    );
    let pane = super::view::pane_of(desktop, &open.leaf);
    let agent = pane
        .and_then(|p| session_of(state, &p.session_id))
        .map(|s| AgentState::parse(&s.state));
    let kind = match tree.find_leaf(&open.leaf) {
        Some(Node::Leaf(l)) => match &l.kind {
            LeafKind::Tile { kind, .. } => Some(super::view::tile_kind_label(kind).to_owned()),
            LeafKind::Pane => None,
        },
        _ => None,
    };
    let width = (area.w * 0.62).min(780.0);
    let height = open.runtime.as_ref().map(|_| (area.h * 0.68).min(640.0));
    let x = (open.anchor[0] - area.x)
        .min(area.w - width - EDGE_PX)
        .max(EDGE_PX);
    let y = open.anchor[1] - area.y + ANCHOR_GAP_PX;
    let y = match height {
        Some(h) => y.min(area.h - h - EDGE_PX),
        None => y,
    }
    .max(EDGE_PX);
    let header = Bar::new("shelf.peek.head");
    let header = match agent {
        Some(a) => header.start(dot(
            crate::sidebar::state_color(a, t),
            DotShape::Filled,
            DotSize::Regular,
        )),
        None => header,
    };
    let header = header
        .lead(
            words(title.clone(), Type::Name, t.text_primary)
                .single_line()
                .truncate(),
        )
        .push_all(kind.map(|k| words(k, Type::Label, t.text_tertiary)))
        .build(t);
    let mut panel = col()
        .key("shelf.peek")
        .role("dialog")
        .label(title)
        .absolute(area.x + x, area.y + y)
        .w(width)
        .on_hover_change(|state: &mut State, inside, tx: &mut Tx| {
            if inside {
                enter_peek(state);
            } else {
                leave_peek(state, tx);
            }
        })
        .child(header);
    if let (Some(runtime), Some(h)) = (&open.runtime, height) {
        panel = panel
            .h(h)
            .children(terminal(state, open, runtime, width >= KEYS_SHOWN_W, t));
    }
    Some(Frame::panel(t).draw_clipped(panel))
}

fn terminal(state: &State, open: &Open, runtime: &str, roomy: bool, t: &Tokens) -> Vec<El> {
    let keys = |kbd| {
        if roomy {
            Keys::Shown(kbd)
        } else {
            Keys::Help(kbd)
        }
    };
    let peeks = &state.window.desk.peeks;
    let reply = peeks.replies.get(runtime);
    let blocked = blocked(state, open);
    let shown = match (&open.screen.text, &open.screen.failure) {
        (None, Some(failure)) => format!("Could not read this terminal: {failure}"),
        (text, _) => text.clone().unwrap_or_default(),
    };
    let screen = ScreenText::showing("shelf.peek.screen", shown, t);
    let notice = notice(reply, blocked).map(|said| {
        Bar::new("shelf.peek.notice")
            .lead(words(said.clone(), Type::Small, t.text_secondary).wrap())
            .build(t)
            .role("status")
            .label(said)
            .fill(t.bg_panel)
    });
    let field = TextInput::new(&peeks.field)
        .key(field_key(&open.leaf))
        .label("Reply")
        .read_only(matches!(
            reply,
            Some(Reply::Sending(_) | Reply::Typed { .. })
        ))
        .placeholder(if blocked.is_some() {
            "A reply cannot be sent now"
        } else {
            "Reply without changing the layout"
        })
        .on_edit(|state: &mut State| &mut state.window.desk.peeks.field)
        .on_change(|state: &mut State, _: &mut Tx| edited(state))
        .on_submit(submit)
        .on_key(|state: &mut State, ev: &rune::KeyEvent, _: &mut Tx| {
            ev.key == rune::Key::Escape && close(state)
        });
    let field: El = line_box(field, FieldState::Ready, t).into();
    let reply_row = Bar::new("shelf.peek.reply")
        .start(words("❯", Type::Mono, t.text_secondary))
        .lead(field)
        .push(button(
            "shelf.peek.open".to_owned(),
            "Open",
            keys("↵"),
            t,
            open_tile,
        ))
        .push(button(
            "shelf.peek.close".to_owned(),
            "Close",
            keys("esc"),
            t,
            |state, _| {
                close(state);
            },
        ))
        .build(t);
    let mut parts = vec![screen];
    parts.extend(notice);
    parts.push(reply_row);
    parts
}
