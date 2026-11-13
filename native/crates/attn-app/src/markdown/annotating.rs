//! Marking a document a tile shows, as the Tauri reader marks one: a selection let go over the
//! document brings a toolbar that strikes the passage, labels it or opens a box to comment on
//! it; marks paint over their passages, and a press on one picks it.

use std::collections::{BTreeMap, HashMap};
use std::ops::Range;

use attn_protocol::MarkdownAnnotationAnchor;
use attn_ui::Tokens;
use rune::tok::type_;
use rune::{
    Chord, Cx, El, Key, KeyEvent, KeyPlatform, SelectionChange, TextEdit, Tx, col, el, row, text,
    text_input,
};
use rune_kit::Highlight;

use super::anchoring::Resolved;
use super::marks::{self, Saying};
use crate::State;
use crate::annotations;

/// How far above the passage the toolbar sits, as the Tauri reader places it.
const TOOLBAR_ABOVE: f32 = 48.0;
/// The comment box's widest, and the room below a passage it needs to open there.
const COMPOSER_W: f32 = 384.0;
const COMPOSER_ROOM: f32 = 280.0;
const EDGE: f32 = 16.0;
/// How long a mark brought into view glows.
const FOCUS_SECONDS: f64 = 2.0;
const CHIP: f32 = 28.0;

/// A passage selected and let go, which the toolbar acts on.
#[derive(Clone, Debug)]
pub(crate) struct Pending {
    anchor: MarkdownAnnotationAnchor,
    source: Vec<Range<usize>>,
}

/// The box a comment, or a note on the whole document, is written in.
pub(crate) struct Composer {
    /// The passage it comments on; none for a note on the whole document.
    about: Option<Pending>,
    pub(crate) text: TextEdit,
}

/// What one document tile shows of its marks beyond the marks themselves.
#[derive(Default)]
pub(crate) struct Reading {
    pub(crate) uri: String,
    pub(crate) pending: Option<Pending>,
    pub(crate) picking: bool,
    pub(crate) composer: Option<Composer>,
    /// The mark last pressed.
    pub(crate) selected: Option<String>,
    /// A mark brought into view, glowing until then.
    pub(crate) focus: Option<(String, f64)>,
    /// The session picked in the header, until the desktop binds the tile to it.
    pub(crate) picked: Option<String>,
    pub(crate) sent: Option<super::drafts::Sent>,
    /// The header's session list, or the review notes, is open.
    pub(crate) menu: bool,
    pub(crate) notes: bool,
    /// "Clear all" waits for its confirmation until then.
    pub(crate) clearing: Option<f64>,
}

/// Each document tile's reading, by tile, and the comments written and not yet saved, by the
/// passage they are about.
#[derive(Default)]
pub struct Readings {
    pub(crate) tiles: HashMap<String, Reading>,
    unsaved: HashMap<String, String>,
    /// What a comment box that closed under an edit edits.
    closed: TextEdit,
    /// Which `<details>` blocks people opened or closed, by the scroll view their document
    /// reads in and the source byte each block's tag starts at.
    folds: HashMap<String, BTreeMap<usize, bool>>,
    /// Moves with every change, so views that show them build again.
    pub(crate) revision: u64,
}

impl Readings {
    /// The `<details>` blocks people opened or closed in the document `view` scrolls.
    pub(crate) fn folds(&self, view: &str) -> BTreeMap<usize, bool> {
        self.folds.get(view).cloned().unwrap_or_default()
    }

    pub(crate) fn fold(&mut self, view: &str, at: usize, open: bool) {
        self.folds
            .entry(view.to_owned())
            .or_default()
            .insert(at, open);
        self.revision += 1;
    }

    fn get_mut(&mut self, tile: &str) -> Option<&mut Reading> {
        self.revision += 1;
        self.tiles.get_mut(tile)
    }
}

/// The tile on screen whose toolbar or comment box is open. A box stays with its tile while
/// the tile is off screen, and shows again with it.
fn open(state: &State) -> Option<String> {
    let mut open = state
        .readings
        .tiles
        .iter()
        .filter(|(_, r)| r.pending.is_some() || r.composer.is_some())
        .peekable();
    open.peek()?;
    let shown = crate::desktop::on_screen_leaves(state);
    open.find(|(tile, _)| shown.contains(tile))
        .map(|(tile, _)| tile.clone())
}

