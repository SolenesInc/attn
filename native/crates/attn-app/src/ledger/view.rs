use attn_protocol::SessionLedgerEntry;
use attn_ui::Tokens;
use rune::tok::{space, type_};
use rune::{El, Key, KeyEvent, Tx, col, el, row, text, text_input};

use super::query::{self, Scope, is_closed};
use super::{LIST, Note, QUERY, Row, Tab, Verb, conversations};
use crate::State;
use crate::sidebar::model::AgentState;

const INSET: f32 = 24.0;
pub(super) const INSPECTOR_WIDTH: f32 = 340.0;

pub(crate) fn view(state: &State, t: &Tokens) -> El {
    let (toolbar, body, status) = match state.window.ledger.tab {
        Tab::Sessions => {
            let rows = super::rows(state);
            let selected = super::selected(state, &rows);
            (
                toolbar(state, t),
                row()
                    .w_full()
                    .grow(1.0)
                    .min_h(0.0)
                    .child(list(state, &rows, selected, t))
                    .child(inspector(state, selected, t)),
                status(state, session_counts(state, &rows, t), t),
            )
        }
        Tab::Conversations => {
            let rows = conversations::rows(state);
            let selected = conversations::selected(state, &rows);
            (
                conversations::toolbar(state, t),
                row()
                    .w_full()
                    .grow(1.0)
                    .min_h(0.0)
                    .child(conversations::list(state, &rows, t))
                    .child(conversations::inspector(state, selected, t)),
                status(state, conversations::status(state, t), t),
            )
        }
    };
    let panel = col()
        .key("ledger.panel")
        .role("dialog")
        .label("Sessions, worktrees and conversations")
        .modal_tx(true, |state: &mut State, tx: &mut Tx| {
            super::close(state, tx)
        })
        .on_key(panel_key)
        .w_full()
        .h_full()
        .radius(8.0)
        .fill(t.bg_panel)
        .border(1.0, t.border)
        .child(header(state, t))
        .child(toolbar)
        .child(body)
        .child(status);
    el().key("ledger")
        .absolute(0.0, 0.0)
        .w_full()
        .h_full()
        .child(
            el().absolute(0.0, 0.0)
                .w_full()
                .h_full()
                .fill(t.bg_overlay)
                .a11y_hidden(),
        )
        .child(
            col()
                .w_full()
                .h_full()
                .pt(crate::shell::TITLE_BAR_HEIGHT + INSET)
                .px(INSET)
                .pb(INSET)
                .child(panel),
        )
}

fn header(state: &State, t: &Tokens) -> El {
    let shown = state.window.ledger.tab;
    row()
        .w_full()
        .items_center()
        .gap(space::S2)
        .px(space::S3)
        .py(space::S2)
        .border_sides([0.0, 0.0, 1.0, 0.0], t.border_subtle)
        .child(
            row()
                .role("navigation")
                .label("Which list")
                .grow(1.0)
                .gap(2.0)
                .children(Tab::ALL.map(|tab| {
                    chip_button(
                        &format!("ledger.tab.{}", tab.label().to_lowercase()),
                        tab.label(),
                        tab == shown,
                        t,
                    )
                    .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                        super::show_tab(state, tab, tx)
                    })
                })),
        )
        .child(
            button("ledger.close", "Close  esc", t)
                .label("Close")
                .on_press_tx(|state: &mut State, tx: &mut Tx| super::close(state, tx)),
        )
}

