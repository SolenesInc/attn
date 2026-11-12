use std::sync::mpsc::{Receiver, RecvTimeoutError, Sender};
use std::time::Duration;

use attn_client::{Clock, Pulse};
use rune::{Runtime, Tx};

use crate::host::{Host, Inspection};

/// A frame while something moves, as a 60 Hz display would step it.
const MOTION_STEP: Duration = Duration::from_micros(16_667);

/// A change to the app's state from another thread, as `AppHandle::update` takes it.
pub type Change<S> = Box<dyn FnOnce(&mut S, &mut Tx) + Send>;

/// Work for the windowless host's thread.
pub enum Work<S: 'static> {
    Update(Change<S>),
    Inspect(Inspection<S>),
}

/// The app with no window and no GPU: its runtime steps on the system clock when work
/// arrives, while it moves, and at its timers, and sleeps otherwise.
pub struct Headless<S: 'static> {
    work: Sender<Work<S>>,
    frames: Pulse,
}

impl<S: 'static> Headless<S> {
    pub fn new() -> (Headless<S>, Receiver<Work<S>>) {
        let (work, inbox) = std::sync::mpsc::channel();
        (
            Headless {
                work,
                frames: Pulse::default(),
            },
            inbox,
        )
    }

    /// Where the link delivers the window's updates.
    pub fn updates(&self) -> Sender<Work<S>> {
        self.work.clone()
    }

    /// Runs the runtime on this thread until every sender has gone.
    pub fn run(&self, mut rt: Runtime<S>, inbox: Receiver<Work<S>>) {
        let clock = Clock::system();
        let stepped = std::cell::Cell::new(clock.now());
        let step = |rt: &mut Runtime<S>| {
            let now = clock.now();
            rt.frame((now - stepped.get()).as_secs_f64());
            stepped.set(now);
            self.frames.beat();
        };
        step(&mut rt);
        loop {
            let wait = if rt.animating || rt.has_pending_input() {
                Some(MOTION_STEP)
            } else {
                rt.wake_deadline()
                    .map(|at| Duration::from_secs_f64((at - stepped.get().as_secs_f64()).max(0.0)))
            };
            let next = match wait {
                Some(wait) => inbox.recv_timeout(wait),
                None => inbox.recv().map_err(|_| RecvTimeoutError::Disconnected),
            };
            match next {
                Ok(Work::Update(f)) => rt.update(f),
                Ok(Work::Inspect(f)) => {
                    f(&mut rt);
                    if rt.has_pending_input() {
                        step(&mut rt);
                    }
                }
                Err(RecvTimeoutError::Timeout) => {}
                Err(RecvTimeoutError::Disconnected) => return,
            }
            step(&mut rt);
        }
    }
}

impl<S: 'static> Host<S> for Headless<S> {
    fn inspect(&self, f: Inspection<S>) {
        let _ = self.work.send(Work::Inspect(f));
    }

    fn frames(&self) -> &Pulse {
        &self.frames
    }
}
