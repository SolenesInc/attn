//! The user's review: notes and questions on lines of code, which go to the agent with a
//! verdict. They are marks in the review document's annotation draft, anchored to the lines
//! at head they cover, so they stay on those lines when the file changes.

use attn_protocol::{MarkdownAnnotation, MarkdownAnnotationAnchor, ReviewRow};
use attn_ui::Tokens;
use attn_ui::type_;
use rune::tok::{radius, space};
use rune::{El, Key, KeyEvent, TextEdit, Tx, col, row, text};
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
                )
                .child(remove(&mark.id, "Remove", t)),
        )
        .children(answer)
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
    col()
        .key(format!("review.mark.{}", mark.id))
        .role("note")
        .label(format!("you edited {range}: it was {was}"))
        .w_full()
        .padding(space::S1)
        .gap(2.0)
        .radius(radius::SM)
        .fill(t.accent.alpha(0.08))
        .border_sides([0.0, 0.0, 0.0, 2.0], t.accent)
        .child(
            row()
                .gap(space::S2)
                .child(text("you edited").font(type_::LABEL).color(t.accent))
                .child(text(range).font(type_::LABEL).color(t.text_secondary))
                .child(
                    text("Saved to the file. It was:")
                        .font(type_::LABEL)
                        .color(t.text_secondary),
                )
                .child(text("u undo").font(type_::LABEL).color(t.text_tertiary))
                .child(remove(
                    &mark.id,
                    "Drop from the review; the file stays as it is",
                    t,
                )),
        )
        .child(
            text(was)
                .font(type_::MONO_CAPTION)
                .color(t.text_primary)
                .wrap(),
        )
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
            text(format!(
                "edit {} {}: it was {} · it is now {}",
                i + 1,
                sent_place(m),
                m.edit_was.clone().unwrap_or_default(),
                m.edit_now.clone().unwrap_or_default()
            ))
            .role("listitem")
            .font(type_::BODY)
            .color(t.text_primary)
            .wrap()
        })
        .collect();
    let edits = (!edited.is_empty()).then(|| {
        col()
            .role("list")
            .label("Your edits, saved to the files")
            .gap(space::S1)
            .child(
                text("Your edits, saved to the files")
                    .font(type_::LABEL)
                    .color(t.text_secondary),
            )
            .children(edited)
    });
    let entries = marks.iter().filter(|m| m.r#type != "edit").map(|m| {
        let kind = match m.r#type.as_str() {
            "question" => "question",
            "deletion" => "remove",
            _ => "note",
        };
        let words = m
            .text
            .clone()
            .or_else(|| m.anchor.as_ref().map(|a| a.exact.clone()))
            .unwrap_or_default();
        text(format!("{kind} {}: {words}", sent_place(m)))
            .role("listitem")
            .font(type_::BODY)
            .color(t.text_primary)
            .wrap()
    });
    let alone = marks.is_empty().then(|| {
        text("No notes: the verdict goes alone.")
            .font(crate::markdown::font(t, None))
            .color(t.text_primary.alpha(0.9))
    });
    let problem = unsent(sent).map(|why| {
        text(why)
            .key("review.send.status")
            .role("status")
            .font(type_::LABEL)
            .color(if matches!(sent, Some(Sent::Sending)) {
                t.text_secondary
            } else {
                t.warning
            })
            .wrap()
    });
    let choices = [Verdict::Approve, Verdict::RequestChanges, Verdict::Comment].map(|v| {
        let key = match v {
            Verdict::Approve => "a",
            Verdict::RequestChanges => "r",
            Verdict::Comment => "c",
        };
        let picked = v == verdict;
        let choice = button(key, v.title(), picked, t)
            .role("radio")
            .label(v.name())
            .key(format!("review.send.verdict.{key}"))
            .checked(picked);
        super::verdict_press(choice, tile, v)
    });
    let (title, onto, problem_to) = match to {
        Some(Recipient::Session(name)) => (format!("Send the review to {name}"), None, None),
        Some(Recipient::Seed(seed)) => (
            "Send the review".into(),
            Some(format!("Goes onto {seed}.")),
            None,
        ),
        None => (
            "Send the review".into(),
            None,
            Some("Bind a session to this tile to send the review."),
        ),
    };
    let send = super::send_press(
        button("↵", "Send", false, t)
            .key("review.send.go")
            .role("button")
            .label("Send"),
        tile,
    );
    let panel = col()
        .key("review.send")
        .role("dialog")
        .label("Send the review")
        .w_full()
        .max_w(640.0)
        .px(20.0)
        .py(16.0)
        .gap(space::S2)
        .child(text(title).font(type_::TITLE).color(t.text_primary))
        .children(onto.map(|o| text(o).font(type_::BODY).color(t.text_secondary)))
        .children(problem_to.map(|p| text(p).font(type_::BODY).color(t.warning).wrap()))
        .child(
            col()
                .role("list")
                .label("Marks")
                .gap(space::S1)
                .children(entries),
        )
        .children(edits)
        .children(alone)
        .child(
            row()
                .role("radiogroup")
                .label("Verdict")
                .gap(space::S2)
                .children(choices),
        )
        .children(problem)
        .child(
            row()
                .mt(4.0)
                .gap(space::S2)
                .items_center()
                .child(send)
                .child(
                    row()
                        .gap(4.0)
                        .items_center()
                        .child(super::keys::caps("esc", t))
                        .child(
                            text("keep reviewing")
                                .font(type_::LABEL)
                                .color(t.text_muted),
                        ),
                ),
        );
    rune_kit::popover::raised(attn_ui::kit(t), panel, 8.0)
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

/// A button of the send sheet: its key's cap and what it does, bordered, in the accent when
/// `picked`, as the Tauri sheet's verdicts and Send.
fn button(key: &str, what: &str, picked: bool, t: &Tokens) -> El {
    let cap = super::keys::caps(key, t).a11y_hidden();
    let words = text(what.to_owned())
        .font(type_::BODY)
        .color(if picked {
            t.text_primary
        } else {
            t.text_secondary
        })
        .a11y_hidden();
    let (first, second) = if key == "↵" {
        (words, cap)
    } else {
        (cap, words)
    };
    row()
        .gap(6.0)
        .items_center()
        .px(10.0)
        .py(4.0)
        .radius(6.0)
        .border(1.0, if picked { t.accent } else { t.border })
        .fill(if picked {
            t.accent.alpha(0.14)
        } else {
            rune::Color::TRANSPARENT
        })
        .child(first)
        .child(second)
}
