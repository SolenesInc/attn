use attn_ui::Tokens;
use attn_ui::finder::{self, Hint};
use attn_ui::keycap;
use attn_ui::type_;
use rune::tok::space;
use rune::{El, Tx, col, el, row, text};
use rune_kit::ScrollBar;
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
    let field = TextInput::new(&search.query)
        .key(super::field_key())
        .plain()
        .text_style(finder::FIELD_TEXT)
        .height(finder::FIELD_TEXT.line_px())
        .width(None)
        .label(label.clone())
        .placeholder(placeholder)
        .on_edit(|state: &mut State| &mut state.window.search.query)
        .on_change(super::edited)
        .on_key(super::on_key)
        .build(attn_ui::kit(t));
    let header = finder::input_row(
        super::badge(state, &listing.mode)
            .map(|b| keycap::Keys::outlined(&[b]).build(t).key("palette.badge")),
        field,
        listing.count.clone().map(|c| {
            text(c)
                .key("palette.count")
                .font(type_::MONO_CAPTION)
                .color(t.text_tertiary)
        }),
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
        .py(4.0)
        .children(
            listing
                .rows
                .iter()
                .enumerate()
                .map(|(i, r)| option(r, at == Some(i), t)),
        )
        .children(empty)
        .scroll_bar_styled(attn_ui::scroll_bar(t));
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
            hints.push(Hint::keys("", "esc", " closes"));
            hints
        }
        Mode::Commands => vec![
            Hint::keys("", "↑↓", " move"),
            Hint::keys("", "↵", " run"),
            Hint::keys("delete ", ">", " to search agents"),
            Hint::keys("", "esc", " closes"),
        ],
        Mode::Snooze { .. } => vec![
            Hint::keys("", "↑↓", " move"),
            Hint::keys("", "↵", " snooze"),
            Hint::keys("", "esc", " back to agents"),
        ],
    };
    let panel = col()
        .key("palette.dialog")
        .role("dialog")
        .label(label)
        .modal_tx(true, super::close)
        .w_full()
        .max_w(760.0)
        .max_h(620.0)
        .shrink()
        .min_h(0.0);
    let panel = finder::panel(
        t,
        panel,
        header,
        list,
        Some(finder::footer(t, "palette", hints)),
    );
    el().key("palette")
        .absolute(0.0, 0.0)
        .w_full()
        .h_full()
        .child(attn_ui::Backdrop::palette(t).draw("palette.scrim"))
        .child(
            col()
                .w_full()
                .h_full()
                .px(16.0)
                .pt(110.0)
                .pb(30.0)
                .items_center()
                .child(panel),
        )
}

fn option(r: &Row, highlighted: bool, t: &Tokens) -> El {
    let (pointing, pressing) = (r.key.clone(), r.key.clone());
    let face = row_face(
        r,
        format!("palette.row.{}", r.key),
        Place::Palette { highlighted },
        t,
    );
    if let Look::Divider = r.look {
        return face;
    }
    finder::option(t, face, highlighted)
        .py(ROW_PY)
        .on_pointer_move(move |state: &mut State, p| super::pointed(state, &pointing, (p.x, p.y)))
        .on_press_tx(move |state: &mut State, tx: &mut Tx| super::pick_key(state, &pressing, tx))
}

/// Where a row is drawn: in the palette, with its crew details and, on the highlighted row,
/// a primary name; or in the queue bar's peek, without them.
#[derive(Clone, Copy)]
pub(crate) enum Place {
    Palette { highlighted: bool },
    Peek,
}

const ROW_GAP: f32 = 10.0;
const ROW_PX: f32 = 10.0;
const ROW_PY: f32 = 5.0;
const ROW_MIN_H: f32 = 22.0;
const DESCRIPTION_TYPE: rune::TextStyle = rune::TextStyle::new(11.0, 400.0);
const PILL_TYPE: rune::TextStyle = rune::TextStyle::new(10.0, 400.0);
const KIND_TYPE: rune::TextStyle = rune::TextStyle::new(10.0, 600.0);
const PILL_W: f32 = 64.0;
const AGE_MIN_W: f32 = 36.0;
const TAG_W: f32 = 34.0;
const KIND_W: f32 = 42.0;
const QUIET_TINT: f32 = 0.12;

