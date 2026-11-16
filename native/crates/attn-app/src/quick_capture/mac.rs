//! The panel's window on macOS: a nonactivating panel on every Space, over full-screen apps,
//! that takes the keyboard while the app in front stays in front, and gives the keyboard back
//! to that app when it hides.

use std::path::PathBuf;
use std::sync::{Arc, Mutex};

use attn_automation::{Host, Inspection, Server};
use attn_client::{Commands, Pulse, Store, Subscription};
use objc2::msg_send;
use objc2::rc::Retained;
use objc2::runtime::{AnyClass, AnyObject};
use objc2_foundation::{NSPoint, NSRect};
use rune::{AppHandle, DisplayTarget, OverlayPlacement, Shortcut, ShortcutId, WindowOptions};

use super::{Panel, SIZE, Update, Window};

const MODAL_PANEL_LEVEL: isize = 8;
const CAN_JOIN_ALL_SPACES: usize = 1 << 0;
const FULL_SCREEN_AUXILIARY: usize = 1 << 8;
const CAN_JOIN_ALL_APPLICATIONS: usize = 1 << 18;

/// The panel's native window and the app that was in front when it came up.
struct MacPanel {
    handle: AppHandle<Panel>,
    front: Mutex<Option<Front>>,
    shortcut: Mutex<Option<ShortcutId>>,
    bound: Mutex<Option<Subscription>>,
    automation: Mutex<Option<Server>>,
}

/// The panel's runtime as its automation server reaches it.
struct PanelHost {
    handle: AppHandle<Panel>,
    frames: Pulse,
}

impl Host<Panel> for PanelHost {
    fn inspect(&self, f: Inspection<Panel>) {
        self.handle.inspect(f);
    }

    fn frames(&self) -> &Pulse {
        &self.frames
    }
}

/// Where the keyboard goes back to when the panel hides.
struct Front {
    pid: Option<i32>,
    app_hidden: bool,
}

/// Opens the panel's window, hidden until `Window::show`, bound to `store`, with an
/// automation server that writes `automation` as its manifest when given one. Call it on the
/// UI thread, as `rune::open_window` needs.
pub fn open(
    store: &Store,
    daemon: Commands,
    data_dir: PathBuf,
    automation: Option<PathBuf>,
) -> Arc<dyn Window> {
    let frames = Pulse::default();
    let app = super::app().on_frame_encoded({
        let frames = frames.clone();
        move |_| frames.beat()
    });
    let handle = app.handle();
    let options = WindowOptions::launcher_panel().macos(|native| {
        native
            .level(MODAL_PANEL_LEVEL)
            .collection_behavior(
                CAN_JOIN_ALL_SPACES | CAN_JOIN_ALL_APPLICATIONS | FULL_SCREEN_AUXILIARY,
            )
            .shadow(true)
            .corner_radius(12.0)
    });
    rune::open_window(rune::WindowSpec::from_app("quick-capture", app).configured_options(options));
    let panel = Arc::new(MacPanel {
        handle: handle.clone(),
        front: Mutex::default(),
        shortcut: Mutex::default(),
        bound: Mutex::default(),
        automation: Mutex::default(),
    });
    let window: Arc<dyn Window> = panel.clone();
    let bound = super::bind(store, daemon, window.clone(), data_dir, {
        let handle = handle.clone();
        move |update| handle.update(update)
    });
    *panel.bound.lock().unwrap() = Some(bound);
    if let Some(manifest) = automation {
        let host = Arc::new(PanelHost { handle, frames });
        match Server::start(host, Arc::new(super::automation::CaptureHooks), &manifest) {
            Ok(server) => *panel.automation.lock().unwrap() = Some(server),
            Err(why) => eprintln!("attn-app: Quick Capture automation is off: {why}"),
        }
    }
    window
}

impl Window for MacPanel {
    fn show(&self) {
        {
            let mut front = self.front.lock().unwrap();
            if front.is_none() {
                *front = Some(Front {
                    pid: frontmost_pid().filter(|&pid| pid != std::process::id() as i32),
                    app_hidden: app_hidden(),
                });
            }
        }
        if let Some(placement) = centered() {
            self.handle.place(DisplayTarget::UnderPointer, placement);
        }
        self.handle.open();
    }

    fn hide(&self) {
        self.handle.hide();
        let Some(front) = self.front.lock().unwrap().take() else {
            return;
        };
        if front.app_hidden {
            hide_app();
        } else if let Some(pid) = front.pid
            && frontmost_pid() == Some(std::process::id() as i32)
        {
            activate(pid);
        }
    }

