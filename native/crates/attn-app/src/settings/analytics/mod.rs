//! Settings → Analytics: what this machine records and what its agents vented, with the machine's
//! sharing switches. The page reads the daemon's status when it shows and a record list only
//! when one is picked, and works from the keyboard: j and k move, [ and ] switch lists, and a
//! vent's own keys act on it, each change undoable with u.

mod items;
mod view;

use std::collections::HashSet;

use attn_protocol::{
    ClearAnalyticsRecordsMessage, ClearAnalyticsRecordsResultMessage,
    ForgetAnalyticsInstallMessage, GetAnalyticsStatusMessage, GetAnalyticsStatusResultMessage,
    ListAnalyticsRecordsMessage, ListAnalyticsRecordsResultMessage, PlantVentSeedMessage,
    PlantVentSeedResultMessage, SetVentDismissedMessage, SetVentKindMessage, SetVentSendMessage,
    UnplantVentSeedMessage, Vent,
};
use rune::{Key, KeyEvent, Tx};

use super::Section;
use super::autosave;
use crate::State;
use crate::requests::Answer;
use items::{ALL, Item, SEND_QUEUED, SEND_SENT, Stored, VENT_ATTN, VENT_TOOLS, VENTS, VIEWS};
pub(super) use view::{page, toast};

/// The Tauri app's page of records per read.
const PAGE_SIZE: i64 = 200;

pub(super) const SHARING: &str = "analytics.sharing_enabled";
pub(super) const SEND_VENTS: &str = "analytics.share_vents";
pub(super) const TOOL_VENTS: &str = "analytics.tool_vents";

/// The page's place and what it read. Showing the page starts it afresh, as the Tauri page
/// mounts afresh.
#[derive(Default)]
pub(crate) struct Analytics {
    view: usize,
    selected: Option<String>,
    machine: bool,
    open_bursts: HashSet<String>,
    expanded: HashSet<String>,
    status: Option<GetAnalyticsStatusResultMessage>,
    records: Option<Records>,
    said: Option<Said>,
    confirm: Option<Confirm>,
    /// Rises with each read, so only the newest status read and records read land.
    reads: u64,
    status_read: u64,
    records_read: u64,
}

struct Records {
    list: Vec<Stored>,
    more: bool,
    loading: bool,
    error: Option<String>,
}

/// What the last action did, and how to take it back.
struct Said {
    title: String,
    detail: Option<String>,
    failed: bool,
    undo: Option<Undo>,
}

enum Undo {
    Dismissed {
        vent: String,
        dismissed: bool,
    },
    Planted {
        vent: String,
    },
    Send {
        vent: String,
        send: bool,
    },
    Kind {
        vent: String,
        was: String,
        requeue: bool,
    },
}

#[derive(Clone, Copy, PartialEq, Eq)]
enum Confirm {
    Clear,
    Forget,
    DeleteServerCopy,
}

fn shown(state: &State) -> bool {
    super::is_open(state) && state.window.settings.section == Section::Analytics
}

fn page_of(state: &mut State) -> &mut Analytics {
    &mut state.window.settings.analytics
}

/// Starts the page afresh and reads the status when the page has just come into view.
pub(super) fn arrive(state: &mut State, tx: &mut Tx) {
    if !shown(state) {
        return;
    }
    let reads = page_of(state).reads;
    *page_of(state) = Analytics {
        reads,
        ..Analytics::default()
    };
    read_status(state, tx);
    tx.focus(view::PAGE, false);
}

fn sharing(state: &State) -> bool {
    crate::setting::on(&state.facts.settings, SHARING)
}

fn read_status(state: &mut State, tx: &mut Tx) {
    let page = page_of(state);
    page.reads += 1;
    page.status_read = page.reads;
    let read = page.reads;
    state.ask(
        |request_id| GetAnalyticsStatusMessage {
            request_id,
            ..GetAnalyticsStatusMessage::new()
        },
        move |state, answer, _tx: &mut Tx| {
            let page = page_of(state);
            if page.status_read == read {
                page.status = answer
                    .outcome()
                    .ok()
                    .and_then(|reply| reply.parse::<GetAnalyticsStatusResultMessage>());
            }
        },
        tx,
    );
}

