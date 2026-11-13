use attn_ui::{GlyphButton, Tokens};
use rune::tok::type_;
use rune::{Drag, El, Key, KeyEvent, Tx, col, el, row, text, text_input};
use rune_kit::{Button, ButtonVariant};

use crate::State;
use crate::annotations::{self, QuickLabel, draft};
use crate::terminal::{Annotation, Marking, Notice, Outcome, Popup};

/// How far a popup or notice keeps from the terminal's edges, and from the point it opens at.
const MARGIN: f32 = 8.0;
const GAP: f32 = 10.0;
/// A label button's side.
const CHIP: f32 = 28.0;
/// The comment box's width and least height.
const COMPOSE_W: f32 = 320.0;
const COMMENT_H: f32 = 64.0;
/// The panel's width, and how far it sits from the slot's bottom right corner.
const PANEL_W: f32 = 300.0;
const PANEL_INSET: f32 = 20.0;
/// How far an arrow key moves the popup, and with Shift held.
const NUDGE: f32 = 10.0;
const NUDGE_FAR: f32 = 40.0;

/// What terminal `id`'s marks put over its slot: the panel listing them, the notice of a drag
/// that marked nothing, and the popup over a mark. `slot` is the slot's size in points;
/// `send_key` names the send key while this terminal holds the keyboard.
pub(super) fn layers(
    id: &str,
    marking: &Marking,
    slot: (f32, f32),
    send_key: Option<&str>,
    t: &Tokens,
) -> Vec<El> {
    let shows_sent = matches!(marking.outcome, Some(Outcome::Sent { .. }));
    let panel = (!marking.marks.is_empty() || shows_sent).then(|| panel(id, marking, send_key, t));
    let notice = marking.notice.as_ref().map(|n| notice(id, n, slot, t));
    let popup = marking
        .popup
        .as_ref()
        .and_then(|p| Some(popup(id, p, marking.mark(&p.mark)?, slot, t)));
    panel.into_iter().chain(notice).chain(popup).collect()
}

/// Which box over the terminal a drag moves.
#[derive(Clone, Copy)]
enum Moves {
    Panel,
    Popup,
}

/// Where the moved box's top-left is in the slot now, and its size.
fn box_at(state: &State, id: &str, moves: Moves) -> Option<((f32, f32), (f32, f32))> {
    let marking = state.terminals.marking(id)?;
    let slot = state.terminals.slot_size(id)?;
    match moves {
        Moves::Panel => {
            let size = marking.panel_size?;
            let at = marking
                .panel_at
                .unwrap_or((slot.0 - PANEL_INSET - size.0, slot.1 - PANEL_INSET - size.1));
            Some((at, size))
        }
        Moves::Popup => {
            let popup = marking.popup.as_ref()?;
            let size = popup.size?;
            Some((popup_at(popup, size, slot), size))
        }
    }
}

/// `at` for a box of `size`, kept `MARGIN` inside the slot when it fits there.
fn clamped(at: (f32, f32), size: (f32, f32), slot: (f32, f32)) -> (f32, f32) {
    let clamp = |v: f32, low: f32, high: f32| v.min(high.max(low)).max(low);
    (
        clamp(at.0, MARGIN, slot.0 - size.0 - MARGIN),
        clamp(at.1, MARGIN, slot.1 - size.1 - MARGIN),
    )
}

fn set_box(state: &mut State, id: &str, moves: Moves, at: (f32, f32)) {
    let Some(((_, size), slot)) = box_at(state, id, moves).zip(state.terminals.slot_size(id))
    else {
        return;
    };
    let at = clamped(at, size, slot);
    let Some(marking) = state.terminals.marking_mut(id) else {
        return;
    };
    match moves {
        Moves::Panel => marking.panel_at = Some(at),
        Moves::Popup => {
            if let Some(popup) = marking.popup.as_mut() {
                popup.moved = Some(at);
            }
        }
    }
}

