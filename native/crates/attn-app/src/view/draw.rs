//! Each part's draw function. A draw function reads its part and its doors, and nothing else.

use attn_protocol::{ViewCommandCall, ViewPart, ViewProblem, ViewTemplatePiece};
use attn_ui::Tokens;
use attn_ui::blocks::{
    Audio, Block, Card, Chart, DataTable, Facts, Inline, Notice, Playing, Reader, Standing,
};
use attn_ui::system::{
    Button, Choice, DotShape, DotSize, FieldState, GroupHead, ListRow, Look, Reveal, Size, Tone,
    Type, answers, area_box, checks, dot, keys_hint, tag, words,
};
use rune::{El, Key, KeyEvent, Tx, col, row};
use rune_kit::TextArea;
use serde_json::Value;

use super::{Doors, not_yet, parts};

/// The problem a part has, in its place.
pub(crate) fn problem(key: &str, problem: &ViewProblem, t: &Tokens) -> Block {
    Notice::error(
        format!("{key}.problem"),
        format!("Line {}: {} {}", problem.line, problem.text, problem.fix),
    )
    .build(t)
}

pub(crate) fn notice(key: &str, said: &str, t: &Tokens) -> Block {
    Notice::warn(format!("{key}.notice"), said.to_owned()).build(t)
}

/// `text` without its last line, and that line's words, when the last line is a heading.
pub(crate) fn last_heading(text: &str) -> Option<(String, String)> {
    let trimmed = text.trim_end();
    let (above, last) = trimmed.rsplit_once('\n').unwrap_or(("", trimmed));
    let words = last.trim_start_matches('#');
    (last.starts_with('#') && words.starts_with(' '))
        .then(|| (above.to_owned(), words.trim().to_owned()))
}

/// Markdown `text`, read as the view's document reads it.
pub(crate) fn prose(text: &str, doors: &Doors<'_>) -> Block {
    let room = &doors.room;
    let reading = crate::markdown::reader(
        text,
        crate::markdown::Document {
            view: room.key.clone(),
            ..room.document.clone()
        },
        room.pictures.clone(),
        Default::default(),
        None,
        crate::markdown::look(room.t).prose_measure(crate::markdown::PROSE_MEASURE),
        room.t,
    );
    Block::custom(
        room.key.clone(),
        col()
            .key(format!("{}.prose", room.key))
            .w_full()
            .child(reading),
    )
}

pub(crate) fn markdown(part: &ViewPart, doors: &Doors<'_>) -> Block {
    let text = part
        .markdown
        .as_ref()
        .map_or_else(String::new, |p| doors.values.text(&p.text));
    prose(&text, doors)
}

/// Whether `file` is a sound, which a view plays rather than shows.
pub(super) fn sounds(file: &str) -> bool {
    let ext = file.rsplit('.').next().unwrap_or("").to_ascii_lowercase();
    matches!(
        ext.as_str(),
        "wav" | "mp3" | "m4a" | "aac" | "aif" | "aiff" | "flac" | "ogg" | "opus"
    )
}

fn picture(label: &[ViewTemplatePiece], file: &[ViewTemplatePiece], doors: &Doors<'_>) -> Block {
    let (label, file) = (doors.values.text(label), doors.values.text(file));
    if sounds(&file) {
        return audio(label, &file, doors);
    }
    prose(&format!("![{label}]({file})"), doors)
}

/// A clip's player, which plays it from its file, wherever the view's file is.
fn audio(label: String, file: &str, doors: &Doors<'_>) -> Block {
    let room = &doors.room;
    let label = match label.trim() {
        "" => file.rsplit('/').next().unwrap_or(file).to_owned(),
        _ => label,
    };
    let player = Audio::new(room.key.clone(), label).highlighted(doors.tile.highlighted(&room.key));
    let player = match room.heard.clip(&room.document.path, file) {
        Some(clip) => {
            let (playing, at, length) = room.heard.of(&clip);
            player
                .playing(playing)
                .time(at, length)
                .on_toggle(room.heard.toggle(clip))
        }
        None => player.playing(Playing::Unplayable(format!("{file} is not a file"))),
    };
    player.build(room.t)
}