/// Reads the shown record list's first page, or its next page after what it holds.
fn read_records(state: &mut State, older: bool, tx: &mut Tx) {
    let page = page_of(state);
    if page.view == VENTS {
        return;
    }
    let before = match (older, page.records.take()) {
        (true, Some(records)) => records.list,
        _ => Vec::new(),
    };
    let before_id = before.last().map(|r| r.id);
    page.records = Some(Records {
        list: before,
        more: false,
        loading: true,
        error: None,
    });
    page.reads += 1;
    page.records_read = page.reads;
    let read = page.reads;
    let stream = (page.view != ALL).then(|| VIEWS[page.view].0.to_owned());
    state.ask(
        |request_id| ListAnalyticsRecordsMessage {
            request_id: Some(request_id),
            stream,
            before_id,
            limit: PAGE_SIZE,
            ..ListAnalyticsRecordsMessage::new()
        },
        move |state, answer, _tx: &mut Tx| {
            let page = page_of(state);
            if page.records_read != read {
                return;
            }
            let Some(records) = page.records.as_mut() else {
                return;
            };
            records.loading = false;
            match answer.outcome() {
                Ok(reply) => {
                    let rows = reply
                        .parse::<ListAnalyticsRecordsResultMessage>()
                        .and_then(|r| r.records)
                        .unwrap_or_default();
                    records.more = rows.len() as i64 == PAGE_SIZE;
                    records.list.extend(rows.into_iter().map(Stored::parse));
                    records.error = None;
                }
                Err(why) => {
                    records.more = !records.list.is_empty();
                    records.error = Some(why);
                }
            }
        },
        tx,
    );
}

fn refresh(state: &mut State, tx: &mut Tx) {
    read_status(state, tx);
    read_records(state, false, tx);
}

fn older(state: &mut State, tx: &mut Tx) {
    read_records(state, true, tx);
}

fn switch_view(state: &mut State, view: usize, tx: &mut Tx) {
    let page = page_of(state);
    page.view = view;
    page.selected = None;
    page.open_bursts.clear();
    page.records = None;
    read_records(state, false, tx);
}

fn step_view(state: &mut State, by: usize, tx: &mut Tx) {
    let view = (page_of(state).view + by) % VIEWS.len();
    switch_view(state, view, tx);
}

fn toggle_machine(state: &mut State) {
    let page = page_of(state);
    page.machine = !page.machine;
}

/// The rows the shown list holds, in order.
fn listed(state: &State) -> Vec<Item<'_>> {
    let page = &state.window.settings.analytics;
    if page.view == VENTS {
        items::vent_items(&state.facts.vents, crate::garden::seeds(state))
    } else {
        let records = page.records.as_ref().map_or(&[][..], |r| &r.list[..]);
        items::record_items(records, &page.open_bursts)
    }
}

/// The selected row's place: the selected key's, or the first row when it is gone.
fn current(items: &[Item], selected: Option<&str>) -> usize {
    selected
        .and_then(|key| items.iter().position(|item| item.key() == key))
        .unwrap_or(0)
}

fn select(state: &mut State, key: String, tx: &mut Tx) {
    page_of(state).selected = Some(key);
    tx.focus(view::PAGE, false);
    reveal(state, tx);
}

fn reveal(state: &State, tx: &mut Tx) {
    let items = listed(state);
    let index = current(&items, state.window.settings.analytics.selected.as_deref());
    tx.scroll_into_view_in(view::LIST, format!("item{index}"));
}

fn step(state: &mut State, by: isize, tx: &mut Tx) {
    let key = {
        let items = listed(state);
        if items.is_empty() {
            return;
        }
        let index = current(&items, state.window.settings.analytics.selected.as_deref());
        let next = index.saturating_add_signed(by).min(items.len() - 1);
        items[next].key()
    };
    page_of(state).selected = Some(key);
    reveal(state, tx);
}

/// Opens the burst under the cursor into its records, and selects its first.
fn expand_burst(state: &mut State, key: String, first: i64, tx: &mut Tx) {
    let page = page_of(state);
    page.open_bursts.insert(key);
    page.selected = Some(format!("record-{first}"));
    reveal(state, tx);
}

