use std::sync::{Arc, Condvar, Mutex};
use std::time::Duration;

/// A counter that beats whenever the link's traffic or state moves, so a waiter can sleep
/// until something happened instead of polling.
#[derive(Clone, Default)]
pub struct Pulse(Arc<(Mutex<u64>, Condvar)>);

impl Pulse {
    pub fn beat(&self) {
        let (count, moved) = &*self.0;
        *count.lock().unwrap() += 1;
        moved.notify_all();
    }

    pub fn count(&self) -> u64 {
        *self.0.0.lock().unwrap()
    }

    /// Blocks until the count passes `seen`. Returns false when `tripwire` ran out first.
    pub fn wait_past(&self, seen: u64, tripwire: Duration) -> bool {
        let (count, moved) = &*self.0;
        let guard = count.lock().unwrap();
        let (_count, waited) = moved
            .wait_timeout_while(guard, tripwire, |count| *count <= seen)
            .unwrap();
        !waited.timed_out()
    }
}
