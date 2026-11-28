//! The keys a view answers while its tile holds the keyboard: j and k move the highlight over
//! the view's rows, foldable headings, questions and `one-by-one` parts; ↩ folds a heading,
//! and keeps and goes on in a `one-by-one`, where s skips and ← and → step back and on. On a
//! question, or in a `one-by-one` item that holds one, digits pick a choice and e writes the
//! text; ⌘↩ sends the view's answers.

use attn_protocol::ViewPart;
use rune::{Key, KeyEvent, Tx};

use attn_client::TileKey;

use super::answers::{self, Answers, KEPT, SKIPPED};
use super::values::{Data, Scope, row_id};
use super::{TileState, draw};
use crate::State;

/// A place the highlight can rest.
#[derive(Clone)]
enum Stop {
    /// A list's foldable heading, by the list's key.
    Fold(String),
    Row(String),
    /// A `one-by-one`: the rows of its items, and what each item shows.
    Steps {
        key: String,
        rows: Vec<String>,
        blocks: Vec<ViewPart>,
    },
    /// A question outside a `one-by-one`, in the row it is in.
    Question {
        key: String,
        row: Option<String>,
        part: Box<ViewPart>,
    },
}

impl Stop {
    fn key(&self) -> String {
        match self {
            Stop::Fold(list) => format!("{list}.head"),
            Stop::Row(key) | Stop::Steps { key, .. } | Stop::Question { key, .. } => key.clone(),
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
    (scope, row): (&Scope<'_>, Option<&str>),
    prefix: &str,
    state: &TileState,
    out: &mut Vec<Stop>,
) {
    for (i, part) in parts.iter().enumerate() {
        if part.problem.is_some() {
            continue;
        }
        let key = format!("{prefix}.{i}");
        if asked(part).is_some() {
            out.push(Stop::Question {
                key: key.clone(),
                row: row.map(str::to_owned),
                part: Box::new(part.clone()),
            });
        }
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
                    (&scope.within(item), Some(&row_id(item, n))),
                    &format!("{key}.{n}"),
                    state,
                    out,
                );
            }
        }
        if let Some(steps) = &part.one_by_one {
            let items = scope.items(&steps.items);
            out.push(Stop::Steps {
                key,
                rows: (items.iter().enumerate())
                    .map(|(n, item)| row_id(item, n))
                    .collect(),
                blocks: steps.blocks.clone(),
            });
        }
    }
}

/// The view tile holding the keyboard, with nothing over it: its tile and its content.
fn held(state: &State) -> Option<(String, &attn_client::TileContent)> {
    if !state.window.tiles_in_front() {
        return None;
    }
    let holder = state.window.keyboard.as_ref()?;
    let key = attn_client::TileKey {
        desktop: holder.desktop.clone(),
        tile: holder.pane.clone(),
    };
    let content = state.facts.tile_contents.get(&key)?;
    content.message.view.as_ref()?;
    Some((holder.pane.clone(), content))
}

/// What a question part asks: its name, its choices' labels, whether it takes several, and
/// whether it is written. A file question takes no keys.
fn asked(part: &ViewPart) -> Option<(&str, Vec<String>, bool, bool)> {
    let labels =
        |q: &attn_protocol::AskQuestion| q.choices.iter().map(|c| c.label.clone()).collect();
    match (&part.question, &part.question_multi, &part.question_text) {
        (Some(q), _, _) => Some((&q.name, labels(&q.question), false, false)),
        (_, Some(q), _) => Some((&q.name, labels(&q.question), true, false)),
        (_, _, Some(q)) => Some((&q.name, Vec::new(), false, true)),
        _ => None,
    }
}

/// Whether the view asks anything, so it has answers to send.
pub(crate) fn asks(parts: &[ViewPart]) -> bool {
    let mut any = false;
    each_part(parts, &mut |part| {
        any |= asked(part).is_some() || part.question_file.is_some() || part.one_by_one.is_some()
    });
    any
}