/// Picks a row: a second pick of the selected burst opens it.
fn pick(state: &mut State, key: String, tx: &mut Tx) {
    let burst = {
        let items = listed(state);
        let index = current(&items, state.window.settings.analytics.selected.as_deref());
        match items.get(index) {
            Some(Item::Burst(records)) if items[index].key() == key => Some(records[0].id),
            _ => None,
        }
    };
    match burst {
        Some(first) => {
            expand_burst(state, key, first, tx);
            tx.focus(view::PAGE, false);
        }
        None => select(state, key, tx),
    }
}

fn say(state: &mut State, title: &str, detail: Option<String>, undo: Option<Undo>) {
    page_of(state).said = Some(Said {
        title: title.to_owned(),
        detail,
        failed: false,
        undo,
    });
}

fn fail(state: &mut State, why: String) {
    page_of(state).said = Some(Said {
        title: "Could not do that".to_owned(),
        detail: Some(why),
        failed: true,
        undo: None,
    });
}

fn quoted(vent: &Vent) -> String {
    format!("“{}”", vent.context.session_label)
}

/// Sends a vent command, then runs `done` once the daemon did it, or says why it did not.
fn command<M: serde::Serialize>(
    state: &mut State,
    build: impl FnOnce(String) -> M,
    done: impl FnOnce(&mut State, attn_client::Reply, &mut Tx) + 'static,
    tx: &mut Tx,
) {
    state.ask(
        build,
        move |state, answer: Answer, tx: &mut Tx| match answer.outcome() {
            Ok(reply) => done(state, reply, tx),
            Err(why) => fail(state, why),
        },
        tx,
    );
}

fn set_dismissed(vent: String, dismissed: bool) -> impl FnOnce(String) -> SetVentDismissedMessage {
    move |request_id| SetVentDismissedMessage {
        request_id,
        vent_id: vent,
        dismissed,
        ..SetVentDismissedMessage::new()
    }
}

fn set_send(vent: String, send: bool) -> impl FnOnce(String) -> SetVentSendMessage {
    move |request_id| SetVentSendMessage {
        request_id,
        vent_id: vent,
        send,
        ..SetVentSendMessage::new()
    }
}

fn set_kind(vent: String, kind: String) -> impl FnOnce(String) -> SetVentKindMessage {
    move |request_id| SetVentKindMessage {
        request_id,
        vent_id: vent,
        kind,
        ..SetVentKindMessage::new()
    }
}

/// The next new vent after the selected one, else this vent, so dismissing moves on.
fn move_on(state: &mut State, vent: &Vent) {
    let next = {
        let items = listed(state);
        let index = current(&items, state.window.settings.analytics.selected.as_deref());
        items
            .iter()
            .skip(index + 1)
            .find_map(|item| match item {
                Item::Vent { vent, done: false } => Some(vent.id.clone()),
                _ => None,
            })
            .unwrap_or_else(|| vent.id.clone())
    };
    page_of(state).selected = Some(next);
}

fn dismiss(state: &mut State, vent: &Vent, tx: &mut Tx) {
    let dismissed = vent.dismissed_at.is_none();
    if dismissed {
        move_on(state, vent);
    }
    let (id, name) = (vent.id.clone(), quoted(vent));
    command(
        state,
        set_dismissed(id.clone(), dismissed),
        move |state, _, _| {
            let verb = if dismissed { "Dismissed" } else { "Restored" };
            let undo = Undo::Dismissed {
                vent: id,
                dismissed,
            };
            say(state, verb, Some(format!("{verb} {name}.")), Some(undo));
        },
        tx,
    );
}

fn plant(state: &mut State, vent: &Vent, tx: &mut Tx) {
    let (id, name) = (vent.id.clone(), quoted(vent));
    let moved = vent.clone();
    let vent_id = id.clone();
    command(
        state,
        move |request_id| PlantVentSeedMessage {
            request_id,
            vent_id,
            ..PlantVentSeedMessage::new()
        },
        move |state, reply, _| {
            let slug = reply
                .parse::<PlantVentSeedResultMessage>()
                .and_then(|r| r.seed)
                .map(|seed| seed.step_slug)
                .unwrap_or_default();
            move_on(state, &moved);
            say(
                state,
                "Planted",
                Some(format!("Planted {slug} from {name}.")),
                Some(Undo::Planted { vent: id }),
            );
        },
        tx,
    );
}

