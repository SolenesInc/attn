use attn_protocol::KeptConversationRow;
use rune::{El, Key, KeyEvent, Tx, col, row, text, text_input};

use super::super::view::{body, button, chip_button, field, label, muted, title};
use super::super::{LIST, Note, QUERY};
use super::{Verb, amount, date, deleted, key, letter_label, retention, verbs};
use crate::State;
use attn_ui::Tokens;
use rune::tok::{space, type_};

pub(crate) fn toolbar(state: &State, t: &Tokens) -> El {
    let kept = &state.window.ledger.conversations;
    let include = kept.include_deleted;
    let field = text_input(&kept.query, type_::MONO_CAPTION)
        .key(QUERY)
        .single_line()
        .label("Filter")
        .placeholder("title  seed  pinned  pending  words", t.text_muted)
        .on_edit(|state: &mut State| &mut state.window.ledger.conversations.query)
        .on_input_change(|state: &mut State, _: &mut Tx| super::query_edited(state))
        .on_input_key(query_key)
        .grow(1.0)
        .min_w(0.0)
        .color(t.text_primary);
    row()
        .w_full()
        .items_center()
        .gap(space::S3)
        .px(space::S3)
        .py(space::S2)
        .border_sides([0.0, 0.0, 1.0, 0.0], t.border_subtle)
        .child(
            chip_button("ledger.deleted", "Show deleted", include, t)
                .role("checkbox")
                .a11y_value(if include { "checked" } else { "" })
                .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                    super::set_include_deleted(state, !include, tx)
                }),
        )
        .child(
            row()
                .grow(1.0)
                .min_w(0.0)
                .items_center()
                .gap(space::S2)
                .px(space::S2)
                .py(space::S1)
                .radius(4.0)
                .fill(t.bg_input)
                .border(1.0, t.border)
                .child(text("/").font(type_::MONO_CAPTION).color(t.text_muted))
                .child(field),
        )
}

pub(crate) fn list(state: &State, rows: &[&KeptConversationRow], t: &Tokens) -> El {
    let kept = &state.window.ledger.conversations;
    let list = col()
        .key(LIST)
        .role("listbox")
        .label("Rows")
        .focusable(true)
        .grow(1.0)
        .min_w(0.0)
        .h_full()
        .scroll(rune::ScrollSpec {
            stick_bottom: false,
        })
        .py(space::S1);
    if rows.is_empty() {
        let words = match (kept.error(), kept.result()) {
            (Some(error), _) => error.to_owned(),
            (None, None) => "Reading kept conversations…".into(),
            (None, Some(_)) => "No conversations match this view.".into(),
        };
        return list.child(
            text(words.clone())
                .key("ledger.empty")
                .role(if kept.error().is_some() {
                    "alert"
                } else {
                    "note"
                })
                .label(words)
                .font(type_::BODY)
                .color(if kept.error().is_some() {
                    t.error
                } else {
                    t.text_tertiary
                })
                .wrap()
                .padding(space::S6),
        );
    }
    let chosen = super::selected(state, rows).map(key);
    list.children(
        rows.iter()
            .map(|r| list_row(state, r, chosen.as_deref() == Some(key(r).as_str()), t)),
    )
}

