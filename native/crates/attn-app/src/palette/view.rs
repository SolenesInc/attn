use attn_ui::Tokens;
use attn_ui::blocks::{Inline, Stack};
use attn_ui::finder::{self, FinderSize, Hint};
use attn_ui::keycap;
use attn_ui::system::{DotShape, DotSize, IconSize, ListRow, Tone, Type, dot, glyph, tag, words};
use rune::{El, Tx, col, row};
use rune_kit::TextInput;

use super::{Look, Mode, Row, Status};
use crate::{Action, State};

/// The search palette over the window: its field, the rows its query lists with the
/// highlighted one marked, and the keys that work here.
pub(crate) fn view(state: &State, t: &Tokens) -> El {
    let search = &state.window.search;
    let listing = super::listing(state);
    let at = super::highlighted(search, &listing.rows);
    let (label, placeholder) = match &listing.mode {
        Mode::Agents => (
            "Agents".to_owned(),
            "Jump to an agent or tile · type > for commands".to_owned(),
        ),
        Mode::Commands => (
            "Commands".to_owned(),
            "Jump to an agent or tile · type > for commands".to_owned(),
        ),
        Mode::Snooze { name } => (format!("Snooze {name}"), format!("Snooze {name}")),
    };
    let field = finder::field(
        TextInput::new(&search.query)
            .key(super::field_key())
            .plain()
            .width(None)
            .label(label.clone())
            .placeholder(placeholder)
            .active_descendant(at.map(|i| row_key(&listing.rows[i])))
            .on_edit(|state: &mut State| &mut state.window.search.query)
            .on_change(super::edited)
            .on_key(super::on_key),
    )
    .build(attn_ui::kit(t));
    let header = finder::input_row(
        super::badge(state, &listing.mode)
            .map(|b| keycap::Keys::outlined(&[b]).build(t).key("palette.badge")),
        field,
        listing
            .count
            .clone()
            .map(|c| words(c, Type::Mono, t.text_tertiary).key("palette.count")),
    )
    .key("palette.header");
    let empty = listing.rows.is_empty().then(|| {
        let message = match listing.mode {
            Mode::Commands if listing.loading => "Loading command history…",
            Mode::Commands => "No matching commands",
            _ => "No match",
        };
        finder::empty(t, "palette.empty", message)
    });
    let list = finder::list("palette.list")
        .label(label.clone())
        .children(
            listing
                .rows
                .iter()
                .enumerate()
                .map(|(i, r)| option(r, at == Some(i), t)),
        )
        .children(empty);
    let hints: Vec<Hint> = match listing.mode {
        Mode::Agents => {
            let actions = super::row_actions(state, at.map(|i| &listing.rows[i]));
            let mut hints = vec![
                Hint::keys("", "↑↓", " move"),
                Hint::keys("", "↵", " jump"),
                Hint::keys("type ", ">", " for commands"),
            ];
            if actions.settle {
                hints.push(super::action_hint(state, &Action::SessionSettle, "settle"));
            }
            if actions.snooze {
                hints.push(super::action_hint(state, &Action::SessionSnooze, "snooze"));
            }
            hints.push(Hint::keys("", "esc", " closes").at_end());
            hints
        }
        Mode::Commands => vec![
            Hint::keys("", "↑↓", " move"),
            Hint::keys("", "↵", " run"),
            Hint::keys("delete ", ">", " to search agents"),
            Hint::keys("", "esc", " closes").at_end(),
        ],
        Mode::Snooze { .. } => vec![
            Hint::keys("", "↑↓", " move"),
            Hint::keys("", "↵", " snooze"),
            Hint::keys("", "esc", " back to agents").at_end(),
        ],
    };
    let panel = col()
        .key("palette.dialog")
        .role("dialog")
        .label(label)
        .modal_tx(true, super::close)
        .w_full()
        .max_h(FinderSize::Palette.max_height())
        .shrink()
        .min_h(0.0);
    let panel = finder::panel(
        t,
        panel,
        header,
        list,
        Some(finder::footer(t, "palette", hints)),
    );
    finder::layer(t, "palette", panel, FinderSize::Palette)
}

