use attn_protocol::KeptConversationRow;
use attn_ui::Tokens;
use attn_ui::blocks::inspector::{aside, lines, value};
use attn_ui::blocks::{Block, Facts, Inline, Inspector, List};
use attn_ui::system::{Beside, IconSize, ListRow, Look, Reveal, Type, icon_button, switch, words};
use rune::{El, Key, KeyEvent, Tx};

use super::super::mark::{Glyph, mark};
use super::super::view::{link, query_field, row_verb, status_text, toolbar_line, verb_button};
use super::super::{LIST, Note, QUERY};
use super::{Verb, amount, date, deleted, key, letter_label, retention, verbs};
use crate::State;
use attn_ui::system::DotSize;

fn field_key(label: &str) -> String {
    format!(
        "ledger.inspector.{}",
        label.to_lowercase().replace(' ', "-")
    )
}

pub(crate) fn toolbar(state: &State, t: &Tokens) -> El {
    let kept = &state.window.ledger.conversations;
    let include = kept.include_deleted;
    let field = query_field(
        &kept.query,
        "title  seed  pinned  pending  words",
        |state: &mut State| &mut state.window.ledger.conversations.query,
        |state: &mut State, _: &mut Tx| super::query_edited(state),
        query_key,
        t,
    );
    let deleted = Inline::loose().child(El::from(switch(
        "ledger.deleted",
        "Show deleted",
        include,
        Beside::Label,
        false,
        t,
        move |state: &mut State, tx: &mut Tx| super::set_include_deleted(state, !include, tx),
    )));
    toolbar_line(vec![deleted, field], Vec::new(), t)
}

pub(crate) fn list(state: &State, rows: &[&KeptConversationRow], t: &Tokens) -> El {
    let kept = &state.window.ledger.conversations;
    let said = match (kept.error(), kept.result()) {
        (Some(error), _) => error.to_owned(),
        (None, None) => "Reading kept conversations…".into(),
        (None, Some(_)) => "No conversations match this view.".into(),
    };
    let chosen = super::selected(state, rows).map(key);
    rows.iter()
        .fold(
            List::new(LIST, "Rows").chooser(chosen.as_ref().map(|k| format!("ledger.row.{k}"))),
            |list, r| {
                let (row, under) =
                    list_row(state, r, chosen.as_deref() == Some(key(r).as_str()), t);
                list.row_with(row, under)
            },
        )
        .failed(kept.error().is_some())
        .empty("ledger.empty", said, None)
        .scrolling(t)
}

