//! Each part's draw function. A draw function reads its part and its doors, and nothing else.

use attn_protocol::{ViewCommandCall, ViewPart, ViewProblem, ViewTemplatePiece};
use attn_ui::Tokens;
use attn_ui::blocks::{Block, Card, Chart, DataTable, Facts, Inline, Notice, Reader, Standing};
use attn_ui::system::{
    DotShape, DotSize, GroupHead, IconSize, ListRow, Reveal, Size, Tone, Type, dot, icon_button,
    tag, words,
};
use rune::{El, col, row};
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

/// Whether `file` is a sound, which a view plays once audio comes.
fn sounds(file: &str) -> bool {
    let ext = file.rsplit('.').next().unwrap_or("").to_ascii_lowercase();
    matches!(
        ext.as_str(),
        "wav" | "mp3" | "m4a" | "aac" | "ogg" | "flac" | "opus"
    )
}

fn picture(label: &[ViewTemplatePiece], file: &[ViewTemplatePiece], doors: &Doors<'_>) -> Block {
    let (label, file) = (doors.values.text(label), doors.values.text(file));
    if sounds(&file) {
        return not_yet(&doors.room.key, &format!("Playing {file}"), doors.room.t);
    }
    prose(&format!("![{label}]({file})"), doors)
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

pub(crate) fn question(part: &ViewPart, doors: &Doors<'_>) -> Block {
    let _ = doors.answers.taken();
    not_yet(
        &doors.room.key,
        &format!("Answering \"{}\" in a view", part.name),
        doors.room.t,
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
            facts.fact(
                format!("{key}.{i}"),
                fact.label.clone(),
                doors.values.text(&fact.value),
            )
        })
        .build(doors.room.t)
}

/// A command a part offers, drawn as a button that does nothing until its command runs.
fn control(key: &str, call: &ViewCommandCall, doors: &Doors<'_>) -> attn_ui::system::IconButton {
    let runs = doors.commands.runs(call);
    icon_button(
        format!("{key}.{}", call.command),
        format!("{} (not available yet)", call.command),
        call.command
            .chars()
            .next()
            .map_or("·".to_owned(), |c| c.to_string()),
        IconSize::Regular,
    )
    .size(Size::Small)
    .disabled(!runs)
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
            head = head.controls(calls.iter().map(|call| control(&head_key, call, doors)));
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
                row = row.controls(
                    calls
                        .iter()
                        .map(|call| control(&row_key, call, doors).build(t).into()),
                    Reveal::Quiet,
                );
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

/// `blocks` drawn once for `item`, as the `n`th of a container's items.
fn item(blocks: &[ViewPart], item: &Value, n: usize, doors: &Doors<'_>) -> El {
    let inner = doors.inner(n, doors.values.within(item), None);
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
    let at = doors.tile.steps(key);
    let held = doors.tile.highlighted(key);
    let progress = if at.at < items.len() {
        format!("{} of {}", at.at + 1, items.len())
    } else {
        format!(
            "All {} seen: {} kept, {} skipped",
            items.len(),
            at.kept.len(),
            at.skipped.len()
        )
    };
    let verdict = if at.kept.contains(&at.at) {
        " · kept"
    } else if at.skipped.contains(&at.at) {
        " · skipped"
    } else {
        ""
    };
    let head = row()
        .key(format!("{key}.progress"))
        .w_full()
        .items_center()
        .child(words(format!("{progress}{verdict}"), Type::Name, t.text_primary).grow(1.0))
        .child(words(
            "↩ keep · s skip · ← →",
            Type::Small,
            if held {
                t.text_secondary
            } else {
                t.text_dimmed
            },
        ));
    let shown = items
        .get(at.at)
        .map(|value| item(&steps.blocks, value, at.at, doors));
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
    let buttons = (part.controls.iter().flat_map(|c| &c.controls))
        .map(|call| El::from(control(key, call, doors).build(doors.room.t)));
    Block::custom(
        format!("{key}.slot"),
        Inline::tight().key(key.clone()).children(buttons),
    )
}

pub(crate) fn commands(_: &ViewPart, doors: &Doors<'_>) -> Block {
    not_yet(&doors.room.key, "A view's own commands", doors.room.t)
}
