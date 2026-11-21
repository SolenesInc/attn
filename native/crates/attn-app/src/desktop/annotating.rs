use attn_ui::Tokens;
use attn_ui::blocks::{Block, Card, FloatingPanel, Inline, Stack, Status, Toolbar, rail};
use attn_ui::system::{
    Button as SystemButton, FieldState, IconButton, IconSize, ListRow, Look, Size, Type, area_box,
    glyph, icon_button, words,
};
use rune::{Drag, El, Key, KeyEvent, Tx, col, el};
use rune_kit::{ScrollBar, TextArea};

use crate::State;
use crate::annotations::{self, QuickLabel, draft};
use crate::terminal::{Annotation, Marking, Notice, Outcome, Popup};

/// How far a popup or notice keeps from the terminal's edges, and from the point it opens at.
const MARGIN: f32 = 8.0;
const GAP: f32 = 10.0;
/// The comment box's width.
const COMPOSE_W: f32 = 320.0;
/// The tallest the cards stand before they scroll.
const CARDS_H: f32 = 240.0;
/// The widest a drag's notice stands.
const NOTICE_W: f32 = 300.0;
/// The panel's width, and how far it sits from the slot's bottom right corner.
const PANEL_W: f32 = 300.0;
const PANEL_INSET: f32 = 20.0;
/// How far an arrow key moves the popup, and with Shift held.
const NUDGE: f32 = 10.0;
const NUDGE_FAR: f32 = 40.0;

