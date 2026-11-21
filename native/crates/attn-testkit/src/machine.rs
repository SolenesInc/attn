//! The machine a wire test runs on, the same everywhere: a Mac unless `RUNE_PLATFORM=linux`
//! asks for Linux, measuring text with the repository's fonts and no installed one. Rune reads
//! the platform once per process, so the daemon, the world and [`key_event`](crate::key_event)
//! settle it first, and a test asks [`platform`], never `Platform::current()` straight.

use rune::Platform;

const MONO: &[u8] = include_bytes!("../fonts/IBMPlexMono-Regular.ttf");

pub(crate) fn settle() {
    Platform::set_default(Platform::Mac);
    rune::text::without_system_fonts(MONO);
}

/// The platform the test acts as.
pub fn platform() -> Platform {
    settle();
    Platform::current()
}

/// Whether the test acts as a Mac.
pub fn mac() -> bool {
    platform() == Platform::Mac
}