fn list_row(state: &State, r: &KeptConversationRow, selected: bool, t: &Tokens) -> El {
    let kept = &state.window.ledger.conversations;
    let k = key(r);
    let note = kept.note(&k);
    let busy = matches!(note, Some(Note::Busy(_)));
    let verbs = verbs(r, kept.confirming.as_deref() == Some(k.as_str()));
    let menu_open = kept.menu.as_deref() == Some(k.as_str());
    let title_words = if r.title.is_empty() {
        r.resume_id.clone()
    } else {
        r.title.clone()
    };
    let copy = if deleted(r) {
        "Deleted copy".to_owned()
    } else {
        r.kept
            .as_ref()
            .map_or("Pending copy".into(), |c| amount(c.bytes))
    };
    let primary = verbs.first().map(|verb| {
        let (k, verb) = (k.clone(), *verb);
        let words = match note {
            Some(Note::Busy(busy)) => busy.clone(),
            _ => verb.label().into(),
        };
        button(&format!("ledger.row.{k}.verb"), &words, t)
            .on_press_tx(move |state: &mut State, tx: &mut Tx| super::run(state, &k, verb, tx))
    });
    let more = (verbs.len() > 1 && !busy).then(|| {
        let k = k.clone();
        button(&format!("ledger.row.{k}.more"), "···", t)
            .label(format!("More for {title_words}"))
            .a11y_value(if menu_open { "expanded" } else { "" })
            .on_press_tx(move |state: &mut State, _: &mut Tx| toggle_menu(state, &k))
    });
    let menu = menu_open.then(|| verb_menu(&k, &verbs, t));
    let note_line = match note {
        Some(Note::Refused(words)) => Some(
            text(words.clone())
                .key(format!("ledger.row.{k}.note"))
                .role("status")
                .label(words.clone())
                .font(type_::LABEL)
                .color(t.warning)
                .single_line()
                .truncate(),
        ),
        _ => None,
    };
    let chosen = k.clone();
    row()
        .key(format!("ledger.row.{k}"))
        .role("option")
        .label(title_words.clone())
        .a11y_value(if selected { "selected" } else { "" })
        .w_full()
        .items_start()
        .gap(space::S2)
        .px(space::S3)
        .py(space::S2)
        .fill(if selected {
            t.bg_selected
        } else {
            rune::Color::TRANSPARENT
        })
        .hover(t.bg_elevated)
        .opacity(if deleted(r) { 0.6 } else { 1.0 })
        .on_press_tx(move |state: &mut State, tx: &mut Tx| {
            super::select(state, &chosen);
            tx.focus(LIST, false);
        })
        .child(
            col()
                .grow(1.0)
                .min_w(0.0)
                .gap(2.0)
                .child(
                    text(title_words)
                        .font(type_::BODY)
                        .color(t.text_primary)
                        .single_line()
                        .truncate(),
                )
                .child(
                    text([r.agent.clone(), copy, retention(r)].join("  ·  "))
                        .key(format!("ledger.row.{k}.meta"))
                        .font(type_::LABEL)
                        .color(t.text_tertiary)
                        .single_line()
                        .truncate(),
                )
                .children(note_line),
        )
        .children(primary)
        .children(more)
        .children(menu)
}

fn verb_menu(k: &str, verbs: &[Verb], t: &Tokens) -> El {
    col()
        .key(format!("ledger.row.{k}.menu"))
        .role("menu")
        .gap(2.0)
        .padding(space::S1)
        .radius(6.0)
        .fill(t.bg_elevated)
        .border(1.0, t.border)
        .children(verbs.iter().enumerate().map(|(at, verb)| {
            let (row_key, verb) = (k.to_owned(), *verb);
            text(format!("{}  {}", at + 1, verb.label()))
                .key(format!("ledger.row.{k}.menu.{at}"))
                .role("menuitem")
                .label(verb.label())
                .font(type_::LABEL)
                .color(t.text_primary)
                .hover(t.bg_selected)
                .radius(4.0)
                .px(space::S2)
                .py(2.0)
                .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                    super::run(state, &row_key, verb, tx)
                })
        }))
}