pub(crate) fn media(part: &ViewPart, doors: &Doors<'_>) -> Block {
    match &part.media {
        Some(media) => picture(&media.label, &media.file, doors),
        None => Block::custom(doors.room.key.clone(), col()),
    }
}

pub(crate) fn tasks(part: &ViewPart, doors: &Doors<'_>) -> Block {
    let lines: Vec<String> = (part.tasks.iter().flat_map(|p| &p.tasks))
        .map(|task| {
            format!(
                "- [{}] {}",
                if task.checked { "x" } else { " " },
                doors.values.text(&task.text)
            )
        })
        .collect();
    prose(&lines.join("\n"), doors)
}

/// A question's text field: its key under the question's.
pub(crate) fn field_key(question: &str) -> String {
    format!("{question}.text")
}

pub(crate) fn question(part: &ViewPart, doors: &Doors<'_>) -> Block {
    let (key, t) = (&doors.room.key, doors.room.t);
    if let Some(file) = &part.question_file {
        let prompt = &file.question.prompt;
        return not_yet(
            key,
            &format!("Answering \"{prompt}\" with a file in a view"),
            t,
        );
    }
    let (name, asked, many) = match (&part.question, &part.question_multi, &part.question_text) {
        (Some(q), _, _) => (&q.name, &q.question, false),
        (_, Some(q), _) => (&q.name, &q.question, true),
        (_, _, Some(q)) => (&q.name, &q.question, false),
        _ => return Block::custom(key.clone(), col()),
    };
    let prompt = doors.values.text(&[attn_protocol::ViewTemplatePiece {
        text: Some(asked.prompt.clone()),
        ..Default::default()
    }]);
    let shown = match &part.question_text {
        Some(text) => written(name, &prompt, text.value.as_ref(), doors),
        None => choices(name, &prompt, asked, many, doors),
    };
    let heading = words(prompt.clone(), Type::Name, t.text_primary).key(format!("{key}.prompt"));
    Block::custom(
        format!("{key}.slot"),
        col()
            .key(key.clone())
            .role("group")
            .label(prompt)
            .w_full()
            .child(
                attn_ui::blocks::Stack::tight()
                    .w_full()
                    .child(heading)
                    .child(shown),
            ),
    )
}

fn choices(
    name: &str,
    prompt: &str,
    asked: &attn_protocol::AskQuestion,
    many: bool,
    doors: &Doors<'_>,
) -> El {
    let (key, t) = (&doors.room.key, doors.room.t);
    let active = doors.tile.reaches(key);
    let picked = doors.answers.picked(name);
    let n = asked.choices.len();
    let options = asked.choices.iter().enumerate().map(|(i, choice)| {
        let mut shown = Choice::new(format!("{key}.choice.{i}"), choice.label.clone())
            .many(many)
            .chosen(picked.contains(&choice.label))
            .selectable(true);
        if let Some(note) = choice.description.as_deref().filter(|d| !d.is_empty()) {
            shown = shown.note(note.to_owned());
        }
        if i < 9 {
            let cap = attn_ui::keycap::Keys::ledged(&[(i + 1).to_string()])
                .build(t)
                .a11y_hidden();
            shown = shown.trailing(if active { cap } else { cap.opacity(0.0) });
        }
        El::from(shown.build((i, n), t, doors.answers.pick(name, &choice.label, many)))
    });
    let group = if many {
        checks(format!("{key}.choices"), prompt)
    } else {
        answers(format!("{key}.choices"), prompt)
    };
    group.children(options)
}

fn written(
    name: &str,
    prompt: &str,
    value: Option<&Vec<ViewTemplatePiece>>,
    doors: &Doors<'_>,
) -> El {
    let (key, t) = (&doors.room.key, doors.room.t);
    let value = value.map(|v| doors.values.text(v));
    let starting = value.clone().unwrap_or_default();
    let edit = doors
        .answers
        .written(name)
        .unwrap_or_else(|| rune::TextEdit::new(&starting));
    let area = TextArea::new(&edit)
        .key(field_key(key))
        .label(prompt.to_owned())
        .placeholder("Your answer…".to_owned())
        .auto_grow(2, 12)
        .on_edit(doors.answers.writes(name, &edit.text, value))
        .on_blur(doors.answers.wrote(name))
        .on_key(leave_field);
    area_box(area, FieldState::Ready, t).into()
}

