use std::cell::RefCell;

use attn_automation::WindowInfo;
use rune::prefs::Prefs;
use rune::{App, Tx, WindowHandle, WindowOptions};

use crate::State;

const KEY: &str = "window.frame";

type Saved = ((i32, i32), (u32, u32), bool, bool);

thread_local! {
    static MAIN: RefCell<Option<(Prefs, WindowHandle)>> = const { RefCell::new(None) };
}

/// Opens the main window where it was when it last closed, and keeps where it is when it
/// quits, in the instance's own prefs (`bundle_id` names them).
pub(crate) fn restored(app: App<State>, bundle_id: &str) -> App<State> {
    let prefs = Prefs::open(bundle_id);
    let app = match prefs.get::<Saved>(KEY) {
        Some((position, size, maximized, full_screen)) => app
            .window_options(WindowOptions {
                position: Some(position),
                physical_size: Some(size),
                ..WindowOptions::default()
            })
            .maximized(maximized)
            .full_screen(full_screen),
        None => app,
    };
    let mut prefs = Some(prefs);
    app.on_window(move |window| {
        let prefs = prefs
            .take()
            .unwrap_or_else(|| MAIN.take().map(|(p, _)| p).unwrap_or_else(Prefs::memory));
        MAIN.set(Some((prefs, window.clone())));
    })
    .on_quit(save)
}

fn save(_: &mut State, _: &mut Tx) {
    MAIN.with_borrow_mut(|main| {
        let Some((prefs, window)) = main else { return };
        let Some(p) = window.placement() else { return };
        let saved: Saved = (p.position, p.content_size, p.maximized, p.full_screen);
        if let Err(why) = prefs.set(KEY, &saved) {
            eprintln!("attn-app: the window's frame was not saved: {why}");
        }
    });
}

/// The main window as a driver outside the process names it, while it is open. Call it on the
/// UI thread.
pub(crate) fn main_window() -> Option<WindowInfo> {
    MAIN.with_borrow(|main| {
        let (_, window) = main.as_ref()?;
        window.placement()?;
        Some(WindowInfo {
            id: platform_id(window)?,
            mapped: true,
        })
    })
}

#[cfg(target_os = "macos")]
fn platform_id(window: &WindowHandle) -> Option<u64> {
    window.window_number().map(u64::from)
}

#[cfg(not(target_os = "macos"))]
fn platform_id(window: &WindowHandle) -> Option<u64> {
    use rune::raw_window_handle::RawWindowHandle;
    window
        .with_raw_handle(|handle| match handle.as_raw() {
            RawWindowHandle::Xlib(x) => Some(x.window as u64),
            RawWindowHandle::Xcb(x) => Some(u64::from(x.window.get())),
            _ => None,
        })
        .ok()
        .flatten()
}
