use attn_ui::Tokens;
use attn_ui::type_;
use rune::tok::space;
use rune::{El, Tx, col, el, row, text};
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
        .text_style(type_::BODY)
        .height(type_::BODY.line_px())
        .width(None)
        .label(label.clone())
        .placeholder(placeholder)
        .on_edit(|state: &mut State| &mut state.window.search.query)
        .on_change(super::edited)
        .on_key(super::on_key)
        .build(attn_ui::kit(t))
        .grow(1.0);
    let header = row()
        .key("palette.header")
        .w_full()
        .items_center()
        .gap(space::S2)
        .children(super::badge(state, &listing.mode).map(|b| {
            text(b)
                .key("palette.badge")
                .font(type_::MONO_CAPTION)
                .color(t.text_tertiary)
        }))
        .child(field)
        .children(listing.count.clone().map(|c| {
            text(c)
                .key("palette.count")
                .font(type_::MONO_CAPTION)
                .color(t.text_tertiary)
        }));
    let empty = listing.rows.is_empty().then(|| {
        let message = match listing.mode {
            Mode::Commands if listing.loading => "Loading command history…",
            Mode::Commands => "No matching commands",
            _ => "No match",
        };
        text(message)
            .key("palette.empty")
            .font(type_::BODY)
            .color(t.text_tertiary)
    });
    let list = col()
        .key("palette.list")
        .role("listbox")
        .label(label.clone())
        .w_full()
        .max_h(620.0)
        .scroll(rune::ScrollSpec {
            stick_bottom: false,
        })
        .gap(2.0)
        .children(
            listing
                .rows
                .iter()
                .enumerate()
                .map(|(i, r)| option(r, at == Some(i), t)),
        )
        .children(empty);
    let hints: Vec<String> = match listing.mode {
        Mode::Agents => {
            let actions = super::row_actions(state, at.map(|i| &listing.rows[i]));
            let mut hints = vec![
                "↑↓ move".into(),
                "↵ jump".into(),
                "type > for commands".into(),
            ];
            if actions.settle {
                hints.push(super::action_hint(state, &Action::SessionSettle, "settle"));
            }
            if actions.snooze {
                hints.push(super::action_hint(state, &Action::SessionSnooze, "snooze"));
            }
            hints.push("esc closes".into());
            hints
        }
        Mode::Commands => [
            "↑↓ move",
            "↵ run",
            "delete > to search agents",
            "esc closes",
        ]
        .map(str::to_owned)
        .to_vec(),
        Mode::Snooze { .. } => ["↑↓ move", "↵ snooze", "esc back to agents"]
            .map(str::to_owned)
            .to_vec(),
    };
    let footer =
        row()
            .key("palette.footer")
            .gap(space::S3)
            .children(hints.into_iter().enumerate().map(|(i, h)| {
                text(h)
                    .key(format!("palette.hint.{i}"))
                    .font(type_::LABEL)
                    .color(t.text_tertiary)
            }));
    let panel = col()
        .key("palette.dialog")
        .role("dialog")
        .label(label)
        .modal_tx(true, super::close)
        .w_full()
        .max_w(760.0)
        .padding(space::S3)
        .gap(space::S2)
        .child(header)
        .child(list)
        .child(footer);
    let panel = rune_kit::popover::raised(attn_ui::kit(t), panel, 8.0);
    el().key("palette")
        .absolute(0.0, 0.0)
        .w_full()
        .h_full()
        .child(
            el().key("palette.scrim")
                .absolute(0.0, 0.0)
                .w_full()
                .h_full()
                .fill(t.bg_overlay)
                .a11y_hidden(),
        )
        .child(
            col()
                .w_full()
                .h_full()
                .px(16.0)
                .pt(110.0)
                .items_center()
                .child(panel),
        )
}

fn option(r: &Row, highlighted: bool, t: &Tokens) -> El {
    let (pointing, pressing) = (r.key.clone(), r.key.clone());
    let face = row_face(r, format!("palette.row.{}", r.key), t);
    if let Look::Divider = r.look {
        return face;
    }
    face.focus_selected(highlighted)
        .fill(if highlighted {
            t.bg_selected
        } else {
            t.bg_elevated
        })
        .on_pointer_move(move |state: &mut State, p| super::pointed(state, &pointing, (p.x, p.y)))
        .on_press_tx(move |state: &mut State, tx: &mut Tx| super::pick_key(state, &pressing, tx))
}

