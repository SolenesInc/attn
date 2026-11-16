use std::collections::HashMap;
use std::sync::{Arc, Mutex};

use attn_client::Reply;
use rune::Tx;
use serde::Serialize;

use crate::State;

pub(crate) type Then<S = State> = Box<dyn FnOnce(&mut S, Answer, &mut Tx)>;

/// The daemon's reply to a request, or why there is none.
pub(crate) enum Answer {
    Reply(Reply),
    NotSent,
    Dropped,
}

impl Answer {
    /// The reply as the protocol type its event names; nothing when the request went unanswered.
    pub(crate) fn parse<T: serde::de::DeserializeOwned>(&self) -> Option<T> {
        match self {
            Answer::Reply(reply) => reply.parse(),
            Answer::NotSent | Answer::Dropped => None,
        }
    }

    /// The reply when the daemon did what the request asked, or why it did not: the `error` of a
    /// reply whose `success` is false, a send that never left, or a connection that ended first.
    pub(crate) fn outcome(self) -> Result<Reply, String> {
        match self {
            Answer::Reply(reply) if reply.body["success"] == true => Ok(reply),
            Answer::Reply(reply) => Err(reply.body["error"]
                .as_str()
                .filter(|e| !e.is_empty())
                .map_or_else(|| format!("{} failed", reply.event), str::to_owned)),
            Answer::NotSent => Err("not connected to the daemon".into()),
            Answer::Dropped => Err("the daemon did not answer".into()),
        }
    }
}

/// How the window names a refused command in its notice: `Prefixed("Could not close that tile")`
/// shows "Could not close that tile: <reason>", `Bare` the reason alone, and `Quiet` nothing, for
/// a command whose refusal the Tauri app does not show either.
#[derive(Clone, Copy)]
pub(crate) enum Refused {
    Prefixed(&'static str),
    Bare,
    Quiet,
}

impl Refused {
    fn say(self, reason: String) -> Option<String> {
        match self {
            Refused::Prefixed(what) => Some(format!("{what}: {reason}")),
            Refused::Bare => Some(reason),
            Refused::Quiet => None,
        }
    }
}

/// What the link hears in answer to the window's requests, waiting for the window's next frame.
#[derive(Clone, Default)]
pub struct ReplyInbox(Arc<Mutex<Vec<Reply>>>);

impl ReplyInbox {
    /// The link's reply sink: queues each reply and calls `wake` so the window takes it.
    pub fn sink(
        &self,
        wake: impl Fn() + Send + Sync + 'static,
    ) -> Box<dyn Fn(Reply) + Send + Sync> {
        let queue = self.0.clone();
        Box::new(move |reply| {
            queue.lock().unwrap().push(reply);
            wake();
        })
    }

    pub fn is_empty(&self) -> bool {
        self.0.lock().unwrap().is_empty()
    }

    pub(crate) fn put(&self, replies: Vec<Reply>) {
        self.0.lock().unwrap().extend(replies);
    }

    pub(crate) fn take(&self) -> Vec<Reply> {
        std::mem::take(&mut *self.0.lock().unwrap())
    }
}

/// A window's requests that wait for the daemon's answer, by request id: the main and tile
/// windows', or Quick Capture's, whose state is `S`.
pub struct Requests<S = State> {
    pub(crate) inbox: ReplyInbox,
    waiting: HashMap<String, Waiting<S>>,
    sent: u64,
}

impl<S> Default for Requests<S> {
    fn default() -> Self {
        Requests {
            inbox: ReplyInbox::default(),
            waiting: HashMap::new(),
            sent: 0,
        }
    }
}

/// A request waiting for its answer, with the command it sent, so a `command_error` that names
/// only the command answers the oldest one.
struct Waiting<S> {
    command: serde_json::Value,
    sent: u64,
    then: Then<S>,
}

impl<S> Requests<S> {
    /// Waits for the answer to `command`, sent under `id`.
    pub(crate) fn wait(&mut self, id: String, command: &impl Serialize, then: Then<S>) {
        self.sent += 1;
        let waiting = Waiting {
            command: serde_json::to_value(command).unwrap_or_default(),
            sent: self.sent,
            then,
        };
        self.waiting.insert(id, waiting);
    }

    /// What waits for `reply`: the request it names, or for a `command_error`, the oldest
    /// request with its command.
    pub(crate) fn answered_by(&mut self, reply: &Reply) -> Option<Then<S>> {
        let id = if reply.event == "command_error" {
            oldest_waiting(self, reply.body["cmd"].as_str().unwrap_or_default())
        } else {
            Some(reply.request_id.clone())
        };
        id.and_then(|id| self.waiting.remove(&id)).map(|w| w.then)
    }

    /// Every request still waiting, which a connection that ended will never answer.
    pub(crate) fn unanswerable(&mut self) -> Vec<Then<S>> {
        self.waiting
            .drain()
            .map(|(_, waiting)| waiting.then)
            .collect()
    }
}

impl State {
    /// Sends the command `build` makes with a fresh request id, and runs `then` on the window
    /// with the reply that names it. A request the link cannot send is answered with nothing
    /// at once, and one the connection drops when the connection ends.
    pub(crate) fn ask<M: Serialize>(
        &mut self,
        build: impl FnOnce(String) -> M,
        then: impl FnOnce(&mut State, Answer, &mut Tx) + 'static,
        tx: &mut Tx,
    ) {
        let id = self.next_request_id();
        let command = build(id.clone());
        self.ask_as(id, &command, then, tx);
    }

