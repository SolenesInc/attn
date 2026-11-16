//! The count a band runs down before it closes on its own, held while the pointer rests on
//! the band.

use rune::Tx;

const CLOSE_AFTER_SECONDS: f64 = 5.0;

#[derive(Default)]
pub(crate) struct Closing {
    countdown: Option<Countdown>,
    /// Whether the pointer rests on the band, which holds a countdown that starts under it.
    pointed: bool,
}

/// The seconds left before the band showing `what` closes. `since` is the clock reading the
/// count last resumed at, and is `None` while the pointer holds it.
struct Countdown {
    what: String,
    left: f64,
    since: Option<f64>,
    shown: u32,
}

impl Countdown {
    fn remaining(&self, now: f64) -> f64 {
        self.left - self.since.map_or(0.0, |since| now - since)
    }

    fn wake(&self, tx: &mut Tx) {
        if let Some(since) = self.since {
            tx.wake_at(since + self.left - f64::from(self.shown.saturating_sub(1)));
        }
    }
}

/// What a poll of the count found.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(crate) enum Tick {
    Same,
    Changed,
    RanOut,
}

impl Closing {
    /// Counts down for `due`, the band that should close on its own, from where it is when
    /// it is the one already counting; with nothing due, stops counting.
    pub(crate) fn follow(&mut self, due: Option<String>, tx: &mut Tx) {
        match due {
            Some(what) if self.countdown.as_ref().is_some_and(|c| c.what == what) => {}
            Some(what) => {
                let countdown = Countdown {
                    what,
                    left: CLOSE_AFTER_SECONDS,
                    since: (!self.pointed).then(|| tx.clock()),
                    shown: CLOSE_AFTER_SECONDS as u32,
                };
                countdown.wake(tx);
                self.countdown = Some(countdown);
            }
            None => self.countdown = None,
        }
    }

    pub(crate) fn poll(&mut self, now: f64, tx: &mut Tx) -> Tick {
        let Some(countdown) = &mut self.countdown else {
            return Tick::Same;
        };
        if countdown.since.is_none() {
            return Tick::Same;
        }
        let left = countdown.remaining(now);
        if left <= 0.0 {
            return Tick::RanOut;
        }
        let shown = left.ceil() as u32;
        let changed = shown != countdown.shown;
        countdown.shown = shown;
        countdown.wake(tx);
        if changed { Tick::Changed } else { Tick::Same }
    }

    /// The pointer came onto the band or left it.
    pub(crate) fn hold(&mut self, inside: bool, tx: &mut Tx) {
        self.pointed = inside;
        let Some(countdown) = &mut self.countdown else {
            tx.live_only();
            return;
        };
        let now = tx.clock();
        match (inside, countdown.since) {
            (true, Some(_)) => {
                countdown.left = countdown.remaining(now);
                countdown.since = None;
            }
            (false, None) => {
                countdown.since = Some(now);
                countdown.wake(tx);
            }
            _ => tx.live_only(),
        }
    }

    /// The band closed: nothing counts, and the pointer no longer rests on it.
    pub(crate) fn stop(&mut self) {
        self.countdown = None;
        self.pointed = false;
    }

    /// The whole seconds left before the band showing `what` closes, while it counts.
    pub(crate) fn seconds(&self, what: &str) -> Option<u32> {
        self.countdown
            .as_ref()
            .filter(|c| c.what == what)
            .map(|c| c.shown)
    }
}
