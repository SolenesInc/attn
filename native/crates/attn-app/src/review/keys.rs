//! The review's own chrome around the document: the change's facts under the title, the Not
//! shown list, the key line under the tile and the sheet of every key.

use std::collections::HashSet;

use attn_protocol::{ReviewBundle, ReviewFile};
use attn_ui::Tokens;
use attn_ui::type_;
use rune::tok::{radius, space};
use rune::{El, col, row, text};

use super::frames::Frame;
use super::{Review, Spot, cursor_frame, frame_press, place_press, prefix};

fn short(commit: &str) -> &str {
    &commit[..commit.len().min(9)]
}

fn plural(count: usize, one: &str, many: &str) -> String {
    format!("{count} {}", if count == 1 { one } else { many })
}

/// The changed lines the stops show, each counted once however many stops show it.
fn shown_changed_lines(bundle: &ReviewBundle) -> usize {
    bundle
        .stops
        .iter()
        .flat_map(|stop| {
            stop.rows
                .iter()
                .filter(|r| r.kind != " ")
                .map(move |r| (stop.path.as_str(), r.kind.as_str(), r.old, r.now))
        })
        .collect::<HashSet<_>>()
        .len()
}

/// The change's facts: base and head, the files, the lines added and removed and how many of
/// them the stops show, which Enter opens into the list of changed files.
pub(super) fn summary(
    tile: &str,
    bundle: &ReviewBundle,
    review: &Review,
    cursor: Option<&Spot>,
    t: &Tokens,
) -> El {
    let prefix = &prefix(tile);
    let added: i64 = bundle.files.iter().map(|f| f.added).sum();
    let removed: i64 = bundle.files.iter().map(|f| f.removed).sum();
    let commits = match bundle.base.as_deref().filter(|b| !b.is_empty()) {
        Some(base) => format!("base {} → head {}", short(base), short(&bundle.head)),
        None => format!("a tour at head {}", short(&bundle.head)),
    };
    let mut facts = vec![
        commits,
        plural(bundle.files.len(), "file", "files"),
        format!("+{added} −{removed}"),
        format!(
            "{} {} of {} changed lines",
            plural(bundle.stops.len(), "stop shows", "stops show"),
            shown_changed_lines(bundle),
            added + removed
        ),
    ];
    if !bundle.files.is_empty() {
        let toggle = if review.files_shown { "hide" } else { "show" };
        facts.push(format!("↵ {toggle} the files"));
    }
    let line = facts.join(" · ");
    let mut meta = row()
        .key(format!("{prefix}.facts"))
        .role("button")
        .label(line.clone())
        .w_full()
        .child(text(line).font(type_::LABEL).color(t.text_secondary).wrap());
    if !bundle.files.is_empty() {
        meta = meta.expanded(review.files_shown);
    }
    let files = review.files_shown.then(|| {
        bundle.files.iter().zip(0..).map(|(file, i)| {
            let current = cursor == Some(&Spot::File(file.path.clone()));
            let el = cursor_frame(file_row(prefix, i, file, t), current, t);
            place_press(el, tile, format!("{prefix}.file.{i}"))
        })
    });
    let problems = (!bundle.problems.is_empty()).then(|| {
        col()
            .role("list")
            .label("Problems in the review document")
            .gap(space::S1)
            .children(bundle.problems.iter().map(|p| {
                let at = if p.line > 0 {
                    format!("line {}: ", p.line)
                } else {
                    String::new()
                };
                text(format!("{at}{}", p.message))
                    .font(type_::BODY)
                    .color(t.warning)
                    .wrap()
            }))
    });
    col()
        .role("region")
        .label("The change")
        .w_full()
        .gap(space::S1)
        .child(place_press(
            cursor_frame(meta, cursor == Some(&Spot::Facts), t),
            tile,
            format!("{prefix}.facts"),
        ))
        .children(files.into_iter().flatten())
        .children(problems)
}

fn file_row(prefix: &str, i: usize, file: &ReviewFile, t: &Tokens) -> El {
    let stat = if file.binary {
        "binary".to_owned()
    } else {
        format!("+{} −{}", file.added, file.removed)
    };
    let from = file.old_path.as_deref().map(|p| format!("from {p}"));
    let label = [
        Some(file.status.clone()),
        Some(file.path.clone()),
        from.clone(),
        Some(stat.clone()),
    ]
    .into_iter()
    .flatten()
    .collect::<Vec<_>>()
    .join(" ");
    row()
        .key(format!("{prefix}.file.{i}"))
        .role("row")
        .label(label)
        .w_full()
        .gap(space::S2)
        .child(
            text(file.status.clone())
                .font(type_::LABEL)
                .color(t.text_tertiary)
                .w(72.0),
        )
        .child(
            text(file.path.clone())
                .font(type_::LABEL)
                .color(t.text_primary),
        )
        .children(from.map(|f| text(f).font(type_::LABEL).color(t.text_tertiary)))
        .child(text(stat).font(type_::LABEL).color(t.text_secondary))
}

