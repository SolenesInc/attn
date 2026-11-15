//! The user's review: notes and questions on lines of code, which go to the agent with a
//! verdict. They are marks in the review document's annotation draft, anchored to the lines
//! at head they cover, so they stay on those lines when the file changes.

use attn_protocol::{MarkdownAnnotation, MarkdownAnnotationAnchor, ReviewRow};
use attn_ui::Tokens;
use rune::tok::{radius, space, type_};
use rune::{El, Key, KeyEvent, TextEdit, Tx, col, row, text};
use rune_kit::TextArea;

use crate::State;
use crate::markdown::drafts::{self, To};

pub(crate) const FIELD: &str = "review.composer.field";

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(crate) enum Kind {
    Note,
    Question,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(crate) enum Verdict {
    Approve,
    RequestChanges,
    Comment,
}

impl Verdict {
    fn wire(self) -> &'static str {
        match self {
            Verdict::Approve => "approve",
            Verdict::RequestChanges => "request_changes",
            Verdict::Comment => "comment",
        }
    }

    fn name(self) -> &'static str {
        match self {
            Verdict::Approve => "approve",
            Verdict::RequestChanges => "request changes",
            Verdict::Comment => "comment",
        }
    }

    pub(crate) fn of_key(key: &str) -> Option<Verdict> {
        match key {
            "a" => Some(Verdict::Approve),
            "r" => Some(Verdict::RequestChanges),
            "c" => Some(Verdict::Comment),
            _ => None,
        }
    }
}

/// The anchor of a mark on rows `picked` of file `path`: the lines at head they show. Rows
/// head does not have sit before the next row it has, from `following`. With removed code in
/// it, the quote keeps each row's sign so it cannot pass for the code at those lines.
pub(crate) fn anchor(
    path: &str,
    picked: &[ReviewRow],
    following: &[ReviewRow],
    doc_line: i64,
) -> Option<MarkdownAnnotationAnchor> {
    let heads: Vec<i64> = picked.iter().filter_map(|r| r.head).collect();
    let from = heads
        .first()
        .copied()
        .or_else(|| following.iter().find_map(|r| r.head))?;
    let to = heads.last().copied().unwrap_or(from);
    let diff = picked.iter().any(|r| r.kind == "-");
    let quote = picked
        .iter()
        .map(|r| {
            if diff {
                format!("{}{}", r.kind, r.text)
            } else {
                r.text.clone()
            }
        })
        .collect::<Vec<_>>()
        .join("\n");
    Some(MarkdownAnnotationAnchor {
        start_line: doc_line,
        end_line: doc_line,
        exact: quote,
        file: Some(path.to_owned()),
        file_start_line: Some(from),
        file_end_line: Some(to),
        ..MarkdownAnnotationAnchor::default()
    })
}

/// Where a file mark shows: under a line now, or moved when one of its lines at head is gone.
#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash)]
pub(crate) enum Placed {
    Line(i64),
    Moved,
}

/// Rows of a mark's file and the lines at head they cover: all of them for a whole file, a
/// stop's lines for a stop.
pub(crate) struct Rows<'a> {
    pub(crate) rows: &'a [ReviewRow],
    pub(crate) covers: Option<(i64, i64)>,
}

/// Places a file mark by its lines at head, in the first of `lists` that covers them.
pub(crate) fn place(anchor: &MarkdownAnnotationAnchor, lists: &[Rows]) -> Option<Placed> {
    let (from, to) = (anchor.file_start_line?, anchor.file_end_line?);
    let covers = |c: Option<(i64, i64)>, h: i64| c.is_none_or(|(a, b)| h >= a && h <= b);
    for Rows { rows, covers: c } in lists {
        if !covers(*c, from) || !covers(*c, to) {
            continue;
        }
        let now = |head: i64| {
            rows.iter()
                .find(|r| r.head == Some(head))
                .and_then(|r| r.now)
        };
        if (from..=to).any(|head| now(head).is_none()) {
            return Some(Placed::Moved);
        }
        return Some(now(to).map_or(Placed::Moved, Placed::Line));
    }
    None
}

fn range(anchor: Option<&MarkdownAnnotationAnchor>) -> String {
    match anchor.and_then(|a| Some((a.file_start_line?, a.file_end_line?))) {
        Some((from, to)) if to > from => format!("L{from}–{to}"),
        Some((from, _)) => format!("L{from}"),
        None => String::new(),
    }
}