/// The reader's key in tile `tile`, under which every piece of a selection in it sits.
pub(crate) fn root_key(tile: &str) -> String {
    format!("tile.{tile}.document.md")
}

fn key(tile: &str, part: &str) -> String {
    format!("tile.{tile}.document.{part}")
}

fn mark_key(tile: &str, mark: &str) -> String {
    key(tile, &format!("mark.{mark}"))
}

fn unsaved_key(uri: &str, about: Option<&Pending>) -> String {
    match about {
        Some(p) => format!(
            "{uri}#{}:{}:{}",
            p.anchor.block_id, p.anchor.start, p.anchor.end
        ),
        None => format!("{uri}#global"),
    }
}

/// What tile `tile` paints over its document: each placed mark, the passage the toolbar acts
/// on, and the glow of a mark brought into view. Marks press by their keys.
pub(crate) fn highlights(
    state_marks: Option<&super::drafts::Draft>,
    reading: Option<&Reading>,
    tile: &str,
    t: &Tokens,
) -> Vec<(Vec<Range<usize>>, Highlight)> {
    let mut out = Vec::new();
    if let Some(draft) = state_marks {
        for painted in &draft.painted {
            let glowing = reading
                .and_then(|r| r.focus.as_ref())
                .is_some_and(|(id, _)| *id == painted.id);
            let bg = match (glowing, painted.deletion) {
                (true, _) => t.accent.alpha(0.32),
                (false, true) => t.error.alpha(0.2),
                (false, false) => t.warning.alpha(0.28),
            };
            let highlight = Highlight::new().bg(bg).key(mark_key(tile, &painted.id));
            out.push((
                painted.source.clone(),
                if painted.deletion {
                    highlight.strike()
                } else {
                    highlight
                },
            ));
        }
    }
    if let Some(pending) = reading.and_then(|r| {
        r.composer
            .as_ref()
            .and_then(|c| c.about.as_ref())
            .or(r.pending.as_ref())
    }) {
        out.push((
            pending.source.clone(),
            Highlight::new()
                .bg(t.warning.alpha(0.28))
                .key(key(tile, "pending")),
        ));
    }
    out
}

/// A selection in tile `tile`'s document changed: let go over a passage, the toolbar opens on
/// it; gone, the toolbar closes.
pub(crate) fn selection_changed(
    state: &mut State,
    tile: &str,
    uri: &str,
    change: &SelectionChange,
    tx: &mut Tx,
) {
    if change.dragging() {
        return;
    }
    let pending = change.selection().and_then(|selection| {
        let passages = state.drafts.get(uri)?.passages.as_ref()?.1.clone();
        let anchor = passages.anchor(selection, &root_key(tile))?;
        let Resolved::Exact(found) = passages.blocks.resolve(&anchor) else {
            return None;
        };
        Some(Pending {
            anchor,
            source: passages.source(found),
        })
    });
    let reading = state.readings.tiles.entry(tile.to_owned()).or_default();
    if reading.composer.is_some() {
        return;
    }
    reading.uri = uri.to_owned();
    reading.picking = false;
    reading.pending = pending;
    state.readings.revision += 1;
    if state.readings.tiles[tile].pending.is_some() {
        for (_, r) in state.readings.tiles.iter_mut().filter(|(t, _)| *t != tile) {
            r.pending = None;
            r.picking = false;
        }
    }
    tx.wake_at(tx.clock());
}

/// A mark in tile `tile` was pressed: it is the one picked.
pub(crate) fn mark_pressed(state: &mut State, tile: &str, key_pressed: &str) {
    let prefix = mark_key(tile, "");
    let Some(id) = key_pressed.strip_prefix(&prefix) else {
        return;
    };
    let reading = state.readings.tiles.entry(tile.to_owned()).or_default();
    reading.selected = Some(id.to_owned());
    state.readings.revision += 1;
}

/// Closes tile `tile`'s toolbar, letting go of the selection.
fn close_toolbar(state: &mut State, tile: &str, tx: &mut Tx) {
    if let Some(reading) = state.readings.get_mut(tile) {
        reading.pending = None;
        reading.picking = false;
    }
    tx.clear_text_selection();
}

