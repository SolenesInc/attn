//! Search of a review's repository: files by name and lines by text, found with git on the
//! machine that holds the review. The empty box lists the change's files.

use attn_protocol::{ReviewBundle, ReviewFile, ReviewSearchMessage, ReviewSearchResultMessage};
use attn_ui::Tokens;
use attn_ui::blocks::{Inline, Notice, Status};
use attn_ui::finder;
use attn_ui::system::{ListRow, Type, words};
use rune::{El, Key, KeyEvent, TextEdit, Tx, col, el, row};
use rune_kit::TextInput;

use super::file::Target;
use crate::State;

pub(crate) const FIELD: &str = "review.search.field";
const LABEL: &str = "Search the repository";
/// As tall as the list of results gets before it scrolls: 14 one-line rows on main.
const LIST_HEIGHT: f32 = 360.0;
/// How many results the list keeps in view before what is under it gives way: the keyboard's
/// row with one beside it.
const ROWS_KEPT: usize = 2;

/// What a search lists.
#[derive(Clone, Debug)]
pub(crate) enum Found {
    /// A file of the change, with its lines added and removed.
    Changed(ReviewFile),
    /// A file whose name matches.
    File(String),
    /// A line whose text matches.
    Hit {
        path: String,
        line: i64,
        text: String,
    },
}

impl Found {
    fn target(&self) -> Target {
        match self {
            Found::Changed(f) => Target {
                path: f.path.clone(),
                ..Target::default()
            },
            Found::File(path) => Target {
                path: path.clone(),
                ..Target::default()
            },
            Found::Hit { path, line, .. } => Target {
                path: path.clone(),
                line: Some(*line),
                range: None,
                outside: false,
            },
        }
    }

    /// What the result's row shows: where or what it found, then at its right end where it
    /// is or what changed, in the colors the Tauri search gives them.
    fn columns(&self, query: &str, t: &Tokens) -> (El, El) {
        let where_ = |w: String| words(w, Type::Mono, t.text_muted);
        match self {
            Found::Changed(f) => {
                let stat = if f.binary {
                    where_("binary".into())
                } else {
                    Inline::joined()
                        .child(words(format!("+{}", f.added), Type::Mono, t.success))
                        .child(words(format!("−{}", f.removed), Type::Mono, t.error))
                };
                (path_label(&f.path, t), stat)
            }
            Found::File(path) => (path_label(path, t), where_("file".into())),
            Found::Hit {
                path,
                line,
                text: hit,
            } => (
                hit_line(hit.trim(), query, t),
                where_(format!("{path}:{line}"))
                    .single_line()
                    .truncate_head()
                    .shrink(),
            ),
        }
    }

    fn label(&self) -> String {
        match self {
            Found::Changed(f) if f.binary => format!("{} binary", f.path),
            Found::Changed(f) => format!("{} +{} −{}", f.path, f.added, f.removed),
            Found::File(path) => format!("{path} file"),
            Found::Hit { path, line, text } => format!("{} {path}:{line}", text.trim()),
        }
    }
}

/// The answer to the box's latest query.
pub(crate) struct Answered {
    query: String,
    found: Vec<Found>,
    problem: Option<String>,
    error: Option<String>,
}

/// The search open over review tile `tile`, which reads the repository of `source`.
pub(crate) struct Search {
    pub(crate) tile: String,
    pub(crate) source: (Option<String>, String),
    answer: Option<Answered>,
    selected: usize,
    asked: u64,
}

impl Search {
    pub(crate) fn new(tile: &str, source: (Option<String>, String)) -> Search {
        Search {
            tile: tile.to_owned(),
            source,
            answer: None,
            selected: 0,
            asked: 0,
        }
    }

    fn found(&self, query: &TextEdit, files: &[ReviewFile]) -> Vec<Found> {
        if query.text.trim().is_empty() {
            return files.iter().cloned().map(Found::Changed).collect();
        }
        self.answer
            .as_ref()
            .filter(|a| a.query == query.text)
            .map(|a| a.found.clone())
            .unwrap_or_default()
    }
}

/// Opens the search over tile `tile` with `query` in its box.
pub(crate) fn open(
    state: &mut State,
    tile: &str,
    source: (Option<String>, String),
    query: &str,
    tx: &mut Tx,
) {
    state.readings.reviews.search = Some(Search::new(tile, source));
    state.readings.reviews.search_query = TextEdit::new(query.to_owned());
    if !query.trim().is_empty() {
        ask(state, tx);
    }
    tx.focus(FIELD, false);
    state.readings.revision += 1;
}

fn close(state: &mut State, tx: &mut Tx) {
    state.readings.reviews.search = None;
    tx.blur();
    state.readings.revision += 1;
}