/// What terminal `id`'s marks put over its slot: the panel listing them, the notice of a drag
/// that marked nothing, and the popup over a mark. `slot` is the slot's size in points and
/// `room` where in it the popup may open; `send_key` names the send key while this terminal
/// holds the keyboard; `unreachable` names the session's machine while the marks cannot reach
/// it.
pub(super) fn layers(
    id: &str,
    marking: &Marking,
    (slot, room): ((f32, f32), Room),
    (send_key, unreachable): (Option<&str>, Option<&str>),
    t: &Tokens,
) -> Vec<El> {
    let shows_sent = matches!(marking.outcome, Some(Outcome::Sent { .. }));
    let panel = (!marking.marks.is_empty() || shows_sent)
        .then(|| panel(id, marking, slot, send_key, unreachable, t));
    let notice = marking.notice.as_ref().map(|n| notice(id, n, slot, t));
    let popup = marking
        .popup
        .as_ref()
        .and_then(|p| Some(popup(id, p, marking.mark(&p.mark)?, (slot, room), t)));
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
            let room = Room::of(&state.terminals, id);
            Some((popup_at(popup, size, slot, room), size))
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

/// Where over a terminal a popup may open: anywhere, or below the find or filter bar standing
/// over the terminal's top, which reaches this far down.
#[derive(Clone, Copy, PartialEq)]
pub(super) enum Room {
    Anywhere,
    Under(f32),
}

impl Room {
    pub(super) fn of(terminals: &crate::terminal::Terminals, id: &str) -> Room {
        match terminals.bars_height(id) {
            Some(height) => Room::Under(attn_ui::blocks::TerminalBar::reach(height)),
            None => Room::Anywhere,
        }
    }

    fn top(self) -> f32 {
        match self {
            Room::Anywhere => MARGIN,
            Room::Under(reach) => reach + MARGIN,
        }
    }
}

fn popup_at(popup: &Popup, size: (f32, f32), slot: (f32, f32), room: Room) -> (f32, f32) {
    match popup.moved {
        Some(at) => clamped(at, size, slot),
        None => place_in(popup.at, size, slot, room),
    }
}

/// Where a box of `size` opens for a point `at` in a slot of `slot`: centred above it, or else
/// below it, or else beside it, kept inside the slot, as the Tauri popup places itself.
pub(crate) fn place(at: (f32, f32), size: (f32, f32), slot: (f32, f32)) -> (f32, f32) {
    place_in(at, size, slot, Room::Anywhere)
}

fn place_in(at: (f32, f32), size: (f32, f32), slot: (f32, f32), room: Room) -> (f32, f32) {
    let clamp = |v: f32, low: f32, high: f32| v.min(high.max(low)).max(low);
    let left = clamp(at.0 - size.0 / 2.0, MARGIN, slot.0 - size.0 - MARGIN);
    let top = room.top();
    let above = at.1 - size.1 - GAP;
    if above >= top {
        return (left, above);
    }
    let below = (at.1 + GAP).max(top);
    if below + size.1 + MARGIN <= slot.1 {
        return (left, below);
    }
    (
        left,
        clamp(at.1 - size.1 / 2.0, top, slot.1 - size.1 - MARGIN),
    )
}

fn notice(id: &str, notice: &Notice, slot: (f32, f32), t: &Tokens) -> El {
    let width = NOTICE_W.min(slot.0 - 2.0 * MARGIN);
    let left = (notice.at.0 - width / 2.0)
        .min(slot.0 - width - MARGIN)
        .max(MARGIN);
    let top = (notice.at.1 + GAP).min(slot.1 - 60.0).max(MARGIN);
    attn_ui::blocks::tip(
        format!("terminal.{id}.annotation.notice"),
        notice.text.clone(),
        width,
        t,
    )
    .absolute(left, top)
}

fn popup(
    id: &str,
    popup: &Popup,
    mark: &crate::terminal::Annotation,
    (slot, room): ((f32, f32), Room),
    t: &Tokens,
) -> El {
    let picked = annotations::label_by_id(&mark.label);
    let in_picker = picked.as_ref().filter(|l| {
        annotations::picker_groups()
            .iter()
            .flatten()
            .any(|p| p.id == l.id)
    });
    let mut labels: Vec<El> = annotations::promoted_labels()
        .into_iter()
        .map(|label| {
            let (pressed, picking) = (id.to_owned(), label.id);
            chip(
                id,
                &format!("label.{}", label.id),
                label.emoji,
                label.text,
                t,
                |chip| {
                    chip.selected(mark.label == label.id).on_press_tx(
                        move |state: &mut State, tx: &mut Tx| {
                            draft::pick_label(state, &pressed, picking, tx)
                        },
                    )
                },
            )
        })
        .collect();
    let toggled = id.to_owned();
    labels.push(chip(
        id,
        "more",
        in_picker.map_or("⚡", |l| l.emoji),
        "More labels",
        t,
        |chip| {
            chip.expanded(popup.picking)
                .selected(in_picker.is_some() || popup.picking)
                .on_press_tx(move |state: &mut State, _: &mut Tx| {
                    draft::toggle_picker(state, &toggled)
                })
        },
    ));
    let written = id.to_owned();
    let write = chip(
        id,
        "write",
        attn_ui::glyph::COMMENT,
        "Write a comment",
        t,
        |chip| {
            chip.on_press_tx(move |state: &mut State, tx: &mut Tx| {
                draft::write(state, &written, tx)
            })
        },
    );
    let (removed, gone) = (id.to_owned(), mark.id.clone());
    let remove = chip(
        id,
        "remove",
        &attn_ui::icon::TRASH,
        "Remove this annotation",
        t,
        |chip| {
            chip.ink(t.error)
                .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                    draft::remove(state, &removed, &gone, tx)
                })
        },
    );
    let hint = popup
        .hint
        .clone()
        .or_else(|| picked.map(|l| l.text.to_owned()))
        .unwrap_or_else(|| "Pick a label, or write a comment".to_owned());
    let under = Stack::tight()
        .w_full()
        .child(
            words(hint, Type::Small, t.text_secondary)
                .key(format!("terminal.{id}.annotation.hint"))
                .single_line()
                .truncate(),
        )
        .children(popup.picking.then(|| picker(id, t)))
        .children(popup.writing.then(|| compose(id, popup, mark, t)));
    let handle = popup.writing.then(|| {
        let (dragged, keyed) = (id.to_owned(), id.to_owned());
        words(attn_ui::glyph::GRABBER, Type::Small, t.text_tertiary)
            .key(format!("terminal.{id}.annotation.handle"))
            .role("button")
            .label("Move comment editor with arrow keys")
            .align(rune::Align::Center)
            .w_full()
            .focusable(true)
            .cursor(rune::Cursor::Grab)
            .on_drag(move |state: &mut State, d: &mut Drag, tx: &mut Tx| {
                drag_box(state, &dragged, Moves::Popup, d, tx);
            })
            .on_key(move |state: &mut State, ev: &KeyEvent, _: &mut Tx| {
                nudge_popup(state, &keyed, ev)
            })
    });
    let toolbar = Toolbar::new(
        format!("terminal.{id}.annotation.labels"),
        "Label this annotation",
    )
    .head(handle)
    .group(labels)
    .group([write, remove])
    .under(Some(under))
    .max_height(slot.1 - room.top() - MARGIN)
    .build(t);
    let (left, top) = match popup.size {
        Some(size) => popup_at(popup, size, slot, room),
        None => (popup.at.0, popup.at.1 - GAP),
    };
    let measured = id.to_owned();
    col()
        .key(format!("terminal.{id}.annotation.popup"))
        .role("dialog")
        .label("Edit terminal annotation")
        .on_press(|_: &mut State| {})
        .on_layout_tx(move |state: &mut State, [_, _, w, h], tx: &mut Tx| {
            if !draft::measured(state, &measured, (w, h)) {
                tx.live_only();
            }
        })
        .child(toolbar)
        .absolute(left, top)
}

