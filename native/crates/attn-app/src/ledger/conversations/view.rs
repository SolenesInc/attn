use attn_protocol::KeptConversationRow;
use rune::{El, Key, KeyEvent, Tx, col, row, text, text_input};

use super::super::parts::{self, Glyph, Ink, body, field, group, muted};
use super::super::{LIST, Note, QUERY};
use super::{Verb, amount, date, deleted, key, letter_label, retention, verbs};
use crate::State;
use attn_ui::Tokens;
use attn_ui::type_;
use rune::tok::space;
use rune_kit::Checkbox;

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
    parts::toolbar(
        vec![
            Checkbox::new(include)
                .label("Show deleted")
                .on_toggle_tx(move |state: &mut State, tx: &mut Tx| {
                    super::set_include_deleted(state, !include, tx)
                })
                .build(attn_ui::kit(t))
                .key("ledger.deleted"),
            parts::query_well(field, Vec::new(), t),
        ],
        t,
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
        parts::row_verb(format!("ledger.row.{k}.verb"), &words, selected, t)
            .on_press_tx(move |state: &mut State, tx: &mut Tx| super::run(state, &k, verb, tx))
    });
    let more = (verbs.len() > 1 && !busy).then(|| {
        let k = k.clone();
        parts::row_more(format!("ledger.row.{k}.more"), selected, t)
            .label(format!("More for {title_words}"))
            .a11y_value(if menu_open { "expanded" } else { "" })
            .on_press_tx(move |state: &mut State, _: &mut Tx| open_menu(state, &k))
    });
    let note_line = match note {
        Some(Note::Refused(words)) => Some(
            text(words.clone())
                .key(format!("ledger.row.{k}.note"))
                .role("status")
                .label(words.clone())
                .font(parts::META)
                .color(t.warning)
                .single_line()
                .truncate(),
        ),
        _ => None,
    };
    let glyph = if deleted(r) {
        Glyph::Removed
    } else if super::pinned(r) {
        Glyph::Pinned
    } else if r.kept.as_ref().is_some_and(|c| c.delete_after.is_some()) {
        Glyph::Scheduled
    } else {
        Glyph::Clean
    };
    let chosen = k.clone();
    let body = parts::row_body(
        title_words.clone(),
        deleted(r),
        parts::meta([r.agent.clone(), copy, retention(r)].join("  ·  "))
            .key(format!("ledger.row.{k}.meta")),
        note_line,
        t,
    );
    parts::list_row(
        format!("ledger.row.{k}"),
        title_words,
        selected,
        glyph,
        body,
        primary.into_iter().chain(more).collect(),
        t,
    )
    .on_press_tx(move |state: &mut State, tx: &mut Tx| {
        super::select(state, &chosen);
        tx.focus(LIST, false);
    })
}

pub(crate) fn row_menu(state: &State, t: &Tokens) -> Vec<El> {
    let kept = &state.window.ledger.conversations;
    let Some(k) = kept.menu.as_deref() else {
        return Vec::new();
    };
    let rows = super::rows(state);
    let Some(r) = rows.iter().find(|r| key(r) == k) else {
        return Vec::new();
    };
    let verbs = verbs(r, kept.confirming.as_deref() == Some(k))
        .into_iter()
        .map(|v| (v.label().to_owned(), v))
        .collect();
    let title = if r.title.is_empty() {
        &r.resume_id
    } else {
        &r.title
    };
    super::super::view::verb_menu(
        k,
        title,
        verbs,
        super::run,
        |state| state.window.ledger.conversations.menu = None,
        t,
    )
}