fn query_field(state: &mut State) -> &mut TextEdit {
    &mut state.readings.reviews.search_query
}

/// Asks the daemon for what the box's query finds; an answer to an older query is dropped.
fn ask(state: &mut State, tx: &mut Tx) {
    let Some(search) = state.readings.reviews.search.as_mut() else {
        return;
    };
    search.selected = 0;
    let text = state.readings.reviews.search_query.text.to_owned();
    let Some(search) = state.readings.reviews.search.as_mut() else {
        return;
    };
    if text.trim().is_empty() {
        search.answer = None;
        return;
    }
    search.asked += 1;
    let asked = search.asked;
    let (host, document) = search.source.clone();
    let asked_text = text.clone();
    state.ask(
        |id| ReviewSearchMessage {
            host,
            document,
            query: asked_text,
            request_id: Some(id),
            ..ReviewSearchMessage::new()
        },
        move |state, answer, _| {
            let Some(search) = state
                .readings
                .reviews
                .search
                .as_mut()
                .filter(|s| s.asked == asked)
            else {
                return;
            };
            let answered = match answer.outcome() {
                Ok(reply) => match reply.parse::<ReviewSearchResultMessage>() {
                    Some(m) => Answered {
                        query: text.clone(),
                        found: m
                            .files
                            .unwrap_or_default()
                            .into_iter()
                            .map(Found::File)
                            .chain(m.hits.unwrap_or_default().into_iter().map(|h| Found::Hit {
                                path: h.path,
                                line: h.line,
                                text: h.text,
                            }))
                            .collect(),
                        problem: m.problem.filter(|p| !p.is_empty()),
                        error: None,
                    },
                    None => Answered {
                        query: text.clone(),
                        found: Vec::new(),
                        problem: None,
                        error: Some("the daemon's answer did not parse".into()),
                    },
                },
                Err(why) => Answered {
                    query: text.clone(),
                    found: Vec::new(),
                    problem: None,
                    error: Some(why),
                },
            };
            search.answer = Some(answered);
            search.selected = 0;
            state.readings.revision += 1;
        },
        tx,
    );
}

fn changed(state: &mut State, tx: &mut Tx) {
    ask(state, tx);
    state.readings.revision += 1;
}

/// Opens result `at` of the open search over the files open in its tile, and closes it.
fn pick(state: &mut State, at: usize, tx: &mut Tx) {
    let Some(search) = state.readings.reviews.search.as_ref() else {
        return;
    };
    let tile = search.tile.clone();
    let files = super::bundle_of(state, &tile)
        .map(|b| b.files.clone())
        .unwrap_or_default();
    let Some(found) = search
        .found(&state.readings.reviews.search_query, &files)
        .get(at)
        .map(Found::target)
    else {
        return;
    };
    let source = search.source.clone();
    close(state, tx);
    super::open_found(state, &tile, source, found, tx);
}

const LIST: &str = "review.search.list";

fn option_key(i: usize) -> String {
    format!("review.search.option.{i}")
}

/// The box's keys: ↑/↓ (and ⌃n/⌃p) pick a result and bring it into view, Enter opens it, Escape closes the search.
fn on_key(state: &mut State, ev: &KeyEvent, tx: &mut Tx) -> bool {
    let Some(search) = state.readings.reviews.search.as_ref() else {
        return false;
    };
    let tile = search.tile.clone();
    let files = super::bundle_of(state, &tile)
        .map(|b| b.files.clone())
        .unwrap_or_default();
    let found = search.found(&state.readings.reviews.search_query, &files);
    let ctrl =
        |c: char| matches!(ev.key, Key::Command(k) if k.eq_ignore_ascii_case(&c)) && ev.mods.ctrl;
    let step = match ev.key {
        Key::Down => 1,
        Key::Up => -1,
        _ if ctrl('n') => 1,
        _ if ctrl('p') => -1,
        Key::Enter => {
            let selected = search.selected;
            pick(state, selected, tx);
            return true;
        }
        Key::Escape => {
            close(state, tx);
            return true;
        }
        _ => return false,
    };
    if let Some(search) = state.readings.reviews.search.as_mut() {
        let last = found.len().saturating_sub(1) as isize;
        search.selected = (search.selected as isize + step).clamp(0, last) as usize;
        tx.scroll_into_view_in(LIST, option_key(search.selected));
        state.readings.revision += 1;
    }
    true
}