fn toolbar(state: &State, t: &Tokens) -> El {
    let ledger = &state.window.ledger;
    let scopes = row()
        .key("ledger.scope")
        .role("group")
        .label("Which sessions")
        .gap(2.0)
        .children(Scope::ALL.map(|scope| {
            chip_button(
                &format!("ledger.scope.{}", scope.label().to_lowercase()),
                scope.label(),
                ledger.filters.scope == scope,
                t,
            )
            .on_press_tx(move |state: &mut State, tx: &mut Tx| super::set_scope(state, scope, tx))
        }));
    let unresolved = {
        let facets = ledger.read_facets();
        let profiles =
            query::profile_choices(&ledger.named_with, facets, ledger.chosen_profile.as_ref());
        query::parse(
            &ledger.query.text,
            facets,
            &profiles,
            &ledger.filters.repository,
        )
        .unresolved
    };
    let chips = ledger
        .query
        .text
        .split_whitespace()
        .filter(|token| query::is_chip(token))
        .enumerate()
        .map(|(at, token)| {
            let token = token.to_owned();
            let lost = unresolved.contains(&token);
            let removed = token.clone();
            text(format!("{token} ×"))
                .key(format!("ledger.chip.{at}"))
                .role("button")
                .label(token.clone())
                .a11y_value(if lost { "unresolved" } else { "" })
                .font(type_::MONO_CAPTION)
                .color(if lost { t.warning } else { t.text_secondary })
                .fill(t.bg_button)
                .radius(4.0)
                .px(space::S1)
                .py(1.0)
                .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                    let text = query::remove_token(&state.window.ledger.query.text, &removed);
                    state.window.ledger.query = rune::TextEdit::new(text);
                    super::query_edited(state, tx);
                })
        })
        .collect::<Vec<_>>();
    let field = text_input(&ledger.query, type_::MONO_CAPTION)
        .key(QUERY)
        .single_line()
        .label("Filter")
        .placeholder(
            "repo:attn  profile:name  7d  from:2026-09-01  dir:~/x  words",
            t.text_muted,
        )
        .on_edit(|state: &mut State| &mut state.window.ledger.query)
        .on_input_change(|state: &mut State, tx: &mut Tx| super::query_edited(state, tx))
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
        .child(scopes)
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
                .child(field)
                .children(chips),
        )
}

fn list(state: &State, rows: &[Row<'_>], selected: Option<&Row<'_>>, t: &Tokens) -> El {
    let ledger = &state.window.ledger;
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
        let failed = ledger.failed(state.epoch_ms);
        let words = ledger.empty_message(state.epoch_ms);
        return list.child(
            text(words.clone())
                .key("ledger.empty")
                .role(if failed { "alert" } else { "note" })
                .label(words)
                .font(type_::BODY)
                .color(if failed { t.error } else { t.text_tertiary })
                .wrap()
                .padding(space::S6),
        );
    }
    let chosen = selected.map(|r| r.entry.id.as_str());
    list.children(
        rows.iter()
            .map(|r| list_row(state, r, Some(r.entry.id.as_str()) == chosen, t)),
    )
}

fn list_row(state: &State, r: &Row<'_>, selected: bool, t: &Tokens) -> El {
    let ledger = &state.window.ledger;
    let entry = r.entry;
    let closed = is_closed(entry);
    let note = ledger.note(entry);
    let busy = matches!(note, Some(Note::Busy(_)));
    let id = entry.id.clone();
    let menu_open = ledger.menu.as_deref() == Some(entry.id.as_str());
    let at_ms = crate::sidebar::queue::epoch_ms(query::instant(entry)).unwrap_or_default();
    let stamp = query::relative_stamp(at_ms, state.epoch_ms);
    let primary = r.verbs.first().map(|verb| {
        let (id, verb) = (entry.id.clone(), verb.clone());
        let words = match note {
            Some(Note::Busy(busy)) => busy.clone(),
            _ => verb.label(),
        };
        button(&format!("ledger.row.{}.verb", entry.id), &words, t).on_press_tx(
            move |state: &mut State, tx: &mut Tx| super::run(state, &id, verb.clone(), tx),
        )
    });
    let more = (r.verbs.len() > 1 && !busy).then(|| {
        let id = entry.id.clone();
        button(&format!("ledger.row.{}.more", entry.id), "···", t)
            .label(format!("More for {}", r.title))
            .a11y_value(if menu_open { "expanded" } else { "" })
            .on_press_tx(move |state: &mut State, _: &mut Tx| toggle_menu(state, &id))
    });
    let menu = menu_open.then(|| verb_menu(&entry.id, &r.verbs, t));
    let note_line = match note {
        Some(Note::Refused(words)) => Some(
            text(words.clone())
                .key(format!("ledger.row.{}.note", entry.id))
                .role("status")
                .label(words.clone())
                .font(type_::LABEL)
                .color(t.warning)
                .single_line()
                .truncate(),
        ),
        _ => None,
    };
    row()
        .key(format!("ledger.row.{}", entry.id))
        .role("option")
        .label(r.title.clone())
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
        .opacity(if closed { 0.75 } else { 1.0 })
        .on_press_tx(move |state: &mut State, tx: &mut Tx| {
            super::select(state, &id);
            tx.focus(LIST, false);
        })
        .child(glyph(entry, r.live, t).mt(5.0))
        .child(
            col()
                .grow(1.0)
                .min_w(0.0)
                .gap(2.0)
                .child(
                    text(r.title.clone())
                        .font(type_::BODY)
                        .color(t.text_primary)
                        .single_line()
                        .truncate(),
                )
                .child(
                    text(meta(state, r).join("  ·  "))
                        .key(format!("ledger.row.{}.meta", entry.id))
                        .font(type_::LABEL)
                        .color(t.text_tertiary)
                        .single_line()
                        .truncate(),
                )
                .children(note_line),
        )
        .child(
            text(stamp)
                .font(type_::MONO_CAPTION)
                .color(t.text_tertiary)
                .single_line(),
        )
        .children(primary)
        .children(more)
        .children(menu)
}

