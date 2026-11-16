//! The seam between browser tiles and the web engine that runs their pages: `WKWebView` in the
//! macOS app, a scripted engine in wire tests, none on Linux.

use std::cell::RefCell;
use std::collections::{HashMap, HashSet};

use attn_client::TileKey;
use rune::{Chord, Key, KeyEvent, KeyPlatform};
use serde_json::{Map, Value};

/// A web engine that holds one page per browser tile, as native views over the window.
pub trait BrowserHost {
    /// Makes the page for `page`, hidden, and loads `url` in it.
    fn open(&mut self, page: &TileKey, url: &str);
    fn navigate(&mut self, page: &TileKey, url: &str);
    fn reload(&mut self, page: &TileKey);
    /// Goes back one page in the page's history, if it has one.
    fn back(&mut self, page: &TileKey);
    fn forward(&mut self, page: &TileKey);
    /// Frees the page, so its web content process ends.
    fn close(&mut self, page: &TileKey);
    /// Shows the page over `at` in window points, clipped to `at`'s visible part, or hides it.
    fn place(&mut self, page: &TileKey, at: Option<Placement>);
    /// Hands the keyboard to the page, or takes it back for the window.
    fn focus(&mut self, page: &TileKey, focused: bool);
    /// Runs an agent's action on the page. The answer comes back through [`hear`] as
    /// [`Heard::Answered`].
    fn control(&mut self, page: &TileKey, request: Control);
}

/// Where a shown page sits in its window, in window points: `frame` is the whole page area,
/// `visible` the part of it the window does not clip away.
#[derive(Clone, Copy, Debug, PartialEq)]
pub struct Placement {
    pub frame: [f32; 4],
    pub visible: [f32; 4],
}

/// An agent's `attn browser` action, as the daemon relays it.
#[derive(Clone, Debug, PartialEq)]
pub struct Control {
    pub request_id: String,
    pub action: String,
    pub params: Map<String, Value>,
}

/// What a page tells the window.
#[derive(Clone, Debug, PartialEq)]
pub enum Heard {
    /// The page is at `url` now, by a link, a redirect, history or the page's own script.
    Located { page: TileKey, url: String },
    /// A click or a key gave the page the keyboard.
    Focused { page: TileKey },
    /// The answer to the [`Control`] with this id: its JSON data, or why it failed.
    Answered {
        request_id: String,
        result: Result<String, String>,
    },
    /// The page opened a dialog while agents' actions ran on it: those actions end, and the
    /// agent answers the dialog.
    Alerted { page: TileKey },
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum AlertKind {
    Alert,
    Confirm,
    Prompt,
}

/// A dialog a page's script opened with `alert`, `confirm` or `prompt`.
pub struct Alert {
    pub kind: AlertKind,
    pub message: String,
    pub default_text: Option<String>,
    /// Closes the dialog: accepted with the prompt's text, or dismissed with `None`.
    pub close: Box<dyn FnOnce(Option<String>)>,
}

/// What agents are doing on one page.
#[derive(Default)]
struct Acting {
    running: HashSet<String>,
    alert: Option<Alert>,
    typed: Option<String>,
}

thread_local! {
    static HOST: RefCell<Option<Box<dyn BrowserHost>>> = const { RefCell::new(None) };
    static HEARD: RefCell<Vec<Heard>> = const { RefCell::new(Vec::new()) };
    static ACTING: RefCell<HashMap<TileKey, Acting>> = RefCell::new(HashMap::new());
    static CLAIMED: RefCell<Vec<Chord>> = const { RefCell::new(Vec::new()) };
}

/// Runs browser tiles' pages on `host`, for the main window on this thread.
pub fn use_host(host: Box<dyn BrowserHost>) {
    HOST.with_borrow_mut(|slot| *slot = Some(host));
}

/// Whether this thread runs pages: false where no engine is, such as on Linux.
pub fn hosts_pages() -> bool {
    HOST.with_borrow(Option::is_some)
}

/// Hands `heard` to the window, which takes it at its next frame. The host wakes the window.
pub fn hear(heard: Heard) {
    HEARD.with_borrow_mut(|all| all.push(heard));
}

pub(super) fn with_host(f: impl FnOnce(&mut dyn BrowserHost)) {
    HOST.with_borrow_mut(|slot| {
        if let Some(host) = slot.as_deref_mut() {
            f(host);
        }
    });
}

pub(super) fn take_heard() -> Vec<Heard> {
    HEARD.with_borrow_mut(std::mem::take)
}

/// A page's script opened `alert`. While an agent's action runs on the page the agent answers
/// it and this keeps it; otherwise it comes back for the host to show the person.
pub fn page_alert(page: &TileKey, alert: Alert) -> Option<Alert> {
    let acting =
        ACTING.with_borrow(|acting| acting.get(page).is_some_and(|a| !a.running.is_empty()));
    if !acting {
        return Some(alert);
    }
    let earlier = ACTING.with_borrow_mut(|acting| {
        let on_page = acting.entry(page.clone()).or_default();
        on_page.typed = None;
        on_page.alert.replace(alert)
    });
    if let Some(earlier) = earlier {
        (earlier.close)(None);
    }
    hear(Heard::Alerted { page: page.clone() });
    None
}

/// An edit key a focused page keeps for itself, as a web page's own Edit menu would run it.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum PageEdit {
    SelectAll,
    Copy,
    Cut,
    Paste,
    Undo,
    Redo,
}

