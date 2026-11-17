//! Tiles in windows of their own: a popped-out leaf leaves its desktop's columns for a window
//! where the daemon's `window` frame says.

use std::sync::Arc;

use attn_protocol::{DesktopBringBackLeafMessage, DesktopPopOutLeafMessage, TileWindowFrame};
use rune::{El, Tx, col};

use super::layout::{Direction, Frame, Node};
use crate::action::Action;
use crate::requests::Refused;
use crate::{State, Update};

/// The smallest window a tile pops out into, in points.
const MIN_WIDTH: f64 = 320.0;
const MIN_HEIGHT: f64 = 200.0;

/// Pops the current desktop's active tile out, or brings it back when it is out.
pub(crate) fn toggle(state: &mut State, tx: &mut Tx) {
    let Some((desktop, leaf)) = state.window.acting(&state.facts) else {
        return;
    };
    let desktop = desktop.id.clone();
    toggle_leaf(state, &desktop, &leaf, tx);
}

pub(crate) fn toggle_leaf(state: &mut State, desktop: &str, leaf: &str, tx: &mut Tx) {
    if is_popped(state, desktop, leaf) {
        bring_back(state, desktop, leaf, tx);
    } else if pops_out(state, desktop, leaf) {
        pop_out(state, desktop, leaf, tx);
    }
}

/// Whether `leaf` is a tile of `desktop`, which can show in a window of its own.
pub(crate) fn pops_out(state: &State, desktop: &str, leaf: &str) -> bool {
    state
        .facts
        .desktops
        .iter()
        .find(|d| d.id == desktop)
        .and_then(|d| Node::parse(&d.tree_json))
        .is_some_and(|tree| matches!(tree.find_leaf(leaf), Some(Node::Leaf(_))))
}

pub(super) fn is_popped(state: &State, desktop: &str, leaf: &str) -> bool {
    state
        .facts
        .desktops
        .iter()
        .find(|d| d.id == desktop)
        .and_then(|d| Node::parse(&d.tree_json))
        .is_some_and(|tree| tree.popped().iter().any(|l| l.id == leaf))
}

/// Asks the daemon to pop `leaf` out into a window over where the tile shows now.
fn pop_out(state: &mut State, desktop: &str, leaf: &str, tx: &mut Tx) {
    let Some(&[x, y, w, h]) = state.window.desk.rects.get(&format!("tile.{leaf}")) else {
        return;
    };
    let (left, top) = crate::frame::main_content_origin();
    let frame = TileWindowFrame {
        x: left + f64::from(x),
        y: top + f64::from(y),
        width: f64::from(w).max(MIN_WIDTH),
        height: f64::from(h).max(MIN_HEIGHT),
    };
    let (desktop_id, leaf_id) = (desktop.to_owned(), leaf.to_owned());
    state.act(
        |request_id| DesktopPopOutLeafMessage {
            request_id,
            desktop_id,
            leaf_id,
            frame,
            ..DesktopPopOutLeafMessage::new()
        },
        Refused::Prefixed("Could not pop the tile out"),
        |_, _, _| {},
        tx,
    );
}

pub(crate) fn bring_back(state: &mut State, desktop: &str, leaf: &str, tx: &mut Tx) {
    let (desktop_id, leaf_id) = (desktop.to_owned(), leaf.to_owned());
    state.act(
        |request_id| DesktopBringBackLeafMessage {
            request_id,
            desktop_id,
            leaf_id,
            ..DesktopBringBackLeafMessage::new()
        },
        Refused::Prefixed("Could not bring the tile back"),
        |_, _, _| {},
        tx,
    );
}

/// Hands a window a change to run on its own state, as `bind`'s `deliver` does.
pub type Deliver = Arc<dyn Fn(Update) + Send + Sync>;

/// A window's content frame on the desktop, in points.
pub type Placement = Box<dyn Fn() -> Option<Frame> + Send>;

/// Opens the windows popped-out tiles show in: native windows in the app, windows the test
/// world drives in a wire test. `open` binds the window to the store, then runs `setup` in it.
pub trait Opener: Send + Sync {
    fn open(&self, key: &str, frame: &Frame, setup: Setup) -> Box<dyn Opened>;
}