/// Marks the toolbar's passage in tile `tile` as saying `saying`, and closes the toolbar.
fn mark(state: &mut State, tile: &str, saying: Saying, tx: &mut Tx) {
    let Some(reading) = state.readings.tiles.get(tile) else {
        return;
    };
    let (uri, about) = (
        reading.uri.clone(),
        reading
            .composer
            .as_ref()
            .and_then(|c| c.about.clone())
            .or_else(|| reading.pending.clone()),
    );
    let now_ms = state.wall_ms(tx);
    let now = tx.clock();
    let Some(draft) = state.drafts.get_mut(&uri) else {
        return;
    };
    let mark = match (about, saying) {
        (Some(p), saying) => marks::new_mark(p.anchor, saying, now_ms),
        (None, Saying::Comment(text)) => marks::overall_note(text, now_ms),
        (None, _) => return,
    };
    draft.marks.push(mark);
    draft.changed(now);
    tx.wake_at(now);
    close_toolbar(state, tile, tx);
}

fn label(state: &mut State, tile: &str, label: &'static str, tx: &mut Tx) {
    mark(state, tile, Saying::Label(label), tx);
}

/// Opens the comment box in tile `tile`: on the toolbar's passage, or on the whole document
/// when there is none, holding what was written there before and then `typed`.
pub(crate) fn compose(state: &mut State, tile: &str, overall: bool, typed: &str, tx: &mut Tx) {
    let Some(reading) = state.readings.tiles.get_mut(tile) else {
        return;
    };
    let about = if overall {
        None
    } else {
        reading.pending.take()
    };
    reading.picking = false;
    let written = state
        .readings
        .unsaved
        .get(&unsaved_key(&reading.uri, about.as_ref()))
        .cloned()
        .unwrap_or_default();
    let mut edit = TextEdit::new(format!("{written}{typed}"));
    edit.set_caret(edit.text.len());
    reading.composer = Some(Composer { about, text: edit });
    state.readings.revision += 1;
    tx.focus(key(tile, "composer.field"), false);
}

/// Opens tile `tile`'s comment box on the whole document.
pub(crate) fn compose_overall(state: &mut State, tile: &str, uri: &str, tx: &mut Tx) {
    let reading = state.readings.tiles.entry(tile.to_owned()).or_default();
    reading.uri = uri.to_owned();
    compose(state, tile, true, "", tx);
}

/// Closes tile `tile`'s comment box, keeping what it holds for the next time it opens on the
/// same passage.
fn close_if_empty(state: &mut State, tile: &str, tx: &mut Tx) {
    let empty = state
        .readings
        .tiles
        .get(tile)
        .and_then(|r| r.composer.as_ref())
        .is_some_and(|c| c.text.text.trim().is_empty());
    if empty {
        close_composer(state, tile, tx);
    }
}

fn close_composer(state: &mut State, tile: &str, tx: &mut Tx) {
    let Some(reading) = state.readings.tiles.get_mut(tile) else {
        return;
    };
    if let Some(composer) = reading.composer.take() {
        let key = unsaved_key(&reading.uri, composer.about.as_ref());
        if composer.text.text.trim().is_empty() {
            state.readings.unsaved.remove(&key);
        } else {
            state
                .readings
                .unsaved
                .insert(key, composer.text.text.clone());
        }
    }
    state.readings.revision += 1;
    tx.blur();
    tx.clear_text_selection();
}

/// Keeps the comment written in tile `tile`'s box as a mark.
fn save_comment(state: &mut State, tile: &str, tx: &mut Tx) {
    let Some(reading) = state.readings.tiles.get_mut(tile) else {
        return;
    };
    let Some(composer) = reading.composer.as_ref() else {
        return;
    };
    let comment = composer.text.text.trim().to_owned();
    if comment.is_empty() {
        return;
    }
    let key = unsaved_key(&reading.uri, composer.about.as_ref());
    state.readings.unsaved.remove(&key);
    mark(state, tile, Saying::Comment(comment), tx);
    if let Some(reading) = state.readings.get_mut(tile) {
        reading.composer = None;
    }
    tx.blur();
}