/// Escape leaves a view's text field, which keeps what was written.
fn leave_field(_: &mut crate::State, ev: &KeyEvent, tx: &mut Tx) -> bool {
    if ev.key != Key::Escape {
        return false;
    }
    tx.blur();
    true
}

/// Send, at the end of a view that asks something, and what the last send did.
pub(crate) fn sending(key: &str, send_key: Option<&str>, doors: &Doors<'_>) -> Block {
    let t = doors.room.t;
    let sent = doors.answers.sent();
    let place = doors.answers.place.clone();
    let button = Button::new(if sent.is_some() { "Send again" } else { "Send" })
        .look(Look::Primary)
        .size(Size::Compact)
        .on_press_tx(move |state: &mut crate::State, tx: &mut Tx| {
            super::answers::send(state, &place, tx)
        })
        .build(t);
    let button = El::from(button).key(format!("{key}.button"));
    let said = match sent {
        None => "Sends your answers to the agent that opened this view.".to_owned(),
        Some(sent) if sent.told => {
            format!("Sent. The agent has your answers in {}.", sent.responses)
        }
        Some(sent) => format!(
            "Sent, but the agent that opened this view is gone, so no agent was told. Your answers are in {}.",
            sent.responses
        ),
    };
    let tone = match sent {
        Some(sent) if !sent.told => t.warning,
        _ => t.text_secondary,
    };
    Block::custom(
        format!("{key}.slot"),
        Inline::loose()
            .key(key.to_owned())
            .w_full()
            .child(button)
            .children(send_key.map(|k| keys_hint([k], "sends", t)))
            .child(
                words(said, Type::Small, tone)
                    .key(format!("{key}.said"))
                    .grow(1.0),
            ),
    )
}

pub(crate) fn compare(part: &ViewPart, doors: &Doors<'_>) -> Block {
    let shown = (part.compare.iter().flat_map(|c| &c.media))
        .enumerate()
        .map(|(i, media)| {
            let inner = doors.inner(i, doors.values.clone(), None);
            Reader::new()
                .push(picture(&media.label, &media.file, &inner))
                .bare(doors.room.t)
                .min_w(0.0)
        });
    let pair = Inline::top()
        .key(doors.room.key.clone())
        .w_full()
        .equal_columns(2)
        .children(shown);
    Block::custom(format!("{}.slot", doors.room.key), pair)
}

/// Where a part's values come from. A source that is not ready says why in the part's place.
pub(crate) fn data(part: &ViewPart, doors: &Doors<'_>) -> Block {
    let Some(data) = &part.data else {
        return Block::custom(doors.room.key.clone(), col());
    };
    let named = [
        ("file", &data.file),
        ("run", &data.run),
        ("attn", &data.attn),
        ("store", &data.store),
    ];
    let Some((kind, id)) = named
        .into_iter()
        .find_map(|(kind, id)| id.as_ref().map(|id| (kind, id)))
    else {
        return Block::custom(doors.room.key.clone(), col());
    };
    match doors.values.source(id) {
        Some(("unavailable", _)) => {
            not_yet(&doors.room.key, &format!("{kind}=\"{id}\""), doors.room.t)
        }
        Some(("failed", said)) => notice(&doors.room.key, &said, doors.room.t),
        _ => Block::custom(doors.room.key.clone(), col().key(doors.room.key.clone())),
    }
}

/// The tone a state's word reads in.
fn tone_of(word: &str) -> Tone {
    match word.to_ascii_lowercase().as_str() {
        "ok" | "success" | "passed" | "pass" | "green" | "done" | "merged" | "ready" => Tone::Good,
        "failed" | "failure" | "error" | "red" | "broken" | "cancelled" => Tone::Bad,
        "running" | "working" | "pending" | "queued" | "waiting" | "in_progress" | "stale" => {
            Tone::Warn
        }
        _ => Tone::Neutral,
    }
}

fn mark(word: &str, t: &Tokens) -> El {
    let ink = match tone_of(word) {
        Tone::Good => t.success,
        Tone::Bad => t.error,
        Tone::Warn => t.state_working,
        _ => t.text_muted,
    };
    dot(ink, DotShape::Filled, DotSize::Small)
}