    /// An id no other request has carried, from any window or run of the app, since the daemon
    /// keeps the ids it has answered.
    pub(crate) fn next_request_id(&self) -> String {
        format!("native-{}", uuid::Uuid::new_v4())
    }

    /// Sends `command`, whose answer names `id`, and runs `then` with that answer.
    pub(crate) fn ask_as(
        &mut self,
        id: String,
        command: &impl Serialize,
        then: impl FnOnce(&mut State, Answer, &mut Tx) + 'static,
        tx: &mut Tx,
    ) {
        match self.daemon.send(command) {
            Ok(()) => self.requests.wait(id, command, Box::new(then)),
            Err(_) => then(self, Answer::NotSent, tx),
        }
    }

    /// Sends the command `build` makes and runs `done` with the daemon's reply when it did what
    /// was asked. A refusal, a failed send or an unanswered request shows in the window's notice,
    /// worded as `refused` says.
    pub(crate) fn act<M: Serialize>(
        &mut self,
        build: impl FnOnce(String) -> M,
        refused: Refused,
        done: impl FnOnce(&mut State, Reply, &mut Tx) + 'static,
        tx: &mut Tx,
    ) {
        let id = self.next_request_id();
        let command = build(id.clone());
        self.act_as(id, &command, refused, done, tx);
    }

    /// As `act`, for a command whose answer names `id`.
    pub(crate) fn act_as(
        &mut self,
        id: String,
        command: &impl Serialize,
        refused: Refused,
        done: impl FnOnce(&mut State, Reply, &mut Tx) + 'static,
        tx: &mut Tx,
    ) {
        self.ask_as(
            id,
            command,
            move |state, answer, tx| match answer.outcome() {
                Ok(reply) => done(state, reply, tx),
                Err(reason) => {
                    if let Some(text) = refused.say(reason) {
                        state.window.notify(text, tx);
                    }
                }
            },
            tx,
        );
    }
}

impl<S> Requests<S> {
    /// Whether a request whose command `asks` is true of still waits for its answer.
    pub(crate) fn waits_for(&self, asks: impl Fn(&serde_json::Value) -> bool) -> bool {
        self.waiting.values().any(|w| asks(&w.command))
    }
}

/// Runs the answer of each reply that came in. A `command_error` answers the oldest request
/// waiting with its command, and shows in the window's notice when none waits. The main window
/// hands the replies it has no use for to its tile windows, whose requests the daemon answers on
/// the main window's link. Returns whether any reply was answered or shown.
pub(crate) fn drain(state: &mut State, tx: &mut Tx) -> bool {
    let replies = state.requests.inbox.take();
    let mut answered = false;
    let mut unclaimed = Vec::new();
    for reply in replies {
        if crate::callouts::receive(state, &reply, tx) || crate::browser::receive(state, &reply, tx)
        {
            answered = true;
            continue;
        }
        if let Some(then) = state.requests.answered_by(&reply) {
            then(state, Answer::Reply(reply), tx);
            answered = true;
        } else if crate::settings::outposts::refused(state, &reply)
            || crate::desktop::refusal::receive(state, &reply, tx)
        {
            answered = true;
        } else if let Some(text) = unasked_refusal(&reply) {
            state.window.notify(text, tx);
            answered = true;
        } else {
            unclaimed.push(reply);
        }
    }
    if state.window.tile.is_none() {
        crate::quick_capture::hand(state, &unclaimed);
        state.window.desk.windows.hand(unclaimed);
    }
    answered
}

fn oldest_waiting<S>(requests: &Requests<S>, cmd: &str) -> Option<String> {
    requests
        .waiting
        .iter()
        .filter(|(_, w)| !cmd.is_empty() && w.command["cmd"] == cmd)
        .min_by_key(|(_, w)| w.sent)
        .map(|(id, _)| id.clone())
}

/// What the window says about a `command_error` no request waits for, in the Tauri app's
/// words; nothing for a command the Tauri app reports only to its console.
fn unasked_refusal(reply: &Reply) -> Option<String> {
    if reply.event != "command_error" {
        return None;
    }
    let cmd = reply.body["cmd"].as_str().unwrap_or_default();
    let error = reply.body["error"].as_str().unwrap_or_default();
    let or_failed = || {
        if error.is_empty() {
            format!("Command {cmd} failed")
        } else {
            error.to_owned()
        }
    };
    match cmd {
        "unregister" => Some(format!("Could not close that session: {}", or_failed())),
        "settle_turn" | "snooze_turn" | "standby_session" | "wake_turn" | "cancel_countdown"
        | "spawn_session" => Some(or_failed()),
        _ => None,
    }
}

/// A connection that ends takes its requests' replies with it: each request is answered with
/// nothing.
pub(crate) fn forget(state: &mut State, tx: &mut Tx) {
    for then in state.requests.unanswerable() {
        then(state, Answer::Dropped, tx);
    }
}

/// Replies the main window handed this tile window, answered on its next frame.
pub(crate) fn receive(state: &mut State, replies: Vec<Reply>) {
    state.requests.inbox.put(replies);
}