/// A key while a toolbar or a comment box is open and no field holds the keyboard: Escape
/// closes it, ⌥ and a digit picks the label the list numbers, a digit picks one while the
/// list is open, and a character starts a comment with it. True when it took the key.
pub(crate) fn on_key(state: &mut State, ev: &KeyEvent, tx: &mut Tx) -> bool {
    let Some(tile) = open(state) else {
        return false;
    };
    let reading = &state.readings.tiles[&tile];
    if reading.composer.is_some() {
        if ev.key != Key::Escape {
            return false;
        }
        close_composer(state, &tile, tx);
        return true;
    }
    let picking = reading.picking;
    let digit = if ev.mods.cmd || ev.mods.ctrl || ev.mods.logo {
        None
    } else if ev.mods.alt {
        let platform = KeyPlatform::current();
        (1..=9).find(|n| {
            Chord::parse(&format!("alt+{n}"))
                .expect("an alt digit chord parses")
                .matches_tx_on(ev, tx, platform)
        })
    } else if picking {
        match &ev.key {
            Key::Text(typed) => typed.parse::<u32>().ok().filter(|n| (1..=9).contains(n)),
            _ => None,
        }
    } else {
        None
    };
    match &ev.key {
        Key::Escape if picking => {
            if let Some(r) = state.readings.get_mut(&tile) {
                r.picking = false;
            }
        }
        Key::Escape => close_toolbar(state, &tile, tx),
        _ if let Some(digit) = digit => {
            if let Some(picked) = annotations::picker_groups()
                .into_iter()
                .flatten()
                .nth(digit as usize - 1)
            {
                label(state, &tile, picked.id, tx);
            }
        }
        Key::Text(typed)
            if !picking
                && !ev.mods.cmd
                && !ev.mods.ctrl
                && !ev.mods.alt
                && !ev.mods.logo
                && typed.chars().count() == 1 =>
        {
            let typed = typed.clone();
            compose(state, &tile, false, &typed, tx);
        }
        _ => return false,
    }
    true
}

/// The comment box of an overall note, over the window under the header's button.
pub(crate) fn layers(state: &State, cx: &Cx<State>, t: &Tokens) -> Vec<El> {
    let Some(tile) = open(state) else {
        return Vec::new();
    };
    let Some(composer) = state.readings.tiles[&tile]
        .composer
        .as_ref()
        .filter(|c| c.about.is_none())
    else {
        return Vec::new();
    };
    let window = cx.layout("window").unwrap_or([0.0, 0.0, 1200.0, 800.0]);
    let around = cx.layout(&key(&tile, "overall"));
    vec![composer_box(&tile, composer, around, window, t)]
}

/// The toolbar, or the comment box on a passage, of tile `tile`, placed beside their passage in
/// the card that holds its reader `inset` points in, so they scroll with it.
pub(crate) fn in_document(readings: &Readings, tile: &str, inset: f32, t: &Tokens) -> Vec<El> {
    let Some(reading) = readings.tiles.get(tile) else {
        return Vec::new();
    };
    let shown =
        reading.pending.is_some() || reading.composer.as_ref().is_some_and(|c| c.about.is_some());
    let (Some(reader), Some(passage)) = (
        shown.then(|| rune::memo::layout(&root_key(tile))).flatten(),
        rune::memo::layout(&key(tile, "pending")),
    ) else {
        return Vec::new();
    };
    let around = [
        passage[0] - reader[0] + inset,
        passage[1] - reader[1] + inset,
        passage[2],
        passage[3],
    ];
    let frame = [0.0, 0.0, reader[2] + 2.0 * inset, reader[3] + 2.0 * inset];
    if let Some(composer) = &reading.composer {
        return vec![composer_box(tile, composer, Some(around), frame, t)];
    }
    let size = rune::memo::layout(&key(tile, "toolbar")).unwrap_or([0.0, 0.0, 260.0, 36.0]);
    let left = (around[0] + around[2] / 2.0 - size[2] / 2.0)
        .clamp(EDGE, (frame[2] - size[2] - EDGE).max(EDGE));
    let above = around[1] - TOOLBAR_ABOVE;
    let top = if above >= 0.0 {
        above
    } else {
        around[1] + around[3] + TOOLBAR_ABOVE - size[3]
    };
    vec![toolbar(tile, reading.picking, t).absolute(left, top)]
}