/// A window the pool opened.
pub trait Opened {
    /// Moves the window to `frame` and shows it, leaving the keyboard where it is.
    fn place(&self, frame: &Frame);
    /// Moves the window to `frame` and shows it in front of the app's other windows with the
    /// app's keyboard, as a newly opened window takes them, leaving the app's activation alone.
    fn pop_out(&self, frame: &Frame);
    fn hide(&self);
    /// Runs `update` on the window's state.
    fn update(&self, update: Update);
}

/// What a tile window needs before it shows its leaf: which leaf, its way to the main
/// window, and the main window's terminal events, whose share for this window
/// `TerminalInbox::for_window` takes.
pub struct Setup {
    pub key: String,
    pub desktop: String,
    pub leaf: String,
    pub main: Deliver,
    pub terminals: crate::TerminalInbox,
}

impl Setup {
    /// The change that makes a freshly bound window this tile window; `placement` reads where
    /// the window is.
    pub fn into_update(self, placement: Placement) -> Update {
        let Setup {
            key,
            desktop,
            leaf,
            main,
            ..
        } = self;
        Box::new(move |state: &mut State, tx: &mut Tx| {
            state.window.hold_keyboard(&desktop, &leaf);
            state.window.tile = Some(TileWindow {
                key,
                desktop,
                leaf,
                main,
                placement,
                quitting: false,
                title: None,
            });
            follow_title(state, tx);
            crate::follow_terminals(state, tx);
        })
    }
}

/// What makes a window a tile window: the leaf it shows, and the main window that owns it.
pub struct TileWindow {
    key: String,
    pub(crate) desktop: String,
    pub(crate) leaf: String,
    main: Deliver,
    placement: Placement,
    quitting: bool,
    title: Option<String>,
}

/// The leaf a tile window shows, by desktop and leaf id; none once it gave its leaf back.
pub fn tile_window_leaf(state: &State) -> Option<(String, String)> {
    let tile = state.window.tile.as_ref().filter(|t| !t.leaf.is_empty())?;
    Some((tile.desktop.clone(), tile.leaf.clone()))
}

/// The main window's tile windows: each holds a popped-out leaf, or waits hidden to be reused.
#[derive(Default)]
pub struct Pool {
    pub opener: Option<Arc<dyn Opener>>,
    /// Hands a tile window's changes to the main window.
    pub main: Option<Deliver>,
    /// The person closed the main window while a tile window showed, so it hid; it shows again
    /// once no tile window does.
    pub main_hidden: bool,
    windows: Vec<Pooled>,
    opened: usize,
}

impl Pool {
    /// A tile window is on the desktop, holding a popped-out leaf.
    pub(crate) fn shows_a_window(&self) -> bool {
        self.windows.iter().any(|w| w.held.is_some() && !w.closed)
    }

    /// Hands each tile window, shown or hidden, the replies the main window heard and did not
    /// wait for, so a tile window's requests are answered, its saves among them.
    pub(crate) fn hand(&self, replies: Vec<attn_client::Reply>) {
        if replies.is_empty() {
            return;
        }
        for pooled in &self.windows {
            let replies = replies.clone();
            pooled.window.update(Box::new(move |state, _| {
                crate::requests::receive(state, replies);
            }));
        }
    }
}

struct Pooled {
    key: String,
    held: Option<Held>,
    window: Box<dyn Opened>,
    /// The person closed the window, which hid it; it holds its leaf until the daemon brings
    /// it back, and is then used again.
    closed: bool,
}

#[derive(Clone, PartialEq)]
struct Held {
    desktop: String,
    leaf: String,
    frame: Frame,
}