    fn update(&self, update: Update) {
        self.handle.update(update);
    }

    fn bind(&self, shortcut: Option<&Shortcut>) -> Result<(), String> {
        let mut held = self.shortcut.lock().unwrap();
        let next = match shortcut {
            Some(shortcut) => {
                let id = self
                    .handle
                    .register_shortcut(shortcut.clone(), |panel, tx| {
                        if let Some(window) = panel.window.clone() {
                            window.show();
                        }
                        super::opened(panel, tx);
                    })
                    .map_err(|why| match self.handle.shortcut_owner(shortcut) {
                        Some(owner) => format!("{why} ({owner} uses it)"),
                        None => why.to_string(),
                    })?;
                Some(id)
            }
            None => None,
        };
        if let Some(previous) = held.take()
            && let Err(why) = self.handle.unregister_shortcut(previous)
        {
            if let Some(next) = next {
                let _ = self.handle.unregister_shortcut(next);
            }
            *held = Some(previous);
            return Err(why.to_string());
        }
        *held = next;
        Ok(())
    }
}

/// The panel's place: centred on the visible part of the screen under the pointer, in that
/// screen's points from its top left.
fn centered() -> Option<OverlayPlacement> {
    let screens = AnyClass::get(c"NSScreen")?;
    let events = AnyClass::get(c"NSEvent")?;
    // SAFETY: class methods that return live objects or nil, and plain struct properties.
    unsafe {
        let pointer: NSPoint = msg_send![events, mouseLocation];
        let all: Retained<AnyObject> = msg_send![screens, screens];
        let count: usize = msg_send![&all, count];
        for index in 0..count {
            let screen: Retained<AnyObject> = msg_send![&all, objectAtIndex: index];
            let frame: NSRect = msg_send![&screen, frame];
            let inside = pointer.x >= frame.origin.x
                && pointer.x < frame.origin.x + frame.size.width
                && pointer.y >= frame.origin.y
                && pointer.y < frame.origin.y + frame.size.height;
            if !inside {
                continue;
            }
            let visible: NSRect = msg_send![&screen, visibleFrame];
            let (width, height) = (f64::from(SIZE.0), f64::from(SIZE.1));
            let x = visible.origin.x - frame.origin.x + (visible.size.width - width) / 2.0;
            let top =
                (frame.origin.y + frame.size.height) - (visible.origin.y + visible.size.height);
            let y = top + (visible.size.height - height) / 2.0;
            return Some(OverlayPlacement::Rect {
                x: x as f32,
                y: y as f32,
                width: SIZE.0,
                height: SIZE.1,
            });
        }
    }
    None
}

/// The process of the app that is active now.
fn frontmost_pid() -> Option<i32> {
    let class = AnyClass::get(c"NSWorkspace")?;
    // SAFETY: `sharedWorkspace` and `frontmostApplication` return live objects or nil, and
    // `processIdentifier` is a plain integer property.
    unsafe {
        let workspace: Retained<AnyObject> = msg_send![class, sharedWorkspace];
        let app: Option<Retained<AnyObject>> = msg_send![&workspace, frontmostApplication];
        app.map(|app| msg_send![&app, processIdentifier])
    }
}

/// Hands the active app back to `pid`; a process that has quit is skipped.
fn activate(pid: i32) {
    let Some(class) = AnyClass::get(c"NSRunningApplication") else {
        return;
    };
    // SAFETY: the receiver is a live object or nil, and the option is `0`: none.
    unsafe {
        let target: Option<Retained<AnyObject>> =
            msg_send![class, runningApplicationWithProcessIdentifier: pid];
        if let Some(target) = target {
            let _: bool = msg_send![&target, activateWithOptions: 0usize];
        }
    }
}

fn shared_app() -> Option<Retained<AnyObject>> {
    let class = AnyClass::get(c"NSApplication")?;
    // SAFETY: `sharedApplication` returns the live application object.
    unsafe { msg_send![class, sharedApplication] }
}

/// Whether attn's windows were hidden (⌘H) when the panel came up.
fn app_hidden() -> bool {
    // SAFETY: a plain boolean property of the application object.
    shared_app().is_some_and(|app| unsafe { msg_send![&app, isHidden] })
}

fn hide_app() {
    if let Some(app) = shared_app() {
        // SAFETY: `hide:` takes an optional sender; nil is allowed.
        unsafe {
            let _: () = msg_send![&app, hide: std::ptr::null::<AnyObject>()];
        }
    }
}