/// A drag on the panel's head or the popup's handle moves that box, inside the slot.
fn drag_box(state: &mut State, id: &str, moves: Moves, d: &Drag, tx: &mut Tx) {
    let pointer = d.window_point();
    match d.phase {
        rune::Phase::Down => {
            let start = box_at(state, id, moves).map(|(at, _)| at);
            if let Some(marking) = state.terminals.marking_mut(id) {
                marking.grab = start.map(|start| (start, pointer));
            }
        }
        rune::Phase::Move => {
            let Some((start, from)) = state.terminals.marking(id).and_then(|m| m.grab) else {
                return;
            };
            let at = (
                start.0 + pointer[0] - from[0],
                start.1 + pointer[1] - from[1],
            );
            set_box(state, id, moves, at);
        }
        rune::Phase::Up => {
            if let Some(marking) = state.terminals.marking_mut(id) {
                marking.grab = None;
            }
            tx.live_only();
        }
    }
}

/// The arrow keys move the popup by its handle, farther with Shift held.
fn nudge_popup(state: &mut State, id: &str, ev: &KeyEvent) -> bool {
    let far = if ev.mods.shift { NUDGE_FAR } else { NUDGE };
    let (dx, dy) = match ev.key {
        Key::Left => (-far, 0.0),
        Key::Right => (far, 0.0),
        Key::Up => (0.0, -far),
        Key::Down => (0.0, far),
        _ => return false,
    };
    let Some((at, _)) = box_at(state, id, Moves::Popup) else {
        return false;
    };
    set_box(state, id, Moves::Popup, (at.0 + dx, at.1 + dy));
    true
}

