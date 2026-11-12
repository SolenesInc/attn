use std::sync::{Arc, Mutex};
use std::time::Duration;

use attn_app::{State, Update};
use attn_client::{
    Clock, Connection, Frame, Link, LinkConfig, ManualClock, Parked, Pulse, Store, Subscription,
};
use rune::{Env, Runtime, Tx};

use crate::daemon::{CLIENT_TOKEN, Daemon, INSTANCE};
use crate::screen::Screen;

/// A wire test's settle takes milliseconds; one that has not gone quiet after this long is
/// stuck, and the test says where.
const STUCK: Duration = Duration::from_secs(10);
const VIEWPORT: (f32, f32) = (960.0, 640.0);

/// The app on a windowless `Runtime`, with its real link connected to a scripted daemon over
/// loopback. Time moves only when the test calls `advance`.
pub struct World {
    pub daemon: Daemon,
    runtime: Runtime<State>,
    link: Option<Link>,
    _window: Subscription,
    inbox: Arc<Mutex<Vec<Update>>>,
    frames: Arc<Mutex<Vec<Frame>>>,
    clock: ManualClock,
    pulse: Pulse,
}

/// What a gesture can do to the app.
pub struct Driver<'a> {
    pub runtime: &'a mut Runtime<State>,
}

impl Driver<'_> {
    /// Changes the window's state as a handler would.
    pub fn update(&mut self, f: impl FnOnce(&mut State, &mut Tx)) {
        self.runtime.update(f);
    }
}

impl World {
    /// Starts the app against `daemon` and returns once both are quiet: after the handshake,
    /// or after the app gave up on the daemon.
    pub fn start(daemon: Daemon) -> World {
        let store = Store::default();
        let pulse = daemon.pulse();
        let inbox: Arc<Mutex<Vec<Update>>> = Arc::default();
        let frames: Arc<Mutex<Vec<Frame>>> = Arc::default();
        let window = {
            let (inbox, pulse) = (inbox.clone(), pulse.clone());
            attn_app::bind(&store, move |update| {
                inbox.lock().unwrap().push(update);
                pulse.beat();
            })
        };
        let clock = ManualClock::default();
        let received = frames.clone();
        let link = Link::start(
            LinkConfig {
                daemon: daemon.address().to_string(),
                client_token: CLIENT_TOKEN.into(),
                instance: INSTANCE.into(),
                profile_id: None,
                client_id: "wire-client".into(),
                app_pid: std::process::id(),
                clock: Clock::Manual(clock.clone()),
                pulse: pulse.clone(),
                frames: Box::new(move |frame| received.lock().unwrap().push(frame)),
            },
            store,
        );
        let env = Env {
            system_dark: true,
            scale: 2.0,
        };
        let runtime = Runtime::new(State::default(), attn_app::view, VIEWPORT, env);
        let mut world = World {
            daemon,
            runtime,
            link: Some(link),
            _window: window,
            inbox,
            frames,
            clock,
            pulse,
        };
        world.settle();
        world
    }

    /// Runs a user action, then returns once the app and the daemon have stopped exchanging
    /// messages.
    pub fn gesture(&mut self, f: impl FnOnce(&mut Driver)) {
        f(&mut Driver {
            runtime: &mut self.runtime,
        });
        self.settle();
    }

    /// Returns once nothing is in flight: every message each side sent has been handled, the
    /// window has drawn what the link delivered with its motion landed, and the link waits on
    /// the socket or on a time the clock has not reached.
    pub fn settle(&mut self) {
        loop {
            let seen = self.pulse.count();
            let updates = std::mem::take(&mut *self.inbox.lock().unwrap());
            for update in updates {
                self.runtime.update(update);
            }
            self.runtime.frame(0.0);
            if self.runtime.animating {
                self.runtime.settle_all();
                self.runtime.frame(0.0);
            }
            if self.quiet() && self.inbox.lock().unwrap().is_empty() {
                return;
            }
            assert!(
                self.pulse.wait_past(seen, STUCK),
                "the app and the daemon were still busy after {STUCK:?}: link {:?}, daemon {:?}, clock at {:?}",
                self.link().traffic(),
                self.daemon.traffic(),
                self.clock.now(),
            );
        }
    }

    /// Moves the app's clock and the link's together, then settles.
    pub fn advance(&mut self, by: Duration) {
        self.clock.advance(by);
        self.pulse.beat();
        self.runtime.frame(by.as_secs_f64());
        self.settle();
    }

    /// Hangs up, waits out the link's retry, and returns once the app has connected again and
    /// finished its handshake.
    pub fn reconnect(&mut self) {
        let before = self.daemon.connections();
        self.daemon.disconnect();
        self.settle();
        while self.daemon.connections() == before
            || self.runtime.state.facts.connection != Connection::Connected
        {
            let Parked::Until(at) = self.link().traffic().parked else {
                panic!(
                    "the link is not waiting to reconnect: {:?}\n{}",
                    self.link().traffic(),
                    self.screen()
                );
            };
            self.advance(at.saturating_sub(self.clock.now()));
        }
    }

    pub fn now(&self) -> Duration {
        self.clock.now()
    }

    pub fn screen(&self) -> Screen {
        Screen(self.runtime.accessibility_json())
    }

    /// No frame requested and no wake deadline: an idle window that draws nothing.
    pub fn runtime_is_at_rest(&self) -> bool {
        !self.runtime.animating && self.runtime.wake_deadline().is_none()
    }

    pub fn runtime(&self) -> &Runtime<State> {
        &self.runtime
    }

    /// The binary frames the link has handed on, oldest first, emptying the list.
    pub fn take_frames(&mut self) -> Vec<Frame> {
        std::mem::take(&mut *self.frames.lock().unwrap())
    }

    pub fn link(&self) -> &Link {
        self.link
            .as_ref()
            .expect("the link runs until the world drops")
    }

    /// Messages in flight on a connection that has ended are lost with it, so the counts are
    /// compared only while the link reads the daemon's newest connection.
    fn quiet(&self) -> bool {
        let link = self.link().traffic();
        let daemon = self.daemon.traffic();
        match link.parked {
            Parked::Reading => {
                link.connection == daemon.connection as u64
                    && link.sent == daemon.received
                    && link.received == daemon.queued
            }
            Parked::Stopped => true,
            Parked::Until(at) => at > self.clock.now(),
            Parked::Busy => false,
        }
    }
}

impl Drop for World {
    fn drop(&mut self) {
        self.link.take();
        let violations = self.daemon.violations();
        if !violations.is_empty() && !std::thread::panicking() {
            panic!(
                "the app broke the daemon's script:\n{}",
                violations.join("\n")
            );
        }
    }
}