/// A mark under its line: who wrote it, its lines, its text and, for a question, the answer.
pub(crate) fn mark_row(mark: &MarkdownAnnotation, t: &Tokens) -> El {
    let question = mark.r#type == "question";
    let who = if question { "you asked" } else { "you" };
    let text_ = mark.text.clone().unwrap_or_default();
    let range = range(mark.anchor.as_ref());
    let answer = question.then(|| match mark.answer.as_deref().filter(|a| !a.is_empty()) {
        Some(answer) => text(answer.to_owned())
            .key(format!("review.mark.{}.answer", mark.id))
            .role("note")
            .label(format!("answer: {answer}"))
            .font(type_::BODY)
            .color(t.text_primary)
            .wrap(),
        None => text("Asked — waiting for the answer")
            .font(type_::LABEL)
            .color(t.text_tertiary),
    });
    col()
        .key(format!("review.mark.{}", mark.id))
        .role("note")
        .label(format!("{who} {range}: {text_}"))
        .w_full()
        .padding(space::S1)
        .gap(2.0)
        .radius(radius::SM)
        .fill(t.success.alpha(0.08))
        .border_sides([0.0, 0.0, 0.0, 2.0], t.success)
        .child(
            row()
                .gap(space::S2)
                .child(text(who).font(type_::LABEL).color(t.success))
                .child(text(range).font(type_::LABEL).color(t.text_secondary))
                .child(
                    text(text_)
                        .font(type_::BODY)
                        .color(t.text_primary)
                        .wrap()
                        .grow(1.0),
                ),
        )
        .children(answer)
}

/// A note or question being written on `anchor`, in review tile `tile`.
pub(crate) struct Composer {
    pub(crate) tile: String,
    pub(crate) kind: Kind,
    pub(crate) anchor: MarkdownAnnotationAnchor,
    /// The document's draft, and who a question goes to.
    pub(crate) uri: String,
    pub(crate) to: Option<To>,
}

pub(crate) fn compose(state: &mut State, composer: Composer, tx: &mut Tx) {
    state.readings.reviews.composer = Some(composer);
    state.readings.reviews.composer_text = TextEdit::new(String::new());
    tx.focus(FIELD, false);
    state.readings.revision += 1;
}

fn close(state: &mut State, tx: &mut Tx) {
    state.readings.reviews.composer = None;
    tx.blur();
    state.readings.revision += 1;
}

fn field(state: &mut State) -> &mut TextEdit {
    &mut state.readings.reviews.composer_text
}

/// The box's keys: Enter saves, ⇧Enter starts a line, Escape cancels.
fn on_key(state: &mut State, ev: &KeyEvent, tx: &mut Tx) -> bool {
    match ev.key {
        Key::Escape => {
            close(state, tx);
            true
        }
        Key::Enter if !ev.mods.shift => {
            save(state, tx);
            true
        }
        _ => false,
    }
}

/// Saves the box's mark in the document's draft; a question also goes to the agent at once.
fn save(state: &mut State, tx: &mut Tx) {
    let Some(composer) = state.readings.reviews.composer.take() else {
        return;
    };
    tx.blur();
    state.readings.revision += 1;
    let words = state.readings.reviews.composer_text.text.trim().to_owned();
    if words.is_empty() {
        return;
    }
    let now = tx.clock();
    let id = format!("review-{}", uuid::Uuid::new_v4());
    let question = composer.kind == Kind::Question;
    let mark = MarkdownAnnotation {
        id: id.clone(),
        r#type: if question { "question" } else { "comment" }.into(),
        text: Some(words),
        anchor: Some(composer.anchor),
        created_at: state.wall_ms(tx),
        ..MarkdownAnnotation::default()
    };
    let Some(draft) = state.drafts.get_mut(&composer.uri) else {
        return;
    };
    draft.marks.push(mark);
    draft.changed(now);
    if question && let Some(to) = composer.to {
        drafts::save_now(state, &composer.uri, tx);
        drafts::submit_review(
            state,
            &composer.uri,
            to,
            drafts::Review {
                only_ids: Some(vec![id]),
                ..drafts::Review::default()
            },
            |state, sent, tx| {
                let why = match sent {
                    drafts::Sent::Failed(why) => why,
                    drafts::Sent::Skipped => "the agent waits on an approval".into(),
                    _ => return,
                };
                state.window.notify(format!("Not asked: {why}"), tx);
            },
            tx,
        );
    }
}