pub(crate) fn inspector(state: &State, selected: Option<&KeptConversationRow>, t: &Tokens) -> El {
    let aside = col()
        .key("ledger.inspector")
        .role("complementary")
        .label("Details")
        .w(super::super::view::INSPECTOR_WIDTH)
        .h_full()
        .gap(space::S3)
        .padding(space::S3)
        .border_sides([0.0, 0.0, 0.0, 1.0], t.border_subtle)
        .scroll(rune::ScrollSpec {
            stick_bottom: false,
        });
    let Some(r) = selected else {
        return aside
            .child(title("Nothing selected", t))
            .child(muted("Pick a conversation to read it here.", t));
    };
    let kept = &state.window.ledger.conversations;
    let k = key(r);
    let confirming = kept.confirming.as_deref() == Some(k.as_str());
    let note = kept.note(&k);
    let name = if r.title.is_empty() {
        r.resume_id.clone()
    } else {
        r.title.clone()
    };
    let copy = match &r.kept {
        Some(c) if c.deleted_at.is_some() => "Deleted".to_owned(),
        Some(c) => amount(c.bytes),
        None => "Pending copy".into(),
    };
    let pending = r
        .kept
        .is_none()
        .then(|| r.pending_reason.clone().unwrap_or_default())
        .filter(|w| !w.is_empty());
    let resume = r.resume_id.clone();
    let letters = r.crew_letters.clone().unwrap_or_default();
    let mut aside = aside
        .child(title(&name, t))
        .child(muted(&format!("{} conversation", r.agent), t))
        .child(field("Retention", retention(r), t))
        .child(
            col()
                .key("ledger.inspector.copy")
                .role("group")
                .label("attn’s copy")
                .gap(2.0)
                .child(label("attn’s copy", t))
                .child(body(&copy, t))
                .children(pending.map(|w| muted(&w, t))),
        );
    if let Some(c) = &r.kept {
        let at = crate::sidebar::queue::epoch_ms(&c.copied_at).unwrap_or_default();
        aside = aside.child(field("Last copied", super::super::query::full_stamp(at), t));
    }
    if let Some(bytes) = r.source_bytes {
        aside = aside.child(field("Source at last copy", amount(bytes), t));
    }
    aside = aside.child(col().gap(2.0).child(label("Conversation", t)).child(
        button("ledger.inspector.conversation", &resume, t).on_press_tx(
            move |state: &mut State, tx: &mut Tx| super::super::copy(state, resume.clone(), tx),
        ),
    ));
    if !r.seeds.is_empty() {
        aside = aside.child(
            col()
                .key("ledger.inspector.seeds")
                .role("group")
                .label("Open seeds")
                .gap(2.0)
                .child(label("Open seeds", t))
                .children(r.seeds.iter().map(|seed| {
                    let id = seed.id.clone();
                    let words = if seed.slug.is_empty() {
                        &seed.title
                    } else {
                        &seed.slug
                    };
                    button(&format!("ledger.inspector.seed.{}", seed.id), words, t).on_press_tx(
                        move |state: &mut State, tx: &mut Tx| {
                            super::super::open_seed(state, id.clone(), tx)
                        },
                    )
                }))
                .child(muted(
                    "Harvest or wither these seeds before forgetting the copy.",
                    t,
                )),
        );
    }
    if !letters.is_empty() {
        aside = aside.child(
            col()
                .key("ledger.inspector.letters")
                .role("group")
                .label("Crew letters")
                .gap(2.0)
                .child(label("Crew letters", t))
                .children(letters.iter().map(|l| body(&letter_label(l), t))),
        );
    }
    if confirming {
        let bytes = r.kept.as_ref().map_or(0, |c| c.bytes);
        aside = aside.child(
            body(
                &format!(
                    "Delete attn’s copy of {name} ({})? Claude’s own files are not touched.",
                    amount(bytes)
                ),
                t,
            )
            .key("ledger.inspector.confirm"),
        );
        if !letters.is_empty() {
            let names: Vec<String> = letters.iter().map(letter_label).collect();
            aside = aside.child(
                body(
                    &format!(
                        "These letters stay, but will no longer point to this transcript: {}.",
                        names.join(", ")
                    ),
                    t,
                )
                .key("ledger.inspector.letters-stay"),
            );
        }
    }
    if let Some(Note::Refused(words)) = note {
        aside = aside.child(
            text(words.clone())
                .key("ledger.inspector.note")
                .role("status")
                .label(words.clone())
                .font(type_::LABEL)
                .color(t.warning)
                .wrap(),
        );
    }
    let busy = matches!(note, Some(Note::Busy(_)));
    aside.child(
        row()
            .gap(space::S2)
            .children(
                verbs(r, confirming)
                    .into_iter()
                    .enumerate()
                    .map(|(at, verb)| {
                        let k = k.clone();
                        let el = button(&format!("ledger.inspector.action.{at}"), verb.label(), t);
                        if busy {
                            el.a11y_value("disabled").opacity(0.5)
                        } else {
                            el.on_press_tx(move |state: &mut State, tx: &mut Tx| {
                                super::run(state, &k, verb, tx)
                            })
                        }
                    }),
            ),
    )
}

