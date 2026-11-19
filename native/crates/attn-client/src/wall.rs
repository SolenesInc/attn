use std::sync::Arc;

/// Reads the wall clock in milliseconds since 1970: the system's in the app, a test's own in
/// wire tests. Read it, never derive it from the runtime's or the link's clock, which stand
/// still while the Mac sleeps.
#[derive(Clone)]
pub struct WallClock(Arc<dyn Fn() -> i64 + Send + Sync>);

impl WallClock {
    pub fn new(read: impl Fn() -> i64 + Send + Sync + 'static) -> WallClock {
        WallClock(Arc::new(read))
    }

    #[allow(
        clippy::disallowed_methods,
        reason = "the one read of the system's wall clock"
    )]
    pub fn system() -> WallClock {
        WallClock::new(|| {
            std::time::SystemTime::now()
                .duration_since(std::time::UNIX_EPOCH)
                .map_or(0, |d| d.as_millis() as i64)
        })
    }

    pub fn now_ms(&self) -> i64 {
        (self.0)()
    }
}

impl Default for WallClock {
    fn default() -> WallClock {
        WallClock::system()
    }
}