fn button(tile: &str, part: &str, glyph: &str, label: &str, t: &Tokens) -> El {
    text(glyph.to_owned())
        .key(key(tile, &format!("toolbar.{part}")))
        .role("button")
        .label(label)
        .font(type_::BODY)
        .color(t.text_primary)
        .w(CHIP)
        .h(CHIP)
        .align(rune::Align::Center)
        .radius(6.0)
}

fn toolbar(tile: &str, picking: bool, t: &Tokens) -> El {
    let on = |f: fn(&mut State, &str, &mut Tx)| {
        let tile = tile.to_owned();
        move |state: &mut State, tx: &mut Tx| f(state, &tile, tx)
    };
    let mut parts = vec![
        button(tile, "delete", attn_ui::icon::TRASH, "Delete", t).on_press_tx(on(
            |state, tile, tx| mark(state, tile, Saying::Deletion, tx),
        )),
        button(tile, "comment", attn_ui::icon::COMMENT, "Comment", t)
            .on_press_tx(on(|state, tile, tx| compose(state, tile, false, "", tx))),
        button(tile, "zap", attn_ui::icon::BOLT, "Quick label", t)
            .fill(if picking {
                t.accent.alpha(0.2)
            } else {
                rune::Color::TRANSPARENT
            })
            .on_press(on_picker(tile)),
    ];
    for promoted in annotations::promoted_labels() {
        let (tile, id) = (tile.to_owned(), promoted.id);
        parts.push(
            button(
                &tile,
                &format!("label.{id}"),
                promoted.emoji,
                promoted.text,
                t,
            )
            .on_press_tx(move |state: &mut State, tx: &mut Tx| label(state, &tile, id, tx)),
        );
    }
    parts.push(el().w(1.0).h(18.0).mx(4.0).fill(t.border));
    parts.push(
        button(tile, "cancel", attn_ui::icon::CLOSE, "Cancel", t).on_press_tx(on(close_toolbar)),
    );
    let list = picking.then(|| {
        let tile = tile.to_owned();
        crate::desktop::annotating::label_list(
            &key(&tile, "toolbar"),
            move |state: &mut State, id, tx: &mut Tx| label(state, &tile, id, tx),
            t,
        )
    });
    col()
        .key(key(tile, "toolbar"))
        .role("toolbar")
        .label("Annotate selection")
        .keeps_text_selection()
        .padding(4.0)
        .radius(8.0)
        .fill(t.bg_elevated)
        .border(1.0, t.border)
        .on_press(|_: &mut State| {})
        .child(row().items_center().gap(2.0).children(parts))
        .children(list)
}

fn on_picker(tile: &str) -> impl Fn(&mut State) + 'static {
    let tile = tile.to_owned();
    move |state: &mut State| {
        if let Some(r) = state.readings.get_mut(&tile) {
            r.picking = !r.picking;
        }
    }
}