/// The files the review leaves out, each with why, keyed `key`; places of the document of
/// review tile `tile` when it names one.
pub(super) fn not_shown(
    (tile, key): (Option<&str>, &str),
    bundle: &ReviewBundle,
    cursor: Option<&Spot>,
    t: &Tokens,
) -> El {
    col()
        .key(key.to_owned())
        .role("list")
        .label("Not shown")
        .w_full()
        .gap(space::S1)
        .children(bundle.not_shown.iter().zip(0..).map(|(entry, j)| {
            let current = cursor == Some(&Spot::NotShown(entry.path.clone()));
            let el = row()
                .key(format!("{key}.entry.{j}"))
                .role("row")
                .label(format!("{} {}", entry.path, entry.reason))
                .w_full()
                .gap(space::S2)
                .pl(6.0)
                .child(
                    text(entry.path.clone())
                        .font(type_::MONO_CAPTION)
                        .color(t.text_primary),
                )
                .child(
                    text(entry.reason.clone())
                        .font(type_::LABEL)
                        .color(t.text_secondary)
                        .wrap(),
                );
            let el = cursor_frame(el, current, t);
            match tile {
                Some(tile) => place_press(el, tile, format!("{key}.entry.{j}")),
                None => el,
            }
        }))
}

/// The keys for what the review shows in `frame`, under the tile, after the frames it offers.
/// The send key as the default keymap binds it on this platform.
pub(super) fn send_label() -> String {
    let keymap = crate::Keymap::from_setting(None, rune::KeyPlatform::current());
    keymap
        .binding(&crate::Action::MarkdownSendAnnotations)
        .map(|b| keymap.label(b))
        .unwrap_or_default()
}