fn send(state: &mut State, vent: &Vent, tx: &mut Tx) {
    let send = vent.send.is_none();
    let (id, name) = (vent.id.clone(), quoted(vent));
    command(
        state,
        set_send(id.clone(), send),
        move |state, _, _| {
            let (title, detail) = if send {
                ("Sending", format!("Sending {name} with the next batch."))
            } else {
                ("Kept here", format!("Keeping {name} here."))
            };
            say(
                state,
                title,
                Some(detail),
                Some(Undo::Send { vent: id, send }),
            );
        },
        tx,
    );
}

fn file(state: &mut State, vent: &Vent, tx: &mut Tx) {
    let to_tools = vent.kind != VENT_TOOLS;
    let kind = if to_tools { VENT_TOOLS } else { VENT_ATTN };
    let (id, name) = (vent.id.clone(), quoted(vent));
    let undo = Undo::Kind {
        vent: id.clone(),
        was: vent.kind.clone(),
        requeue: vent.send.as_deref() == Some(SEND_QUEUED),
    };
    command(
        state,
        set_kind(id, kind.to_owned()),
        move |state, _, _| {
            let (title, detail) = if to_tools {
                (
                    "Filed under tools",
                    format!("Filed {name} under tools; it stays on this machine."),
                )
            } else {
                ("Filed under attn", format!("Filed {name} under attn."))
            };
            say(state, title, Some(detail), Some(undo));
        },
        tx,
    );
}

fn undone(state: &mut State, _: attn_client::Reply, _: &mut Tx) {
    say(state, "Undone.", None, None);
}

/// Takes back the last vent change, when it can be.
fn undo(state: &mut State, tx: &mut Tx) -> bool {
    let Some(undo) = page_of(state).said.take().and_then(|said| said.undo) else {
        return false;
    };
    match undo {
        Undo::Dismissed { vent, dismissed } => {
            command(state, set_dismissed(vent, !dismissed), undone, tx)
        }
        Undo::Planted { vent } => command(
            state,
            |request_id| UnplantVentSeedMessage {
                request_id,
                vent_id: vent,
                ..UnplantVentSeedMessage::new()
            },
            undone,
            tx,
        ),
        Undo::Send { vent, send } => command(state, set_send(vent, !send), undone, tx),
        Undo::Kind { vent, was, requeue } => command(
            state,
            set_kind(vent.clone(), was),
            move |state, reply, tx| {
                if requeue {
                    command(state, set_send(vent, true), undone, tx);
                } else {
                    undone(state, reply, tx);
                }
            },
            tx,
        ),
    }
    true
}

fn open_session(state: &mut State, vent: &Vent, tx: &mut Tx) {
    let session = vent.session_id.clone();
    super::close(state, tx);
    crate::sidebar::open_agent(state, &session, tx);
}

fn open_seed(state: &mut State, seed: &str, tx: &mut Tx) {
    super::close(state, tx);
    crate::asks::open_seed(state, seed, tx);
}

fn open_transcript(state: &mut State, path: &str, tx: &mut Tx) {
    if let Err(why) = tx.open_uri(&attn_term::links::file_url(path)) {
        fail(state, format!("Could not open {path}: {why}"));
    }
}

fn is_live(state: &State, vent: &Vent) -> bool {
    state.facts.sessions.iter().any(|s| s.id == vent.session_id)
}

/// Runs a vent's key on it: true when the key is one the vent takes now.
fn act(state: &mut State, key: &str, vent: &Vent, tx: &mut Tx) -> bool {
    let withdrawn = vent.withdrawn_at.is_some();
    match key {
        "o" if is_live(state, vent) => open_session(state, vent, tx),
        "t" => match &vent.context.transcript {
            Some(transcript) => open_transcript(state, &transcript.path, tx),
            None => return false,
        },
        "g" => match &vent.context.seed_id {
            Some(seed) => open_seed(state, seed, tx),
            None => return false,
        },
        "p" if !withdrawn && vent.planted_seed_id.is_none() => plant(state, vent, tx),
        "d" if !withdrawn => dismiss(state, vent, tx),
        "x" if items::destination(vent, sharing(state)).movable => send(state, vent, tx),
        "s" if vent.send.as_deref() != Some(SEND_SENT) => file(state, vent, tx),
        _ => return false,
    }
    true
}

