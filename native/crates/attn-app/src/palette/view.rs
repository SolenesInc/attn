use attn_ui::Tokens;
use rune::tok::{space, type_};
use rune::{El, Tx, col, el, row, text, text_input};

use super::{Look, Mode, Row};
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
    let field = text_input(&search.query, type_::BODY)
        .key(super::field_key())
        .single_line()
        .label(label.clone())
        .placeholder(placeholder, t.text_muted)
        .on_edit(|state: &mut State| &mut state.window.search.query)
        .on_input_change(super::edited)
        .on_input_key(super::on_key)
        .grow(1.0)
        .color(t.text_primary);
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
                .font(type_::LABEL)
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
        .radius(8.0)
        .fill(t.bg_elevated)
        .border(1.0, t.border)
        .padding(space::S3)
        .gap(space::S2)
        .child(header)
        .child(list)
        .child(footer);
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
    let key = format!("palette.row.{}", r.key);
    if let Look::Divider = r.look {
        return el()
            .key(key)
            .w_full()
            .h(1.0)
            .my(space::S1)
            .fill(t.border)
            .a11y_hidden();
    }
    let (pointing, pressing) = (r.key.clone(), r.key.clone());
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
        .focus_selected(highlighted)
        .w_full()
        .items_center()
        .gap(space::S2)
        .px(space::S2)
        .py(space::S1)
        .radius(4.0)
        .fill(if highlighted {
            t.bg_selected
        } else {
            t.bg_elevated
        })
        .on_pointer_move(move |state: &mut State, p| super::pointed(state, &pointing, (p.x, p.y)))
        .on_press_tx(move |state: &mut State, tx: &mut Tx| super::pick_key(state, &pressing, tx));
    match &r.look {
        Look::Command {
            description,
            shortcut,
        } => {
            let mut name = row().gap(space::S2).grow(1.0).child(title);
            if let Some(description) = description {
                name = name.child(muted(description.clone()));
                option = option.description(description.clone());
            }
            option
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
            let shown = if agent.priority {
                format!("⚑{}", agent.name)
            } else {
                agent.name.clone()
            };
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
                .a11y_value(*status)
                .child(crate::sidebar::state_mark(&key, agent, t))
                .child(name)
                .child(caption(slot.clone(), "slot"))
                .child(
                    text(status.to_string())
                        .key(format!("{key}.status"))
                        .font(type_::LABEL)
                        .color(t.text_secondary),
                )
                .child(caption(age.clone(), "age"))
                .children(tag.clone().map(|tag| caption(tag, "tag")))
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