/// The keys for what the review shows in `frame` with a file open or not, as the Tauri key
/// line lists them, with the marking keys after the moves when the file open takes marks.
fn keys_for(
    frame: Frame,
    file_open: bool,
    sides: bool,
    marking: bool,
) -> Vec<(String, &'static str)> {
    let list: &[(&str, &'static str)] = match (file_open, frame) {
        (true, _) => &[
            ("↑ ↓", "line"),
            ("← →", "change"),
            ("⇧← ⇧→", "sideways"),
            ("z", "fill the tile"),
            ("/", "search"),
            ("⌃o", "back"),
            ("esc", "close"),
        ],
        (false, Frame::Walkthrough) => &[
            ("↑ ↓", "line"),
            ("← →", "stop"),
            ("⇧← ⇧→", "sideways"),
            ("z", "hide the script"),
            ("/", "search"),
        ],
        (false, Frame::Map) => &[
            ("↑ ↓", "line"),
            ("← →", "arrow"),
            ("↵", "open the file"),
            ("e", "more lines"),
            ("/", "search"),
        ],
        (false, Frame::Brief) => &[
            ("↑ ↓", "line"),
            ("← →", "stop"),
            ("↵", "open the file"),
            ("e", "more lines"),
            ("z", "fill the tile"),
            ("/", "search"),
        ],
    };
    let mut keys: Vec<(String, &'static str)> =
        list.iter().map(|(k, w)| ((*k).to_owned(), *w)).collect();
    if sides && !file_open {
        let last = keys.pop();
        keys.push(("b".into(), "before, after"));
        keys.extend(last);
    }
    let marks = [
        ("c".to_owned(), "note"),
        ("a".to_owned(), "ask"),
        ("i".to_owned(), "edit"),
        (send_label(), "send"),
    ];
    if marking {
        keys.splice(2..2, marks);
    }
    keys
}

pub(super) fn key_line(
    tile: &str,
    frame: Frame,
    offered: &[Frame],
    (sides, file_open, marking): (bool, bool, bool),
    servers: Option<El>,
    t: &Tokens,
) -> El {
    let prefix = &prefix(tile);
    let entry = |keys: &str, what: &str| {
        text(format!("{keys} {what}"))
            .font(type_::LABEL)
            .color(t.text_secondary)
    };
    let keys = keys_for(frame, file_open, sides, marking);
    let tabs = (offered.len() > 1).then(|| {
        row()
            .role("tablist")
            .label("Frames")
            .gap(space::S2)
            .children(
                Frame::ALL
                    .iter()
                    .filter(|(f, _, _)| offered.contains(f))
                    .map(|(f, key, name)| {
                        let on = *f == frame;
                        let tab = text(format!("{key} {name}"))
                            .font(type_::LABEL)
                            .color(if on { t.text_primary } else { t.text_tertiary })
                            .role("tab")
                            .label(format!("{key} {name}"))
                            .key(format!("{prefix}.frame.{key}"))
                            .checked(on);
                        frame_press(tab, tile, *f)
                    }),
            )
    });
    row()
        .key(format!("{prefix}.keys"))
        .role("toolbar")
        .label("Review keys")
        .w_full()
        .flex_wrap()
        .gap(space::S3)
        .px(space::S3)
        .py(space::S1)
        .border_sides([1.0, 0.0, 0.0, 0.0], t.border.alpha(0.5))
        .children(tabs)
        .children(keys.iter().map(|(k, w)| entry(k, w)))
        .children(servers)
        .child(col().grow(1.0))
        .child(entry("?", "all keys"))
}

const ALL_KEYS: &[(&str, &str)] = &[
    (
        "1 2 3",
        "Brief: the document top to bottom. Walkthrough: the stops as a script beside the file. Map: the sequence diagram beside the step of the review that explains the current arrow. A frame the document cannot fill is left out.",
    ),
    (
        "↑ ↓",
        "Move by line in code, by block in prose, by arrow with a stop in a drawn diagram. In the file view, by line; PageUp and PageDown move a page.",
    ),
    (
        "⇧↑ ⇧↓",
        "Select several lines of code, so a note or question covers them.",
    ),
    (
        "c",
        "Write a note on the line, the selected lines or the paragraph. It waits for the send.",
    ),
    (
        "a",
        "Ask the agent about the line, the selected lines or the paragraph. The question goes at once; the answer shows under the line.",
    ),
    (
        "i",
        "Edit what the cursor is on: the selected lines, else a whole string or comment of several lines, else the line. ⌥↑ and ⌥↓ take in a line. Saving writes the file and lists the edit in the review.",
    ),
    (
        "u",
        "Undo your edit where the cursor is: the file gets its old text back and the edit leaves the review.",
    ),
    (
        "send",
        "Send the review: see the notes and edits it carries, pick a verdict (a approve, r request changes, c comment), then ↵.",
    ),
    (
        "← →",
        "The previous or next stop; in the Map, the previous or next arrow with a stop. In the file view opened from Brief or Map, the previous or next change.",
    ),
    (
        "⇧← ⇧→",
        "Scroll the file sideways by half its width, in the file view and in the Walkthrough, to read the rest of a long line. A long line wraps in a stop.",
    ),
    (
        "b",
        "Switch between the diagrams before and after the change, when the document has both. Every frame shows the same side.",
    ),
    (
        "o",
        "On a stop of a markdown file, open the file in the editor tile, rooted at the repository. Only for a review on this machine.",
    ),
    (
        "f",
        "Go to the definition of a name on the line: each name gets a label, type it. On a definition, list its uses. An answer that comes after you moved on is offered in one line above the keys: ↵ takes it, esc drops it, and every other key keeps working. The line asked about is marked … until the answer comes.",
    ),
    (
        "r",
        "List the uses of a name on the line; ↑↓ pick one, ↵ opens it.",
    ),
    ("h", "Show the type of a name on the line."),
    (
        "s",
        "When a language of the review has no working server, start an agent on the review’s machine that sets one up. The review gains navigation once it works.",
    ),
    (
        "x",
        "Stop offering setup in this repository, or offer it again. s still sets up.",
    ),
    (
        "↵",
        "Open the file the cursor is on, at that line, with the stop's lines marked. On an arrow of a diagram, open its stop's file. On the change's facts under the title, show or hide the changed files.",
    ),
    (
        "e",
        "Show 10 more lines on each side of the stop. The stop's label names the lines it shows.",
    ),
    (
        "z",
        "Fill the tile with the file. In the file view, switch between filling the tile and the side panel. In the Walkthrough, hide or show the script.",
    ),
    (
        "/",
        "Search the repository: files by name, lines by text. The empty box lists the changed files with their added and removed lines.",
    ),
    (
        "⌃o",
        "Go back to the file you came from; from the first file, close the file view. The Walkthrough keeps its stop.",
    ),
    (
        "esc",
        "Close the edit box, the send, the search, this sheet, a selection, or the file view. In the Walkthrough, return to the stop.",
    ),
    ("?", "Show or hide this sheet."),
];

/// Every key the review takes, with what it does.
pub(super) fn sheet(prefix: &str, read_only: bool, t: &Tokens) -> El {
    col()
        .key(format!("{prefix}.sheet"))
        .role("dialog")
        .label("Review keys")
        .absolute(0.0, 0.0)
        .w_full()
        .padding(space::S3)
        .gap(space::S2)
        .radius(radius::LG)
        .fill(t.bg_elevated)
        .border(1.0, t.border)
        .child(text("Keys").font(type_::TITLE).color(t.text_primary))
        .children(read_only.then(|| {
            text("This file is outside the repository and read-only: c, a and i do nothing here.")
                .font(type_::BODY)
                .color(t.text_secondary)
                .wrap()
        }))
        .children(ALL_KEYS.iter().map(|(keys, what)| {
            let keys = if *keys == "send" {
                send_label()
            } else {
                (*keys).to_owned()
            };
            row()
                .role("row")
                .label(format!("{keys}: {what}"))
                .gap(space::S3)
                .child(text(keys).font(type_::LABEL).color(t.text_primary).w(64.0))
                .child(
                    text(*what)
                        .font(type_::BODY)
                        .color(t.text_secondary)
                        .wrap()
                        .grow(1.0),
                )
        }))
}