/// What the user did with the row `row` of a `one-by-one` of `blocks`: skipped, changed when
/// they wrote over a question's `Value:`, or kept.
pub(crate) fn verdict(
    blocks: &[ViewPart],
    answers: &Answers<'_>,
    row: &str,
) -> Option<&'static str> {
    let changed = (blocks.iter())
        .filter_map(|b| b.question_text.as_ref().filter(|q| q.value.is_some()))
        .any(|q| answers.text_in(row, &q.name).is_some());
    match answers.step(row) {
        Some(SKIPPED) => Some("skipped"),
        _ if changed => Some("changed"),
        Some(KEPT) => Some("kept"),
        _ => None,
    }
}

/// The keys a `one-by-one` of `blocks` answers.
pub(crate) fn steps_hint(blocks: &[ViewPart]) -> String {
    let mut hint = String::from("↩ keep · s skip");
    if blocks.iter().any(|b| asked(b).is_some_and(|a| a.3)) {
        hint.push_str(" · e write");
    }
    if blocks.iter().any(|b| asked(b).is_some_and(|a| !a.3)) {
        hint.push_str(" · 1–9 pick");
    }
    hint.push_str(" · ← →");
    hint
}

/// The question of `blocks` the keys reach in a `one-by-one` item keyed `item`: the first
/// written one for e, the first with choices for a digit, with its key.
fn question_in<'a>(
    blocks: &'a [ViewPart],
    item: &str,
    written: bool,
) -> Option<(String, &'a ViewPart)> {
    (blocks.iter().enumerate())
        .find(|(_, b)| b.problem.is_none() && asked(b).is_some_and(|a| a.3 == written))
        .map(|(i, b)| (format!("{item}.{i}"), b))
}

/// Picks the `digit`th choice of `question`, keyed `key`, in `row`; or, on a written one,
/// puts the keyboard in its field.
fn answer_with(
    state: &mut State,
    (place, row): (&TileKey, Option<String>),
    (key, question): (&str, &ViewPart),
    digit: Option<usize>,
    tx: &mut Tx,
) -> bool {
    let Some((name, labels, many, written)) = asked(question) else {
        return false;
    };
    if written {
        if digit.is_some() {
            return false;
        }
        tx.focus(draw::field_key(key), true);
        return true;
    }
    let Some(label) = digit
        .and_then(|d| d.checked_sub(1))
        .and_then(|d| labels.get(d))
    else {
        return false;
    };
    let pick = {
        let content = answers::content_of(state, place);
        let kept = content.and_then(|c| c.message.answers.as_ref());
        let answers = Answers::of(place, kept, &state.views.writing);
        let answers = match row {
            Some(row) => answers.within(row),
            None => answers,
        };
        answers.pick(name, label, many)
    };
    pick(state, tx);
    true
}

/// The item a `one-by-one` keyed `key`, whose items are `rows`, rests on.
fn steps_at(state: &State, place: &TileKey, key: &str, rows: &[String]) -> usize {
    let moved = state
        .views
        .of(&place.tile)
        .steps
        .get(key)
        .and_then(|s| s.at);
    let kept = answers::content_of(state, place).and_then(|c| c.message.answers.as_ref());
    resting(moved, rows, &Answers::of(place, kept, &state.views.writing))
}

/// Where a `one-by-one` whose items are `rows` rests: where the user `moved` it, or else its
/// first item they have not kept or skipped.
pub(crate) fn resting(moved: Option<usize>, rows: &[String], answers: &Answers<'_>) -> usize {
    moved.unwrap_or_else(|| {
        rows.iter()
            .position(|r| answers.step(r).is_none())
            .unwrap_or(rows.len())
    })
}