/// The selected vent, when the selected row is one.
fn selected_vent(state: &State) -> Option<Vent> {
    let items = listed(state);
    let index = current(&items, state.window.settings.analytics.selected.as_deref());
    match items.get(index)? {
        Item::Vent { vent, .. } => Some((*vent).clone()),
        _ => None,
    }
}

fn act_on_selected(state: &mut State, key: &str, tx: &mut Tx) -> bool {
    match selected_vent(state) {
        Some(vent) => act(state, key, &vent, tx),
        None => false,
    }
}

fn copy_record(state: &mut State, tx: &mut Tx) -> bool {
    let raw = {
        let items = listed(state);
        let index = current(&items, state.window.settings.analytics.selected.as_deref());
        match items.get(index) {
            Some(Item::Record(record)) => record.raw.clone(),
            Some(Item::Burst(records)) => records
                .iter()
                .map(|r| r.raw.as_str())
                .collect::<Vec<_>>()
                .join("\n"),
            _ => return false,
        }
    };
    tx.copy(raw);
    say(state, "Copied the record JSON.", None, None);
    true
}

fn enter(state: &mut State, tx: &mut Tx) -> bool {
    let burst = {
        let items = listed(state);
        let index = current(&items, state.window.settings.analytics.selected.as_deref());
        match items.get(index) {
            Some(item @ Item::Burst(records)) => Some((item.key(), records[0].id)),
            _ => None,
        }
    };
    let Some((key, first)) = burst else {
        return false;
    };
    expand_burst(state, key, first, tx);
    true
}

/// The page's keys, while the focus is on the page or a control in it.
fn key(state: &mut State, ev: &KeyEvent, tx: &mut Tx) -> bool {
    if ev.mods.cmd || ev.mods.ctrl || ev.mods.alt {
        return false;
    }
    match &ev.key {
        Key::Down => step(state, 1, tx),
        Key::Up => step(state, -1, tx),
        Key::Enter => return enter(state, tx),
        Key::Text(typed) => match typed.as_str() {
            "j" => step(state, 1, tx),
            "k" => step(state, -1, tx),
            "]" => step_view(state, 1, tx),
            "[" => step_view(state, VIEWS.len() - 1, tx),
            "m" => toggle_machine(state),
            "r" => refresh(state, tx),
            "u" => return undo(state, tx),
            "c" => return copy_record(state, tx),
            other => return act_on_selected(state, other, tx),
        },
        _ => return false,
    }
    true
}

/// Saves a machine switch and reads the status again, as the Tauri page does.
fn set(state: &mut State, key: &str, on: bool, tx: &mut Tx) {
    autosave::apply(state, key, if on { "true" } else { "false" }, tx);
    read_status(state, tx);
}

fn toggle_sharing(state: &mut State, tx: &mut Tx) {
    let was = sharing(state);
    set(state, SHARING, !was, tx);
    let sent_before = page_of(state)
        .status
        .as_ref()
        .is_some_and(|s| s.last_sent_at.is_some());
    page_of(state).confirm = (was && sent_before).then_some(Confirm::DeleteServerCopy);
}

fn clear(state: &mut State, tx: &mut Tx) {
    page_of(state).confirm = None;
    command(
        state,
        |request_id| ClearAnalyticsRecordsMessage {
            request_id,
            ..ClearAnalyticsRecordsMessage::new()
        },
        |state, reply, tx| {
            let cleared = reply.parse::<ClearAnalyticsRecordsResultMessage>();
            let (records, vents) = cleared.map_or((0, 0), |c| (c.records, c.vents));
            let detail = format!(
                "Cleared {} and {vents} dismissed vent{}.",
                items::record_count(records),
                if vents == 1 { "" } else { "s" }
            );
            say(state, "Cleared", Some(detail), None);
            refresh(state, tx);
        },
        tx,
    );
}

fn forget(state: &mut State, tx: &mut Tx) {
    page_of(state).confirm = None;
    command(
        state,
        |request_id| ForgetAnalyticsInstallMessage {
            request_id,
            ..ForgetAnalyticsInstallMessage::new()
        },
        |state, _, tx| {
            say(
                state,
                "Forgot this install",
                Some("Forgot this install. The next upload starts a new one.".into()),
                None,
            );
            refresh(state, tx);
        },
        tx,
    );
}
