//! Tile windows in a wire test: the app opens, places and hides them through an opener that
//! hands each request to the world, which keeps a windowless runtime for each.

use std::sync::{Arc, Mutex};

use attn_app::State;
use attn_app::desktop::layout::Frame;
use attn_app::desktop::{Opened, Opener, Setup};
use attn_client::{Pulse, Store, Subscription};
use rune::{Env, Runtime};

use crate::screen::Screen;

/// What the app asked of its tile windows since the world last looked.
pub(crate) enum Order {
    Open {
        key: String,
        frame: Frame,
        setup: Setup,
    },
    Place {
        key: String,
        frame: Frame,
    },
    Hide {
        key: String,
    },
    Update {
        key: String,
        update: attn_app::Update,
    },
}

#[derive(Clone)]
pub(crate) struct Orders {
    pub list: Arc<Mutex<Vec<Order>>>,
    pub pulse: Pulse,
}

impl Orders {
    fn push(&self, order: Order) {
        self.list.lock().unwrap().push(order);
        self.pulse.beat();
    }
}

pub(crate) struct HeadlessOpener(pub Orders);

struct HeadlessWindow {
    key: String,
    orders: Orders,
}

impl Opener for HeadlessOpener {
    fn open(&self, key: &str, frame: &Frame, setup: Setup) -> Box<dyn Opened> {
        self.0.push(Order::Open {
            key: key.to_owned(),
            frame: *frame,
            setup,
        });
        Box::new(HeadlessWindow {
            key: key.to_owned(),
            orders: self.0.clone(),
        })
    }
}

impl Opened for HeadlessWindow {
    fn place(&self, frame: &Frame) {
        self.orders.push(Order::Place {
            key: self.key.clone(),
            frame: *frame,
        });
    }

    fn hide(&self) {
        self.orders.push(Order::Hide {
            key: self.key.clone(),
        });
    }

    fn update(&self, update: attn_app::Update) {
        self.orders.push(Order::Update {
            key: self.key.clone(),
            update,
        });
    }
}

/// A tile window as the world keeps it.
pub(crate) struct TileRuntime {
    pub key: String,
    pub runtime: Runtime<State>,
    pub inbox: Arc<Mutex<Vec<attn_app::Update>>>,
    pub shown: bool,
    /// Where the window is, which the app reads back as its placement.
    pub frame: Arc<Mutex<Frame>>,
    _bound: Subscription,
}

/// One of the app's tile windows, as a test sees it.
#[derive(Clone, Debug, PartialEq)]
pub struct TileWindowView {
    pub key: String,
    pub shown: bool,
    pub frame: Frame,
    /// The leaf the window shows, by desktop and leaf id.
    pub showing: Option<(String, String)>,
}

impl TileRuntime {
    pub(crate) fn open(
        key: String,
        frame: Frame,
        setup: Setup,
        store: &Store,
        daemon: attn_client::Commands,
        pulse: &Pulse,
    ) -> TileRuntime {
        let env = Env {
            system_dark: true,
            scale: 2.0,
        };
        let mut runtime = attn_app::tile_window_app()
            .headless_runtime((frame.width as f32, frame.height as f32), env);
        runtime.use_clipboard(&rune::clipboard::memory());
        let inbox: Arc<Mutex<Vec<attn_app::Update>>> = Arc::default();
        let placed = Arc::new(Mutex::new(frame));
        let read = placed.clone();
        inbox
            .lock()
            .unwrap()
            .push(setup.into_update(Box::new(move || Some(*read.lock().unwrap()))));
        let bound = {
            let (inbox, pulse) = (inbox.clone(), pulse.clone());
            attn_app::bind(
                store,
                daemon,
                attn_app::TerminalInbox::default(),
                attn_app::ReplyInbox::default(),
                crate::WALL_START_MS,
                move |update| {
                    inbox.lock().unwrap().push(update);
                    pulse.beat();
                },
            )
        };
        TileRuntime {
            key,
            runtime,
            inbox,
            shown: true,
            frame: placed,
            _bound: bound,
        }
    }

    pub(crate) fn view(&self) -> TileWindowView {
        TileWindowView {
            key: self.key.clone(),
            shown: self.shown,
            frame: *self.frame.lock().unwrap(),
            showing: attn_app::desktop::tile_window_leaf(&self.runtime.state),
        }
    }

    pub(crate) fn screen(&self) -> Screen {
        Screen(self.runtime.accessibility_json())
    }

    /// Runs what the app handed the window, and draws it.
    pub(crate) fn settle(&mut self) {
        let updates = std::mem::take(&mut *self.inbox.lock().unwrap());
        for update in updates {
            self.runtime.update(update);
        }
        self.runtime.frame(0.0);
        if self.runtime.animating {
            self.runtime.settle_all();
            self.runtime.frame(0.0);
        }
        while self.runtime.has_pending_input() {
            self.runtime.frame(0.0);
        }
    }

    pub(crate) fn idle(&self) -> bool {
        self.inbox.lock().unwrap().is_empty()
    }
}
