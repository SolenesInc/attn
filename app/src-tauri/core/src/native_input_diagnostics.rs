use serde::Serialize;

const CAPACITY: usize = 512;

#[derive(Clone, Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct NativeInputObservation {
    sequence: u64,
    at_unix_ms: i64,
    monotonic_us: u64,
    event_class: &'static str,
    modifiers: u64,
    repeat: bool,
    window_number: i64,
    window_focused: bool,
    responder_category: &'static str,
}

#[derive(Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct NativeInputSnapshot {
    supported: bool,
    os: &'static str,
    arch: &'static str,
    capacity: usize,
    total: u64,
    captured_at_unix_ms: i64,
    observations: Vec<NativeInputObservation>,
}

fn unix_millis() -> i64 {
    std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .unwrap_or_default()
        .as_millis() as i64
}

#[cfg(target_os = "macos")]
mod platform {
    use super::{unix_millis, NativeInputObservation, NativeInputSnapshot, CAPACITY};
    use block2::RcBlock;
    use objc2::MainThreadMarker;
    use objc2_app_kit::{NSEvent, NSEventMask, NSEventModifierFlags, NSEventType};
    use std::ptr::NonNull;
    use std::sync::atomic::{AtomicI64, AtomicU64, Ordering};

    const EVENT_KEY_DOWN: u64 = 1;
    const EVENT_FLAGS_CHANGED: u64 = 2;
    const RESPONDER_NONE: u64 = 0;
    const RESPONDER_WEBVIEW: u64 = 1;
    const RESPONDER_TEXT: u64 = 2;
    const RESPONDER_VIEW: u64 = 3;
    const RESPONDER_OTHER: u64 = 4;

    struct Slot {
        sequence: AtomicU64,
        at_unix_ms: AtomicI64,
        monotonic_us: AtomicU64,
        event_class: AtomicU64,
        modifiers: AtomicU64,
        repeat: AtomicU64,
        window_number: AtomicI64,
        window_focused: AtomicU64,
        responder_category: AtomicU64,
    }

    impl Slot {
        const fn new() -> Self {
            Self {
                sequence: AtomicU64::new(0),
                at_unix_ms: AtomicI64::new(0),
                monotonic_us: AtomicU64::new(0),
                event_class: AtomicU64::new(0),
                modifiers: AtomicU64::new(0),
                repeat: AtomicU64::new(0),
                window_number: AtomicI64::new(0),
                window_focused: AtomicU64::new(0),
                responder_category: AtomicU64::new(RESPONDER_NONE),
            }
        }
    }

    static NEXT_SEQUENCE: AtomicU64 = AtomicU64::new(0);
    static WALL_OFFSET_MS: AtomicI64 = AtomicI64::new(0);
    static SLOTS: [Slot; CAPACITY] = [const { Slot::new() }; CAPACITY];

    fn contains(haystack: &[u8], needle: &[u8]) -> bool {
        haystack
            .windows(needle.len())
            .any(|window| window == needle)
    }

    fn responder_category(event: &NSEvent, mtm: MainThreadMarker) -> u64 {
        let Some(window) = event.window(mtm) else {
            return RESPONDER_NONE;
        };
        let Some(responder) = window.firstResponder() else {
            return RESPONDER_NONE;
        };
        let name = responder.class().name().to_bytes();
        if contains(name, b"WK") || contains(name, b"Web") {
            RESPONDER_WEBVIEW
        } else if contains(name, b"Text") || contains(name, b"Field") {
            RESPONDER_TEXT
        } else if contains(name, b"View") {
            RESPONDER_VIEW
        } else {
            RESPONDER_OTHER
        }
    }

    fn wall_time_ms(event_timestamp_seconds: f64) -> i64 {
        let event_ms = (event_timestamp_seconds * 1_000.0).round() as i64;
        let offset = WALL_OFFSET_MS.load(Ordering::Relaxed);
        if offset != 0 {
            return offset.saturating_add(event_ms);
        }
        let calculated = unix_millis().saturating_sub(event_ms);
        let _ =
            WALL_OFFSET_MS.compare_exchange(0, calculated, Ordering::Relaxed, Ordering::Relaxed);
        WALL_OFFSET_MS
            .load(Ordering::Relaxed)
            .saturating_add(event_ms)
    }

    fn compact_modifiers(flags: NSEventModifierFlags) -> u64 {
        u64::from(flags.contains(NSEventModifierFlags::Shift))
            | (u64::from(flags.contains(NSEventModifierFlags::Control)) << 1)
            | (u64::from(flags.contains(NSEventModifierFlags::Option)) << 2)
            | (u64::from(flags.contains(NSEventModifierFlags::Command)) << 3)
    }