/// One of the popup's buttons, keyed `terminal.{id}.annotation.{part}`, which says what it does
/// in the popup's hint while the pointer rests on it.
fn chip(
    id: &str,
    part: &str,
    glyph: impl Into<attn_ui::Glyph>,
    label: &str,
    t: &Tokens,
    then: impl FnOnce(IconButton) -> IconButton,
) -> El {
    let (hinted, hint) = (id.to_owned(), label.to_owned());
    let button = icon_button(
        format!("terminal.{id}.annotation.{part}"),
        label,
        glyph,
        IconSize::Regular,
    )
    .size(Size::Regular);
    El::from(then(button).build(t)).on_hover_change(move |state: &mut State, inside, _: &mut Tx| {
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
            ListRow::column().children(group.into_iter().map(|label| {
                index += 1;
                pick(key, &label, index, on_pick.clone(), t)
            }))
        })
        .collect();
    attn_ui::Frame::menu(t).draw(
        Stack::tight()
            .key(format!("{key}.picker"))
            .w_full()
            .children(groups),
    )
}

fn pick(
    key: &str,
    label: &QuickLabel,
    index: usize,
    on_pick: impl Fn(&mut State, &'static str, &mut Tx) + 'static,
    t: &Tokens,
) -> El {
    let picked = label.id;
    let digit = (index <= 10).then(|| {
        if index == 10 {
            "0".to_owned()
        } else {
            index.to_string()
        }
    });
    ListRow::new(format!("{key}.pick.{}", label.id), label.text)
        .lead(glyph(label.emoji, IconSize::Regular, t.text_primary))
        .title(label.text)
        .keys(digit, t)
        .on_press_tx(move |state: &mut State, tx: &mut Tx| on_pick(state, picked, tx))
        .build(t)
}

fn compose(id: &str, popup: &Popup, mark: &crate::terminal::Annotation, t: &Tokens) -> El {
    let (edited, changed, keyed) = (id.to_owned(), id.to_owned(), id.to_owned());
    let field = area_box(
        TextArea::new(&popup.comment)
            .key(draft::comment_field_key(id))
            .width(None)
            .resizable()
            .label("Annotation comment")
            .placeholder("What should change here?")
            .on_edit(move |state: &mut State| state.terminals.popup_comment(&edited))
            .on_change(move |state: &mut State, tx: &mut Tx| {
                draft::comment_edited(state, &changed, tx);
            })
            .on_key(move |state: &mut State, ev: &KeyEvent, tx: &mut Tx| {
                on_comment_key(state, &keyed, ev, tx)
            }),
        FieldState::Ready,
        t,
    );
    let (removed, gone, dismissed, saved) =
        (id.to_owned(), mark.id.clone(), id.to_owned(), id.to_owned());
    let actions = Inline::joined()
        .w_full()
        .child(
            SystemButton::new("Remove")
                .look(Look::Danger)
                .size(Size::Compact)
                .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                    draft::remove(state, &removed, &gone, tx)
                })
                .build(t)
                .key(format!("terminal.{id}.annotation.delete"))
                .into(),
        )
        .child(el().grow(1.0))
        .child(
            SystemButton::new("Cancel")
                .look(Look::Plain)
                .size(Size::Compact)
                .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                    draft::dismiss(state, &dismissed, tx)
                })
                .build(t)
                .key(format!("terminal.{id}.annotation.cancel"))
                .into(),
        )
        .child(
            SystemButton::new("Comment")
                .look(Look::Plain)
                .size(Size::Compact)
                .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                    draft::save_comment(state, &saved, tx)
                })
                .build(t)
                .key(format!("terminal.{id}.annotation.comment"))
                .into(),
        );
    Stack::tight()
        .w(COMPOSE_W)
        .child(rail(
            words(mark.quote.clone(), Type::Small, t.text_tertiary)
                .key(format!("terminal.{id}.annotation.quote"))
                .wrap()
                .max_lines(2),
            false,
            t,
        ))
        .child(field.into())
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

