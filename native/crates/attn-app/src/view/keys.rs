//! The keys a view answers while its tile holds the keyboard: j and k move the highlight over
//! the view's rows, foldable headings and `one-by-one` parts; ↩ folds a heading, and keeps
//! and goes on in a `one-by-one`, where s skips and ← and → step back and on.

use attn_protocol::{ViewDocument, ViewPart};
use rune::{Key, KeyEvent, Tx};

use super::values::{Data, Scope};
use super::{TileState, draw};
use crate::State;

/// A place the highlight can rest.
#[derive(Clone, Debug, PartialEq)]
enum Stop {
    /// A list's foldable heading, by the list's key.
    Fold(String),
    Row(String),
    /// A `one-by-one`, with how many items it has.
    Steps(String, usize),
}

impl Stop {
    fn key(&self) -> String {
        match self {
            Stop::Fold(list) => format!("{list}.head"),
            Stop::Row(key) | Stop::Steps(key, _) => key.clone(),
        }
    }
}

/// Visits `parts` and every part inside them.
pub(crate) fn each_part(parts: &[ViewPart], visit: &mut impl FnMut(&ViewPart)) {
    for part in parts {
        visit(part);
        if let Some(each) = &part.each {
            each_part(&each.blocks, visit);
        }
        if let Some(steps) = &part.one_by_one {
            each_part(&steps.blocks, visit);
        }
    }
}

/// Whether any of `parts` draws an instant as an age.
pub(crate) fn ages(parts: &[ViewPart]) -> bool {
    serde_json::to_string(parts).is_ok_and(|json| json.contains("\"format\":\"ago\""))
}

/// The markdown of `parts`' prose and pictures, with each item's values in place.
pub(crate) fn each_text(parts: &[ViewPart], scope: &Scope<'_>, out: &mut impl FnMut(String)) {
    for part in parts {
        if let Some(prose) = &part.markdown {
            out(scope.text(&prose.text));
        }
        let media = part.media.iter().map(|m| (&m.label, &m.file));
        let compared = part
            .compare
            .iter()
            .flat_map(|c| &c.media)
            .map(|m| (&m.label, &m.file));
        for (label, file) in media.chain(compared) {
            out(format!("![{}]({})", scope.text(label), scope.text(file)));
        }
        let containers = part.each.iter().map(|e| (&e.items, &e.blocks));
        let steps = part.one_by_one.iter().map(|s| (&s.items, &s.blocks));
        for (items, blocks) in containers.chain(steps) {
            for item in scope.items(items) {
                each_text(blocks, &scope.within(item), out);
            }
        }
    }
}

/// The places the highlight can rest in `parts`, in order, as `parts` draws them under
/// `prefix`.
fn stops(
    parts: &[ViewPart],
    scope: &Scope<'_>,
    prefix: &str,
    state: &TileState,
    out: &mut Vec<Stop>,
) {
    for (i, part) in parts.iter().enumerate() {
        if part.problem.is_some() {
            continue;
        }
        let key = format!("{prefix}.{i}");
        if let Some(list) = &part.list {
            let items = scope.items(&list.items);
            let headed = i
                .checked_sub(1)
                .and_then(|above| parts[above].markdown.as_ref())
                .is_some_and(|prose| draw::last_heading(&scope.text(&prose.text)).is_some());
            let shown = !items.is_empty() || list.slots.empty.is_some();
            if headed && shown && list.foldable == Some(true) {
                out.push(Stop::Fold(key.clone()));
            }
            if !state.folded.contains(&key) {
                out.extend((0..items.len()).map(|n| Stop::Row(format!("{key}.row.{n}"))));
            }
        }
        if let Some(table) = &part.table {
            let rows = scope.items(&table.items).len();
            out.extend((0..rows).map(|n| Stop::Row(format!("{key}.row.{n}"))));
        }
        if let Some(each) = &part.each {
            for (n, item) in scope.items(&each.items).iter().enumerate() {
                stops(
                    &each.blocks,
                    &scope.within(item),
                    &format!("{key}.{n}"),
                    state,
                    out,
                );
            }
        }
        if let Some(steps) = &part.one_by_one {
            out.push(Stop::Steps(key, scope.items(&steps.items).len()));
        }
    }
}

/// The view tile holding the keyboard, with nothing over it: its tile and its view.
fn held(state: &State) -> Option<(String, &ViewDocument)> {
    if !state.window.tiles_in_front() {
        return None;
    }
    let holder = state.window.keyboard.as_ref()?;
    let key = attn_client::TileKey {
        desktop: holder.desktop.clone(),
        tile: holder.pane.clone(),
    };
    let view = state.facts.tile_contents.get(&key)?.message.view.as_ref()?;
    Some((holder.pane.clone(), view))
}

