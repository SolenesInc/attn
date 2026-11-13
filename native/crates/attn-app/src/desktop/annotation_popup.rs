use attn_ui::Tokens;
use rune::tok::type_;
use rune::{El, Key, KeyEvent, Tx, col, el, row, text, text_input};

use crate::State;
use crate::annotations::{self, QuickLabel, draft};
use crate::terminal::{Marking, Notice, Popup};

/// How far a popup or notice keeps from the terminal's edges, and from the point it opens at.
const MARGIN: f32 = 8.0;
const GAP: f32 = 10.0;
/// A label button's side.
const CHIP: f32 = 28.0;
/// The comment box's width and least height.
const COMPOSE_W: f32 = 320.0;
const COMMENT_H: f32 = 64.0;

/// What terminal `id`'s marks put over its slot: the notice of a drag that marked nothing, and
/// the popup over a mark. `slot` is the slot's size in points.
pub(super) fn layers(id: &str, marking: &Marking, slot: (f32, f32), t: &Tokens) -> Vec<El> {
    let notice = marking.notice.as_ref().map(|n| notice(id, n, slot, t));
    let popup = marking
        .popup
        .as_ref()
        .and_then(|p| Some(popup(id, p, marking.mark(&p.mark)?, slot, t)));
    notice.into_iter().chain(popup).collect()
}

/// Where a box of `size` opens for a point `at` in a slot of `slot`: centred above it, or else
/// below it, or else beside it, kept inside the slot, as the Tauri popup places itself.
pub(crate) fn place(at: (f32, f32), size: (f32, f32), slot: (f32, f32)) -> (f32, f32) {
    let clamp = |v: f32, low: f32, high: f32| v.min(high.max(low)).max(low);
    let left = clamp(at.0 - size.0 / 2.0, MARGIN, slot.0 - size.0 - MARGIN);
    let above = at.1 - size.1 - GAP;
    if above >= MARGIN {
        return (left, above);
    }
    let below = at.1 + GAP;
    if below + size.1 + MARGIN <= slot.1 {
        return (left, below);
    }
    (
        left,
        clamp(at.1 - size.1 / 2.0, MARGIN, slot.1 - size.1 - MARGIN),
    )
}

fn notice(id: &str, notice: &Notice, slot: (f32, f32), t: &Tokens) -> El {
    let width = 300.0_f32.min(slot.0 - 2.0 * MARGIN);
    let left = (notice.at.0 - width / 2.0)
        .min(slot.0 - width - MARGIN)
        .max(MARGIN);
    let top = (notice.at.1 + GAP).min(slot.1 - 60.0).max(MARGIN);
    text(notice.text.clone())
        .key(format!("terminal.{id}.annotation.notice"))
        .role("status")
        .font(type_::LABEL)
        .color(t.text_primary)
        .wrap()
        .w(width)
        .px(10.0)
        .py(6.0)
        .radius(6.0)
        .fill(t.bg_elevated)
        .border(1.0, t.border)
        .absolute(left, top)
}

