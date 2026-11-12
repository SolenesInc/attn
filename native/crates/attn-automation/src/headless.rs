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

    /// Runs the runtime on this thread for the process's life: the host keeps a sender.
    pub fn run(&self, mut rt: Runtime<S>, inbox: Receiver<Work<S>>) {
        let clock = Clock::system();
        let stepped = std::cell::Cell::new(clock.now());
        let step = |rt: &mut Runtime<S>, beat: bool| {
            let now = clock.now();
            rt.frame((now - stepped.get()).as_secs_f64());
            stepped.set(now);
            if beat {
                self.frames.beat();
            }
        };
        step(&mut rt, true);
        loop {
            let wait = if rt.animating {
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
                Ok(Work::Update(f)) => {
                    rt.update(f);
                    step(&mut rt, true);
                }
                Ok(Work::Inspect(f)) => {
                    f(&mut rt);
                    let (input, moving) = (rt.has_pending_input(), rt.animating);
                    step(&mut rt, false);
                    if input || (moving && !rt.animating) {
                        self.frames.beat();
                    }
                }
                Err(RecvTimeoutError::Timeout) => step(&mut rt, true),
                Err(RecvTimeoutError::Disconnected) => return,
            }
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

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn an_inspection_s_own_frame_wakes_no_waiting_request() {
        let (host, inbox) = Headless::<u32>::new();
        let host = std::sync::Arc::new(host);
        let runner = host.clone();
        std::thread::spawn(move || {
            let rt = Runtime::new(0, |_, _| rune::el(), (10.0, 10.0), rune::Env::default());
            runner.run(rt, inbox)
        });
        let (read, seen) = std::sync::mpsc::channel();
        host.inspect(Box::new(move |_| read.send(()).unwrap()));
        seen.recv().unwrap();
        let after_start = host.frames().count();

        host.inspect(Box::new(|_| {}));
        host.updates()
            .send(Work::Update(Box::new(|n, _| *n += 1)))
            .unwrap();

        assert!(
            host.frames()
                .wait_past(after_start, Duration::from_secs(10))
        );
        assert_eq!(
            host.frames().count(),
            after_start + 1,
            "only the update's frame beat"
        );
    }
}