fn meta(state: &State, r: &Row<'_>) -> Vec<String> {
    let entry = r.entry;
    let mut meta = Vec::new();
    if entry.priority.unwrap_or_default() {
        meta.push("priority".into());
    }
    meta.push(entry.agent.clone());
    if let Some(host) = entry.host.as_deref().filter(|h| !h.is_empty()) {
        meta.push(crate::desktop::outpost_name(host, &state.facts.outposts));
    }
    let profile = profile_text(entry);
    if !profile.is_empty() {
        meta.push(profile);
    }
    meta.push(query::short_path(&entry.directory));
    if let Some(branch) = entry.branch.as_deref().filter(|b| !b.is_empty()) {
        meta.push(branch.to_owned());
    }
    if r.live && entry.hidden.unwrap_or_default() {
        meta.push("hidden".into());
    }
    if is_closed(entry) {
        meta.push(closed_line(state, entry));
    }
    if let Some(verdict) = state
        .window
        .ledger
        .verdict(entry)
        .filter(|v| is_closed(entry) && v.actions.is_empty())
    {
        meta.push(name_ids(state, &query::compact_paths(&verdict.summary)));
    }
    meta
}

fn closed_line(state: &State, entry: &SessionLedgerEntry) -> String {
    let by = query::closed_by(entry, |id| label_of(state, id));
    match entry.close_reason.as_deref().filter(|r| !r.is_empty()) {
        Some(reason) => format!("closed by {by}: {}", name_ids(state, reason)),
        None => format!("closed by {by}"),
    }
}

fn label_of(state: &State, id: &str) -> Option<String> {
    state
        .window
        .ledger
        .entries()
        .iter()
        .find(|e| e.id == id)
        .map(|e| e.label.clone())
        .filter(|l| !l.is_empty())
}

fn name_ids(state: &State, words: &str) -> String {
    query::name_ids(words, |id| label_of(state, id))
}

fn profile_text(entry: &SessionLedgerEntry) -> String {
    if entry.profile_name.is_empty() {
        String::new()
    } else if entry.profile_deleted.unwrap_or_default() {
        format!("{} (deleted)", entry.profile_name)
    } else {
        entry.profile_name.clone()
    }
}

fn glyph(entry: &SessionLedgerEntry, live: bool, t: &Tokens) -> El {
    let color = if is_closed(entry) || !live {
        t.text_muted
    } else {
        crate::sidebar::state_color(AgentState::parse(&entry.state), t)
    };
    el().w(8.0).h(8.0).radius(4.0).fill(color).a11y_hidden()
}

fn verb_menu(id: &str, verbs: &[Verb], t: &Tokens) -> El {
    col()
        .key(format!("ledger.row.{id}.menu"))
        .role("menu")
        .gap(2.0)
        .padding(space::S1)
        .radius(6.0)
        .fill(t.bg_elevated)
        .border(1.0, t.border)
        .children(verbs.iter().enumerate().map(|(at, verb)| {
            let (row_id, verb) = (id.to_owned(), verb.clone());
            text(format!("{}  {}", at + 1, verb.label()))
                .key(format!("ledger.row.{id}.menu.{at}"))
                .role("menuitem")
                .label(verb.label())
                .font(type_::LABEL)
                .color(t.text_primary)
                .hover(t.bg_selected)
                .radius(4.0)
                .px(space::S2)
                .py(2.0)
                .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                    super::run(state, &row_id, verb.clone(), tx)
                })
        }))
}