/// The search over review tile `tile`, when it is open there.
pub(crate) fn view(search: &Search, query: &TextEdit, bundle: &ReviewBundle, t: &Tokens) -> El {
    let found = search.found(query, &bundle.files);
    let field = finder::field(
        TextInput::new(query)
            .key(FIELD)
            .plain()
            .width(None)
            .label("Search")
            .placeholder("Search files by name and lines by text")
            .active_descendant((!found.is_empty()).then(|| option_key(search.selected)))
            .on_edit(query_field)
            .on_change(changed)
            .on_key(on_key),
    )
    .build(attn_ui::kit(t));
    let failed: Vec<_> = search
        .answer
        .iter()
        .filter_map(|a| a.error.clone())
        .map(|e| Notice::error("review.search.error", e).build(t))
        .collect();
    let limit = search
        .answer
        .as_ref()
        .filter(|a| a.error.is_none())
        .and_then(|a| a.problem.clone())
        .map(|p| {
            finder::scrolling_notices(
                t,
                "review.search.limit",
                vec![Status::new("review.search.problem", p, false).build(t)],
            )
        });
    let empty = (!query.text.trim().is_empty()
        && search
            .answer
            .as_ref()
            .is_some_and(|a| a.found.is_empty() && a.error.is_none()))
    .then(|| {
        finder::empty(
            t,
            "review.search.empty",
            format!("Nothing matches {}", query.text),
        )
    });
    let rows = found
        .iter()
        .enumerate()
        .map(|(i, f)| {
            let on = i == search.selected;
            let (what, where_) = f.columns(&query.text, t);
            ListRow::new(option_key(i), f.label())
                .role("option")
                .unfocusable()
                .cursor(on)
                .selected(on)
                .title_el(what.a11y_hidden())
                .aside(where_.a11y_hidden())
                .on_press_tx(move |state: &mut State, tx: &mut Tx| pick(state, i, tx))
                .build(t)
        })
        .chain(empty)
        .collect();
    let list = finder::list(LIST, rows)
        .label(LABEL)
        .min_h(ROWS_KEPT.min(found.len()) as f32 * ListRow::height())
        .max_h(LIST_HEIGHT);
    let input = col()
        .w_full()
        .child(finder::input_row(None, field, None))
        .children((!failed.is_empty()).then(|| finder::notices(failed)));
    let panel = col()
        .key("review.search")
        .role("dialog")
        .label(LABEL)
        .modal_tx(true, close)
        .w_full()
        .shrink()
        .min_h(0.0);
    let panel = finder::panel(t, panel, input, list, limit)
        .shrink()
        .min_h(0.0);
    col()
        .w_full()
        .grow(1.0)
        .min_h(0.0)
        .child(panel)
        .child(el().h(key_line_height(&search.tile)))
}

/// How tall the key line along the bottom of review tile `tile` stands, which the search leaves
/// in view under it.
fn key_line_height(tile: &str) -> f32 {
    let review = super::prefix(tile);
    let bottom = rune::memo::layout_with(&review, |[_, y, _, h]| y + h);
    let top = rune::memo::layout_with(&format!("{review}.keys"), |[_, y, _, _]| y);
    bottom
        .zip(top)
        .map_or(0.0, |(bottom, top)| (bottom - top).max(0.0))
}

/// A path with its folder quieter than its file's name, as the Tauri review's path label.
fn path_label(path: &str, t: &Tokens) -> El {
    let at = path.rfind('/').map_or(0, |i| i + 1);
    let (dir, name) = path.split_at(at);
    row()
        .shrink()
        .min_w(0.0)
        .children((!dir.is_empty()).then(|| {
            words(dir.to_owned(), Type::Mono, t.text_muted)
                .single_line()
                .truncate()
                .shrink()
        }))
        .child(words(name.to_owned(), Type::Mono, t.text_primary).single_line())
}

/// `line`, a line a search found for `query`, cut short from its start rather than its end
/// when it does not fit, so the match stays in view.
fn hit_line(line: &str, query: &str, t: &Tokens) -> El {
    let (before, from) = line.split_at(match_at(line, query.trim()));
    row()
        .grow(1.0)
        .shrink()
        .min_w(0.0)
        .children((!before.is_empty()).then(|| {
            words(before.to_owned(), Type::Mono, t.text_primary)
                .single_line()
                .truncate_head()
                .shrink()
        }))
        .child(
            words(from.to_owned(), Type::Mono, t.text_primary)
                .single_line()
                .truncate()
                .max_w_frac(1.0),
        )
}

/// Where `query` first stands in `line`, its case ignored as git grep -i ignores it, or 0.
fn match_at(line: &str, query: &str) -> usize {
    if query.is_empty() {
        return 0;
    }
    line.char_indices()
        .map(|(i, _)| i)
        .find(|&i| {
            line.get(i..i + query.len())
                .is_some_and(|s| s.eq_ignore_ascii_case(query))
        })
        .unwrap_or(0)
}