/// The panel over the terminal's bottom right corner: each mark as a card that opens it, the
/// note sent with them, and Send all while the session's machine can take them.
fn panel(
    id: &str,
    marking: &Marking,
    slot: (f32, f32),
    send_key: Option<&str>,
    unreachable: Option<&str>,
    t: &Tokens,
) -> El {
    let count = marking.marks.len();
    let (dragged, measured) = (id.to_owned(), id.to_owned());
    let cards = Stack::tight()
        .key(format!("terminal.{id}.annotation.cards"))
        .w_full()
        .max_h(CARDS_H)
        .scroll(rune::ScrollSpec {
            stick_bottom: false,
        })
        .children(marking.marks.iter().map(|m| card(id, m, t)))
        .scroll_bar_styled(attn_ui::scroll_bar(t));
    let (edited, changed, keyed) = (id.to_owned(), id.to_owned(), id.to_owned());
    let note = area_box(
        TextArea::new(&marking.note)
            .key(format!("terminal.{id}.annotation.note.field"))
            .width(None)
            .resizable()
            .label("Note sent with these annotations")
            .placeholder("Anything else to say with these?")
            .on_edit(move |state: &mut State| state.terminals.note(&edited))
            .on_change(move |state: &mut State, tx: &mut Tx| {
                draft::edited(state, &changed, tx);
            })
            .on_key(move |state: &mut State, ev: &KeyEvent, tx: &mut Tx| {
                if ev.key == Key::Enter && (ev.mods.cmd || ev.mods.ctrl) {
                    draft::send(state, &keyed, tx);
                    return true;
                }
                false
            }),
        FieldState::Ready,
        t,
    );
    let said = match &marking.outcome {
        Some(Outcome::Skipped) => Some("Not sent — the session is waiting on an approval, where the sending Enter would answer it. Send again once you have answered.".to_owned()),
        Some(Outcome::Failed(why)) => Some(why.clone()),
        Some(Outcome::Sent { count, kept, .. }) if *kept > 0 => Some(format!(
            "✓ sent {count} to the session. {kept} still here — annotated or changed while it was sending, so not part of what went."
        )),
        _ => None,
    };
    let foot: El = match marking.outcome {
        Some(Outcome::Sent { count, kept: 0, .. }) => words(
            format!("✓ sent {count} to the session"),
            Type::Small,
            t.success,
        )
        .key(format!("terminal.{id}.annotation.sent")),
        _ => {
            let sending = marking.outcome == Some(Outcome::Sending);
            let plural = if count == 1 { "" } else { "s" };
            let pressed = id.to_owned();
            let send = SystemButton::new(if sending { "Sending…" } else { "Send all" })
                .look(Look::Plain)
                .size(Size::Compact)
                .disabled(sending)
                .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                    draft::send(state, &pressed, tx);
                });
            let send = match send_key.filter(|_| !sending) {
                Some(key) => send.trailing(words(key.to_owned(), Type::Small, t.text_tertiary)),
                None => send,
            };
            Inline::joined()
                .w_full()
                .child(
                    words(
                        format!("{count} annotation{plural}"),
                        Type::Small,
                        t.text_secondary,
                    )
                    .grow(1.0),
                )
                .children(unreachable.is_none().then(|| {
                    send.build(t)
                        .key(format!("terminal.{id}.annotation.send"))
                        .into()
                }))
        }
    };
    let panel =
        FloatingPanel::new(
            format!("terminal.{id}.annotation.panel"),
            "Annotations",
            PANEL_W,
        )
        .max_height(slot.1 - 2.0 * PANEL_INSET)
        .aside(words(count.to_string(), Type::Small, t.text_secondary))
        .push_all(unreachable.map(|host| {
            Status::new(
                format!("terminal.{id}.annotation.unreachable"),
                format!("{host} is unreachable"),
                true,
            )
            .build(t)
        }))
        .push(Block::custom(
            format!("terminal.{id}.annotation.cards.slot"),
            cards,
        ))
        .push(Block::control(
            format!("terminal.{id}.annotation.note"),
            El::from(note).w_full(),
        ))
        .push_all(said.map(|said| {
            Status::new(format!("terminal.{id}.annotation.outcome"), said, true).build(t)
        }))
        .foot(foot)
        .build(t, move |state: &mut State, d: &mut Drag, tx: &mut Tx| {
            drag_box(state, &dragged, Moves::Panel, d, tx);
        })
        .on_press(|_: &mut State| {})
        .on_layout_tx(move |state: &mut State, [_, _, w, h], tx: &mut Tx| {
            if !draft::panel_measured(state, &measured, (w, h)) {
                tx.live_only();
            }
        });
    match marking.panel_at {
        Some((left, top)) => panel.absolute(left, top),
        None => panel.pin(None, None, Some(PANEL_INSET), Some(PANEL_INSET)),
    }
}