fn composer_box(
    tile: &str,
    composer: &Composer,
    around: Option<[f32; 4]>,
    window: [f32; 4],
    t: &Tokens,
) -> El {
    let width = COMPOSER_W.min(window[2] - 2.0 * EDGE);
    let around = around.unwrap_or([window[2] / 2.0, window[3] / 3.0, 0.0, 0.0]);
    let left = around[0].clamp(EDGE, (window[2] - width - EDGE).max(EDGE));
    let below = around[1] + around[3] + 8.0;
    let top = if window[3] - below < COMPOSER_ROOM {
        (around[1] - COMPOSER_ROOM).max(EDGE)
    } else {
        below
    };
    let heading = match &composer.about {
        Some(p) => {
            let quote: String = p.anchor.exact.chars().take(50).collect();
            let cut = if p.anchor.exact.chars().count() > 50 {
                "..."
            } else {
                ""
            };
            format!("\u{201c}{quote}{cut}\u{201d}")
        }
        None => "Overall Note".to_owned(),
    };
    let overall = composer.about.is_none();
    let blank = composer.text.text.trim().is_empty();
    let (edited, keyed, saved, closed, outside) = (
        tile.to_owned(),
        tile.to_owned(),
        tile.to_owned(),
        tile.to_owned(),
        tile.to_owned(),
    );
    let send_key = if cfg!(target_os = "macos") {
        "⌘Enter"
    } else {
        "Ctrl+Enter"
    };
    let field = text_input(&composer.text, type_::BODY)
        .key(key(tile, "composer.field"))
        .label(if overall { "Overall note" } else { "Comment" })
        .placeholder(
            if overall {
                "Add an overall note..."
            } else {
                "Add a comment..."
            },
            t.text_muted,
        )
        .on_edit(move |state: &mut State| composer_text(state, &edited))
        .on_input_key(
            move |state: &mut State, ev: &KeyEvent, tx: &mut Tx| match ev.key {
                Key::Escape => {
                    close_composer(state, &keyed, tx);
                    true
                }
                Key::Enter if ev.mods.cmd || ev.mods.ctrl => {
                    save_comment(state, &keyed, tx);
                    true
                }
                _ => false,
            },
        )
        .w_full()
        .min_h(72.0)
        .px(8.0)
        .py(6.0)
        .radius(6.0)
        .fill(t.bg_input)
        .border(1.0, t.border)
        .color(t.text_primary);
    col()
        .key(key(tile, "composer"))
        .role("dialog")
        .label(if overall { "Overall Note" } else { "Comment" })
        .w(width)
        .gap(8.0)
        .padding(10.0)
        .radius(8.0)
        .fill(t.bg_elevated)
        .border(1.0, t.border)
        .on_press(|_: &mut State| {})
        .modal_tx(true, move |state: &mut State, tx: &mut Tx| {
            close_if_empty(state, &outside, tx)
        })
        .modal_press_through(true)
        .child(
            row()
                .items_center()
                .child(
                    text(heading)
                        .font(type_::LABEL)
                        .color(t.text_secondary)
                        .single_line()
                        .truncate()
                        .grow(1.0),
                )
                .child(
                    text(attn_ui::icon::CLOSE)
                        .key(key(tile, "composer.close"))
                        .role("button")
                        .label("Close")
                        .font(type_::LABEL)
                        .color(t.text_tertiary)
                        .px(4.0)
                        .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                            close_composer(state, &closed, tx)
                        }),
                ),
        )
        .child(field)
        .child(
            row()
                .items_center()
                .child(
                    text(format!("{send_key} to save"))
                        .font(type_::LABEL)
                        .color(t.text_tertiary)
                        .grow(1.0),
                )
                .child(
                    text(if overall { "Add" } else { "Save" })
                        .key(key(tile, "composer.save"))
                        .role("button")
                        .disabled(blank)
                        .font(type_::LABEL)
                        .color(if blank { t.text_muted } else { t.text_primary })
                        .px(12.0)
                        .py(4.0)
                        .radius(6.0)
                        .fill(t.bg_button)
                        .border(1.0, t.border)
                        .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                            save_comment(state, &saved, tx)
                        }),
                ),
        )
        .absolute(left, top)
}

/// The text tile `tile`'s comment box edits; a box that closed edits nothing that stays.
fn composer_text<'a>(state: &'a mut State, tile: &str) -> &'a mut TextEdit {
    let readings = &mut state.readings;
    readings.revision += 1;
    match readings
        .tiles
        .get_mut(tile)
        .and_then(|r| r.composer.as_mut())
    {
        Some(composer) => &mut composer.text,
        None => {
            readings.closed = TextEdit::default();
            &mut readings.closed
        }
    }
}

/// Lets each glow whose time is up go. True when one went.
pub(crate) fn expire(state: &mut State, now: f64) -> bool {
    let mut went = false;
    for reading in state.readings.tiles.values_mut() {
        if reading
            .focus
            .as_ref()
            .is_some_and(|(_, until)| *until <= now)
        {
            reading.focus = None;
            went = true;
        }
    }
    if went {
        state.readings.revision += 1;
    }
    went
}

/// Brings mark `mark` of tile `tile` into view, glowing a while.
pub(crate) fn focus(state: &mut State, tile: &str, mark: &str, tx: &mut Tx) {
    let until = tx.clock() + FOCUS_SECONDS;
    let reading = state.readings.tiles.entry(tile.to_owned()).or_default();
    reading.selected = Some(mark.to_owned());
    reading.focus = Some((mark.to_owned(), until));
    state.readings.revision += 1;
    tx.scroll_into_view(mark_key(tile, mark));
    tx.wake_at(until);
}
