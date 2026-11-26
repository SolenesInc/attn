//! The user's review: notes and questions on lines of code, which go to the agent with a
//! verdict. They are marks in the review document's annotation draft, anchored to the lines
//! at head they cover, so they stay on those lines when the file changes.

use attn_protocol::{MarkdownAnnotation, MarkdownAnnotationAnchor, ReviewRow};
use attn_ui::Tokens;
use attn_ui::blocks::{Actions, Block, Inline, Popover, Remark, Stack, Voice, key_choice};
use attn_ui::system::{Reading, Type, keys_hint, read_as, words};
use rune::{El, Key, KeyEvent, TextEdit, Tx, text};
use rune_kit::TextArea;

use crate::State;
use crate::markdown::drafts::{self, Sent, To};

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

    fn title(self) -> &'static str {
        match self {
            Verdict::Approve => "Approve",
            Verdict::RequestChanges => "Request changes",
            Verdict::Comment => "Comment",
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
    if mark.r#type == "edit" {
        return edit_row(mark, t);
    }
    let question = mark.r#type == "question";
    let who = if question { "you asked" } else { "you" };
    let text_ = mark.text.clone().unwrap_or_default();
    let range = range(mark.anchor.as_ref());
    let answer = question.then(|| match mark.answer.as_deref().filter(|a| !a.is_empty()) {
        Some(answer) => words(answer.to_owned(), Type::Body, t.text_primary)
            .key(format!("review.mark.{}.answer", mark.id))
            .role("note")
            .label(format!("answer: {answer}"))
            .wrap(),
        None => words(
            "Asked — waiting for the answer",
            Type::Small,
            t.text_tertiary,
        ),
    });
    let remark = Remark::new(who, range, text_)
        .voice(Voice::Yours)
        .key(format!("review.mark.{}", mark.id))
        .control(remove(&mark.id, "Remove", t));
    match answer {
        Some(answer) => remark.under(answer),
        None => remark,
    }
    .build(t)
}

fn remove(id: &str, help: &'static str, t: &Tokens) -> El {
    let key = format!("review.mark.{id}.remove");
    let id = id.to_owned();
    attn_ui::GlyphButton::new(attn_ui::glyph::TIMES, help)
        .help(help)
        .on_press_tx(move |state: &mut State, tx: &mut Tx| {
            if state.drafts.remove(&id, tx.clock()) {
                state.readings.revision += 1;
            }
        })
        .build(attn_ui::kit(t))
        .key(key)
}

/// An edit under its last line: it was saved to the file, with its old text, and `u` undoes it.
fn edit_row(mark: &MarkdownAnnotation, t: &Tokens) -> El {
    let range = range(mark.anchor.as_ref());
    let was = mark.edit_was.clone().unwrap_or_default();
    Remark::new("you edited", range.clone(), "Saved to the file. It was:")
        .voice(Voice::Edited)
        .key(format!("review.mark.{}", mark.id))
        .label(format!("you edited {range}: it was {was}"))
        .control(words("u undo", Type::Small, t.text_tertiary))
        .control(remove(
            &mark.id,
            "Drop from the review; the file stays as it is",
            t,
        ))
        .under(words(was, Type::Mono, t.text_primary).wrap())
        .build(t)
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

/// Saves the box's mark in the document's draft and ends the selection it covers; a question
/// also goes to the agent at once.
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
        created_at: state.wall_ms(),
        ..MarkdownAnnotation::default()
    };
    let Some(draft) = state.drafts.get_mut(&composer.uri) else {
        return;
    };
    draft.marks.push(mark);
    draft.changed(now);
    if let Some(review) = state.readings.reviews.tiles.get_mut(&composer.tile) {
        review.end_selection();
    }
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
pub(crate) fn composer_view(composer: &Composer, written: &TextEdit, t: &Tokens) -> El {
    let (title, hint) = match composer.kind {
        Kind::Note => ("Write a note", "Waits for the send."),
        Kind::Question => ("Ask a question", "Sent at once."),
    };
    let hint = match (&composer.to, composer.kind) {
        (Some(To::Session(s)), Kind::Question) => format!("Sent to {s} at once"),
        _ => hint.to_owned(),
    };
    let place = composer
        .anchor
        .file
        .as_ref()
        .map(|file| format!("{file} {}", range(Some(&composer.anchor))));
    let area = TextArea::new(written)
        .key(FIELD)
        .width(None)
        .resizable()
        .label(title)
        .placeholder(if composer.kind == Kind::Question {
            "What do you want to know?"
        } else {
            "What should change here?"
        })
        .on_edit(field)
        .on_key(on_key)
        .build(attn_ui::kit(t));
    Popover::new("review.composer", title)
        .reading()
        .push(Block::custom(
            "review.composer.place",
            Stack::tight()
                .children(place.map(|place| words(place, Type::Small, t.text_secondary)))
                .child(words(hint, Type::Small, t.text_tertiary)),
        ))
        .push(Block::custom("review.composer.input", area))
        .actions(
            Actions::new("review.composer.keys")
                .push(keys_hint(["↵"], "save", t))
                .push(keys_hint(["⇧", "↵"], "new line", t))
                .push(keys_hint(["esc"], "cancel", t)),
        )
        .build(t)
        .modal_tx(true, close)
}

/// Where a mark of the send sits: its file's lines, a line of the document, or the whole
/// document.
fn sent_place(mark: &MarkdownAnnotation) -> String {
    match mark.anchor.as_ref() {
        Some(a) if a.file.is_some() => {
            format!("{} {}", a.file.clone().unwrap_or_default(), range(Some(a)))
        }
        Some(a) => format!("line {}", a.start_line),
        None => "the document".into(),
    }
}