    fn record(event: &NSEvent, mtm: MainThreadMarker) {
        let event_class = if event.r#type() == NSEventType::KeyDown {
            EVENT_KEY_DOWN
        } else if event.r#type() == NSEventType::FlagsChanged {
            EVENT_FLAGS_CHANGED
        } else {
            return;
        };
        let sequence = NEXT_SEQUENCE.fetch_add(1, Ordering::Relaxed) + 1;
        let slot = &SLOTS[((sequence - 1) as usize) % CAPACITY];
        slot.sequence.store(0, Ordering::Release);
        let timestamp = event.timestamp();
        slot.at_unix_ms
            .store(wall_time_ms(timestamp), Ordering::Relaxed);
        slot.monotonic_us
            .store((timestamp * 1_000_000.0).round() as u64, Ordering::Relaxed);
        slot.event_class.store(event_class, Ordering::Relaxed);
        slot.modifiers
            .store(compact_modifiers(event.modifierFlags()), Ordering::Relaxed);
        slot.repeat.store(
            u64::from(event_class == EVENT_KEY_DOWN && event.isARepeat()),
            Ordering::Relaxed,
        );
        slot.window_number
            .store(event.windowNumber() as i64, Ordering::Relaxed);
        let focused = event.window(mtm).is_some_and(|window| window.isKeyWindow());
        slot.window_focused
            .store(u64::from(focused), Ordering::Relaxed);
        slot.responder_category
            .store(responder_category(event, mtm), Ordering::Relaxed);
        slot.sequence.store(sequence, Ordering::Release);
    }

    pub fn install() {
        let Some(mtm) = MainThreadMarker::new() else {
            return;
        };
        let handler = RcBlock::new(move |event: NonNull<NSEvent>| -> *mut NSEvent {
            // Local monitors hand us a live event for the duration of this call.
            record(unsafe { event.as_ref() }, mtm);
            event.as_ptr()
        });
        let monitor = unsafe {
            NSEvent::addLocalMonitorForEventsMatchingMask_handler(
                NSEventMask::KeyDown | NSEventMask::FlagsChanged,
                &handler,
            )
        };
        // The monitor is process-scoped. AppKit owns the copied block; retaining
        // the token for the process lifetime keeps observation installed.
        if let Some(monitor) = monitor {
            std::mem::forget(monitor);
        }
    }

    fn event_class(value: u64) -> &'static str {
        match value {
            EVENT_KEY_DOWN => "key_down",
            EVENT_FLAGS_CHANGED => "flags_changed",
            _ => "unknown",
        }
    }

    fn responder_category_name(value: u64) -> &'static str {
        match value {
            RESPONDER_WEBVIEW => "webview",
            RESPONDER_TEXT => "native_text",
            RESPONDER_VIEW => "native_view",
            RESPONDER_OTHER => "native_other",
            _ => "none",
        }
    }

    pub fn snapshot() -> NativeInputSnapshot {
        let total = NEXT_SEQUENCE.load(Ordering::Acquire);
        let mut observations = Vec::with_capacity((total as usize).min(CAPACITY));
        for slot in &SLOTS {
            let before = slot.sequence.load(Ordering::Acquire);
            if before == 0 || before > total {
                continue;
            }
            let observation = NativeInputObservation {
                sequence: before,
                at_unix_ms: slot.at_unix_ms.load(Ordering::Relaxed),
                monotonic_us: slot.monotonic_us.load(Ordering::Relaxed),
                event_class: event_class(slot.event_class.load(Ordering::Relaxed)),
                modifiers: slot.modifiers.load(Ordering::Relaxed),
                repeat: slot.repeat.load(Ordering::Relaxed) != 0,
                window_number: slot.window_number.load(Ordering::Relaxed),
                window_focused: slot.window_focused.load(Ordering::Relaxed) != 0,
                responder_category: responder_category_name(
                    slot.responder_category.load(Ordering::Relaxed),
                ),
            };
            if slot.sequence.load(Ordering::Acquire) == before {
                observations.push(observation);
            }
        }
        observations.sort_by_key(|observation| observation.sequence);
        NativeInputSnapshot {
            supported: true,
            os: std::env::consts::OS,
            arch: std::env::consts::ARCH,
            capacity: CAPACITY,
            total,
            captured_at_unix_ms: unix_millis(),
            observations,
        }
    }
}

#[cfg(not(target_os = "macos"))]
mod platform {
    use super::{unix_millis, NativeInputSnapshot, CAPACITY};

    pub fn install() {}

    pub fn snapshot() -> NativeInputSnapshot {
        NativeInputSnapshot {
            supported: false,
            os: std::env::consts::OS,
            arch: std::env::consts::ARCH,
            capacity: CAPACITY,
            total: 0,
            captured_at_unix_ms: unix_millis(),
            observations: Vec::new(),
        }
    }
}

pub use platform::install;

#[tauri::command]
pub fn native_input_diagnostics_snapshot() -> NativeInputSnapshot {
    platform::snapshot()
}
