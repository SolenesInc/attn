use std::collections::HashMap;
use std::sync::{Arc, Mutex};

use attn_client::Reply;
use rune::Tx;
use serde::Serialize;

use crate::State;

type Then = Box<dyn FnOnce(&mut State, Answer, &mut Tx)>;

/// The daemon's reply to a request, or nothing when the request never reached the daemon or the
/// connection carrying it ended before the reply.
pub(crate) struct Answer(Option<Reply>);

impl Answer {
    /// The reply as the protocol type its event names; nothing when the request went unanswered.
    pub(crate) fn parse<T: serde::de::DeserializeOwned>(&self) -> Option<T> {
        self.0.as_ref()?.parse()
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
    waiting: HashMap<String, Then>,
    next: u64,
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
                self.requests.waiting.insert(id, Box::new(then));
            }
            Err(_) => then(self, Answer(None), tx),
        }
    }
}

/// Runs the answer of each reply that came in. Returns whether any did.
pub(crate) fn drain(state: &mut State, tx: &mut Tx) -> bool {
    let replies = state.requests.inbox.take();
    let mut answered = false;
    for reply in replies {
        if let Some(then) = state.requests.waiting.remove(&reply.request_id) {
            then(state, Answer(Some(reply)), tx);
            answered = true;
        }
    }
    answered
}

/// A connection that ends takes its requests' replies with it: each request is answered with
/// nothing.
pub(crate) fn forget(state: &mut State, tx: &mut Tx) {
    let unanswered: Vec<Then> = state
        .requests
        .waiting
        .drain()
        .map(|(_, then)| then)
        .collect();
    for then in unanswered {
        then(state, Answer(None), tx);
    }
}