fn popup(
    id: &str,
    popup: &Popup,
    mark: &crate::terminal::Annotation,
    slot: (f32, f32),
    t: &Tokens,
) -> El {
    let picked = annotations::label_by_id(&mark.label);
    let in_picker = picked.as_ref().filter(|l| {
        annotations::picker_groups()
            .iter()
            .flatten()
            .any(|p| p.id == l.id)
    });
    let mut chips: Vec<El> = annotations::promoted_labels()
        .into_iter()
        .map(|label| {
            let on = mark.label == label.id;
            let picking = label.id;
            chip(
                id,
                &format!("label.{}", label.id),
                label.emoji,
                label.text,
                t,
            )
            .fill(if on {
                t.accent
            } else {
                rune::Color::TRANSPARENT
            })
            .on_press_tx({
                let id = id.to_owned();
                move |state: &mut State, tx: &mut Tx| {
                    draft::pick_label(state, &id, picking, tx);
                }
            })
        })
        .collect();
    chips.push(
        chip(
            id,
            "more",
            in_picker.map_or("⚡", |l| l.emoji),
            "More labels",
            t,
        )
        .fill(if in_picker.is_some() {
            t.accent
        } else {
            rune::Color::TRANSPARENT
        })
        .on_press({
            let id = id.to_owned();
            move |state: &mut State| draft::toggle_picker(state, &id)
        }),
    );
    chips.push(el().w(1.0).h(18.0).mx(4.0).fill(t.border));
    chips.push(chip(id, "write", "💬", "Write a comment", t).on_press_tx({
        let id = id.to_owned();
        move |state: &mut State, tx: &mut Tx| draft::write(state, &id, tx)
    }));
    chips.push(
        chip(id, "remove", "🗑", "Remove this annotation", t)
            .ml(2.0)
            .fill(t.error.alpha(0.16))
            .border(1.0, t.error.alpha(0.4))
            .on_press_tx({
                let (id, mark) = (id.to_owned(), mark.id.clone());
                move |state: &mut State, tx: &mut Tx| draft::remove(state, &id, &mark, tx)
            }),
    );
    let hint = popup
        .hint
        .clone()
        .or_else(|| picked.map(|l| l.text.to_owned()))
        .unwrap_or_else(|| "Pick a label, or write a comment".to_owned());
    let mut parts = vec![
        row()
            .key(format!("terminal.{id}.annotation.labels"))
            .items_center()
            .gap(2.0)
            .children(chips),
        text(hint)
            .key(format!("terminal.{id}.annotation.hint"))
            .font(type_::LABEL)
            .color(t.text_secondary)
            .single_line()
            .truncate()
            .mt(4.0)
            .min_h(15.0),
    ];
    if popup.picking {
        parts.push(picker(id, t));
    }
    if popup.writing {
        parts.push(compose(id, popup, mark, t));
    }
    let size = popup.size.unwrap_or((0.0, 0.0));
    let (left, top) = match popup.size {
        Some(size) => place(popup.at, size, slot),
        None => (popup.at.0 - size.0 / 2.0, popup.at.1 - GAP),
    };
    let measured = id.to_owned();
    col()
        .key(format!("terminal.{id}.annotation.popup"))
        .role("dialog")
        .label("Edit terminal annotation")
        .padding(6.0)
        .radius(8.0)
        .fill(t.bg_elevated)
        .border(1.0, t.border)
        .on_press(|_: &mut State| {})
        .on_layout_tx(move |state: &mut State, [_, _, w, h], tx: &mut Tx| {
            if !draft::measured(state, &measured, (w, h)) {
                tx.live_only();
            }
        })
        .children(parts)
        .absolute(left, top)
}

fn chip(id: &str, part: &str, glyph: &str, label: &str, t: &Tokens) -> El {
    let (hinted, hint) = (id.to_owned(), label.to_owned());
    text(glyph.to_owned())
        .key(format!("terminal.{id}.annotation.{part}"))
        .role("button")
        .label(label)
        .font(type_::BODY)
        .color(t.text_primary)
        .w(CHIP)
        .h(CHIP)
        .align(rune::Align::Center)
        .radius(7.0)
        .on_hover_change(move |state: &mut State, inside, _: &mut Tx| {
            draft::hint(state, &hinted, inside.then_some(hint.as_str()));
        })
}

/// The list of every label the popup does not show, numbered for the digit keys.
fn picker(id: &str, t: &Tokens) -> El {
    let mut index = 0;
    let groups: Vec<El> = annotations::picker_groups()
        .into_iter()
        .map(|group| {
            col().gap(1.0).children(group.into_iter().map(|label| {
                index += 1;
                pick(id, &label, index, t)
            }))
        })
        .collect();
    col()
        .key(format!("terminal.{id}.annotation.picker"))
        .mt(4.0)
        .padding(4.0)
        .gap(4.0)
        .radius(8.0)
        .fill(t.bg_elevated)
        .border(1.0, t.border)
        .children(groups)
}