/// A row as the palette and the queue bar's peek draw it, keyed `key`.
pub(crate) fn row_face(r: &Row, key: String, t: &Tokens) -> El {
    if let Look::Divider = r.look {
        return el()
            .key(key)
            .w_full()
            .h(1.0)
            .my(space::S1)
            .fill(t.border)
            .a11y_hidden();
    }
    let muted = |s: String| {
        text(s)
            .font(type_::BODY)
            .color(t.text_tertiary)
            .single_line()
    };
    let caption = |s: String, part: &str| {
        text(s)
            .key(format!("{key}.{part}"))
            .font(type_::MONO_CAPTION)
            .color(t.text_tertiary)
    };
    let title = text(r.label.clone())
        .font(type_::BODY)
        .color(t.text_primary)
        .single_line()
        .truncate();
    let mut option = row()
        .key(key.clone())
        .role("option")
        .label(r.label.clone())
        .w_full()
        .items_center()
        .gap(space::S2)
        .px(space::S2)
        .py(space::S1)
        .radius(4.0);
    match &r.look {
        Look::Command {
            icon,
            description,
            shortcut,
        } => {
            let mut name = row().gap(space::S2).grow(1.0).child(title);
            if let Some(description) = description {
                name = name.child(muted(description.clone()));
                option = option.description(description.clone());
            }
            option
                .children(icon.map(|icon| {
                    text(icon.glyph())
                        .key(format!("{key}.icon"))
                        .role("img")
                        .label(icon.word())
                        .font(type_::BODY)
                        .color(t.text_tertiary)
                        .w(16.0)
                }))
                .child(name)
                .children(shortcut.clone().map(|s| caption(s, "shortcut")))
        }
        Look::Agent {
            agent,
            crew,
            status,
            slot,
            age,
            tag,
        } => {
            let shown = agent.shown_name();
            let name = row()
                .gap(space::S1)
                .grow(1.0)
                .child(
                    text(shown)
                        .font(type_::BODY)
                        .color(t.text_primary)
                        .single_line()
                        .truncate(),
                )
                .children(crew.then(|| muted("· crew".into()).key(format!("{key}.crew"))));
            option
                .a11y_value(status.word())
                .child(status_dot(&key, *status, t))
                .child(name)
                .child(caption(slot.clone(), "slot"))
                .child(status_pill(&key, *status, t))
                .child(caption(age.clone(), "age"))
                .children(tag.clone().map(|tag| caption(tag, "tag")))
        }
        Look::Member {
            chief,
            unconfigured,
        } => {
            let (pill, hint) = if *unconfigured {
                ("not set up", "set up")
            } else {
                ("between sessions", "start")
            };
            let name = row()
                .gap(space::S1)
                .grow(1.0)
                .child(title)
                .child(muted(if *chief { "· chief" } else { "· crew" }.into()));
            option
                .a11y_value(pill)
                .child(
                    el().key(format!("{key}.state"))
                        .w(7.0)
                        .h(7.0)
                        .radius(3.5)
                        .border(1.0, t.text_tertiary),
                )
                .child(name)
                .child(caption(String::new(), "slot"))
                .child(
                    text(pill)
                        .key(format!("{key}.status"))
                        .font(PILL_TYPE)
                        .color(t.text_tertiary),
                )
                .child(caption(hint.into(), "age"))
        }
        Look::Tile { word, slot } => option
            .child(caption(word.to_string(), "kind"))
            .child(row().grow(1.0).child(title))
            .child(caption(slot.clone(), "slot")),
        Look::Choice { detail } => option
            .child(row().grow(1.0).child(title))
            .child(caption(detail.clone(), "detail")),
        Look::Divider => unreachable!("a divider returned above"),
    }
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
        Status::Idle => dot.fill(t.inactive),
    }
}

const PILL_TYPE: rune::TextStyle = rune::TextStyle::new(10.0, 400.0);

/// An agent row's status word on a tint of its own color, as the Tauri app's pill.
fn status_pill(row_key: &str, status: Status, t: &Tokens) -> El {
    let (ink, tint) = match status {
        Status::Waiting => (t.warning, 0.14),
        Status::Approval | Status::Unreachable => (t.error, 0.14),
        Status::Working => (t.success, 0.14),
        Status::Chief => (t.accent, 0.14),
        Status::Snoozed | Status::Idle => (t.text_tertiary, 0.12),
    };
    row()
        .w(64.0)
        .py(1.0)
        .radius(4.0)
        .center()
        .fill(ink.alpha(tint))
        .child(
            text(status.word())
                .key(format!("{row_key}.status"))
                .font(PILL_TYPE)
                .color(ink),
        )
}