fn card(id: &str, mark: &Annotation, t: &Tokens) -> El {
    let emoji = annotations::label_by_id(&mark.label).map_or(attn_ui::glyph::COMMENT, |l| l.emoji);
    let (opened, removed, mark_id, gone) = (
        id.to_owned(),
        id.to_owned(),
        mark.id.clone(),
        mark.id.clone(),
    );
    let open = Inline::top()
        .key(format!("terminal.{id}.annotation.card.{}", mark.id))
        .role("button")
        .label("Edit this annotation")
        .grow(1.0)
        .min_w(0.0)
        .child(glyph(emoji, IconSize::Small, t.text_secondary))
        .child(
            Stack::tight()
                .grow(1.0)
                .min_w(0.0)
                .child(
                    words(mark.quote.clone(), Type::Small, t.text_secondary)
                        .wrap()
                        .max_lines(2),
                )
                .children((!mark.comment.is_empty()).then(|| {
                    words(mark.comment.clone(), Type::Small, t.text_primary)
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
    let remove = icon_button(
        format!("terminal.{id}.annotation.card.{}.remove", mark.id),
        "Remove annotation",
        attn_ui::glyph::TIMES,
        IconSize::Small,
    )
    .size(Size::Small)
    .on_press_tx(move |state: &mut State, tx: &mut Tx| {
        draft::remove_card(state, &removed, &gone, tx);
    })
    .build(t);
    Card::new(format!("terminal.{id}.annotation.card.{}.face", mark.id))
        .push(Inline::top().child(open).child(remove.into()))
        .build(t)
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
