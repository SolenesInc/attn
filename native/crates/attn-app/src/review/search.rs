//! Search of a review's repository: files by name and lines by text, found with git on the
//! machine that holds the review. The empty box lists the change's files.

use attn_protocol::{ReviewBundle, ReviewFile, ReviewSearchMessage, ReviewSearchResultMessage};
use attn_ui::Tokens;
use attn_ui::type_;
use rune::tok::space;
use rune::{El, Key, KeyEvent, TextEdit, Tx, col, row, text};
use rune_kit::TextInput;

use super::file::Target;
use crate::State;

pub(crate) const FIELD: &str = "review.search.field";
const LABEL: &str = "Search the repository";

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
    let field = TextInput::new(query)
        .key(FIELD)
        .plain()
        .text_style(type_::BODY)
        .height(type_::BODY.line_px())
        .width(None)
        .label("Search")
        .placeholder("Search files by name and lines by text")
        .on_edit(query_field)
        .on_change(changed)
        .on_key(on_key)
        .build(attn_ui::kit(t));
    let note = search.answer.as_ref().and_then(|a| {
        a.error
            .clone()
            .map(|e| (e, true))
            .or(a.problem.clone().map(|p| (p, false)))
    });
    let note = note.map(|(text_, alert)| {
        text(text_)
            .font(type_::LABEL)
            .color(if alert { t.error } else { t.warning })
            .wrap()
            .role(if alert { "alert" } else { "status" })
    });
    let empty = (!query.text.trim().is_empty()
        && search
            .answer
            .as_ref()
            .is_some_and(|a| a.found.is_empty() && a.error.is_none()))
    .then(|| {
        text(format!("Nothing matches {}", query.text))
            .font(type_::BODY)
            .color(t.text_tertiary)
    });
    let list = col()
        .key(LIST)
        .role("listbox")
        .label(LABEL)
        .w_full()
        .max_h(360.0)
        .scroll(rune::ScrollSpec {
            stick_bottom: false,
        })
        .gap(2.0)
        .padding(space::S1)
        .children(found.iter().enumerate().map(|(i, f)| {
            let on = i == search.selected;
            let el = row()
                .key(option_key(i))
                .role("option")
                .label(f.label())
                .w_full()
                .h(25.0)
                .items_center()
                .px(space::S2)
                .child(
                    text(f.label())
                        .font(type_::BODY.mono())
                        .color(t.text_primary)
                        .a11y_hidden(),
                )
                .on_press_tx(move |state: &mut State, tx: &mut Tx| pick(state, i, tx));
            if on {
                el.fill(t.bg_selected).focus_selected(true)
            } else {
                el
            }
        }))
        .children(empty);
    let panel = col()
        .key("review.search")
        .role("dialog")
        .label(LABEL)
        .modal_tx(true, close)
        .w_full()
        .max_w(640.0)
        .padding(space::S3)
        .gap(space::S2)
        .child(field)
        .children(note)
        .child(list);
    rune_kit::popover::raised(attn_ui::kit(t), panel, 8.0)
}
