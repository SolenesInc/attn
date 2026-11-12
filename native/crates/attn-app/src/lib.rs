mod connection;

use attn_client::{Connection, Facts, Slices, Store, Subscription};
use rune::tok::{self, type_};
use rune::{AppSpec, Cx, El, Tx, col, memo, text};

/// A change for a window's state, made on the link's thread and run on the UI thread.
pub type Update = Box<dyn FnOnce(&mut State, &mut Tx) + Send>;

#[derive(Default)]
pub struct State {
    pub facts: Facts,
}

const READS: Slices = Slices::CONNECTION;

/// Hands the window every change to a slice it reads, through `deliver`: `AppHandle::update`
/// in the app, the world's inbox in a wire test.
pub fn bind(store: &Store, deliver: impl Fn(Update) + Send + Sync + 'static) -> Subscription {
    store.subscribe(READS, move |facts, _| {
        let facts = facts.clone();
        deliver(Box::new(move |state, _| state.facts = facts));
    })
}

pub fn view(state: &State, _: &mut Cx<State>) -> El {
    let theme = tok::theme(true);
    let connection = &state.facts.connection;
    let screen = memo("screen", connection, || {
        col().child(match connection {
            Connection::Connected | Connection::Reconnecting => text("attn")
                .key("placeholder")
                .font(type_::TITLE)
                .color(theme.text),
            Connection::Connecting => connection::connecting(&theme),
            Connection::Refused(refusal) => connection::refused(&theme, refusal),
        })
    });
    let banner =
        (*connection == Connection::Reconnecting).then(|| connection::reconnecting(&theme));
    col()
        .w_full()
        .h_full()
        .fill(theme.bg)
        .children(banner)
        .child(col().w_full().grow(1.0).center().child(screen))
}

pub fn app() -> AppSpec<State> {
    AppSpec::new("attn", (960.0, 640.0), State::default, view)
}
