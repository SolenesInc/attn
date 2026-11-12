use std::collections::HashMap;
use std::sync::{Arc, Mutex};

use attn_client::Reply;
use rune::Tx;
use serde::Serialize;

use crate::State;

type Answer = Box<dyn FnOnce(&mut State, Reply, &mut Tx)>;

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
    waiting: HashMap<String, Answer>,
    next: u64,
}

impl State {
    /// Sends the command `build` makes with a fresh request id, and runs `then` on the window
    /// with the reply that names it. A request the link cannot send is never answered.
    pub(crate) fn ask<M: Serialize>(
        &mut self,
        build: impl FnOnce(String) -> M,
        then: impl FnOnce(&mut State, Reply, &mut Tx) + 'static,
    ) {
        self.requests.next += 1;
        let id = format!("native-{}", self.requests.next);
        let command = build(id.clone());
        self.ask_as(id, &command, then);
    }

    /// Sends `command`, whose answer names `id`, and runs `then` with that answer.
    pub(crate) fn ask_as(
        &mut self,
        id: String,
        command: &impl Serialize,
        then: impl FnOnce(&mut State, Reply, &mut Tx) + 'static,
    ) {
        if self.daemon.send(command).is_ok() {
            self.requests.waiting.insert(id, Box::new(then));
        }
    }
}

/// Runs the answer of each reply that came in. Returns whether any did.
pub(crate) fn drain(state: &mut State, tx: &mut Tx) -> bool {
    let replies = state.requests.inbox.take();
    let mut answered = false;
    for reply in replies {
        if let Some(answer) = state.requests.waiting.remove(&reply.request_id) {
            answer(state, reply, tx);
            answered = true;
        }
    }
    answered
}

/// A connection that ends takes its requests' answers with it.
pub(crate) fn forget(state: &mut State) {
    state.requests.waiting.clear();
}