pub(crate) fn inspector(state: &State, selected: Option<&KeptConversationRow>, t: &Tokens) -> El {
    let Some(r) = selected else {
        return super::super::view::nothing_selected("Pick a conversation to read it here.", t);
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
    let mut fields = vec![
        field("Retention", &retention(r), t),
        group("copy", "attn’s copy", t)
            .child(body(&copy, t))
            .children(pending.map(|w| muted(&w, t))),
    ];
    if let Some(c) = &r.kept {
        let at = crate::sidebar::queue::epoch_ms(&c.copied_at).unwrap_or_default();
        fields.push(field(
            "Last copied",
            &super::super::query::full_stamp(at),
            t,
        ));
    }
    if let Some(bytes) = r.source_bytes {
        fields.push(field("Source at last copy", &amount(bytes), t));
    }
    fields.push(group("conversation", "Conversation", t).child(
        parts::link("ledger.inspector.conversation", &resume, t).on_press_tx(
            move |state: &mut State, tx: &mut Tx| super::super::copy(state, resume.clone(), tx),
        ),
    ));
    if !r.seeds.is_empty() {
        fields.push(
            col()
                .key("ledger.inspector.seeds")
                .role("group")
                .label("Open seeds")
                .gap(2.0)
                .child(parts::label("Open seeds", t))
                .children(r.seeds.iter().map(|seed| {
                    let id = seed.id.clone();
                    let words = if seed.slug.is_empty() {
                        &seed.title
                    } else {
                        &seed.slug
                    };
                    parts::link(format!("ledger.inspector.seed.{}", seed.id), words, t).on_press_tx(
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
        fields.push(
            col()
                .key("ledger.inspector.letters")
                .role("group")
                .label("Crew letters")
                .gap(2.0)
                .child(parts::label("Crew letters", t))
                .children(letters.iter().map(|l| body(&letter_label(l), t))),
        );
    }
    if confirming {
        let bytes = r.kept.as_ref().map_or(0, |c| c.bytes);
        fields.push(
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
            fields.push(
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
        fields.push(
            text(words.clone())
                .key("ledger.inspector.note")
                .role("status")
                .label(words.clone())
                .font(parts::META)
                .color(t.warning)
                .wrap(),
        );
    }
    let busy = matches!(note, Some(Note::Busy(_)));
    fields.push(
        row().gap(6.0).children(
            verbs(r, confirming)
                .into_iter()
                .enumerate()
                .map(|(at, verb)| {
                    let k = k.clone();
                    let ink = match verb {
                        Verb::Forget | Verb::ForgetNow => Ink::Danger,
                        _ => Ink::Quiet,
                    };
                    let el = parts::verb(
                        format!("ledger.inspector.action.{at}"),
                        verb.label(),
                        ink,
                        t,
                    );
                    if busy {
                        el.a11y_value("disabled").opacity(0.5)
                    } else {
                        el.on_press_tx(move |state: &mut State, tx: &mut Tx| {
                            super::run(state, &k, verb, tx)
                        })
                    }
                }),
        ),
    );
    parts::inspector(
        parts::head(
            &name,
            Some(muted(&format!("{} conversation", r.agent), t)),
            t,
        ),
        fields,
        t,
    )
}

pub(crate) fn status(state: &State, t: &Tokens) -> Vec<El> {
    let kept = &state.window.ledger.conversations;
    let mut out = Vec::new();
    if let Some(error) = kept.error() {
        out.push(
            parts::status_text(error.to_owned(), t.error)
                .key("ledger.error")
                .role("alert"),
        );
    }
    if let Some(result) = kept.result() {
        out.push(
            parts::status_text(
                format!("{} kept  ·  {}", result.count, amount(result.stored_bytes)),
                t.text_tertiary,
            )
            .key("ledger.counts"),
        );
        if result.pending_count > 0 {
            out.push(
                parts::status_text(format!("{} pending", result.pending_count), t.text_tertiary)
                    .key("ledger.pending"),
            );
        }
        if let Some(next) = &result.next_delete_after {
            out.push(
                parts::status_text(format!("next deletion {}", date(next)), t.text_tertiary)
                    .key("ledger.next-deletion"),
            );
        }
    }
    out
}

fn open_menu(state: &mut State, k: &str) {
    super::select(state, k);
    let kept = &mut state.window.ledger.conversations;
    kept.menu = Some(k.to_owned());
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
            Some((k, n, _)) if n > 1 => open_menu(state, &k),
            _ => return false,
        },
        Key::Text(typed) => match (typed.as_str(), chosen) {
            ("j", _) => super::step(state, 1),
            ("k", _) => super::step(state, -1),
            (".", Some((k, n, _))) if n > 1 => open_menu(state, &k),
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

/// Backs out of a waiting Forget.
pub(crate) fn back_out(state: &mut State) -> bool {
    let kept = &mut state.window.ledger.conversations;
    kept.confirming.take().is_some()
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