fn inspector(state: &State, selected: Option<&Row<'_>>, t: &Tokens) -> El {
    let aside = col()
        .key("ledger.inspector")
        .role("complementary")
        .label("Details")
        .w(INSPECTOR_WIDTH)
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
            .child(muted("Pick a row to read it here.", t));
    };
    let entry = r.entry;
    let ledger = &state.window.ledger;
    let verdict = ledger.verdict(entry);
    let closed = is_closed(entry);
    let mut kicker = vec![if closed {
        "closed".to_owned()
    } else {
        entry.state.clone()
    }];
    if r.live && entry.hidden.unwrap_or_default() {
        kicker.push("hidden".into());
    }
    kicker.push(entry.agent.clone());
    if let Some(host) = entry.host.as_deref().filter(|h| !h.is_empty()) {
        kicker.push(crate::desktop::outpost_name(host, &state.facts.outposts));
    }
    let profile = profile_text(entry);
    let at_ms = crate::sidebar::queue::epoch_ms(query::instant(entry)).unwrap_or_default();
    let mut fields = vec![
        field(
            "Profile",
            if profile.is_empty() {
                "—".into()
            } else {
                profile
            },
            t,
        ),
        directory_field(entry, verdict, ledger.copied.as_deref(), t),
    ];
    let branch_state = verdict
        .and_then(|v| v.branch_state.as_deref())
        .map(query::branch_state_label);
    if entry.branch.as_deref().is_some_and(|b| !b.is_empty()) || branch_state.is_some() {
        fields.push(
            field(
                "Branch",
                entry
                    .branch
                    .clone()
                    .filter(|b| !b.is_empty())
                    .unwrap_or("—".into()),
                t,
            )
            .children(branch_state.map(|s| muted(s, t))),
        );
    }
    fields.push(
        field(
            if closed { "Closed" } else { "Last seen" },
            format!(
                "{} ({})",
                query::full_stamp(at_ms),
                query::relative_stamp(at_ms, state.epoch_ms)
            ),
            t,
        )
        .children(closed.then(|| muted(&closed_line(state, entry)["closed ".len()..], t))),
    );
    let usage = (!closed)
        .then(|| state.facts.sessions.iter().find(|s| s.id == entry.id))
        .flatten()
        .and_then(|s| s.usage.as_ref())
        .or(entry.usage.as_ref());
    if let Some(usage) = usage {
        let tokens = format!("{} tokens", thousands(usage.total_tokens));
        let words = match usage.cost_usd {
            Some(cost) if !usage.measurement_incomplete.unwrap_or_default() => {
                format!("${cost:.2} · {tokens}")
            }
            _ => tokens,
        };
        fields.push(
            field("Usage", words, t).children(
                usage
                    .measurement_incomplete
                    .unwrap_or_default()
                    .then(|| muted("Measurement incomplete; some usage may be missing.", t)),
            ),
        );
    }
    if let Some(Verb::Seed { title, .. }) = r.verbs.iter().find(|v| matches!(v, Verb::Seed { .. }))
    {
        let (id, seed) = (
            entry.id.clone(),
            r.verbs
                .iter()
                .find(|v| matches!(v, Verb::Seed { .. }))
                .cloned(),
        );
        fields.push(col().gap(2.0).child(label("Seed", t)).child(
            button("ledger.inspector.seed", title, t).on_press_tx(
                move |state: &mut State, tx: &mut Tx| {
                    if let Some(seed) = seed.clone() {
                        super::run(state, &id, seed, tx)
                    }
                },
            ),
        ));
    }
    if closed {
        fields.push(reopen_verdict(state, r, t));
    }
    if super::keeps_conversation(entry) {
        let keep = entry.conversation_pinned_at.is_none();
        let verb = Verb::Keep(keep);
        let id = entry.id.clone();
        let busy = matches!(state.window.ledger.note(entry), Some(Note::Busy(_)));
        let pin = if keep {
            "Keep attn’s copy forever"
        } else {
            "Kept forever"
        };
        let refused = match state.window.ledger.note(entry) {
            Some(Note::Refused(words)) if !closed => Some(
                text(words.clone())
                    .key("ledger.inspector.keep-note")
                    .role("status")
                    .font(type_::LABEL)
                    .color(t.warning)
                    .wrap(),
            ),
            _ => None,
        };
        fields.push(
            col()
                .key("ledger.inspector.conversation")
                .role("group")
                .label("Conversation")
                .gap(2.0)
                .child(label("Conversation", t))
                .child(muted(pin, t))
                .children(refused)
                .child(
                    button("ledger.inspector.keep", &verb.label(), t)
                        .a11y_value(if busy { "busy" } else { "" })
                        .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                            super::run(state, &id, Verb::Keep(keep), tx)
                        }),
                ),
        );
    }
    if r.live {
        let id = entry.id.clone();
        fields.push(
            button("ledger.inspector.focus", "↵  Focus", t)
                .label("Focus")
                .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                    super::run(state, &id, Verb::Focus, tx)
                }),
        );
    }
    aside
        .child(
            col()
                .gap(2.0)
                .child(title(&r.title, t))
                .child(muted(&kicker.join(" · "), t)),
        )
        .children(fields)
}