/// Keeps a window on each popped-out leaf of the profile's desktops, where the daemon says
/// it is, hides the windows of leaves that came back to use them again, and shows the main
/// window it hid once no tile window shows.
pub(crate) fn follow(state: &mut State) {
    if state.window.tile.is_some() {
        return;
    }
    let terminals = state.terminals.inbox.clone();
    let pool = &mut state.window.desk.windows;
    let (Some(opener), Some(main)) = (pool.opener.clone(), pool.main.clone()) else {
        return;
    };
    let wanted: Vec<Held> = state
        .facts
        .desktops
        .iter()
        .filter(|d| d.profile_id == state.facts.profiles.selected)
        .filter_map(|d| Some((d, Node::parse(&d.tree_json)?)))
        .flat_map(|(d, tree)| {
            tree.popped()
                .into_iter()
                .filter_map(|leaf| {
                    Some(Held {
                        desktop: d.id.clone(),
                        leaf: leaf.id.clone(),
                        frame: leaf.window?,
                    })
                })
                .collect::<Vec<_>>()
        })
        .collect();
    for pooled in &mut pool.windows {
        let gone = pooled.held.as_ref().is_some_and(|held| {
            !wanted
                .iter()
                .any(|w| w.desktop == held.desktop && w.leaf == held.leaf)
        });
        if gone && !pooled.closed {
            pooled.window.hide();
        }
        if gone {
            pooled.window.update(Box::new(|state, tx| {
                if let Some(tile) = &mut state.window.tile {
                    tile.leaf.clear();
                }
                crate::follow_terminals(state, tx);
            }));
            pooled.held = None;
            pooled.closed = false;
        }
    }
    for want in wanted {
        let held = pool.windows.iter_mut().find(|p| {
            p.held
                .as_ref()
                .is_some_and(|h| h.desktop == want.desktop && h.leaf == want.leaf)
        });
        if let Some(pooled) = held {
            if !pooled.closed && pooled.held.as_ref().map(|h| h.frame) != Some(want.frame) {
                pooled.window.place(&want.frame);
                pooled.held = Some(want);
            }
            continue;
        }
        let (desktop, leaf) = (want.desktop.clone(), want.leaf.clone());
        match pool.windows.iter_mut().find(|p| p.held.is_none()) {
            Some(pooled) => {
                pooled.window.update(Box::new(move |state, tx| {
                    state.window.hold_keyboard(&desktop, &leaf);
                    if let Some(tile) = &mut state.window.tile {
                        tile.desktop = desktop;
                        tile.leaf = leaf;
                    }
                    follow_title(state, tx);
                    crate::follow_terminals(state, tx);
                }));
                pooled.window.pop_out(&want.frame);
                pooled.held = Some(want);
            }
            None => {
                pool.opened += 1;
                let key = format!("tile-window-{}", pool.opened);
                let window = opener.open(
                    &key,
                    &want.frame,
                    Setup {
                        key: key.clone(),
                        desktop,
                        leaf,
                        main: main.clone(),
                        terminals: terminals.clone(),
                    },
                );
                pool.windows.push(Pooled {
                    key,
                    held: Some(want),
                    window,
                    closed: false,
                });
            }
        }
    }
    if pool.main_hidden && !pool.shows_a_window() {
        pool.main_hidden = false;
        crate::frame::open_main();
    }
}

/// The tile window `key` closed, which hid it: it waits for the daemon to bring its leaf back.
fn forget(state: &mut State, key: &str) {
    let pool = &mut state.window.desk.windows;
    if let Some(pooled) = pool.windows.iter_mut().find(|p| p.key == key) {
        pooled.closed = true;
    }
    follow(state);
}

/// Runs `update` in the main window, when this is a tile window.
pub(crate) fn to_main(state: &State, update: Update) -> bool {
    let Some(tile) = &state.window.tile else {
        return false;
    };
    (tile.main)(update);
    true
}

/// Runs `action`, pressed in a tile window, in the main window: the tile's own actions act on
/// it, the row's do nothing, and the rest run as if pressed in the main window, which comes to
/// the front when one ran.
pub(crate) fn forward(state: &mut State, action: Action) -> bool {
    let Some(tile) = &state.window.tile else {
        return false;
    };
    let (desktop, leaf) = (tile.desktop.clone(), tile.leaf.clone());
    (tile.main)(Box::new(move |state: &mut State, tx: &mut Tx| {
        run_from(state, &desktop, &leaf, action, tx)
    }));
    true
}

fn run_from(state: &mut State, desktop: &str, leaf: &str, action: Action, tx: &mut Tx) {
    use Action::*;
    match action {
        TilePopOut | SessionClose | TerminalClose => bring_back(state, desktop, leaf, tx),
        TerminalSplitVertical => {
            super::actions::split_at(state, desktop, leaf, Direction::Vertical, tx)
        }
        TerminalSplitHorizontal => {
            super::actions::split_at(state, desktop, leaf, Direction::Horizontal, tx)
        }
        ShelfCarryLeft
        | ShelfCarryRight
        | ShelfSendToShelf
        | ShelfFewerTiles
        | ShelfMoreTiles
        | TerminalToggleZoom
        | TerminalToggleMaximize
        | TerminalFocusLeft
        | TerminalFocusRight
        | TerminalFocusUp
        | TerminalFocusDown
        | TerminalOpen => {}
        action => {
            if crate::dispatch::run(state, action, tx) == crate::dispatch::Outcome::Ran {
                crate::frame::raise_main();
            }
        }
    }
}

