use std::sync::Arc;
use std::time::Duration;

use tokio::sync::watch;

/// The link's time. Production reads the system's monotonic clock; a test holds a
/// `ManualClock` and moves it.
#[derive(Clone)]
pub enum Clock {
    System(tokio::time::Instant),
    Manual(ManualClock),
}

impl Clock {
    pub fn system() -> Clock {
        Clock::System(tokio::time::Instant::now())
    }

    pub fn now(&self) -> Duration {
        match self {
            Clock::System(start) => start.elapsed(),
            Clock::Manual(clock) => clock.now(),
        }
    }

    pub(crate) async fn sleep_until(&self, at: Duration) {
        match self {
            Clock::System(start) => tokio::time::sleep_until(*start + at).await,
            Clock::Manual(clock) => {
                let mut now = clock.now.subscribe();
                let _ = now.wait_for(|now| *now >= at).await;
            }
        }
    }
}

/// A clock that stands still until `advance` moves it.
#[derive(Clone)]
pub struct ManualClock {
    now: Arc<watch::Sender<Duration>>,
}

impl Default for ManualClock {
    fn default() -> Self {
        ManualClock {
            now: Arc::new(watch::Sender::new(Duration::ZERO)),
        }
    }
}

impl ManualClock {
    pub fn now(&self) -> Duration {
        *self.now.borrow()
    }

    pub fn advance(&self, by: Duration) {
        self.now.send_modify(|now| *now += by);
    }
}
