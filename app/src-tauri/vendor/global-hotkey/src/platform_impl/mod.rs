#[cfg(target_os = "macos")]
#[path = "macos/mod.rs"]
mod platform;

#[cfg(not(target_os = "macos"))]
compile_error!("attn's global-hotkey fork is only used on macOS");

pub(crate) use self::platform::*;