/// The box over tile `composer.tile`, naming where the mark goes.
pub(crate) fn composer_view(composer: &Composer, words: &TextEdit, t: &Tokens) -> El {
    let (title, hint) = match composer.kind {
        Kind::Note => ("Write a note", "Waits for the send."),
        Kind::Question => ("Ask a question", "Sent at once."),
    };
    let hint = match (&composer.to, composer.kind) {
        (Some(To::Session(s)), Kind::Question) => format!("Sent to {s} at once"),
        _ => hint.to_owned(),
    };
    let place = format!(
        "{} {}",
        composer.anchor.file.clone().unwrap_or_default(),
        range(Some(&composer.anchor))
    );
    let area = TextArea::new(words)
        .key(FIELD)
        .width(None)
        .label(title)
        .placeholder(if composer.kind == Kind::Question {
            "What do you want to know?"
        } else {
            "What should change here?"
        })
        .on_edit(field)
        .on_key(on_key)
        .build(attn_ui::kit(t));
    let panel = col()
        .key("review.composer")
        .role("dialog")
        .label(title)
        .modal_tx(true, close)
        .w_full()
        .max_w(560.0)
        .padding(space::S3)
        .gap(space::S2)
        .child(text(title).font(type_::TITLE).color(t.text_primary))
        .child(text(place).font(type_::LABEL).color(t.text_secondary))
        .child(text(hint).font(type_::LABEL).color(t.text_tertiary))
        .child(area)
        .child(
            text("↵ save · ⇧↵ new line · esc cancel")
                .font(type_::LABEL)
                .color(t.text_tertiary),
        );
    rune_kit::popover::raised(attn_ui::kit(t), panel, 8.0)
}

/// The send: the marks in reading order with their places, and the verdict picked.
pub(crate) fn sheet(
    marks: &[&MarkdownAnnotation],
    verdict: Verdict,
    to: Option<&To>,
    t: &Tokens,
) -> El {
    let entries = marks.iter().map(|m| {
        let kind = if m.r#type == "question" {
            "question"
        } else {
            "note"
        };
        let place = format!(
            "{} {}",
            m.anchor
                .as_ref()
                .and_then(|a| a.file.clone())
                .unwrap_or_default(),
            range(m.anchor.as_ref())
        );
        text(format!(
            "{kind} {place}: {}",
            m.text.clone().unwrap_or_default()
        ))
        .role("listitem")
        .font(type_::BODY)
        .color(t.text_primary)
        .wrap()
    });
    let choices = [Verdict::Approve, Verdict::RequestChanges, Verdict::Comment].map(|v| {
        let key = match v {
            Verdict::Approve => "a",
            Verdict::RequestChanges => "r",
            Verdict::Comment => "c",
        };
        text(format!("{key} {}", v.name()))
            .role("radio")
            .label(v.name())
            .checked(v == verdict)
            .font(type_::LABEL)
            .color(if v == verdict {
                t.text_primary
            } else {
                t.text_tertiary
            })
    });
    let to = match to {
        Some(To::Session(s)) => format!("Goes to {s}."),
        Some(To::Seed(s)) => format!("Goes onto {s}."),
        None => "No session takes it: bind the document to a session first.".into(),
    };
    let panel = col()
        .key("review.send")
        .role("dialog")
        .label("Send the review")
        .w_full()
        .max_w(640.0)
        .padding(space::S3)
        .gap(space::S2)
        .child(
            text("Send the review")
                .font(type_::TITLE)
                .color(t.text_primary),
        )
        .child(text(to).font(type_::LABEL).color(t.text_secondary))
        .child(
            col()
                .role("list")
                .label("Marks")
                .gap(space::S1)
                .children(entries),
        )
        .child(
            row()
                .role("radiogroup")
                .label("Verdict")
                .gap(space::S3)
                .children(choices),
        )
        .child(
            text("a, r, c pick the verdict · ↵ send · esc back")
                .font(type_::LABEL)
                .color(t.text_tertiary),
        );
    rune_kit::popover::raised(attn_ui::kit(t), panel, 8.0)
}

/// Sends the review in document `uri`'s draft with `verdict`, its marks whose lines moved
/// labelled so.
pub(crate) fn send(
    state: &mut State,
    uri: &str,
    to: To,
    verdict: Verdict,
    moved: Vec<String>,
    tx: &mut Tx,
) {
    drafts::submit_review(
        state,
        uri,
        to,
        drafts::Review {
            verdict: Some(verdict.wire().into()),
            moved,
            ..drafts::Review::default()
        },
        |state, sent, tx| {
            if let drafts::Sent::Failed(why) = sent {
                state
                    .window
                    .notify(format!("The review did not go: {why}"), tx);
            }
        },
        tx,
    );
}