fn pick(id: &str, label: &QuickLabel, index: usize, t: &Tokens) -> El {
    let (pressed, picked) = (id.to_owned(), label.id);
    let digit = if index == 10 {
        "0".to_owned()
    } else {
        index.to_string()
    };
    row()
        .key(format!("terminal.{id}.annotation.pick.{}", label.id))
        .role("button")
        .label(label.text)
        .items_center()
        .gap(8.0)
        .px(6.0)
        .py(3.0)
        .radius(5.0)
        .child(text(label.emoji.to_owned()).font(type_::BODY))
        .child(
            text(label.text.to_owned())
                .font(type_::LABEL)
                .color(t.text_primary)
                .grow(1.0),
        )
        .children((index <= 10).then(|| text(digit).font(type_::LABEL).color(t.text_tertiary)))
        .on_press_tx(move |state: &mut State, tx: &mut Tx| {
            draft::pick_label(state, &pressed, picked, tx);
        })
}

fn compose(id: &str, popup: &Popup, mark: &crate::terminal::Annotation, t: &Tokens) -> El {
    let (edited, keyed) = (id.to_owned(), id.to_owned());
    let field = text_input(&popup.comment, type_::LABEL)
        .key(draft::comment_field_key(id))
        .label("Annotation comment")
        .placeholder("What should change here?", t.text_muted)
        .on_edit(move |state: &mut State| state.terminals.popup_comment(&edited))
        .on_input_key(move |state: &mut State, ev: &KeyEvent, tx: &mut Tx| {
            on_comment_key(state, &keyed, ev, tx)
        })
        .w_full()
        .min_h(COMMENT_H)
        .px(8.0)
        .py(6.0)
        .radius(6.0)
        .fill(t.bg_input)
        .border(1.0, t.border)
        .color(t.text_primary);
    let button = |part: &str, title: &str| {
        text(title.to_owned())
            .key(format!("terminal.{id}.annotation.{part}"))
            .role("button")
            .font(type_::LABEL)
            .px(10.0)
            .py(4.0)
            .radius(6.0)
    };
    let actions = row()
        .items_center()
        .gap(6.0)
        .child(button("delete", "Remove").color(t.error).on_press_tx({
            let (id, mark) = (id.to_owned(), mark.id.clone());
            move |state: &mut State, tx: &mut Tx| draft::remove(state, &id, &mark, tx)
        }))
        .child(el().grow(1.0))
        .child(
            button("cancel", "Cancel")
                .color(t.text_secondary)
                .fill(t.bg_button)
                .border(1.0, t.border)
                .on_press_tx({
                    let id = id.to_owned();
                    move |state: &mut State, tx: &mut Tx| draft::dismiss(state, &id, tx)
                }),
        )
        .child(
            button("comment", "Comment")
                .color(t.text_secondary)
                .fill(t.bg_button)
                .border(1.0, t.border)
                .on_press_tx({
                    let id = id.to_owned();
                    move |state: &mut State, tx: &mut Tx| draft::save_comment(state, &id, tx)
                }),
        );
    col()
        .w(COMPOSE_W)
        .gap(6.0)
        .mt(4.0)
        .child(
            text(mark.quote.clone())
                .key(format!("terminal.{id}.annotation.quote"))
                .font(type_::LABEL)
                .color(t.text_tertiary)
                .wrap()
                .max_lines(2)
                .pl(8.0)
                .border_sides([0.0, 0.0, 0.0, 2.0], t.border_hover),
        )
        .child(field)
        .child(actions)
}

/// Escape dismisses the popup and ⌘Enter, or Ctrl+Enter, keeps the comment.
fn on_comment_key(state: &mut State, id: &str, ev: &KeyEvent, tx: &mut Tx) -> bool {
    match ev.key {
        Key::Escape => draft::dismiss(state, id, tx),
        Key::Enter if ev.mods.cmd || ev.mods.ctrl => draft::save_comment(state, id, tx),
        _ => return false,
    }
    true
}