fn list_row(
    state: &State,
    r: &KeptConversationRow,
    selected: bool,
    t: &Tokens,
) -> (ListRow, Vec<Block>) {
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
        let said = match note {
            Some(Note::Busy(busy)) => busy.clone(),
            _ => verb.label().into(),
        };
        row_verb(format!("ledger.row.{k}.verb"), said, t)
            .on_press_tx(move |state: &mut State, tx: &mut Tx| super::run(state, &k, verb, tx))
    });
    let more = (verbs.len() > 1 && !busy).then(|| {
        let k = k.clone();
        icon_button(
            format!("ledger.row.{k}.more"),
            format!("More for {title_words}"),
            "···",
            IconSize::Small,
        )
        .expanded(menu_open)
        .on_press_tx(move |state: &mut State, _: &mut Tx| open_menu(state, &k))
    });
    let under = match note {
        Some(Note::Refused(said)) => vec![Block::custom(
            format!("ledger.row.{k}.note.block"),
            words(said.clone(), Type::Small, t.warning)
                .key(format!("ledger.row.{k}.note"))
                .role("status")
                .label(said.clone())
                .wrap(),
        )],
        _ => Vec::new(),
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
    let row = ListRow::new(format!("ledger.row.{k}"), title_words.clone())
        .lead(mark(glyph, DotSize::Regular, t))
        .title(title_words)
        .subtitle_keyed(
            format!("ledger.row.{k}.meta"),
            [r.agent.clone(), copy, retention(r)].join("  ·  "),
            t,
        )
        .controls(primary, Reveal::Quiet)
        .hover_controls(more)
        .dimmed(deleted(r))
        .in_view(selected)
        .role("option")
        .unfocusable()
        .value(if selected { "selected" } else { "" })
        .on_press_tx(move |state: &mut State, tx: &mut Tx| {
            super::select(state, &chosen);
            tx.focus(LIST, false);
        });
    (row, under)
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
    let mut facts = Facts::new("ledger.inspector.facts")
        .fact(field_key("Retention"), "Retention", retention(r))
        .item(
            "ledger.inspector.copy",
            "attn’s copy",
            lines(std::iter::once(value(&copy, t)).chain(pending.map(|w| aside(&w, t)))),
        );
    if let Some(c) = &r.kept {
        let at = crate::sidebar::queue::epoch_ms(&c.copied_at).unwrap_or_default();
        facts = facts.fact(
            field_key("Last copied"),
            "Last copied",
            super::super::query::full_stamp(at),
        );
    }
    if let Some(bytes) = r.source_bytes {
        facts = facts.fact(
            field_key("Source at last copy"),
            "Source at last copy",
            amount(bytes),
        );
    }
    facts = facts.control(
        "ledger.inspector.conversation",
        "Conversation",
        link(
            "ledger.inspector.conversation.copy",
            &resume.clone(),
            t,
            move |state: &mut State, tx: &mut Tx| super::super::copy(state, resume.clone(), tx),
        ),
    );
    if !r.seeds.is_empty() {
        facts = facts.control(
            "ledger.inspector.seeds",
            "Open seeds",
            lines(
                r.seeds
                    .iter()
                    .map(|seed| {
                        let id = seed.id.clone();
                        let words = if seed.slug.is_empty() {
                            &seed.title
                        } else {
                            &seed.slug
                        };
                        link(
                            &format!("ledger.inspector.seed.{}", seed.id),
                            words,
                            t,
                            move |state: &mut State, tx: &mut Tx| {
                                super::super::open_seed(state, id.clone(), tx)
                            },
                        )
                    })
                    .chain(std::iter::once(aside(
                        "Harvest or wither these seeds before forgetting the copy.",
                        t,
                    ))),
            ),
        );
    }
    if !letters.is_empty() {
        facts = facts.item(
            "ledger.inspector.letters",
            "Crew letters",
            lines(letters.iter().map(|l| value(letter_label(l), t))),
        );
    }
    let mut fields = Vec::new();
    if confirming {
        let bytes = r.kept.as_ref().map_or(0, |c| c.bytes);
        fields.push(
            value(
                format!(
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
                value(
                    format!(
                        "These letters stay, but will no longer point to this transcript: {}.",
                        names.join(", ")
                    ),
                    t,
                )
                .key("ledger.inspector.letters-stay"),
            );
        }
    }
    if let Some(Note::Refused(said)) = note {
        fields.push(
            words(said.clone(), Type::Small, t.warning)
                .key("ledger.inspector.note")
                .role("status")
                .label(said.clone())
                .wrap(),
        );
    }
    let busy = matches!(note, Some(Note::Busy(_)));
    fields.push(
        Inline::tight().children(
            verbs(r, confirming)
                .into_iter()
                .enumerate()
                .map(|(at, verb)| {
                    let k = k.clone();
                    let look = match verb {
                        Verb::Forget | Verb::ForgetNow => Look::Danger,
                        _ => Look::Quiet,
                    };
                    let el = verb_button(
                        &format!("ledger.inspector.action.{at}"),
                        verb.label(),
                        look,
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
    fields
        .into_iter()
        .fold(
            Inspector::new("ledger.inspector", name.clone())
                .kicker(aside(format!("{} conversation", r.agent), t))
                .block(facts.build(t)),
            Inspector::push,
        )
        .build(t)
}

pub(crate) fn status(state: &State, t: &Tokens) -> Vec<El> {
    let kept = &state.window.ledger.conversations;
    let mut out = Vec::new();
    if let Some(error) = kept.error() {
        out.push(
            status_text(error.to_owned(), t.error)
                .key("ledger.error")
                .role("alert"),
        );
    }
    if let Some(result) = kept.result() {
        out.push(
            status_text(
                format!("{} kept  ·  {}", result.count, amount(result.stored_bytes)),
                t.text_tertiary,
            )
            .key("ledger.counts"),
        );
        if result.pending_count > 0 {
            out.push(
                status_text(format!("{} pending", result.pending_count), t.text_tertiary)
                    .key("ledger.pending"),
            );
        }
        if let Some(next) = &result.next_delete_after {
            out.push(
                status_text(format!("next deletion {}", date(next)), t.text_tertiary)
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
