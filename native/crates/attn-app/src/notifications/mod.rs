//! The notifications panel: the selected profile's feed, opened from the title bar's bell, the
//! sidebar's critical strip or the palette, and each notification's actions.

mod view;

use std::collections::BTreeSet;

use attn_protocol::{
    DeclineUpdateMessage, InstallCodexUpdateMessage, Notification, NotificationListMessage,
    NotificationListResultMessage, NotificationMarkReadMessage, StartUpdateMessage,
    TaskRetryMessage,
};
use rune::Tx;

use crate::State;
use crate::requests::{Answer, Refused};
use crate::shell::Layer;

pub(crate) use view::{critical_strip, panel, title_button};

#[derive(Default)]
pub(crate) struct Panel {
    /// The feed as last read; `None` until a read for this profile answers.
    list: Option<Vec<Notification>>,
    error: Option<String>,
    epoch: u64,
    /// The feed changes the shown list has taken in.
    seen: u64,
    profile: String,
    expanded: Option<String>,
    diagnostic: bool,
    retrying: BTreeSet<String>,
    installing_codex: bool,
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub(crate) enum Act {
    Retry { notification: String, task: String },
    OpenSession(String),
    OpenSeed(String),
    InstallCodexUpdate,
    StartUpdate(String),
    DeclineUpdate(String),
}

impl Act {
    fn of(n: &Notification, kind: &str, target: &str) -> Option<Act> {
        let target = target.to_owned();
        Some(match kind {
            "retry_task" => Act::Retry {
                notification: n.id.clone(),
                task: target,
            },
            "open_session" => Act::OpenSession(target),
            "open_seed" => Act::OpenSeed(target),
            "install_codex_update" => Act::InstallCodexUpdate,
            "start_update" => Act::StartUpdate(target),
            "decline_update" => Act::DeclineUpdate(target),
            _ => return None,
        })
    }
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(crate) enum Severity {
    Info,
    Warning,
    Critical,
}

impl Severity {
    /// A severity this app does not know shows as info.
    fn of(n: &Notification) -> Severity {
        match n.severity.as_str() {
            "critical" => Severity::Critical,
            "warning" => Severity::Warning,
            _ => Severity::Info,
        }
    }

