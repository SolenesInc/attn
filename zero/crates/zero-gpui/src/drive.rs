//! A test seam. `ZERO_DRIVE=<fifo>` makes zero read mouse gestures from that FIFO and replay them
//! through its own AppKit window, the path a physical mouse takes. It exists because macOS drops
//! mouse events another process posts to a pid, while keys posted that way arrive fine.
//!
//! A line is `down|move|up <x> <y>` in window points with the origin at the top left, the same
//! coordinates the grid traces print.

use gpui::Window;

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Gesture {
    Press,
    Drag,
    Release,
}

pub fn parse(line: &str) -> Option<(Gesture, f64, f64)> {
    let mut parts = line.split_whitespace();
    let gesture = match parts.next()? {
        "down" => Gesture::Press,
        "move" => Gesture::Drag,
        "up" => Gesture::Release,
        _ => return None,
    };
    let x = parts.next()?.parse().ok()?;
    let y = parts.next()?.parse().ok()?;
    parts.next().is_none().then_some((gesture, x, y))
}

/// Where a gesture lands: the window's AppKit view and how tall its content is right now. Read it
/// while the window is borrowed, post after the borrow ends: the post re-enters GPUI the way a
/// physical mouse does, and GPUI drops input for a window it is already updating.
pub struct Target {
    #[cfg(target_os = "macos")]
    ns_view: *mut std::ffi::c_void,
    #[cfg(target_os = "macos")]
    height: f64,
}

impl Target {
    #[cfg(target_os = "macos")]
    pub fn of(window: &Window) -> Option<Target> {
        use raw_window_handle::{HasWindowHandle, RawWindowHandle};
        let handle = HasWindowHandle::window_handle(window).ok()?;
        let RawWindowHandle::AppKit(appkit) = handle.as_raw() else {
            return None;
        };
        Some(Target { ns_view: appkit.ns_view.as_ptr(), height: f64::from(window.viewport_size().height) })
    }

    #[cfg(not(target_os = "macos"))]
    pub fn of(_window: &Window) -> Option<Target> {
        None
    }
}

/// Sends one left-button event to the window through `-[NSWindow sendEvent:]`.
#[cfg(target_os = "macos")]
pub fn post(target: &Target, gesture: Gesture, x: f64, y: f64) {
    use objc::runtime::Object;
    use objc::{class, msg_send, sel, sel_impl};

    #[repr(C)]
    struct NSPoint {
        x: f64,
        y: f64,
    }

    // NSEventType: NSLeftMouseDown, NSLeftMouseUp, NSLeftMouseDragged.
    let event_type: u64 = match gesture {
        Gesture::Press => 1,
        Gesture::Release => 2,
        Gesture::Drag => 6,
    };
    // AppKit measures from the bottom left, and the content view is as tall as the viewport.
    let location = NSPoint { x, y: target.height - y };
    objc::rc::autoreleasepool(|| unsafe {
        let ns_view = target.ns_view as *mut Object;
        let ns_window: *mut Object = msg_send![ns_view, window];
        if ns_window.is_null() {
            return;
        }
        let number: i64 = msg_send![ns_window, windowNumber];
        let event: *mut Object = msg_send![class!(NSEvent),
            mouseEventWithType: event_type
            location: location
            modifierFlags: 0u64
            timestamp: 0f64
            windowNumber: number
            context: std::ptr::null_mut::<Object>()
            eventNumber: 0i64
            clickCount: 1i64
            pressure: 1.0f32];
        if event.is_null() {
            return;
        }
        let _: () = msg_send![ns_window, sendEvent: event];
    })
}

#[cfg(not(target_os = "macos"))]
pub fn post(_target: &Target, _gesture: Gesture, _x: f64, _y: f64) {}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn a_line_is_a_gesture_and_a_point() {
        assert_eq!(parse("down 24.5 100"), Some((Gesture::Press, 24.5, 100.0)));
        assert_eq!(parse("move 30 110.25"), Some((Gesture::Drag, 30.0, 110.25)));
        assert_eq!(parse("up 30 110.25"), Some((Gesture::Release, 30.0, 110.25)));
        assert_eq!(parse("click 1 2"), None);
        assert_eq!(parse("down 1"), None);
        assert_eq!(parse("down 1 2 3"), None);
    }
}