pub(crate) fn status(state: &State, t: &Tokens) -> Vec<El> {
    let kept = &state.window.ledger.conversations;
    let mut parts = Vec::new();
    if let Some(error) = kept.error() {
        parts.push(
            text(error.to_owned())
                .key("ledger.error")
                .role("alert")
                .font(type_::LABEL)
                .color(t.error),
        );
    }
    if let Some(result) = kept.result() {
        parts.push(
            text(format!(
                "{} kept  ·  {}",
                result.count,
                amount(result.stored_bytes)
            ))
            .key("ledger.counts")
            .font(type_::LABEL)
            .color(t.text_secondary),
        );
        if result.pending_count > 0 {
            parts.push(
                text(format!("{} pending", result.pending_count))
                    .key("ledger.pending")
                    .font(type_::LABEL)
                    .color(t.text_secondary),
            );
        }
        if let Some(next) = &result.next_delete_after {
            parts.push(
                text(format!("next deletion {}", date(next)))
                    .key("ledger.next-deletion")
                    .font(type_::LABEL)
                    .color(t.text_secondary),
            );
        }
    }
    parts
}

fn toggle_menu(state: &mut State, k: &str) {
    super::select(state, k);
    let kept = &mut state.window.ledger.conversations;
    kept.menu = match kept.menu {
        Some(_) => None,
        None => Some(k.to_owned()),
    };
}

/// The Conversations list's own keys, outside the query field. Escape and the keys every
/// list shares stay with the ledger.
pub(crate) fn panel_key(state: &mut State, ev: &KeyEvent, tx: &mut Tx) -> bool {
    let chosen = {
        let rows = super::rows(state);
        super::selected(state, &rows).map(|r| {
            let confirming =
                state.window.ledger.conversations.confirming.as_deref() == Some(key(r).as_str());
            (key(r), verbs(r, confirming).len(), r.resume_id.clone())
        })
    };
    let menu_open = state.window.ledger.conversations.menu.is_some();
    match &ev.key {
        Key::Down => super::step(state, 1),
        Key::Up => super::step(state, -1),
        Key::Home => super::step(state, isize::MIN / 2),
        Key::End => super::step(state, isize::MAX / 2),
        Key::Enter => match chosen {
            Some((k, ..)) => super::run_nth(state, &k, 0, tx),
            None => return false,
        },
        Key::Right => match chosen {
            Some((k, n, _)) if n > 1 && !menu_open => toggle_menu(state, &k),
            _ => return false,
        },
        Key::Left if menu_open => state.window.ledger.conversations.menu = None,
        Key::Text(typed) => match (typed.as_str(), chosen) {
            ("j", _) => super::step(state, 1),
            ("k", _) => super::step(state, -1),
            (".", Some((k, n, _))) if n > 1 => toggle_menu(state, &k),
            ("y", Some((_, _, resume))) => super::super::copy(state, resume, tx),
            (digit, Some((k, ..))) if digit.len() == 1 && ("1"..="9").contains(&digit) => {
                let index = usize::from(digit.as_bytes()[0] - b'1');
                super::run_nth(state, &k, index, tx);
            }
            _ => return false,
        },
        _ => return false,
    }
    true
}

/// Backs out of the Conversations list's own layers: a waiting Forget, then the verb menu.
pub(crate) fn back_out(state: &mut State) -> bool {
    let kept = &mut state.window.ledger.conversations;
    kept.confirming.take().is_some() || kept.menu.take().is_some()
}

fn query_key(state: &mut State, ev: &KeyEvent, tx: &mut Tx) -> bool {
    if ev.key != Key::Escape {
        return false;
    }
    let kept = &mut state.window.ledger.conversations;
    if kept.query.text.is_empty() {
        tx.focus(LIST, false);
    } else {
        kept.query = rune::TextEdit::default();
        super::query_edited(state);
        tx.focus(QUERY, false);
    }
    true
}