/// The tile window's content: its leaf, with its header, filling the window.
pub(crate) fn view(state: &State, cx: &mut rune::Cx<State>) -> El {
    let t = crate::colors(&state.facts.settings, cx.system_dark()).ui;
    cx.zoom(crate::settings::text_scale(&state.facts.settings));
    let shown = state.window.tile.as_ref().and_then(|tile| {
        let desktop = state.facts.desktops.iter().find(|d| d.id == tile.desktop)?;
        let tree = Node::parse(&desktop.tree_json)?;
        let Some(Node::Leaf(leaf)) = tree.find_leaf(&tile.leaf) else {
            return None;
        };
        Some(super::view::lone(
            state,
            desktop,
            leaf,
            &t,
            cx.env().scale,
            cx.chrome_insets().leading,
        ))
    });
    let root = col()
        .key("window")
        .w_full()
        .h_full()
        .padding(4.0)
        .fill(t.bg_app)
        .on_layout(|state: &mut State, [_, _, w, _]: [f32; 4]| {
            if state.notebook.window_w != w {
                state.notebook.window_w = w;
                state.notebook.revision += 1;
            }
        })
        .children(shown);
    crate::markdown::with_image_preview(root, state, cx.is_reduced_motion(), &t)
}

/// Names the tile window after its leaf, as the leaf's header does.
pub(crate) fn follow_title(state: &mut State, tx: &mut Tx) {
    let Some(tile) = &state.window.tile else {
        return;
    };
    let title = state
        .facts
        .desktops
        .iter()
        .find(|d| d.id == tile.desktop)
        .and_then(|d| {
            let tree = Node::parse(&d.tree_json)?;
            match tree.find_leaf(&tile.leaf)? {
                Node::Leaf(leaf) => Some(super::view::leaf_title(
                    d,
                    &state.facts.sessions,
                    &state.facts.tile_contents,
                    crate::garden::seeds(state),
                    leaf,
                )),
                Node::Split(_) => None,
            }
        });
    if let Some(tile) = &mut state.window.tile
        && let Some(title) = title
        && tile.title.as_ref() != Some(&title)
    {
        tx.set_title(title.clone());
        tile.title = Some(title);
    }
}

/// The tile window lost focus: if the person moved or resized it, the daemon keeps where.
pub(crate) fn on_focus(state: &mut State, focused: bool, _: &mut Tx) {
    if !focused {
        report_frame(state);
    }
}

fn report_frame(state: &State) {
    let Some(tile) = &state.window.tile else {
        return;
    };
    let Some(frame) = (tile.placement)() else {
        return;
    };
    let kept = state
        .facts
        .desktops
        .iter()
        .find(|d| d.id == tile.desktop)
        .and_then(|d| Node::parse(&d.tree_json))
        .and_then(|tree| tree.popped().iter().find(|l| l.id == tile.leaf)?.window);
    if kept.is_none() || kept == Some(frame) {
        return;
    }
    let _ = state.daemon.send(&DesktopPopOutLeafMessage {
        request_id: super::request_id(),
        desktop_id: tile.desktop.clone(),
        leaf_id: tile.leaf.clone(),
        frame: TileWindowFrame {
            x: frame.x,
            y: frame.y,
            width: frame.width,
            height: frame.height,
        },
        ..DesktopPopOutLeafMessage::new()
    });
}

/// The app quits: the tile stays out for the next launch.
pub fn on_quit(state: &mut State, _: &mut Tx) {
    report_frame(state);
    if let Some(tile) = &mut state.window.tile {
        tile.quitting = true;
    }
}

/// The person closed the tile window: its tile comes back to its desktop, and the window
/// hides with its state, as bringing the tile back hides it, so an edit it holds still saves.
pub fn on_close(state: &mut State, _: &mut Tx) {
    let Some(tile) = &state.window.tile else {
        return;
    };
    if tile.quitting {
        return;
    }
    let (key, desktop, leaf) = (tile.key.clone(), tile.desktop.clone(), tile.leaf.clone());
    (tile.main)(Box::new(move |state: &mut State, tx: &mut Tx| {
        bring_back(state, &desktop, &leaf, tx);
        forget(state, &key)
    }));
}