    fn word(self) -> &'static str {
        match self {
            Severity::Info => "info",
            Severity::Warning => "warning",
            Severity::Critical => "critical",
        }
    }
}

pub(crate) fn is_open(state: &State) -> bool {
    state.window.layer == Some(Layer::Notifications)
}

pub(crate) fn toggle(state: &mut State, tx: &mut Tx) {
    if is_open(state) {
        close(state, tx);
    } else {
        open(state, tx);
    }
}

pub(crate) fn open(state: &mut State, tx: &mut Tx) {
    if is_open(state) {
        return;
    }
    state.window.layer = Some(Layer::Notifications);
    tx.focus(view::PANEL, false);
    read(state, tx);
}

pub(crate) fn close(state: &mut State, tx: &mut Tx) {
    if !is_open(state) {
        return;
    }
    let panel = &mut state.window.notifications;
    panel.epoch += 1;
    panel.expanded = None;
    state.window.layer = None;
    crate::picker::refocus_inline(state, tx);
}

/// Seeds the counts on each connect, reads the open feed again when it changed, and forgets
/// another profile's feed.
pub(crate) fn follow(state: &mut State, was_connected: bool, tx: &mut Tx) {
    let connected = state.facts.connection == attn_client::Connection::Connected;
    if connected && !was_connected {
        state.ask(
            |request_id| NotificationListMessage {
                request_id: Some(request_id),
                ..NotificationListMessage::new()
            },
            |_, _, _| {},
            tx,
        );
    }
    let profile = &state.facts.profiles.selected;
    let panel = &mut state.window.notifications;
    if panel.profile != *profile {
        panel.profile = profile.clone();
        panel.list = None;
        panel.error = None;
        panel.expanded = None;
        panel.epoch += 1;
    }
    if !is_open(state) || !connected {
        return;
    }
    if !was_connected || state.window.notifications.seen != state.facts.notifications.changes {
        read(state, tx);
    }
}

fn read(state: &mut State, tx: &mut Tx) {
    let panel = &mut state.window.notifications;
    panel.epoch += 1;
    panel.seen = state.facts.notifications.changes;
    let epoch = panel.epoch;
    state.ask(
        |request_id| NotificationListMessage {
            request_id: Some(request_id),
            ..NotificationListMessage::new()
        },
        move |state, answer, _| {
            let panel = &mut state.window.notifications;
            if panel.epoch != epoch {
                return;
            }
            let listed = answer.parse::<NotificationListResultMessage>();
            match listed.filter(|r| r.success) {
                Some(listed) => {
                    panel.list = Some(listed.notifications.unwrap_or_default());
                    panel.error = None;
                }
                None => panel.error = Some(failure(answer)),
            }
        },
        tx,
    );
}

fn failure(answer: Answer) -> String {
    match answer.outcome() {
        Err(why) => why,
        Ok(_) => "Could not load notifications".into(),
    }
}

/// Opens a notification's detail, or closes it, and marks it read.
pub(crate) fn toggle_row(state: &mut State, id: &str, tx: &mut Tx) {
    let panel = &mut state.window.notifications;
    if panel.expanded.as_deref() == Some(id) {
        panel.expanded = None;
        return;
    }
    panel.expanded = Some(id.to_owned());
    panel.diagnostic = false;
    let unread = panel
        .list
        .iter()
        .flatten()
        .any(|n| n.id == id && n.read_at.is_empty());
    if unread {
        mark_read(state, Some(id.to_owned()), tx);
    }
}

pub(crate) fn toggle_diagnostic(state: &mut State) {
    let panel = &mut state.window.notifications;
    panel.diagnostic = !panel.diagnostic;
}

pub(crate) fn mark_all_read(state: &mut State, tx: &mut Tx) {
    mark_read(state, None, tx);
}

fn mark_read(state: &mut State, notification_id: Option<String>, tx: &mut Tx) {
    state.act(
        |request_id| NotificationMarkReadMessage {
            request_id: Some(request_id),
            notification_id,
            ..NotificationMarkReadMessage::new()
        },
        Refused::Quiet,
        |_, _, _| {},
        tx,
    );
}

pub(crate) fn run(state: &mut State, act: Act, tx: &mut Tx) {
    match act {
        Act::Retry { notification, task } => {
            if !state
                .window
                .notifications
                .retrying
                .insert(notification.clone())
            {
                return;
            }
            state.ask(
                |request_id| TaskRetryMessage {
                    request_id: Some(request_id),
                    task_id: task,
                    ..TaskRetryMessage::new()
                },
                move |state, _, _| {
                    state.window.notifications.retrying.remove(&notification);
                },
                tx,
            );
        }
        Act::OpenSession(session) => {
            close(state, tx);
            crate::sidebar::open_agent(state, &session, tx);
        }
        Act::OpenSeed(seed) => {
            close(state, tx);
            crate::asks::open_seed(state, &seed, tx);
        }
        Act::InstallCodexUpdate => install_codex_update(state, tx),
        Act::StartUpdate(update) => state.act(
            |request_id| StartUpdateMessage {
                request_id: Some(request_id),
                update_request_id: Some(update),
                ..StartUpdateMessage::new()
            },
            Refused::Bare,
            |_, _, _| {},
            tx,
        ),
        Act::DeclineUpdate(update) => state.act(
            |request_id| DeclineUpdateMessage {
                request_id: Some(request_id),
                update_request_id: update,
                ..DeclineUpdateMessage::new()
            },
            Refused::Bare,
            |_, _, _| {},
            tx,
        ),
    }
}

fn install_codex_update(state: &mut State, tx: &mut Tx) {
    let Some(desktop) = state.facts.current_desktop().map(|d| d.id.clone()) else {
        return;
    };
    if state.window.notifications.installing_codex {
        return;
    }
    close(state, tx);
    state.window.notifications.installing_codex = true;
    state.ask(
        |request_id| InstallCodexUpdateMessage {
            request_id: Some(request_id),
            desktop_id: desktop,
            ..InstallCodexUpdateMessage::new()
        },
        |state, answer, tx| {
            state.window.notifications.installing_codex = false;
            if let Err(why) = answer.outcome() {
                state
                    .window
                    .notify(format!("Could not install the Codex update: {why}"), tx);
            }
        },
        tx,
    );
}