/// A key the window heard while nothing has focus, which the view tile holding the keyboard
/// takes.
pub(crate) fn on_key(state: &mut State, ev: &KeyEvent, tx: &mut Tx) -> bool {
    if tx.focused().is_some() || ev.mods.cmd || ev.mods.ctrl || ev.mods.alt {
        return false;
    }
    let Some((tile, view)) = held(state) else {
        return false;
    };
    let data = Data::of(view);
    let tile_state = state.views.of(&tile);
    let mut found = Vec::new();
    stops(
        &view.parts,
        &data.scope(0),
        &format!("tile.{tile}.view"),
        &tile_state,
        &mut found,
    );
    let at =
        (tile_state.highlight.as_ref()).and_then(|key| found.iter().position(|s| &s.key() == key));
    let here = at.and_then(|at| found.get(at)).cloned();
    let typed = match &ev.key {
        Key::Text(text) => text.as_str(),
        _ => "",
    };
    match (&ev.key, typed, here) {
        (Key::Down, _, _) | (_, "j", _) => {
            highlight(state, &tile, &found, at.map_or(0, |at| at + 1), tx)
        }
        (Key::Up, _, _) | (_, "k", _) => {
            let to = at.map_or(found.len().saturating_sub(1), |at| at.saturating_sub(1));
            highlight(state, &tile, &found, to, tx)
        }
        (Key::Enter, _, Some(Stop::Fold(list))) => {
            toggle_fold(state, &tile, &list);
            true
        }
        (Key::Left, _, Some(Stop::Fold(list))) => {
            fold(state, &tile, &list, true);
            true
        }
        (Key::Right, _, Some(Stop::Fold(list))) => {
            fold(state, &tile, &list, false);
            true
        }
        (Key::Enter, _, Some(Stop::Steps(key, count))) => {
            step(state, &tile, &key, count, Verdict::Keep)
        }
        (_, "s", Some(Stop::Steps(key, count))) => step(state, &tile, &key, count, Verdict::Skip),
        (Key::Left, _, Some(Stop::Steps(key, count))) => {
            step(state, &tile, &key, count, Verdict::Back)
        }
        (Key::Right, _, Some(Stop::Steps(key, count))) => {
            step(state, &tile, &key, count, Verdict::On)
        }
        (Key::Enter, _, Some(Stop::Row(_))) => true,
        _ => false,
    }
}

fn highlight(state: &mut State, tile: &str, found: &[Stop], to: usize, tx: &mut Tx) -> bool {
    let Some(stop) = found.get(to.min(found.len().saturating_sub(1))) else {
        return false;
    };
    let key = stop.key();
    state
        .views
        .change(tile, |s| s.highlight = Some(key.clone()));
    tx.scroll_into_view(key);
    true
}

/// Folds or unfolds the list keyed `list` and puts the highlight on its heading.
pub(super) fn toggle_fold(state: &mut State, tile: &str, list: &str) {
    let folded = state.views.of(tile).folded.contains(list);
    fold(state, tile, list, !folded);
}

fn fold(state: &mut State, tile: &str, list: &str, folded: bool) {
    state.views.change(tile, |s| {
        if folded {
            s.folded.insert(list.to_owned());
        } else {
            s.folded.remove(list);
        }
        s.highlight = Some(format!("{list}.head"));
    });
}

enum Verdict {
    Keep,
    Skip,
    Back,
    On,
}

/// Moves the `one-by-one` keyed `key`, of `count` items, past its item with `verdict`. After
/// the last item it shows what was kept and skipped.
fn step(state: &mut State, tile: &str, key: &str, count: usize, verdict: Verdict) -> bool {
    state.views.change(tile, |s| {
        let steps = s.steps.entry(key.to_owned()).or_default();
        let at = steps.at;
        match verdict {
            Verdict::Keep | Verdict::Skip if at < count => {
                let (into, from) = match verdict {
                    Verdict::Keep => (&mut steps.kept, &mut steps.skipped),
                    _ => (&mut steps.skipped, &mut steps.kept),
                };
                into.insert(at);
                from.remove(&at);
                steps.at = at + 1;
            }
            Verdict::Back => steps.at = at.saturating_sub(1),
            Verdict::On => steps.at = (at + 1).min(count),
            _ => {}
        }
    });
    true
}
