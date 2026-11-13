use std::collections::HashMap;
use std::sync::{Arc, Mutex};

use attn_client::Reply;
use rune::Tx;
use serde::Serialize;

use crate::State;

type Then = Box<dyn FnOnce(&mut State, Answer, &mut Tx)>;

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

    fn take(&self) -> Vec<Reply> {
        std::mem::take(&mut *self.0.lock().unwrap())
    }
}

/// The window's requests that wait for the daemon's answer, by request id.
#[derive(Default)]
pub struct Requests {
    pub(crate) inbox: ReplyInbox,
    waiting: HashMap<String, Waiting>,
    next: u64,
    sent: u64,
}

/// A request waiting for its answer, with the command it sent, so a `command_error` that names
/// only the command answers the oldest one.
struct Waiting {
    command: serde_json::Value,
    sent: u64,
    then: Then,
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
        self.requests.next += 1;
        let id = format!("native-{}", self.requests.next);
        let command = build(id.clone());
        self.ask_as(id, &command, then, tx);
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
            Ok(()) => {
                self.requests.sent += 1;
                let waiting = Waiting {
                    command: serde_json::to_value(command).unwrap_or_default(),
                    sent: self.requests.sent,
                    then: Box::new(then),
                };
                self.requests.waiting.insert(id, waiting);
            }
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
        self.requests.next += 1;
        let id = format!("native-{}", self.requests.next);
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

impl Requests {
    /// Whether a request whose command `asks` is true of still waits for its answer.
    pub(crate) fn waits_for(&self, asks: impl Fn(&serde_json::Value) -> bool) -> bool {
        self.waiting.values().any(|w| asks(&w.command))
    }
}

/// Runs the answer of each reply that came in. A `command_error` answers the oldest request
/// waiting with its command, and shows in the window's notice when none waits. Returns whether
/// any reply did either.
pub(crate) fn drain(state: &mut State, tx: &mut Tx) -> bool {
    let replies = state.requests.inbox.take();
    let mut answered = false;
    for reply in replies {
        let waiting = if reply.event == "command_error" {
            oldest_waiting(
                &state.requests,
                reply.body["cmd"].as_str().unwrap_or_default(),
            )
        } else {
            Some(reply.request_id.clone())
        };
        if let Some(waiting) = waiting.and_then(|id| state.requests.waiting.remove(&id)) {
            (waiting.then)(state, Answer::Reply(reply), tx);
            answered = true;
        } else if let Some(text) = unasked_refusal(&reply) {
            state.window.notify(text, tx);
            answered = true;
        }
    }
    answered
}

fn oldest_waiting(requests: &Requests, cmd: &str) -> Option<String> {
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
    let unanswered: Vec<Then> = state
        .requests
        .waiting
        .drain()
        .map(|(_, waiting)| waiting.then)
        .collect();
    for then in unanswered {
        then(state, Answer::Dropped, tx);
    }
}