fn directory_field(
    entry: &SessionLedgerEntry,
    verdict: Option<&query::Verdict>,
    copied: Option<&str>,
    t: &Tokens,
) -> El {
    let directory = entry.directory.clone();
    let copied = copied == Some(entry.directory.as_str());
    col()
        .gap(2.0)
        .child(label("Directory", t))
        .child(
            text(format!(
                "{}{}",
                query::tilde_path(&entry.directory),
                if copied { "  copied" } else { "" }
            ))
            .key("ledger.inspector.directory")
            .role("button")
            .label("Copy the path (y)")
            .font(type_::MONO_CAPTION)
            .color(t.text_primary)
            .wrap()
            .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                super::copy(state, directory.clone(), tx)
            }),
        )
        .children(verdict.map(|v| muted(query::directory_state_label(&v.directory_state), t)))
}

fn reopen_verdict(state: &State, r: &Row<'_>, t: &Tokens) -> El {
    let entry = r.entry;
    let ledger = &state.window.ledger;
    let verdict = ledger.verdict(entry);
    let note = ledger.note(entry);
    let busy = matches!(note, Some(Note::Busy(_)));
    let deleted = entry.profile_deleted.unwrap_or_default();
    let mut block = col()
        .key("ledger.inspector.reopen")
        .gap(space::S1)
        .child(label("Reopen", t));
    if deleted {
        block = block.child(body(
            "Its profile was deleted. This session cannot be reopened.",
            t,
        ));
    } else if let Some(v) = verdict {
        block = block
            .child(body(
                &name_ids(
                    state,
                    &query::compact_paths(
                        v.reason
                            .as_deref()
                            .unwrap_or("It can be reopened where it ran."),
                    ),
                ),
                t,
            ))
            .children(
                v.warning
                    .as_deref()
                    .map(|w| muted(&name_ids(state, &query::compact_paths(w)), t)),
            )
            .child(muted(v.placement(), t));
    }
    if let Some(Note::Refused(words)) = note {
        block = block.child(
            text(words.clone())
                .key("ledger.inspector.note")
                .role("status")
                .label(words.clone())
                .font(type_::LABEL)
                .color(t.warning)
                .wrap(),
        );
    }
    let actions: Vec<Verb> = r
        .verbs
        .iter()
        .filter(|v| matches!(v, Verb::Reopen(_)))
        .cloned()
        .collect();
    let buttons = actions.iter().enumerate().map(|(at, verb)| {
        let (id, verb) = (entry.id.clone(), verb.clone());
        let hint = if at == 0 {
            "↵".to_owned()
        } else {
            (at + 1).to_string()
        };
        let words = match note {
            Some(Note::Busy(busy)) if at == 0 => busy.clone(),
            _ => verb.label(),
        };
        button(
            &format!("ledger.inspector.action.{at}"),
            &format!("{hint}  {words}"),
            t,
        )
        .label(verb.label())
        .a11y_value(if busy { "busy" } else { "" })
        .on_press_tx(move |state: &mut State, tx: &mut Tx| super::run(state, &id, verb.clone(), tx))
    });
    let none = (actions.is_empty()).then(|| muted("Nothing here brings it back.", t));
    block.child(row().gap(space::S1).children(buttons).children(none))
}