fn option(r: &Row, highlighted: bool, t: &Tokens) -> El {
    let (pointing, pressing) = (r.key.clone(), r.key.clone());
    let face = row_face(r, row_key(r), Place::Palette { highlighted }, t);
    if let Look::Divider = r.look {
        return face;
    }
    face.on_pointer_move(move |state: &mut State, p| super::pointed(state, &pointing, (p.x, p.y)))
        .on_press_tx(move |state: &mut State, tx: &mut Tx| super::pick_key(state, &pressing, tx))
}

fn row_key(r: &Row) -> String {
    format!("palette.row.{}", r.key)
}

/// Where a row is drawn: in the palette, with its crew details and, on the highlighted row,
/// a primary name; or in the queue bar's peek, without them.
#[derive(Clone, Copy)]
pub(crate) enum Place {
    Palette { highlighted: bool },
    Peek,
}

/// The state column: "UNREACHABLE", the longest state, in `Tag` with its insets.
const STATE_W: f32 = 104.0;
/// The age column: "12d", the longest age, in `Mono`.
const AGE_W: f32 = 36.0;
/// The keys column: one outlined key.
const KEYS_W: f32 = 34.0;
/// The kind column of a tile: "NOTE", the longest kind, in `Tag` with its insets.
const KIND_W: f32 = 56.0;

/// A row as the palette and the queue bar's peek draw it, keyed `key`, its trailing columns at
/// fixed widths so they line up row to row.
pub(crate) fn row_face(r: &Row, key: String, place: Place, t: &Tokens) -> El {
    if let Look::Divider = r.look {
        return Stack::tight()
            .key(key)
            .w_full()
            .child(rune::el().w_full().h(1.0).fill(t.border_subtle))
            .a11y_hidden();
    }
    let (highlighted, details) = match place {
        Place::Palette { highlighted } => (highlighted, true),
        Place::Peek => (false, false),
    };
    let column = |width: f32, e: Option<El>| row().w(width).justify_end().children(e);
    let age = |s: String, part: &str| {
        column(
            AGE_W,
            Some(
                words(s, Type::Mono, t.text_tertiary)
                    .key(format!("{key}.{part}"))
                    .single_line(),
            ),
        )
    };
    let chip = |s: String, part: &str| {
        keycap::Keys::outlined(&[s])
            .build(t)
            .key(format!("{key}.{part}"))
    };
    let mut row = ListRow::new(key.clone(), r.label.clone())
        .role("option")
        .unfocusable()
        .cursor(highlighted)
        .selected(highlighted);
    match &r.look {
        Look::Command {
            icon,
            description,
            shortcut,
        } => {
            row = row.title(r.label.clone());
            if let Some(icon) = icon {
                row = row.lead(
                    Stack::tight()
                        .key(format!("{key}.icon"))
                        .role("img")
                        .label(icon.word())
                        .child(glyph(icon.glyph(), IconSize::Small, t.text_tertiary)),
                );
            }
            if let Some(description) = description {
                row = row.tag(
                    words(description.clone(), Type::Small, t.text_tertiary)
                        .single_line()
                        .truncate()
                        .shrink(),
                );
            }
            if let Some(caps) = shortcut {
                row = row.aside(
                    keycap::Keys::raised(caps)
                        .build(t)
                        .key(format!("{key}.shortcut")),
                );
            }
            let built = row.build(t);
            match description {
                Some(d) => built.description(d.clone()),
                None => built,
            }
        }
        Look::Agent {
            agent,
            crew,
            status,
            slot,
            age: owed,
            tag,
        } => {
            let mut title = Inline::tight().child(
                words(agent.name.clone(), Type::Body, t.text_primary)
                    .single_line()
                    .truncate()
                    .shrink(),
            );
            if agent.priority {
                title = Inline::tight()
                    .child(crate::sidebar::priority_flag(&key, t))
                    .child(
                        words(agent.name.clone(), Type::Body, t.text_primary)
                            .single_line()
                            .truncate()
                            .shrink(),
                    );
            }
            row = row.title_el(title).lead(status_dot(&key, *status, t));
            if *crew && details {
                row = row
                    .tag(words("· crew", Type::Body, t.text_tertiary).key(format!("{key}.crew")));
            }
            row.aside(chip(slot.clone(), "slot"))
                .aside(column(
                    STATE_W,
                    Some(state(&key, status.word(), status_tone(*status), t)),
                ))
                .aside(age(owed.clone(), "age"))
                .aside(column(KEYS_W, tag.clone().map(|tag| chip(tag, "tag"))))
                .build(t)
                .a11y_value(status.word())
        }
        Look::Member {
            chief,
            unconfigured,
            desk,
        } => {
            let (word, hint) = if *unconfigured {
                ("not set up", "set up")
            } else {
                ("between sessions", "start")
            };
            let role = match (chief, details, desk) {
                (true, _, _) => Some("· chief".to_owned()),
                (false, false, _) => None,
                (false, true, None) => Some("· crew".to_owned()),
                (false, true, Some(desk)) => Some(format!("· crew · {desk}")),
            };
            row = row.title(r.label.clone()).lead(state_mark(
                &key,
                word,
                t.text_tertiary,
                DotShape::Hollow,
            ));
            if let Some(role) = role {
                row = row.tag(words(role, Type::Body, t.text_tertiary).key(format!("{key}.crew")));
            }
            row.aside(column(STATE_W, Some(state(&key, word, Tone::Neutral, t))))
                .aside(age(hint.into(), "age"))
                .aside(column(KEYS_W, None))
                .build(t)
                .a11y_value(word)
        }
        Look::Tile { icon, word, slot } => row
            .title(r.label.clone())
            .lead(
                Stack::tight()
                    .key(format!("{key}.icon"))
                    .a11y_hidden()
                    .child(glyph(*icon, IconSize::Small, t.text_tertiary)),
            )
            .tag(column(
                KIND_W,
                Some(tag(format!("{key}.kind"), word, Tone::Neutral, t).into()),
            ))
            .aside(chip(slot.clone(), "slot"))
            .build(t),
        Look::Choice { detail } => row
            .title(r.label.clone())
            .aside(age(detail.clone(), "detail"))
            .build(t),
        Look::Divider => unreachable!("a divider returned above"),
    }
}