/// The edit `ev` asks of a focused page: Command with a, c, x, v or z, and Shift-Command-Z.
pub fn page_edit(ev: &KeyEvent) -> Option<PageEdit> {
    let Key::Command(c) = ev.key else {
        return None;
    };
    if ev.mods.alt || (cfg!(target_os = "macos") && ev.mods.ctrl) {
        return None;
    }
    match (c, ev.mods.shift) {
        ('a', false) => Some(PageEdit::SelectAll),
        ('c', false) => Some(PageEdit::Copy),
        ('x', false) => Some(PageEdit::Cut),
        ('v', false) => Some(PageEdit::Paste),
        ('z', false) => Some(PageEdit::Undo),
        ('z', true) => Some(PageEdit::Redo),
        _ => None,
    }
}

/// Whether the window takes `ev` from a focused page: a key attn binds to an action the page
/// does not keep, other than the page's edit keys.
pub fn claims_key(ev: &KeyEvent) -> bool {
    page_edit(ev).is_none()
        && CLAIMED.with_borrow(|claimed| {
            claimed
                .iter()
                .any(|chord| chord.matches_on(ev, KeyPlatform::current()))
        })
}

pub(super) fn claim_keys(chords: Vec<Chord>) {
    CLAIMED.with_borrow_mut(|claimed| *claimed = chords);
}

/// Runs an agent's action on the page; its dialogs are the agent's until it is answered.
pub(super) fn run(page: &TileKey, request: Control) {
    ACTING.with_borrow_mut(|acting| {
        acting
            .entry(page.clone())
            .or_default()
            .running
            .insert(request.request_id.clone())
    });
    with_host(|h| h.control(page, request));
}

/// The action with this id answered: false when a dialog ended it first.
pub(super) fn settle(request_id: &str) -> bool {
    ACTING.with_borrow_mut(|acting| acting.values_mut().any(|a| a.running.remove(request_id)))
}

/// Ends every action running on `page`, by their ids.
pub(super) fn interrupt(page: &TileKey) -> Vec<String> {
    ACTING.with_borrow_mut(|acting| {
        acting
            .get_mut(page)
            .map(|a| a.running.drain().collect())
            .unwrap_or_default()
    })
}

pub(super) fn alert_text(page: &TileKey) -> Option<String> {
    ACTING.with_borrow(|acting| Some(acting.get(page)?.alert.as_ref()?.message.clone()))
}

/// Types `text` into the page's open prompt; false when no prompt is open.
pub(super) fn type_alert(page: &TileKey, text: &str) -> bool {
    ACTING.with_borrow_mut(|acting| {
        let Some(on_page) = acting.get_mut(page) else {
            return false;
        };
        let prompt = on_page
            .alert
            .as_ref()
            .is_some_and(|a| a.kind == AlertKind::Prompt);
        if prompt {
            on_page.typed = Some(text.to_owned());
        }
        prompt
    })
}

/// Accepts or dismisses the page's open dialog; false when none is open.
pub(super) fn close_alert(page: &TileKey, accept: bool) -> bool {
    let taken = ACTING.with_borrow_mut(|acting| {
        let on_page = acting.get_mut(page)?;
        let alert = on_page.alert.take()?;
        let typed = on_page.typed.take();
        Some((alert, typed))
    });
    let Some((alert, typed)) = taken else {
        return false;
    };
    let text = accept.then(|| typed.or(alert.default_text).unwrap_or_default());
    (alert.close)(text);
    true
}

/// The page is gone: its open dialog is dismissed and what ran on it is forgotten.
pub(super) fn forget(page: &TileKey) {
    let gone = ACTING.with_borrow_mut(|acting| acting.remove(page));
    if let Some(alert) = gone.and_then(|a| a.alert) {
        (alert.close)(None);
    }
}