fn session_counts(state: &State, rows: &[Row<'_>], t: &Tokens) -> Vec<El> {
    let ledger = &state.window.ledger;
    let shown = rows.len();
    let live = rows.iter().filter(|r| r.live).count();
    let closed = rows.iter().filter(|r| is_closed(r.entry)).count();
    let mut parts = vec![format!(
        "{shown} {}",
        if shown == 1 { "session" } else { "sessions" }
    )];
    if live > 0 {
        parts.push(format!("{live} live"));
    }
    if closed > 0 {
        parts.push(format!("{closed} closed"));
    }
    let hidden = ledger.entries().len() - shown;
    if hidden > 0 {
        parts.push(format!("{hidden} hidden by the query"));
    }
    let older = (ledger.omitted() > 0).then(|| {
        let words = if ledger.loading_more() {
            "loading…".to_owned()
        } else {
            format!("{} older ↓", ledger.omitted())
        };
        button("ledger.older", &words, t)
            .on_press_tx(|state: &mut State, tx: &mut Tx| super::load_more(state, tx))
    });
    std::iter::once(
        text(parts.join("  ·  "))
            .key("ledger.counts")
            .font(type_::LABEL)
            .color(t.text_secondary),
    )
    .chain(older)
    .collect()
}

fn status(state: &State, counts: Vec<El>, t: &Tokens) -> El {
    let ledger = &state.window.ledger;
    let copied = ledger.copied.as_ref().map(|_| {
        text("copied")
            .key("ledger.copied")
            .font(type_::LABEL)
            .color(t.success)
    });
    let legend = ledger.legend.then(|| legend(t));
    row()
        .key("ledger.status")
        .role("status")
        .w_full()
        .items_center()
        .gap(space::S3)
        .px(space::S3)
        .py(space::S2)
        .border_sides([1.0, 0.0, 0.0, 0.0], t.border_subtle)
        .children(counts)
        .children(copied)
        .child(el().grow(1.0))
        .children(legend)
        .child(
            button("ledger.keys", "?  keys", t)
                .label("Keys")
                .a11y_value(if ledger.legend { "expanded" } else { "" })
                .on_press_tx(|state: &mut State, _: &mut Tx| {
                    state.window.ledger.legend = !state.window.ledger.legend
                }),
        )
}

fn legend(t: &Tokens) -> El {
    const KEYS: [(&str, &str); 8] = [
        ("↑ ↓", "move"),
        ("↵", "primary verb"),
        (".", "more verbs"),
        ("1–9", "nth verb"),
        ("/", "filter"),
        ("[ ]", "switch list"),
        ("y", "copy path"),
        ("esc", "back out"),
    ];
    row()
        .key("ledger.legend")
        .role("dialog")
        .label("Keys")
        .gap(space::S3)
        .children(KEYS.map(|(keys, does)| {
            text(format!("{keys} {does}"))
                .font(type_::LABEL)
                .color(t.text_tertiary)
        }))
}

fn toggle_menu(state: &mut State, id: &str) {
    super::select(state, id);
    let ledger = &mut state.window.ledger;
    ledger.menu = match ledger.menu {
        Some(_) => None,
        None => Some(id.to_owned()),
    };
}

/// The keys of the open list, outside the query field.
fn panel_key(state: &mut State, ev: &KeyEvent, tx: &mut Tx) -> bool {
    let m = ev.mods;
    if ev.key == Key::Escape {
        escape(state, tx);
        return true;
    }
    if tx.focused() == Some(QUERY) || m.cmd || m.ctrl || m.alt {
        return false;
    }
    state.window.ledger.copied = None;
    if let Key::Text(typed) = &ev.key {
        match typed.as_str() {
            "/" => tx.focus(QUERY, true),
            "?" => state.window.ledger.legend = !state.window.ledger.legend,
            "[" => super::step_tab(state, -1, tx),
            "]" => super::step_tab(state, 1, tx),
            _ => return tab_key(state, ev, tx),
        }
        return true;
    }
    tab_key(state, ev, tx)
}

fn tab_key(state: &mut State, ev: &KeyEvent, tx: &mut Tx) -> bool {
    if state.window.ledger.tab == Tab::Conversations {
        return conversations::panel_key(state, ev, tx);
    }
    let chosen = {
        let rows = super::rows(state);
        super::selected(state, &rows)
            .map(|r| (r.entry.id.clone(), r.verbs.len(), r.entry.directory.clone()))
    };
    let menu_open = state.window.ledger.menu.is_some();
    match &ev.key {
        Key::Down => super::step(state, 1),
        Key::Up => super::step(state, -1),
        Key::Home => super::step(state, isize::MIN / 2),
        Key::End => super::step(state, isize::MAX / 2),
        Key::Enter => match chosen {
            Some((id, ..)) => super::run_nth(state, &id, 0, tx),
            None => return false,
        },
        Key::Right => match chosen {
            Some((id, verbs, _)) if verbs > 1 && !menu_open => toggle_menu(state, &id),
            _ => return false,
        },
        Key::Left if menu_open => state.window.ledger.menu = None,
        Key::Text(typed) => match (typed.as_str(), chosen) {
            ("j", _) => super::step(state, 1),
            ("k", _) => super::step(state, -1),
            (".", Some((id, verbs, _))) if verbs > 1 => toggle_menu(state, &id),
            ("y", Some((_, _, directory))) => super::copy(state, directory, tx),
            (digit, Some((id, ..))) if digit.len() == 1 && ("1"..="9").contains(&digit) => {
                let index = usize::from(digit.as_bytes()[0] - b'1');
                super::run_nth(state, &id, index, tx);
            }
            _ => return false,
        },
        _ => return false,
    }
    true
}

/// Escape backs out one layer: the verb menu, then the keys legend, then the list.
fn escape(state: &mut State, tx: &mut Tx) {
    if state.window.ledger.tab == Tab::Conversations && conversations::back_out(state) {
        return;
    }
    let ledger = &mut state.window.ledger;
    if ledger.menu.take().is_some() {
        return;
    }
    if ledger.legend {
        ledger.legend = false;
        return;
    }
    super::close(state, tx);
}

/// The query field's own Escape: it clears the text, then hands the keyboard back to the rows.
fn query_key(state: &mut State, ev: &KeyEvent, tx: &mut Tx) -> bool {
    if ev.key != Key::Escape {
        return false;
    }
    if state.window.ledger.query.text.is_empty() {
        tx.focus(LIST, false);
    } else {
        state.window.ledger.query = rune::TextEdit::default();
        super::query_edited(state, tx);
        tx.focus(QUERY, false);
    }
    true
}

fn thousands(n: i64) -> String {
    let digits = n.unsigned_abs().to_string();
    let mut out = String::new();
    for (at, c) in digits.chars().enumerate() {
        if at > 0 && (digits.len() - at).is_multiple_of(3) {
            out.push(',');
        }
        out.push(c);
    }
    if n < 0 { format!("-{out}") } else { out }
}

pub(super) fn title(words: &str, t: &Tokens) -> El {
    text(words.to_owned())
        .role("heading")
        .font(type_::TITLE)
        .color(t.text_primary)
        .wrap()
}

pub(super) fn label(words: &str, t: &Tokens) -> El {
    text(words.to_owned())
        .font(type_::LABEL)
        .color(t.text_tertiary)
}

pub(super) fn body(words: &str, t: &Tokens) -> El {
    text(words.to_owned())
        .font(type_::BODY)
        .color(t.text_primary)
        .wrap()
}

pub(super) fn muted(words: &str, t: &Tokens) -> El {
    text(words.to_owned())
        .font(type_::LABEL)
        .color(t.text_tertiary)
        .wrap()
}

pub(super) fn field(name: &str, value: String, t: &Tokens) -> El {
    col()
        .key(format!(
            "ledger.inspector.{}",
            name.to_lowercase().replace(' ', "-")
        ))
        .role("group")
        .label(name.to_owned())
        .gap(2.0)
        .child(label(name, t))
        .child(body(&value, t))
}

pub(super) fn button(key: &str, words: &str, t: &Tokens) -> El {
    text(words.to_owned())
        .key(key.to_owned())
        .role("button")
        .focusable(true)
        .label(words.to_owned())
        .font(type_::LABEL)
        .color(t.text_primary)
        .fill(t.bg_button)
        .hover(t.bg_elevated)
        .radius(4.0)
        .px(space::S2)
        .py(2.0)
        .single_line()
}

pub(super) fn chip_button(key: &str, words: &str, on: bool, t: &Tokens) -> El {
    text(words.to_owned())
        .key(key.to_owned())
        .role("button")
        .focusable(true)
        .label(words.to_owned())
        .a11y_value(if on { "pressed" } else { "" })
        .font(type_::LABEL)
        .color(if on { t.text_primary } else { t.text_secondary })
        .fill(if on { t.bg_selected } else { t.bg_button })
        .hover(t.bg_elevated)
        .radius(4.0)
        .px(space::S2)
        .py(2.0)
}
