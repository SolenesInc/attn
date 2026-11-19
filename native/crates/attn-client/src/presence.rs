//! What the app tells the daemon about the person at it, as `set_client_presence`: whether its
//! window is on screen, whether it shows Home, and how long since the person's last input. The
//! daemon believes a report for 90 seconds, so the link repeats it every 30 while connected.
//! The app reports at once when a connection's handshake completes and when the window or its
//! view changes.

use std::time::Duration;

use attn_protocol::SetClientPresenceMessage;

pub(crate) const HEARTBEAT: Duration = Duration::from_secs(30);

/// Input reports at once only when the last report is this old; the heartbeat carries the rest.
const INPUT_REPORT_FLOOR: Duration = Duration::from_secs(10);

/// What the person can see of the app.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct Sight {
    pub visible: bool,
    pub home: bool,
}

#[derive(Default)]
pub(crate) struct Presence {
    sight: Option<Sight>,
    input_at_ms: Option<i64>,
    reported_at: Option<Duration>,
}

impl Presence {
    /// Whether `sight` is news to report: it changed, or this connection has had no report.
    pub(crate) fn see(&mut self, sight: Sight) -> bool {
        self.sight.replace(sight) != Some(sight) || self.reported_at.is_none()
    }

    /// Whether input at `now_ms`, wall time, is news to report at `now`, link time.
    pub(crate) fn heard_input(&mut self, now_ms: i64, now: Duration) -> bool {
        self.input_at_ms = Some(now_ms);
        self.reported_at
            .is_some_and(|at| now.saturating_sub(at) >= INPUT_REPORT_FLOOR)
    }

    /// When the heartbeat reports again, on the link's clock, once something has been reported
    /// on this connection.
    pub(crate) fn due(&self) -> Option<Duration> {
        Some(self.reported_at? + HEARTBEAT)
    }

    /// The report to send now, once the app has said what the person sees.
    pub(crate) fn report(&mut self, now_ms: i64, now: Duration) -> Option<String> {
        let sight = self.sight?;
        self.reported_at = Some(now);
        let message = SetClientPresenceMessage {
            visible: sight.visible,
            dashboard_visible: sight.home,
            idle_seconds: self
                .input_at_ms
                .map(|at| (now_ms - at).max(0) as f64 / 1000.0),
            ..SetClientPresenceMessage::new()
        };
        Some(serde_json::to_string(&message).expect("a protocol command serializes"))
    }

    /// The connection ended: the next one reports once the app sees it.
    pub(crate) fn disconnected(&mut self) {
        self.reported_at = None;
    }
}