/// A key the window heard while nothing has focus, which the view tile holding the keyboard
/// takes.
pub(crate) fn on_key(state: &mut State, ev: &KeyEvent, tx: &mut Tx) -> bool {
    if tx.focused().is_some() || ev.mods.cmd || ev.mods.ctrl || ev.mods.alt {
        return false;
    }
    let Some((tile, content)) = held(state) else {
        return false;
    };
    let place = TileKey {
        desktop: content.message.desktop_id.clone(),
        tile: tile.clone(),
    };
    let view = content.message.view.as_ref().expect("held holds a view");
    let data = Data::of(view);
    let tile_state = state.views.of(&tile);
    let mut found = Vec::new();
    stops(
        &view.parts,
        (&data.scope(0), None),
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
    let digit = typed.parse::<usize>().ok().filter(|d| (1..=9).contains(d));
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
        (Key::Enter, _, Some(Stop::Steps { key, rows, .. })) => {
            step(state, &place, (&key, &rows), Verdict::Keep, tx)
        }
        (_, "s", Some(Stop::Steps { key, rows, .. })) => {
            step(state, &place, (&key, &rows), Verdict::Skip, tx)
        }
        (Key::Left, _, Some(Stop::Steps { key, rows, .. })) => {
            step(state, &place, (&key, &rows), Verdict::Back, tx)
        }
        (Key::Right, _, Some(Stop::Steps { key, rows, .. })) => {
            step(state, &place, (&key, &rows), Verdict::On, tx)
        }
        (_, _, Some(Stop::Steps { key, rows, blocks })) if digit.is_some() || typed == "e" => {
            let at = steps_at(state, &place, &key, &rows);
            let Some(row) = rows.get(at).cloned() else {
                return false;
            };
            let item = format!("{key}.{at}");
            match question_in(&blocks, &item, digit.is_none()) {
                Some((question, part)) => {
                    answer_with(state, (&place, Some(row)), (&question, part), digit, tx)
                }
                None => false,
            }
        }
        (_, _, Some(Stop::Question { key, row, part }))
            if digit.is_some() || typed == "e" || ev.key == Key::Enter =>
        {
            answer_with(state, (&place, row), (&key, &part), digit, tx)
        }
        (Key::Enter, _, Some(Stop::Row(_))) => true,
        _ => false,
    }
}

/// The send key on a view tile holding the keyboard: sends its answers to the agent that opened
/// it, when it asks something and an agent did.
pub(crate) fn send_key(state: &mut State, tx: &mut Tx) -> bool {
    let Some((tile, content)) = held(state) else {
        return false;
    };
    let place = TileKey {
        desktop: content.message.desktop_id.clone(),
        tile,
    };
    let asking = content
        .message
        .view
        .as_ref()
        .is_some_and(|v| asks(&v.parts));
    if !asking || !bound(state, &place) {
        return false;
    }
    answers::send(state, &place, tx);
    true
}

/// Whether an agent opened the view the tile at `place` shows, so its answers can go back.
fn bound(state: &State, place: &TileKey) -> bool {
    state
        .facts
        .desktops
        .iter()
        .find(|d| d.id == place.desktop)
        .is_some_and(|d| crate::desktop::tile_session(d, &place.tile).is_some())
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

/// Moves the `one-by-one` keyed `key`, whose items are `rows`, past its item with `verdict`:
/// keeping or skipping it is an answer the home keeps. After the last item it shows what the
/// user did with each.
fn step(
    state: &mut State,
    place: &TileKey,
    (key, rows): (&str, &[String]),
    verdict: Verdict,
    tx: &mut Tx,
) -> bool {
    let count = rows.len();
    let at = steps_at(state, place, key, rows);
    let given = match verdict {
        Verdict::Keep if at < count => Some(KEPT),
        Verdict::Skip if at < count => Some(SKIPPED),
        _ => None,
    };
    state.views.change(&place.tile, |s| {
        let steps = s.steps.entry(key.to_owned()).or_default();
        steps.at = Some(match verdict {
            Verdict::Keep | Verdict::Skip if at < count => at + 1,
            Verdict::Back => at.saturating_sub(1),
            Verdict::On => (at + 1).min(count),
            _ => at,
        });
    });
    if let Some(given) = given {
        answers::step(state, place, rows[at].clone(), given, tx);
    }
    true
}
