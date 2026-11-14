use std::cell::RefCell;

use attn_automation::{Bounds, WindowBounds, WindowInfo};
use rune::prefs::Prefs;
use rune::{App, Tx, WindowHandle, WindowOptions};

use crate::State;

const KEY: &str = "window.frame";

type Saved = ((i32, i32), (u32, u32), bool, bool);

thread_local! {
    static MAIN: RefCell<Option<WindowHandle>> = const { RefCell::new(None) };
    static PREFS: RefCell<Option<Prefs>> = const { RefCell::new(None) };
    static TILES: RefCell<Vec<WindowHandle>> = const { RefCell::new(Vec::new()) };
}

/// Opens the main window where it was when it last closed, from the instance's own prefs
/// (`bundle_id` names them). [`save`] keeps where it is.
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
        if let Some(prefs) = prefs.take() {
            use_prefs(prefs);
        }
        MAIN.set(Some(window.clone()));
    })
}

pub(crate) fn save(_: &mut State, _: &mut Tx) {
    let Some(p) = MAIN.with_borrow(|main| main.as_ref()?.placement()) else {
        return;
    };
    let saved: Saved = (p.position, p.content_size, p.maximized, p.full_screen);
    PREFS.with_borrow_mut(|prefs| {
        let Some(prefs) = prefs else { return };
        if let Err(why) = prefs.set(KEY, &saved) {
            eprintln!("attn-app: the window's frame was not saved: {why}");
        }
    });
}

/// Shows the main window, restored and in front.
pub(crate) fn open_main() {
    MAIN.with_borrow(|main| {
        if let Some(window) = main {
            window.open();
        }
    });
}

/// Where the app keeps its own settings on this thread: the instance's prefs file once the
/// main window opens, or the store a windowless run gives it.
pub fn use_prefs(prefs: Prefs) {
    PREFS.set(Some(prefs));
}

/// The app's own setting `key`, as a string.
pub(crate) fn pref_string(key: &str) -> Option<String> {
    PREFS.with_borrow(|prefs| prefs.as_ref()?.get::<String>(key))
}

/// Keeps the app's own setting `key`.
pub(crate) fn set_pref_string(key: &str, value: &str) {
    PREFS.with_borrow_mut(|prefs| {
        let Some(prefs) = prefs else { return };
        if let Err(why) = prefs.set(key, &value.to_owned()) {
            eprintln!("attn-app: {key} was not saved: {why}");
        }
    });
}

/// Where the main window's content starts on the desktop, in points: (0, 0) without one.
pub(crate) fn main_content_origin() -> (f64, f64) {
    MAIN.with_borrow(|main| {
        main.as_ref()
            .and_then(|window| window.placement())
            .map_or((0.0, 0.0), |p| {
                let (x, y) = p.content_position;
                (f64::from(x) / p.scale, f64::from(y) / p.scale)
            })
    })
}

/// Moves the main window's top-left corner toward `bounds`' corner, where the system lets it,
/// and sizes its content to `bounds`' size, in points. Call it on the UI thread.
pub(crate) fn move_main(bounds: Bounds) -> Result<(), String> {
    MAIN.with_borrow(|main| {
        let window = main.as_ref().ok_or("the app has no window open")?;
        let p = window.placement().ok_or("the app has no window open")?;
        window.set_position((
            (bounds.x * p.scale).round() as i32,
            (bounds.y * p.scale).round() as i32,
        ));
        window.resize_instant((bounds.width as f32, bounds.height as f32));
        Ok(())
    })
}

/// Where the main window is and how big its content is, in points, while it is open. Call it
/// on the UI thread.
pub(crate) fn main_bounds() -> Option<WindowBounds> {
    MAIN.with_borrow(|main| {
        let p = main.as_ref()?.placement()?;
        Some(WindowBounds {
            scale_factor: p.scale,
            minimized: p.minimized,
            logical_bounds: Bounds {
                x: f64::from(p.position.0) / p.scale,
                y: f64::from(p.position.1) / p.scale,
                width: f64::from(p.content_size.0) / p.scale,
                height: f64::from(p.content_size.1) / p.scale,
            },
        })
    })
}

/// Brings the main window to the front.
pub(crate) fn raise_main() {
    MAIN.with_borrow(|main| {
        if let Some(window) = main {
            window.focus();
        }
    });
}

/// The main window as a driver outside the process names it, while it is open. Call it on the
/// UI thread.
pub(crate) fn main_window() -> Option<WindowInfo> {
    MAIN.with_borrow(|main| {
        let window = main.as_ref()?;
        window.placement()?;
        Some(WindowInfo {
            id: platform_id(window)?,
            mapped: true,
        })
    })
}

/// Keeps `window`, a tile window, for drivers outside the process to find. Call it on the
/// UI thread.
pub(crate) fn tile_window_opened(window: &WindowHandle) {
    TILES.with_borrow_mut(|tiles| tiles.push(window.clone()));
}

/// The tile windows that exist, by their keys and platform ids. Call it on the UI thread.
pub(crate) fn tile_windows() -> Vec<(String, u64)> {
    TILES.with_borrow(|tiles| {
        tiles
            .iter()
            .filter_map(|window| Some((window.key().to_owned(), platform_id(window)?)))
            .collect()
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
            RawWindowHandle::Xlib(x) => Some(x.window),
            RawWindowHandle::Xcb(x) => Some(u64::from(x.window.get())),
            _ => None,
        })
        .ok()
        .flatten()
}