/// A row as the palette and the queue bar's peek draw it, keyed `key`: the Tauri app's
/// `.unified-palette-row` inside its option's padding, its trailing columns at fixed widths so
/// they line up row to row.
pub(crate) fn row_face(r: &Row, key: String, place: Place, t: &Tokens) -> El {
    if let Look::Divider = r.look {
        return el()
            .key(key)
            .w_full()
            .h(1.0)
            .my(space::S1)
            .fill(t.border_subtle)
            .a11y_hidden();
    }
    let (name_color, details) = match place {
        Place::Palette { highlighted } => (finder::title_color(t, highlighted), true),
        Place::Peek => (t.text_secondary, false),
    };
    let muted = |s: String| {
        text(s)
            .font(type_::BODY)
            .color(t.text_tertiary)
            .single_line()
    };
    let age = |s: String| {
        text(s)
            .key(format!("{key}.age"))
            .font(type_::MONO_CAPTION)
            .color(t.text_tertiary)
            .single_line()
            .min_w(AGE_MIN_W)
            .align(rune::Align::End)
    };
    let chip = |s: String, part: &str| {
        keycap::Keys::outlined(&[s])
            .build(t)
            .key(format!("{key}.{part}"))
    };
    let title = |s: String| {
        text(s)
            .font(type_::BODY)
            .color(name_color)
            .single_line()
            .truncate()
    };
    let name = || row().grow(1.0).min_w(0.0).items_center().gap(4.0);
    let option = row()
        .key(key.clone())
        .role("option")
        .label(r.label.clone())
        .w_full()
        .items_center()
        .gap(ROW_GAP)
        .px(ROW_PX)
        .py(ROW_PY)
        .min_h(ROW_MIN_H + 2.0 * ROW_PY);
    match &r.look {
        Look::Command {
            icon,
            description,
            shortcut,
        } => {
            let mut name = name().gap(8.0).child(title(r.label.clone()));
            let mut option = option;
            if let Some(description) = description {
                name = name.child(
                    text(description.clone())
                        .font(DESCRIPTION_TYPE)
                        .color(t.text_tertiary)
                        .single_line()
                        .truncate()
                        .shrink(),
                );
                option = option.description(description.clone());
            }
            option
                .children(icon.map(|icon| {
                    row()
                        .key(format!("{key}.icon"))
                        .role("img")
                        .label(icon.word())
                        .w(16.0)
                        .justify_center()
                        .child(icon.glyph().draw(15.0, type_::BODY, t.text_tertiary))
                }))
                .child(name)
                .children(shortcut.as_ref().map(|caps| {
                    keycap::Keys::raised(caps)
                        .build(t)
                        .key(format!("{key}.shortcut"))
                }))
        }
        Look::Agent {
            agent,
            crew,
            status,
            slot,
            age: owed,
            tag,
        } => {
            let name = name().child(title(agent.shown_name())).children(
                (*crew && details).then(|| muted("· crew".into()).key(format!("{key}.crew"))),
            );
            let (ink, tint) = status_ink(*status, t);
            option
                .a11y_value(status.word())
                .child(status_dot(&key, *status, t))
                .child(name)
                .child(chip(slot.clone(), "slot"))
                .child(pill(&key, status.word(), ink, tint))
                .child(age(owed.clone()))
                .child(tag_column(tag.clone().map(|tag| chip(tag, "tag"))))
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
            option
                .a11y_value(word)
                .child(
                    el().key(format!("{key}.state"))
                        .w(7.0)
                        .h(7.0)
                        .radius(3.5)
                        .border(1.0, t.text_tertiary),
                )
                .child(
                    name()
                        .child(title(r.label.clone()))
                        .children(role.map(|role| muted(role).key(format!("{key}.crew")))),
                )
                .child(pill(&key, word, t.text_tertiary, QUIET_TINT))
                .child(age(hint.into()))
                .child(tag_column(None))
        }
        Look::Tile { icon, word, slot } => option
            .child(
                row()
                    .key(format!("{key}.icon"))
                    .w(14.0)
                    .h(14.0)
                    .center()
                    .a11y_hidden()
                    .child(attn_ui::Glyph::from(*icon).draw(14.0, type_::BODY, t.text_tertiary)),
            )
            .child(
                row()
                    .w(KIND_W)
                    .py(2.0)
                    .radius(4.0)
                    .justify_center()
                    .fill(t.text_tertiary.alpha(QUIET_TINT))
                    .child(
                        text(word.to_string())
                            .key(format!("{key}.kind"))
                            .font(KIND_TYPE)
                            .color(t.text_tertiary),
                    ),
            )
            .child(name().child(title(r.label.clone())))
            .child(chip(slot.clone(), "slot")),
        Look::Choice { detail } => option
            .child(name().child(title(r.label.clone())))
            .child(age(detail.clone()).key(format!("{key}.detail"))),
        Look::Divider => unreachable!("a divider returned above"),
    }
}

/// The last column, which holds the keys that jump to a row, kept as wide on a row without
/// them so the columns before it line up.
fn tag_column(tag: Option<El>) -> El {
    row().w(TAG_W).justify_end().children(tag)
}

/// An agent row's dot: filled warm while the agent waits for the user, green while it works or
/// is the chief, an outline while it sleeps or its outpost is out of reach.
fn status_dot(row_key: &str, status: Status, t: &Tokens) -> El {
    let dot = el()
        .key(format!("{row_key}.state"))
        .role("img")
        .label(status.word())
        .a11y_value(status.word())
        .w(7.0)
        .h(7.0)
        .radius(3.5);
    match status {
        Status::Waiting | Status::Approval => dot.fill(t.warning),
        Status::Working | Status::Chief => dot.fill(t.success),
        Status::Snoozed => dot.border(1.0, t.text_tertiary),
        Status::Unreachable => dot.border(1.0, t.error),
        Status::Idle => dot.fill(t.text_dimmed),
    }
}

/// A status's ink and how strongly its pill is tinted with it.
fn status_ink(status: Status, t: &Tokens) -> (rune::Color, f32) {
    match status {
        Status::Waiting => (t.warning, 0.14),
        Status::Approval | Status::Unreachable => (t.error, 0.14),
        Status::Working => (t.success, 0.14),
        Status::Chief => (t.brand, 0.14),
        Status::Snoozed | Status::Idle => (t.text_tertiary, QUIET_TINT),
    }
}

/// A row's status word on a tint of its ink, as the Tauri app's `.unified-palette-pill`: a
/// fixed column whose words wrap when they do not fit.
fn pill(row_key: &str, word: &str, ink: rune::Color, tint: f32) -> El {
    col()
        .w(PILL_W)
        .py(1.0)
        .radius(4.0)
        .justify_center()
        .fill(ink.alpha(tint))
        .child(
            text(word)
                .key(format!("{row_key}.status"))
                .font(PILL_TYPE)
                .color(ink)
                .w_full()
                .wrap()
                .align(rune::Align::Center),
        )
}
