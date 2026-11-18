use std::sync::{Arc, Mutex};
use std::time::Duration;

use attn_protocol::{AppPerformance, FrameTimes};

/// Frame times fall into buckets each 2% wider than the last, from 0.01 ms up past 10 s, so a
/// percentile reads within 1% of the frames' own times whatever their range.
const FLOOR_MS: f64 = 0.01;
const GROWTH: f64 = 1.02;
const BUCKETS: usize = 700;

/// What the app measures of its own performance for the daemon's analytics samples: its CPU
/// time and the main window's frames since the daemon last took them.
#[derive(Clone)]
pub struct Performance(Arc<Mutex<Meter>>);

struct Meter {
    since: Duration,
    cpu_since: Duration,
    drawn: u64,
    unchanged: u64,
    skipped: u64,
    frame_ms: Box<[u32; BUCKETS]>,
    slowest_ms: f64,
}

impl Default for Performance {
    fn default() -> Self {
        Performance(Arc::new(Mutex::new(Meter::starting(
            Duration::ZERO,
            cpu_time(),
        ))))
    }
}

impl Performance {
    /// A frame the window drew, which took `ms` from its start until its commands were
    /// submitted.
    pub fn drawn(&self, ms: f64) {
        let mut meter = self.0.lock().unwrap();
        meter.drawn += 1;
        meter.frame_ms[bucket(ms)] += 1;
        meter.slowest_ms = meter.slowest_ms.max(ms);
    }

    /// A frame a timer asked for that drew what was already on screen.
    pub fn unchanged(&self) {
        self.0.lock().unwrap().unchanged += 1;
    }

    /// A frame that was due but not drawn, because the window was covered or no drawable came.
    pub fn skipped(&self) {
        self.0.lock().unwrap().skipped += 1;
    }

    /// What was measured since the last take, and starts measuring afresh. `now` is the link's
    /// clock, which starts with the link.
    pub fn take(&self, now: Duration) -> AppPerformance {
        let cpu = cpu_time();
        let meter = std::mem::replace(&mut *self.0.lock().unwrap(), Meter::starting(now, cpu));
        AppPerformance {
            period_ms: millis(now.saturating_sub(meter.since)),
            cpu_ms: millis(cpu.saturating_sub(meter.cpu_since)),
            frames_drawn: meter.drawn as i64,
            frames_unchanged: meter.unchanged as i64,
            frames_skipped: meter.skipped as i64,
            frame_ms: meter.frame_times(),
        }
    }
}

impl Meter {
    fn starting(since: Duration, cpu_since: Duration) -> Self {
        Meter {
            since,
            cpu_since,
            drawn: 0,
            unchanged: 0,
            skipped: 0,
            frame_ms: Box::new([0; BUCKETS]),
            slowest_ms: 0.0,
        }
    }

    fn frame_times(&self) -> Option<FrameTimes> {
        (self.drawn > 0).then(|| FrameTimes {
            p50: self.percentile(0.50),
            p95: self.percentile(0.95),
            p99: self.percentile(0.99),
            max: hundredths(self.slowest_ms),
        })
    }

    /// The time below which `share` of the drawn frames fell: the middle of the bucket that
    /// holds the frame at that rank, never past the slowest frame.
    fn percentile(&self, share: f64) -> f64 {
        let rank = ((self.drawn as f64 * share).ceil() as u64).max(1);
        let mut seen = 0;
        for (i, &count) in self.frame_ms.iter().enumerate() {
            seen += u64::from(count);
            if seen >= rank {
                let middle = FLOOR_MS * GROWTH.powf(i as f64 + 0.5);
                return hundredths(middle.min(self.slowest_ms));
            }
        }
        hundredths(self.slowest_ms)
    }
}

fn bucket(ms: f64) -> usize {
    if ms.is_nan() || ms <= FLOOR_MS {
        return 0;
    }
    ((ms / FLOOR_MS).ln() / GROWTH.ln()).min((BUCKETS - 1) as f64) as usize
}

fn hundredths(ms: f64) -> f64 {
    (ms * 100.0).round() / 100.0
}

fn millis(d: Duration) -> i64 {
    i64::try_from(d.as_millis()).unwrap_or(i64::MAX)
}

/// The CPU time every thread of this process has used, in user and system mode.
fn cpu_time() -> Duration {
    let mut usage = std::mem::MaybeUninit::<libc::rusage>::zeroed();
    // SAFETY: getrusage writes a whole rusage into the buffer it is given.
    if unsafe { libc::getrusage(libc::RUSAGE_SELF, usage.as_mut_ptr()) } != 0 {
        return Duration::ZERO;
    }
    // SAFETY: getrusage returned 0, so it filled the buffer.
    let usage = unsafe { usage.assume_init() };
    let time = |t: libc::timeval| {
        Duration::from_secs(t.tv_sec as u64) + Duration::from_micros(t.tv_usec as u64)
    };
    time(usage.ru_utime) + time(usage.ru_stime)
}