/// What the last send of the sheet's review says, when it did not go.
fn unsent(sent: Option<&Sent>) -> Option<String> {
    match sent? {
        Sent::Sending => Some("Sending…".into()),
        Sent::Skipped => Some(
            "Not sent — the session is waiting on an approval, where the sending Enter would \
             answer it. Send again once you have answered."
                .into(),
        ),
        Sent::Failed(why) => Some(format!("Not sent: {why}")),
        Sent::Done { .. } | Sent::Warning(_) => None,
    }
}

/// The send: every mark it carries in reading order with its place, the verdict picked, and
/// how the last send went when it did not.
/// Who a sent review goes to, by the name the user knows it by.
pub(crate) enum Recipient {
    Session(String),
    Seed(String),
}

pub(crate) fn sheet(
    tile: &str,
    marks: &[&MarkdownAnnotation],
    verdict: Verdict,
    to: Option<Recipient>,
    sent: Option<&Sent>,
    t: &Tokens,
) -> El {
    let edited: Vec<El> = marks
        .iter()
        .filter(|m| m.r#type == "edit")
        .enumerate()
        .map(|(i, m)| {
            words(
                format!(
                    "edit {} {}: it was {} · it is now {}",
                    i + 1,
                    sent_place(m),
                    m.edit_was.clone().unwrap_or_default(),
                    m.edit_now.clone().unwrap_or_default()
                ),
                Type::Body,
                t.text_primary,
            )
            .wrap()
            .role("listitem")
        })
        .collect();
    let edits = (!edited.is_empty()).then(|| {
        Stack::tight()
            .role("list")
            .label("Your edits, saved to the files")
            .child(words(
                "Your edits, saved to the files",
                Type::Small,
                t.text_secondary,
            ))
            .children(edited)
    });
    let entries = marks.iter().filter(|m| m.r#type != "edit").map(|m| {
        let kind = match m.r#type.as_str() {
            "question" => "question",
            "deletion" => "remove",
            _ => "note",
        };
        let said = m
            .text
            .clone()
            .or_else(|| m.anchor.as_ref().map(|a| a.exact.clone()))
            .unwrap_or_default();
        words(
            format!("{kind} {}: {said}", sent_place(m)),
            Type::Body,
            t.text_primary,
        )
        .role("listitem")
        .wrap()
    });
    let alone = marks.is_empty().then(|| {
        read_as(text("No notes: the verdict goes alone."), Reading::Text)
            .color(t.text_primary.alpha(0.9))
    });
    let problem = unsent(sent).map(|why| {
        let ink = if matches!(sent, Some(Sent::Sending)) {
            t.text_secondary
        } else {
            t.warning
        };
        words(why, Type::Small, ink)
            .key("review.send.status")
            .role("status")
            .wrap()
    });
    let choices = [Verdict::Approve, Verdict::RequestChanges, Verdict::Comment].map(|v| {
        let key = match v {
            Verdict::Approve => "a",
            Verdict::RequestChanges => "r",
            Verdict::Comment => "c",
        };
        let picked = v == verdict;
        let choice = key_choice(key, v.title(), picked, t)
            .role("radio")
            .label(v.name())
            .key(format!("review.send.verdict.{key}"))
            .checked(picked);
        super::verdict_press(choice, tile, v)
    });
    let (onto, problem_to) = match to {
        Some(Recipient::Session(name)) => (Some(format!("Goes to {name}.")), None),
        Some(Recipient::Seed(seed)) => (Some(format!("Goes onto {seed}.")), None),
        None => (
            None,
            Some("Bind a session to this tile to send the review."),
        ),
    };
    let send = super::send_press(
        key_choice("↵", "Send", false, t)
            .key("review.send.go")
            .role("button")
            .label("Send"),
        tile,
    );
    Popover::new("review.send", "Send the review")
        .reading()
        .push(Block::custom(
            "review.send.marks",
            Stack::tight()
                .children(onto.map(|o| words(o, Type::Body, t.text_secondary)))
                .children(problem_to.map(|p| words(p, Type::Body, t.warning).wrap()))
                .child(Stack::tight().role("list").label("Marks").children(entries))
                .children(edits)
                .children(alone),
        ))
        .push(Block::custom(
            "review.send.verdict",
            Inline::joined()
                .role("radiogroup")
                .label("Verdict")
                .children(choices),
        ))
        .push_all(problem.map(|p| Block::custom("review.send.problem", p)))
        .actions(
            Actions::new("review.send.actions")
                .push(send)
                .push(keys_hint(["esc"], "keep reviewing", t)),
        )
        .build(t)
}

/// Sends the review in document `uri`'s draft from review tile `tile` with `verdict`, its marks
/// whose lines moved labelled so. The sheet closes and forgets the verdict once the review
/// went; until then it stays to say why it did not.
pub(crate) fn send(
    state: &mut State,
    (tile, uri): (&str, &str),
    to: To,
    verdict: Verdict,
    moved: Vec<String>,
    tx: &mut Tx,
) {
    let closed = tile.to_owned();
    crate::markdown::sending::deliver_review(
        state,
        tile,
        uri,
        to,
        drafts::Review {
            verdict: Some(verdict.wire().into()),
            moved,
            ..drafts::Review::default()
        },
        move |state, sent, _| {
            if matches!(sent, Sent::Done { .. } | Sent::Warning(_)) {
                crate::review::sent(state, &closed);
            }
        },
        tx,
    );
}