pub(crate) fn status(part: &ViewPart, doors: &Doors<'_>) -> Block {
    let word = part
        .status
        .as_ref()
        .and_then(|s| doors.values.get(&s.field))
        .map_or_else(String::new, super::values::said);
    let key = doors.room.key.clone();
    let shown = tag(
        format!("{key}.state"),
        word.clone(),
        tone_of(&word),
        doors.room.t,
    );
    Block::control(key, El::from(shown).label(word))
}

pub(crate) fn facts(part: &ViewPart, doors: &Doors<'_>) -> Block {
    let key = &doors.room.key;
    (part.facts.iter().flat_map(|f| &f.facts))
        .enumerate()
        .fold(Facts::new(key.clone()), |facts, (i, fact)| {
            let (key, said) = (format!("{key}.{i}"), doors.values.text(&fact.value));
            match said.contains('`') {
                true => facts.item(key, fact.label.clone(), with_code(&said, doors.room.t)),
                false => facts.fact(key, fact.label.clone(), said),
            }
        })
        .build(doors.room.t)
}

/// A command a part offers, drawn as a button that stays off until its command runs.
fn control(key: &str, call: &ViewCommandCall, doors: &Doors<'_>) -> El {
    let runs = doors.commands.runs(call);
    let shown: String = (call.command.chars().enumerate())
        .map(|(i, c)| match (i, c) {
            (0, c) => c.to_ascii_uppercase(),
            (_, '-' | '_') => ' ',
            (_, c) => c,
        })
        .collect();
    let button = Button::new(shown)
        .look(Look::Quiet)
        .size(Size::Compact)
        .disabled(!runs)
        .help(format!("{} is not available yet", call.command))
        .build(doors.room.t);
    El::from(button).key(format!("{key}.{}", call.command))
}

fn controls_of<'a>(
    key: &'a str,
    calls: &'a [ViewCommandCall],
    doors: &'a Doors<'_>,
) -> impl Iterator<Item = El> + 'a {
    calls.iter().map(move |call| control(key, call, doors))
}

/// `text` in words, its `code` spans set in Mono.
fn with_code(text: &str, t: &Tokens) -> El {
    let pieces = (text.split('`').enumerate())
        .filter(|(_, piece)| !piece.trim().is_empty())
        .map(|(i, piece)| {
            let kind = if i % 2 == 1 { Type::Mono } else { Type::Body };
            words(piece.trim().to_owned(), kind, t.text_primary)
        });
    Inline::tight()
        .flex_wrap()
        .children(pieces)
        .a11y_value(text.replace('`', ""))
}

/// What a row's lead shows: a state's mark for a word, the words otherwise.
fn lead(said: &str, t: &Tokens) -> El {
    match tone_of(said) {
        Tone::Neutral => words(said.to_owned(), Type::Small, t.text_tertiary).single_line(),
        _ => mark(said, t),
    }
}