fn popup_at(popup: &Popup, size: (f32, f32), slot: (f32, f32)) -> (f32, f32) {
    match popup.moved {
        Some(at) => clamped(at, size, slot),
        None => place(popup.at, size, slot),
    }
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
            let pressed = id.to_owned();
            chip(
                id,
                &format!("label.{}", label.id),
                label.emoji,
                label.text,
                t,
                move |state, tx| draft::pick_label(state, &pressed, picking, tx),
            )
            .fill(if on {
                t.accent
            } else {
                rune::Color::TRANSPARENT
            })
        })
        .collect();
    let toggled = id.to_owned();
    chips.push(
        chip(
            id,
            "more",
            in_picker.map_or("⚡", |l| l.emoji),
            "More labels",
            t,
            move |state, _| draft::toggle_picker(state, &toggled),
        )
        .fill(if in_picker.is_some() {
            t.accent
        } else {
            rune::Color::TRANSPARENT
        }),
    );
    chips.push(el().w(1.0).h(18.0).mx(4.0).fill(t.border));
    let written = id.to_owned();
    chips.push(chip(
        id,
        "write",
        attn_ui::icon::COMMENT,
        "Write a comment",
        t,
        move |state, tx| draft::write(state, &written, tx),
    ));
    let (removed, gone) = (id.to_owned(), mark.id.clone());
    chips.push(
        chip(
            id,
            "remove",
            attn_ui::icon::TRASH,
            "Remove this annotation",
            t,
            move |state, tx| draft::remove(state, &removed, &gone, tx),
        )
        .ml(2.0)
        .fill(t.error.alpha(0.16))
        .border(1.0, t.error.alpha(0.4)),
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
    if popup.writing {
        let (dragged, keyed) = (id.to_owned(), id.to_owned());
        parts.insert(
            0,
            text(attn_ui::icon::GRABBER)
                .key(format!("terminal.{id}.annotation.handle"))
                .role("button")
                .label("Move comment editor with arrow keys")
                .font(type_::LABEL)
                .color(t.text_tertiary)
                .align(rune::Align::Center)
                .w_full()
                .h(18.0)
                .focusable(true)
                .cursor(rune::Cursor::Grab)
                .on_drag(move |state: &mut State, d: &mut Drag, tx: &mut Tx| {
                    drag_box(state, &dragged, Moves::Popup, d, tx);
                })
                .on_key(move |state: &mut State, ev: &KeyEvent, _: &mut Tx| {
                    nudge_popup(state, &keyed, ev)
                }),
        );
    }
    let (left, top) = match popup.size {
        Some(size) => popup_at(popup, size, slot),
        None => (popup.at.0, popup.at.1 - GAP),
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

fn chip(
    id: &str,
    part: &str,
    glyph: &str,
    label: &str,
    t: &Tokens,
    press: impl Fn(&mut State, &mut Tx) + 'static,
) -> El {
    let (hinted, hint) = (id.to_owned(), label.to_owned());
    GlyphButton::new(glyph, label)
        .variant(ButtonVariant::Ghost)
        .font(type_::BODY)
        .side(CHIP)
        .on_press_tx(press)
        .build(attn_ui::kit(t))
        .key(format!("terminal.{id}.annotation.{part}"))
        .w(CHIP)
        .radius(7.0)
        .on_hover_change(move |state: &mut State, inside, _: &mut Tx| {
            draft::hint(state, &hinted, inside.then_some(hint.as_str()));
        })
}

/// The list of every label the popup does not show, numbered for the digit keys.
fn picker(id: &str, t: &Tokens) -> El {
    let id = id.to_owned();
    label_list(
        &format!("terminal.{id}.annotation"),
        move |state: &mut State, label, tx: &mut Tx| {
            draft::pick_label(state, &id, label, tx);
        },
        t,
    )
}

/// The labels a mark's quick row does not show, in their groups and numbered for the digit
/// keys, as `{key}.picker` with each label `{key}.pick.{id}`; `on_pick` hears the one pressed.
pub(crate) fn label_list(
    key: &str,
    on_pick: impl Fn(&mut State, &'static str, &mut Tx) + Clone + 'static,
    t: &Tokens,
) -> El {
    let mut index = 0;
    let groups: Vec<El> = annotations::picker_groups()
        .into_iter()
        .map(|group| {
            col().gap(1.0).children(group.into_iter().map(|label| {
                index += 1;
                pick(key, &label, index, on_pick.clone(), t)
            }))
        })
        .collect();
    col()
        .key(format!("{key}.picker"))
        .mt(4.0)
        .padding(4.0)
        .gap(4.0)
        .radius(8.0)
        .fill(t.bg_elevated)
        .border(1.0, t.border)
        .children(groups)
}

fn pick(
    key: &str,
    label: &QuickLabel,
    index: usize,
    on_pick: impl Fn(&mut State, &'static str, &mut Tx) + 'static,
    t: &Tokens,
) -> El {
    let picked = label.id;
    let digit = if index == 10 {
        "0".to_owned()
    } else {
        index.to_string()
    };
    row()
        .key(format!("{key}.pick.{}", label.id))
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
        .on_press_tx(move |state: &mut State, tx: &mut Tx| on_pick(state, picked, tx))
}

fn compose(id: &str, popup: &Popup, mark: &crate::terminal::Annotation, t: &Tokens) -> El {
    let (edited, changed, keyed) = (id.to_owned(), id.to_owned(), id.to_owned());
    let field = text_input(&popup.comment, type_::LABEL)
        .key(draft::comment_field_key(id))
        .label("Annotation comment")
        .placeholder("What should change here?", t.text_muted)
        .on_edit(move |state: &mut State| state.terminals.popup_comment(&edited))
        .on_input_change(move |state: &mut State, tx: &mut Tx| {
            draft::comment_edited(state, &changed, tx);
        })
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
    let (removed, gone, dismissed, saved) =
        (id.to_owned(), mark.id.clone(), id.to_owned(), id.to_owned());
    let actions = row()
        .items_center()
        .gap(6.0)
        .child(compose_button(
            id,
            "delete",
            "Remove",
            t.error,
            t,
            move |state, tx| draft::remove(state, &removed, &gone, tx),
        ))
        .child(el().grow(1.0))
        .child(
            compose_button(
                id,
                "cancel",
                "Cancel",
                t.text_secondary,
                t,
                move |state, tx| draft::dismiss(state, &dismissed, tx),
            )
            .fill(t.bg_button)
            .border(1.0, t.border),
        )
        .child(
            compose_button(
                id,
                "comment",
                "Comment",
                t.text_secondary,
                t,
                move |state, tx| draft::save_comment(state, &saved, tx),
            )
            .fill(t.bg_button)
            .border(1.0, t.border),
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

fn compose_button(
    id: &str,
    part: &str,
    title: &str,
    ink: rune::Color,
    t: &Tokens,
    press: impl Fn(&mut State, &mut Tx) + 'static,
) -> El {
    let face = title.to_owned();
    Button::new(title)
        .variant(ButtonVariant::Ghost)
        .on_press_tx(press)
        .label_with(move |_, _| text(face).font(type_::LABEL).color(ink))
        .build(attn_ui::kit(t))
        .key(format!("terminal.{id}.annotation.{part}"))
        .h(type_::LABEL.line_px() + 8.0)
        .px(10.0)
        .radius(6.0)
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

/// The panel over the terminal's bottom right corner: each mark as a card that opens it, the
/// note sent with them, and Send all.
fn panel(id: &str, marking: &Marking, send_key: Option<&str>, t: &Tokens) -> El {
    let count = marking.marks.len();
    let (dragged, measured) = (id.to_owned(), id.to_owned());
    let head = row()
        .key(format!("terminal.{id}.annotation.panel.head"))
        .items_center()
        .gap(8.0)
        .px(10.0)
        .py(8.0)
        .cursor(rune::Cursor::Grab)
        .on_drag(move |state: &mut State, d: &mut Drag, tx: &mut Tx| {
            drag_box(state, &dragged, Moves::Panel, d, tx);
        })
        .child(
            text(attn_ui::icon::GRABBER)
                .font(type_::LABEL)
                .color(t.text_tertiary),
        )
        .child(
            text("Annotations")
                .font(type_::TITLE)
                .color(t.text_primary)
                .grow(1.0),
        )
        .child(
            text(count.to_string())
                .font(type_::LABEL)
                .color(t.text_secondary),
        );
    let cards = col()
        .key(format!("terminal.{id}.annotation.cards"))
        .gap(4.0)
        .px(8.0)
        .max_h(240.0)
        .scroll(rune::ScrollSpec {
            stick_bottom: false,
        })
        .children(marking.marks.iter().map(|m| card(id, m, t)));
    let (edited, changed, keyed) = (id.to_owned(), id.to_owned(), id.to_owned());
    let note = text_input(&marking.note, type_::LABEL)
        .key(format!(
            "terminal.{id}.annotation.note{}",
            crate::keys::FIELD_KEY_SUFFIX
        ))
        .label("Note sent with these annotations")
        .placeholder("Anything else to say with these?", t.text_muted)
        .on_edit(move |state: &mut State| state.terminals.note(&edited))
        .on_input_change(move |state: &mut State, tx: &mut Tx| {
            draft::edited(state, &changed, tx);
        })
        .on_input_key(move |state: &mut State, ev: &KeyEvent, tx: &mut Tx| {
            if ev.key == Key::Enter && (ev.mods.cmd || ev.mods.ctrl) {
                draft::send(state, &keyed, tx);
                return true;
            }
            false
        })
        .mx(8.0)
        .mt(6.0)
        .min_h(44.0)
        .px(8.0)
        .py(6.0)
        .radius(6.0)
        .fill(t.bg_input)
        .border(1.0, t.border)
        .color(t.text_primary);
    let said = match &marking.outcome {
        Some(Outcome::Skipped) => Some("Not sent — the session is waiting on an approval, where the sending Enter would answer it. Send again once you have answered.".to_owned()),
        Some(Outcome::Failed(why)) => Some(why.clone()),
        Some(Outcome::Sent { count, kept, .. }) if *kept > 0 => Some(format!(
            "✓ sent {count} to the session. {kept} still here — annotated or changed while it was sending, so not part of what went."
        )),
        _ => None,
    };
    let said = said.map(|said| {
        text(said)
            .key(format!("terminal.{id}.annotation.outcome"))
            .role("status")
            .font(type_::LABEL)
            .color(t.text_secondary)
            .wrap()
            .px(10.0)
            .pt(6.0)
    });
    let foot = match marking.outcome {
        Some(Outcome::Sent { count, kept: 0, .. }) => row().child(
            text(format!("✓ sent {count} to the session"))
                .key(format!("terminal.{id}.annotation.sent"))
                .font(type_::LABEL)
                .color(t.success),
        ),
        _ => {
            let sending = marking.outcome == Some(Outcome::Sending);
            let plural = if count == 1 { "" } else { "s" };
            let pressed = id.to_owned();
            let (secondary, tertiary) = (t.text_secondary, t.text_tertiary);
            let send_key = send_key.filter(|_| !sending).map(str::to_owned);
            let button = Button::new("Send all").variant(ButtonVariant::GhostMuted);
            let button = if sending {
                button
            } else {
                button.on_press_tx(move |state: &mut State, tx: &mut Tx| {
                    draft::send(state, &pressed, tx);
                })
            };
            let button = button
                .label_with(move |_, _| {
                    row()
                        .items_center()
                        .gap(6.0)
                        .child(
                            text(if sending { "Sending…" } else { "Send all" })
                                .font(type_::LABEL)
                                .color(secondary),
                        )
                        .children(send_key.map(|key| text(key).font(type_::LABEL).color(tertiary)))
                })
                .build(attn_ui::kit(t))
                .key(format!("terminal.{id}.annotation.send"))
                .h(type_::LABEL.line_px() + 8.0)
                .px(10.0)
                .radius(6.0)
                .fill(t.bg_button)
                .border(1.0, t.border);
            row()
                .items_center()
                .child(
                    text(format!("{count} annotation{plural}"))
                        .font(type_::LABEL)
                        .color(t.text_secondary)
                        .grow(1.0),
                )
                .child(button)
        }
    };
    let placed = col()
        .key(format!("terminal.{id}.annotation.panel"))
        .role("group")
        .label("Annotations")
        .w(PANEL_W)
        .radius(10.0)
        .fill(t.bg_panel)
        .border(1.0, t.border)
        .on_press(|_: &mut State| {})
        .on_layout_tx(move |state: &mut State, [_, _, w, h], tx: &mut Tx| {
            if !draft::panel_measured(state, &measured, (w, h)) {
                tx.live_only();
            }
        })
        .child(head)
        .child(cards)
        .child(note)
        .children(said)
        .child(foot.px(10.0).py(8.0));
    match marking.panel_at {
        Some((left, top)) => placed.absolute(left, top),
        None => placed.pin(None, None, Some(PANEL_INSET), Some(PANEL_INSET)),
    }
}

fn card(id: &str, mark: &Annotation, t: &Tokens) -> El {
    let emoji = annotations::label_by_id(&mark.label).map_or(attn_ui::icon::COMMENT, |l| l.emoji);
    let (opened, removed, mark_id, gone) = (
        id.to_owned(),
        id.to_owned(),
        mark.id.clone(),
        mark.id.clone(),
    );
    let open = row()
        .key(format!("terminal.{id}.annotation.card.{}", mark.id))
        .role("button")
        .label("Edit this annotation")
        .gap(6.0)
        .grow(1.0)
        .min_w(0.0)
        .child(text(emoji.to_owned()).font(type_::LABEL))
        .child(
            col()
                .grow(1.0)
                .min_w(0.0)
                .child(
                    text(mark.quote.clone())
                        .font(type_::LABEL)
                        .color(t.text_secondary)
                        .wrap()
                        .max_lines(2),
                )
                .children((!mark.comment.is_empty()).then(|| {
                    text(mark.comment.clone())
                        .font(type_::LABEL)
                        .color(t.text_primary)
                        .wrap()
                        .max_lines(2)
                })),
        )
        .on_drag(move |state: &mut State, d: &mut Drag, tx: &mut Tx| {
            if d.phase == rune::Phase::Up && !d.cancelled() {
                let at = card_point(state, &opened, d);
                draft::open_card(state, &opened, &mark_id, at, tx);
            }
        });
    let remove = GlyphButton::new(attn_ui::icon::CLOSE, "Remove annotation")
        .variant(ButtonVariant::GhostFaint)
        .font(type_::LABEL)
        .side(type_::LABEL.line_px())
        .on_press_tx(move |state: &mut State, tx: &mut Tx| {
            draft::remove_card(state, &removed, &gone, tx);
        })
        .build(attn_ui::kit(t))
        .key(format!("terminal.{id}.annotation.card.{}.remove", mark.id))
        .px(4.0);
    row()
        .items_start()
        .gap(4.0)
        .px(6.0)
        .py(5.0)
        .radius(6.0)
        .fill(t.bg_elevated)
        .child(open)
        .child(remove)
}

/// Where a card press opens its popup in the slot: over the panel's left edge, where the card
/// is.
fn card_point(state: &State, id: &str, d: &Drag) -> (f32, f32) {
    let panel = box_at(state, id, Moves::Panel).map(|(at, _)| at);
    match panel {
        Some((left, top)) => (left + PANEL_W / 2.0, top + d.y.max(0.0)),
        None => (d.x, d.y),
    }
}