fn state(row_key: &str, word: &str, tone: Tone, t: &Tokens) -> El {
    tag(format!("{row_key}.status"), word, tone, t).into()
}

/// An agent row's dot: filled warm while the agent waits for the user, green while it works or
/// is the chief, a ring while it sleeps or its outpost is out of reach.
fn status_dot(row_key: &str, status: Status, t: &Tokens) -> El {
    let (ink, shape) = match status {
        Status::Waiting | Status::Approval => (t.warning, DotShape::Filled),
        Status::Working | Status::Chief => (t.success, DotShape::Filled),
        Status::Snoozed => (t.text_tertiary, DotShape::Hollow),
        Status::Unreachable => (t.error, DotShape::Hollow),
        Status::Idle => (t.text_dimmed, DotShape::Filled),
    };
    state_mark(row_key, status.word(), ink, shape)
}

/// A row's state as a dot, keyed `{row_key}.state` and read as `said`.
fn state_mark(row_key: &str, said: &str, ink: rune::Color, shape: DotShape) -> El {
    Stack::tight()
        .key(format!("{row_key}.state"))
        .role("img")
        .label(said.to_owned())
        .a11y_value(said.to_owned())
        .child(dot(ink, shape, DotSize::Regular))
}

/// How a state's tag reads.
fn status_tone(status: Status) -> Tone {
    match status {
        Status::Waiting => Tone::Warn,
        Status::Approval | Status::Unreachable => Tone::Bad,
        Status::Working | Status::Chief => Tone::Good,
        Status::Snoozed | Status::Idle => Tone::Neutral,
    }
}