pub(crate) fn list(part: &ViewPart, doors: &Doors<'_>) -> Block {
    let Some(list) = &part.list else {
        return Block::custom(doors.room.key.clone(), col());
    };
    let (key, t) = (&doors.room.key, doors.room.t);
    if let Some(failed) = items_failed(&list.items, doors) {
        return failed;
    }
    let items = doors.values.items(&list.items);
    let slots = &list.slots;
    if items.is_empty() && slots.empty.is_none() {
        return Block::custom(key.clone(), col().key(key.clone()));
    }
    let head_key = format!("{key}.head");
    let foldable = list.foldable == Some(true);
    let folded = foldable && doors.tile.folded(key);
    let head = doors.room.heading.as_ref().map(|heading| {
        let mut head = GroupHead::new(head_key.clone(), heading.clone())
            .name(heading.clone())
            .current(doors.tile.highlighted(&head_key));
        if list.count == Some(true) {
            head = head.aside(words(items.len().to_string(), Type::Mono, t.text_tertiary));
        }
        if let Some(aside) = &slots.group_aside {
            head = head.aside(words(
                doors.values.text(aside),
                Type::Small,
                t.text_tertiary,
            ));
        }
        if let Some(calls) = &slots.group_controls {
            head = head.aside(Inline::tight().children(controls_of(&head_key, calls, doors)));
        }
        let head = if foldable { head.open(!folded) } else { head };
        let head = head.build(t);
        if foldable {
            head.on_press(doors.tile.fold(key))
        } else {
            head
        }
    });
    let rows = (!folded).then(|| {
        items.iter().enumerate().map(|(i, item)| {
            let scope = doors.values.within(item);
            let row_key = format!("{key}.row.{i}");
            let title = slots
                .title
                .as_ref()
                .map_or_else(String::new, |s| scope.text(s));
            let mut row = ListRow::new(row_key.clone(), title.clone())
                .title(title)
                .selected(doors.tile.highlighted(&row_key));
            if let Some(s) = &slots.lead {
                row = row.lead(lead(&scope.text(s), t));
            }
            if let Some(s) = &slots.sub {
                row = row.subtitle(scope.text(s), t);
            }
            if let Some(s) = &slots.aside {
                row = row.aside(words(scope.text(s), Type::Mono, t.text_tertiary));
            }
            if let Some(calls) = &slots.controls {
                row = row.controls(controls_of(&row_key, calls, doors), Reveal::Quiet);
            }
            row.disabled(
                slots
                    .press
                    .as_ref()
                    .is_some_and(|call| !doors.commands.runs(call)),
            )
            .build(t)
        })
    });
    let empty = (items.is_empty() && !folded)
        .then(|| slots.empty.clone())
        .flatten()
        .map(|said| words(said, Type::Small, t.text_tertiary).key(format!("{key}.empty")));
    let shown = col()
        .key(key.clone())
        .role("list")
        .label(
            doors
                .room
                .heading
                .clone()
                .unwrap_or_else(|| "List".to_owned()),
        )
        .w_full()
        .children(head)
        .children(rows.into_iter().flatten())
        .children(empty);
    Block::custom(format!("{key}.slot"), shown)
}

/// The notice of a part whose items come from a file that could not be read.
fn items_failed(items: &attn_protocol::ViewItems, doors: &Doors<'_>) -> Option<Block> {
    let file = items.file.as_ref()?;
    match doors.values.source(file)? {
        ("failed", said) => Some(notice(&doors.room.key, &said, doors.room.t)),
        _ => None,
    }
}

pub(crate) fn table(part: &ViewPart, doors: &Doors<'_>) -> Block {
    let Some(table) = &part.table else {
        return Block::custom(doors.room.key.clone(), col());
    };
    if let Some(failed) = items_failed(&table.items, doors) {
        return failed;
    }
    let key = &doors.room.key;
    let columns = table.columns.iter().map(|c| c.header.clone()).collect();
    doors
        .values
        .items(&table.items)
        .iter()
        .enumerate()
        .fold(
            DataTable::new(key.clone(), "Table", columns),
            |shown, (i, item)| {
                let scope = doors.values.within(item);
                let row_key = format!("{key}.row.{i}");
                let cells = table.columns.iter().map(|c| scope.text(&c.cell)).collect();
                let selected = doors.tile.highlighted(&row_key);
                shown.row(row_key, cells, selected)
            },
        )
        .build(doors.room.t)
}

pub(crate) fn chart(part: &ViewPart, doors: &Doors<'_>) -> Block {
    let Some(chart) = &part.chart else {
        return Block::custom(doors.room.key.clone(), col());
    };
    if let Some(failed) = items_failed(&chart.items, doors) {
        return failed;
    }
    let key = &doors.room.key;
    let shown = doors.values.items(&chart.items).iter().fold(
        Chart::new(
            key.clone(),
            format!("{} by {}", chart.y.join("."), chart.x.join(".")),
        ),
        |shown, item| {
            let scope = doors.values.within(item);
            match scope.number(&chart.y) {
                Some(y) => shown.point(
                    scope
                        .get(&chart.x)
                        .map_or_else(String::new, super::values::said),
                    scope
                        .get(&chart.y)
                        .map_or_else(String::new, super::values::said),
                    y,
                ),
                None => shown,
            }
        },
    );
    Block::custom(format!("{key}.slot"), shown.el(doors.room.t))
}

/// `blocks` drawn once for `item`, as the `n`th of a container's items, its answers in its row.
fn item(blocks: &[ViewPart], item: &Value, n: usize, doors: &Doors<'_>) -> El {
    let mut inner = doors.inner(n, doors.values.within(item), None);
    inner.answers = inner.answers.within(super::values::row_id(item, n));
    Reader::new()
        .push_all(parts(blocks, &inner))
        .bare(doors.room.t)
        .key(inner.room.key.clone())
}

pub(crate) fn each(part: &ViewPart, doors: &Doors<'_>) -> Block {
    let Some(each) = &part.each else {
        return Block::custom(doors.room.key.clone(), col());
    };
    if let Some(failed) = items_failed(&each.items, doors) {
        return failed;
    }
    let items = doors.values.items(&each.items);
    let shown = (items.iter().enumerate()).map(|(n, value)| item(&each.blocks, value, n, doors));
    Block::custom(
        format!("{}.slot", doors.room.key),
        attn_ui::blocks::Stack::loose()
            .key(doors.room.key.clone())
            .w_full()
            .children(shown),
    )
}

pub(crate) fn one_by_one(part: &ViewPart, doors: &Doors<'_>) -> Block {
    let Some(steps) = &part.one_by_one else {
        return Block::custom(doors.room.key.clone(), col());
    };
    if let Some(failed) = items_failed(&steps.items, doors) {
        return failed;
    }
    let (key, t) = (&doors.room.key, doors.room.t);
    let items = doors.values.items(&steps.items);
    let rows: Vec<String> = (items.iter().enumerate())
        .map(|(n, item)| super::values::row_id(item, n))
        .collect();
    let at = super::keys::resting(doors.tile.steps(key).at, &rows, &doors.answers);
    let held = doors.tile.highlighted(key);
    let verdicts: Vec<Option<&str>> = (rows.iter())
        .map(|row| super::keys::verdict(&steps.blocks, &doors.answers, row))
        .collect();
    let counted = |word: &str| verdicts.iter().filter(|v| **v == Some(word)).count();
    let progress = if at < items.len() {
        format!("{} of {}", at + 1, items.len())
    } else {
        let unseen = verdicts.iter().filter(|v| v.is_none()).count();
        let mut said = format!(
            "All {}: {} kept, {} changed, {} skipped",
            items.len(),
            counted("kept"),
            counted("changed"),
            counted("skipped")
        );
        if unseen > 0 {
            said.push_str(&format!(", {unseen} not answered"));
        }
        said
    };
    let verdict = verdicts
        .get(at)
        .copied()
        .flatten()
        .map_or(String::new(), |v| format!(" · {v}"));
    let head = row()
        .key(format!("{key}.progress"))
        .w_full()
        .items_center()
        .child(words(format!("{progress}{verdict}"), Type::Name, t.text_primary).grow(1.0))
        .child(words(
            super::keys::steps_hint(&steps.blocks),
            Type::Small,
            if held {
                t.text_secondary
            } else {
                t.text_dimmed
            },
        ));
    let shown = items
        .get(at)
        .map(|value| item(&steps.blocks, value, at, doors));
    let card = Card::new(key.clone())
        .standing(if held {
            Standing::Picked
        } else {
            Standing::Resting
        })
        .push(head)
        .push_all(shown)
        .build(t)
        .role("group")
        .label(format!("One by one: {progress}"))
        .a11y_value(progress);
    Block::custom(format!("{key}.slot"), card)
}

pub(crate) fn entry(_: &ViewPart, doors: &Doors<'_>) -> Block {
    not_yet(
        &doors.room.key,
        "Typing into a view's command",
        doors.room.t,
    )
}

pub(crate) fn controls(part: &ViewPart, doors: &Doors<'_>) -> Block {
    let key = &doors.room.key;
    let calls = part
        .controls
        .as_ref()
        .map_or(&[][..], |c| c.controls.as_slice());
    let buttons = controls_of(key, calls, doors);
    Block::custom(
        format!("{key}.slot"),
        Inline::tight().key(key.clone()).children(buttons),
    )
}

pub(crate) fn commands(_: &ViewPart, doors: &Doors<'_>) -> Block {
    not_yet(
        &doors.room.key,
        "Running a view's own commands",
        doors.room.t,
    )
}
