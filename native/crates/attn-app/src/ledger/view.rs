use attn_protocol::SessionLedgerEntry;
use attn_ui::blocks::inspector::{self, aside, lines, value};
use attn_ui::blocks::{Block, Facts, Inline, Inspector, List, Sheet, Stack};
use attn_ui::system::{
    Button, FieldState, IconSize, ListRow, Look, Part, Reveal, Said, Size, Type, icon_button,
    line_box, press_area, words,
};
use attn_ui::{Tokens, glyph, keycap};
use rune::{El, Key, KeyEvent, Tx, el};
use rune_kit::{Menu, MenuItem, TextInput};

use super::mark::{Glyph, mark};
use super::query::{self, Scope, is_closed};
use super::{LIST, Note, QUERY, Row, Tab, Verb, conversations, worktrees};
use crate::State;
use crate::desktop::usage;
use crate::sidebar::model::AgentState;
use attn_ui::system::DotSize;

const INSET: f32 = 10.0;

/// The open row's verb menu, beside its More button, for the shell's layers.
pub(crate) fn row_menu(state: &State, t: &Tokens) -> Vec<El> {
    match state.window.ledger.tab {
        Tab::Sessions => {
            let Some(id) = state.window.ledger.menu.as_deref() else {
                return Vec::new();
            };
            let rows = super::rows(state);
            let Some(r) = rows.iter().find(|r| r.entry.id == id) else {
                return Vec::new();
            };
            let verbs = r.verbs.iter().map(|v| (v.label(), v.clone())).collect();
            verb_menu(
                id,
                &r.title,
                verbs,
                super::run,
                |state| state.window.ledger.menu = None,
                t,
            )
        }
        Tab::Worktrees => worktrees::row_menu(state, t),
        Tab::Conversations => conversations::row_menu(state, t),
    }
}

/// A row's verb menu, opened beside its More button: each verb runs on the row `row`, from its
/// item or from its digit, and `close` forgets the open menu.
pub(super) fn verb_menu<V: Clone + 'static>(
    row: &str,
    title: &str,
    verbs: Vec<(String, V)>,
    run: fn(&mut State, &str, V, &mut Tx),
    close: fn(&mut State),
    t: &Tokens,
) -> Vec<El> {
    let by_digit: Vec<V> = verbs.iter().map(|(_, verb)| verb.clone()).collect();
    let digit_row = row.to_owned();
    let menu = verbs.into_iter().enumerate().fold(
        Menu::new(format!("ledger.row.{row}"))
            .label(format!("More for {title}"))
            .anchor(format!("ledger.row.{row}.more"))
            .open(true)
            .on_close(close),
        |menu, (at, (label, verb))| {
            let row = row.to_owned();
            menu.item(
                MenuItem::new(label)
                    .key(format!("ledger.row.{row}.menu.{at}"))
                    .shortcut((at + 1).to_string())
                    .on_select_tx(move |state: &mut State, tx: &mut Tx| {
                        run(state, &row, verb.clone(), tx)
                    }),
            )
        },
    );
    let mut layer = Vec::new();
    menu.build(attn_ui::kit(t), &mut layer);
    let surface = layer.pop().map(|surface| {
        surface.on_key(move |state: &mut State, ev: &KeyEvent, tx: &mut Tx| {
            let Some(verb) = digit_index(ev).and_then(|at| by_digit.get(at)) else {
                return false;
            };
            run(state, &digit_row, verb.clone(), tx);
            true
        })
    });
    layer.extend(surface);
    layer
}

/// The verb a bare digit 1 to 9 names, counted from zero.
fn digit_index(ev: &KeyEvent) -> Option<usize> {
    let m = ev.mods;
    if m.cmd || m.ctrl || m.alt {
        return None;
    }
    let Key::Text(typed) = &ev.key else {
        return None;
    };
    match typed.as_bytes() {
        [digit @ b'1'..=b'9'] => Some(usize::from(digit - b'1')),
        _ => None,
    }
}

pub(crate) fn view(state: &State, t: &Tokens) -> El {
    let (toolbar, body, counts) = match state.window.ledger.tab {
        Tab::Sessions => {
            let rows = super::rows(state);
            let selected = super::selected(state, &rows);
            (
                toolbar(state, t),
                inspector::beside(
                    list(state, &rows, selected, t),
                    self::inspector(state, selected, t),
                ),
                session_counts(state, &rows, t),
            )
        }
        Tab::Worktrees => (
            worktrees::toolbar(state, t),
            inspector::beside(worktrees::list(state, t), worktrees::inspector(state, t)),
            worktrees::status(state, t),
        ),
        Tab::Conversations => {
            let rows = conversations::rows(state);
            let selected = conversations::selected(state, &rows);
            (
                conversations::toolbar(state, t),
                inspector::beside(
                    conversations::list(state, &rows, t),
                    conversations::inspector(state, selected, t),
                ),
                conversations::status(state, t),
            )
        }
    };
    let panel = Sheet::new("ledger.sheet", "Sessions, worktrees and conversations")
        .framed()
        .head(tabs(state, t))
        .close(
            keyed("ledger.close", "Close", "esc", Look::Quiet, t)
                .on_press_tx(|state: &mut State, tx: &mut Tx| super::close(state, tx)),
        )
        .top(toolbar)
        .fill(body)
        .footer(Block::custom(
            "ledger.status.block",
            status(state, counts, t),
        ))
        .build(t)
        .key("ledger.panel")
        .role("dialog")
        .label("Sessions, worktrees and conversations")
        .modal_tx(true, |state: &mut State, tx: &mut Tx| {
            super::close(state, tx)
        })
        .on_key(panel_key);
    crate::shell::under_title_bar("ledger", INSET, panel, t)
}

fn tabs(state: &State, t: &Tokens) -> El {
    let shown = state.window.ledger.tab;
    Inline::tight()
        .role("navigation")
        .label("Which list")
        .children(Tab::ALL.map(|tab| {
            chosen(
                format!("ledger.tab.{}", tab.label().to_lowercase()),
                tab.label(),
                tab == shown,
                Size::Compact,
                t,
            )
            .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                tx.instant();
                super::show_tab(state, tab, tx)
            })
        }))
}

/// One of a few choices shown as buttons, the chosen one raised and read as pressed.
pub(super) fn chosen(key: String, said: &'static str, on: bool, size: Size, t: &Tokens) -> El {
    El::from(
        Button::new(said)
            .size(size)
            .look(if on { Look::Plain } else { Look::Quiet })
            .build(t)
            .key(key),
    )
    .a11y_value(if on { "pressed" } else { "" })
}

/// The query field, its chips after it.
pub(super) fn query_field(
    edit: &rune::TextEdit,
    placeholder: &str,
    on_edit: fn(&mut State) -> &mut rune::TextEdit,
    on_change: fn(&mut State, &mut Tx),
    on_key: fn(&mut State, &KeyEvent, &mut Tx) -> bool,
    t: &Tokens,
) -> El {
    let input = TextInput::new(edit)
        .key(QUERY)
        .label("Filter")
        .placeholder(placeholder)
        .spellcheck(false)
        .on_edit(on_edit)
        .on_change(on_change)
        .on_key(on_key);
    El::from(line_box(input, FieldState::Ready, t))
        .grow(1.0)
        .min_w(0.0)
}

fn toolbar(state: &State, t: &Tokens) -> El {
    let ledger = &state.window.ledger;
    let scopes = Inline::tight()
        .key("ledger.scope")
        .role("group")
        .label("Which sessions")
        .children(Scope::ALL.map(|scope| {
            chosen(
                format!("ledger.scope.{}", scope.label().to_lowercase()),
                scope.label(),
                ledger.filters.scope == scope,
                Size::Compact,
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
            let chip = El::from(
                Button::new(format!("{token} ×"))
                    .size(Size::Compact)
                    .look(if lost { Look::Danger } else { Look::Quiet })
                    .help(format!("Remove {token}"))
                    .build(t)
                    .key(format!("ledger.chip.{at}"))
                    .label(token.clone()),
            )
            .a11y_value(if lost { "unresolved" } else { "" })
            .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                let text = query::remove_token(&state.window.ledger.query.text, &removed);
                state.window.ledger.query = rune::TextEdit::new(text);
                super::query_edited(state, tx);
            });
            if token.chars().count() > SHORT_TOKEN {
                chip.shrink()
            } else {
                chip
            }
        });
    let field = query_field(
        &ledger.query,
        "repo:attn  profile:name  7d  from:2026-09-01  dir:~/x  words",
        |state: &mut State| &mut state.window.ledger.query,
        |state: &mut State, tx: &mut Tx| super::query_edited(state, tx),
        query_key,
        t,
    );
    toolbar_line(vec![scopes, field], chips.collect(), t)
}

/// A list's toolbar: its controls, then the chips of its query.
pub(super) fn toolbar_line(controls: Vec<El>, chips: Vec<El>, _t: &Tokens) -> El {
    Stack::tight()
        .w_full()
        .child(Inline::loose().w_full().children(controls))
        .children((!chips.is_empty()).then(|| Inline::tight().children(chips)))
}

fn list(state: &State, rows: &[Row<'_>], selected: Option<&Row<'_>>, t: &Tokens) -> El {
    let ledger = &state.window.ledger;
    let chosen = selected.map(|r| format!("ledger.row.{}", r.entry.id));
    let chosen_id = selected.map(|r| r.entry.id.as_str());
    rows.iter()
        .fold(List::new(LIST, "Rows").chooser(chosen), |list, r| {
            let (row, under) = list_row(state, r, Some(r.entry.id.as_str()) == chosen_id, t);
            list.row_with(row, under)
        })
        .failed(ledger.failed(state.window.now_ms))
        .empty(
            "ledger.empty",
            ledger.empty_message(state.window.now_ms),
            None,
        )
        .scrolling(t)
}

fn list_row(state: &State, r: &Row<'_>, selected: bool, t: &Tokens) -> (El, Vec<Block>) {
    let ledger = &state.window.ledger;
    let entry = r.entry;
    let closed = is_closed(entry);
    let note = ledger.note(entry);
    let busy = matches!(note, Some(Note::Busy(_)));
    let id = entry.id.clone();
    let menu_open = ledger.menu.as_deref() == Some(entry.id.as_str());
    let at_ms = crate::sidebar::queue::epoch_ms(query::instant(entry)).unwrap_or_default();
    let stamp = query::relative_stamp(at_ms, state.window.now_ms);
    let primary = r.verbs.first().map(|verb| {
        let (id, verb) = (entry.id.clone(), verb.clone());
        let said = match note {
            Some(Note::Busy(busy)) => busy.clone(),
            _ => verb.label(),
        };
        row_verb(format!("ledger.row.{}.verb", entry.id), said, t).on_press_tx(
            move |state: &mut State, tx: &mut Tx| super::run(state, &id, verb.clone(), tx),
        )
    });
    let more = (r.verbs.len() > 1 && !busy).then(|| {
        let id = entry.id.clone();
        icon_button(
            format!("ledger.row.{}.more", entry.id),
            format!("More for {}", r.title),
            "···",
            IconSize::Small,
        )
        .expanded(menu_open)
        .on_press_tx(move |state: &mut State, _: &mut Tx| open_menu(state, &id))
    });
    let row = ListRow::new(format!("ledger.row.{}", entry.id), r.title.clone())
        .lead(mark(glyph(entry, r.live, t), DotSize::Regular, t))
        .title(r.title.clone())
        .subtitle_keyed(
            format!("ledger.row.{}.meta", entry.id),
            meta_line(
                std::iter::once((stamp, false))
                    .chain(meta(state, r))
                    .collect(),
            ),
            t,
        )
        .controls(primary, Reveal::Quiet)
        .hover_controls(more)
        .dimmed(closed)
        .in_view(selected)
        .role("option")
        .unfocusable()
        .build(t)
        .a11y_value(if selected { "selected" } else { "" })
        .on_press_tx(move |state: &mut State, tx: &mut Tx| {
            super::select(state, &id);
            tx.focus(LIST, false);
        });
    let under = match note {
        Some(Note::Refused(said)) => vec![Block::custom(
            format!("ledger.row.{}.note.block", entry.id),
            words(said.clone(), Type::Small, t.warning)
                .key(format!("ledger.row.{}.note", entry.id))
                .role("status")
                .label(said.clone())
                .wrap(),
        )],
        _ => Vec::new(),
    };
    (row, under)
}

/// A verb on a row: a quiet compact button.
pub(super) fn row_verb(key: String, said: impl Into<attn_ui::system::Said>, t: &Tokens) -> El {
    Button::new(said)
        .size(Size::Compact)
        .look(Look::Quiet)
        .build(t)
        .key(key)
        .into()
}

/// A row's meta line, its parts apart by dots and its directory in monospace, each drawn as
/// written.
fn meta_line(parts: Vec<(String, bool)>) -> Said {
    Said::parts(parts.into_iter().enumerate().flat_map(|(i, (part, path))| {
        let gap = (i > 0).then(|| Part::Text("  ·  ".to_owned()));
        let part = if path {
            Part::Code(part)
        } else {
            Part::Text(part)
        };
        gap.into_iter().chain(std::iter::once(part))
    }))
}

fn meta(state: &State, r: &Row<'_>) -> Vec<(String, bool)> {
    let entry = r.entry;
    let mut meta = Vec::new();
    if entry.priority.unwrap_or_default() {
        meta.push(("priority".into(), false));
    }
    meta.push((entry.agent.clone(), false));
    if let Some(host) = entry.host.as_deref().filter(|h| !h.is_empty()) {
        meta.push((
            crate::desktop::outpost_name(host, &state.facts.outposts),
            false,
        ));
    }
    let profile = profile_text(entry);
    if !profile.is_empty() {
        meta.push((profile, false));
    }
    meta.push((query::short_path(&entry.directory), true));
    if let Some(branch) = entry.branch.as_deref().filter(|b| !b.is_empty()) {
        meta.push((branch.to_owned(), false));
    }
    if r.live && entry.hidden.unwrap_or_default() {
        meta.push(("hidden".into(), false));
    }
    if is_closed(entry) {
        meta.push((closed_line(state, entry), false));
    }
    if let Some(verdict) = state
        .window
        .ledger
        .verdict(entry)
        .filter(|v| is_closed(entry) && v.actions.is_empty())
    {
        meta.push((
            name_ids(state, &query::compact_paths(&verdict.summary)),
            false,
        ));
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

fn glyph(entry: &SessionLedgerEntry, live: bool, t: &Tokens) -> Glyph {
    if is_closed(entry) || !live {
        return Glyph::Closed;
    }
    let state = AgentState::parse(&entry.state);
    let ink = crate::sidebar::state_color(state, t);
    if state == AgentState::Working {
        Glyph::Working(ink)
    } else {
        Glyph::Live(ink)
    }
}

fn inspector(state: &State, selected: Option<&Row<'_>>, t: &Tokens) -> El {
    let Some(r) = selected else {
        return nothing_selected("Pick a row to read it here.", t);
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
    let mut facts = Facts::new("ledger.inspector.facts").fact(
        "ledger.inspector.profile",
        "Profile",
        if profile.is_empty() {
            "—".to_owned()
        } else {
            profile.clone()
        },
    );
    facts = directory_field(facts, entry, verdict, ledger.copied.as_deref(), t);
    let branch_state = verdict
        .and_then(|v| v.branch_state.as_deref())
        .map(query::branch_state_label);
    if entry.branch.as_deref().is_some_and(|b| !b.is_empty()) || branch_state.is_some() {
        let branch = entry
            .branch
            .as_deref()
            .filter(|b| !b.is_empty())
            .unwrap_or("—");
        facts = facts.item(
            "ledger.inspector.branch",
            "Branch",
            lines(
                std::iter::once(value(branch.to_owned(), t))
                    .chain(branch_state.map(|s| aside(s.to_owned(), t))),
            ),
        );
    }
    let instant = if closed { "Closed" } else { "Last seen" };
    facts = facts.item(
        format!(
            "ledger.inspector.{}",
            instant.to_lowercase().replace(' ', "-")
        ),
        instant,
        lines(
            std::iter::once(value_with(
                &query::full_stamp(at_ms),
                &format!("({})", query::relative_stamp(at_ms, state.window.now_ms)),
                t,
            ))
            .chain(
                closed.then(|| aside(closed_line(state, entry)["closed ".len()..].to_owned(), t)),
            ),
        ),
    );
    let usage = (!closed)
        .then(|| state.facts.sessions.iter().find(|s| s.id == entry.id))
        .flatten()
        .and_then(|s| s.usage.as_ref())
        .or(entry.usage.as_ref());
    if let Some(usage) = usage {
        facts = facts.item(
            "ledger.inspector.usage",
            "Usage",
            lines(usage_field(usage, t)),
        );
    }
    if let Some((seed, title)) = r.verbs.iter().find_map(|v| match v {
        Verb::Seed { title, .. } => Some((v.clone(), title.clone())),
        _ => None,
    }) {
        let id = entry.id.clone();
        facts = facts.control(
            "ledger.inspector.seed",
            "Seed",
            link(
                "ledger.inspector.seed.open",
                &title,
                t,
                move |state: &mut State, tx: &mut Tx| super::run(state, &id, seed.clone(), tx),
            ),
        );
    }
    let mut shown = Inspector::new("ledger.inspector", r.title.clone())
        .kicker(kicker_line(glyph(entry, r.live, t), &kicker.join(" · "), t))
        .block(facts.build(t));
    if closed {
        shown = shown.push(reopen_verdict(state, r, t));
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
            Some(Note::Refused(said)) if !closed => Some(
                words(said.clone(), Type::Small, t.warning)
                    .key("ledger.inspector.keep-note")
                    .role("status")
                    .wrap(),
            ),
            _ => None,
        };
        let keep_button = El::from(press_area(
            "ledger.inspector.keep",
            &verb.label(),
            false,
            false,
            words(verb.label(), Type::Control, t.text_secondary),
            t,
            move |state: &mut State, tx: &mut Tx| super::run(state, &id, Verb::Keep(keep), tx),
        ))
        .a11y_value(if busy { "busy" } else { "" });
        shown = shown.block(
            Facts::new("ledger.inspector.keeping")
                .item(
                    "ledger.inspector.conversation",
                    "Conversation",
                    lines(
                        std::iter::once(aside(pin, t))
                            .chain(refused)
                            .chain(std::iter::once(keep_button)),
                    ),
                )
                .build(t),
        );
    }
    if r.live {
        let id = entry.id.clone();
        shown = shown.push(
            keyed("ledger.inspector.focus", "Focus", "↵", Look::Primary, t).on_press_tx(
                move |state: &mut State, tx: &mut Tx| super::run(state, &id, Verb::Focus, tx),
            ),
        );
    }
    shown.build(t)
}

/// The inspector while no row is chosen.
pub(super) fn nothing_selected(hint: &str, t: &Tokens) -> El {
    Inspector::new("ledger.inspector", "Nothing selected")
        .push(aside(hint.to_owned(), t))
        .build(t)
}

/// What the chosen row is, under its name: its mark, then its words.
pub(super) fn kicker_line(g: Glyph, said: &str, t: &Tokens) -> El {
    Inline::tight()
        .key("ledger.inspector.kicker")
        .child(mark(g, DotSize::Small, t))
        .child(words(said.to_owned(), Type::Small, t.text_tertiary).wrap())
}

/// A value with an aside after it in quieter text, as a date and its age.
pub(super) fn value_with(said: &str, after: &str, t: &Tokens) -> El {
    Inline::loose()
        .child(value(said.to_owned(), t))
        .child(aside(after.to_owned(), t))
}

/// A link to something the chosen row names, such as its seed: its words in the link ink, on
/// the line of the words around it.
pub(super) fn link(
    key: &str,
    said: &str,
    t: &Tokens,
    press: impl Fn(&mut State, &mut Tx) + 'static,
) -> El {
    press_area(
        key,
        said,
        false,
        false,
        words(said.to_owned(), Type::Body, t.accent).wrap_anywhere(),
        t,
        press,
    )
    .into()
}

/// A verb in the inspector, at its own width.
pub(super) fn verb_button(
    key: &str,
    said: impl Into<attn_ui::system::Said>,
    look: Look,
    t: &Tokens,
) -> El {
    El::from(
        Button::new(said)
            .size(Size::Compact)
            .look(look)
            .build(t)
            .key(key.to_owned()),
    )
    .self_start()
}

/// What a session spent: the header's badge, then the whole count beside a price; the whole
/// count alone while the measurement is incomplete.
fn usage_field(u: &attn_protocol::SessionUsage, t: &Tokens) -> Vec<El> {
    let tokens = format!("{} tokens", usage::grouped(u.total_tokens));
    if !usage::measured(u) {
        return std::iter::once(value(&tokens, t))
            .chain(
                u.measurement_incomplete
                    .unwrap_or_default()
                    .then(|| aside("Measurement incomplete; some usage may be missing.", t)),
            )
            .collect();
    }
    let badge = usage::badge_of(u);
    vec![
        Inline::loose()
            .child(words(badge.text, Type::Body, t.text_primary).label(badge.label))
            .children(
                u.cost_usd
                    .is_some()
                    .then(|| aside(format!("· {tokens}"), t)),
            ),
    ]
}

fn directory_field(
    facts: Facts,
    entry: &SessionLedgerEntry,
    verdict: Option<&query::Verdict>,
    copied: Option<&str>,
    t: &Tokens,
) -> Facts {
    let directory = entry.directory.clone();
    let copied = copied == Some(entry.directory.as_str());
    facts.control(
        "ledger.inspector.directory",
        "Directory",
        lines(
            std::iter::once(copy_path(
                "ledger.inspector.directory.copy",
                &query::tilde_path(&entry.directory),
                copied,
                t,
                move |state: &mut State, tx: &mut Tx| super::copy(state, directory.clone(), tx),
            ))
            .chain(verdict.map(|v| {
                aside(
                    query::directory_state_label(&v.directory_state).to_owned(),
                    t,
                )
            })),
        ),
    )
}

fn reopen_verdict(state: &State, r: &Row<'_>, t: &Tokens) -> El {
    let entry = r.entry;
    let ledger = &state.window.ledger;
    let verdict = ledger.verdict(entry);
    let note = ledger.note(entry);
    let busy = matches!(note, Some(Note::Busy(_)));
    let deleted = entry.profile_deleted.unwrap_or_default();
    let mut parts = vec![words("Reopen", Type::Label, t.text_tertiary)];
    if deleted {
        parts.push(value(
            "Its profile was deleted. This session cannot be reopened.",
            t,
        ));
    } else if let Some(v) = verdict {
        parts.push(value(
            name_ids(
                state,
                &query::compact_paths(
                    v.reason
                        .as_deref()
                        .unwrap_or("It can be reopened where it ran."),
                ),
            ),
            t,
        ));
        parts.extend(
            v.warning
                .as_deref()
                .map(|w| aside(name_ids(state, &query::compact_paths(w)), t)),
        );
        parts.push(aside(v.placement(), t));
    }
    if let Some(Note::Refused(said)) = note {
        parts.push(
            words(said.clone(), Type::Small, t.warning)
                .key("ledger.inspector.note")
                .role("status")
                .label(said.clone())
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
        let said = match note {
            Some(Note::Busy(busy)) if at == 0 => busy.clone(),
            _ => verb.label(),
        };
        El::from(
            Button::new(said)
                .size(Size::Compact)
                .look(if at == 0 { Look::Primary } else { Look::Quiet })
                .trailing(keycap::Keys::ledged(&[hint.as_str()]).build(t))
                .build(t)
                .key(format!("ledger.inspector.action.{at}"))
                .label(verb.label()),
        )
        .a11y_value(if busy { "busy" } else { "" })
        .on_press_tx(move |state: &mut State, tx: &mut Tx| super::run(state, &id, verb.clone(), tx))
    });
    let none = actions
        .is_empty()
        .then(|| aside("Nothing here brings it back.", t));
    parts.push(Inline::tight().children(buttons).children(none));
    Stack::tight()
        .key("ledger.inspector.reopen")
        .w_full()
        .children(parts)
}

fn session_counts(state: &State, rows: &[Row<'_>], t: &Tokens) -> Vec<El> {
    let ledger = &state.window.ledger;
    let shown = rows.len();
    let live = rows.iter().filter(|r| r.live).count();
    let closed = rows.iter().filter(|r| is_closed(r.entry)).count();
    let mut counts = vec![format!(
        "{shown} {}",
        if shown == 1 { "session" } else { "sessions" }
    )];
    if live > 0 {
        counts.push(format!("{live} live"));
    }
    if closed > 0 {
        counts.push(format!("{closed} closed"));
    }
    let hidden = ledger.entries().len() - shown;
    if hidden > 0 {
        counts.push(format!("{hidden} hidden by the query"));
    }
    let older = (ledger.omitted() > 0).then(|| {
        let said = if ledger.loading_more() {
            "loading…".to_owned()
        } else {
            format!("{} older ↓", ledger.omitted())
        };
        status_link("ledger.older", said, t)
            .on_press_tx(|state: &mut State, tx: &mut Tx| super::load_more(state, tx))
    });
    std::iter::once(status_text(counts.join("  ·  "), t.text_tertiary).key("ledger.counts"))
        .chain(older)
        .collect()
}

/// Words of the status line.
pub(super) fn status_text(said: String, ink: rune::Color) -> El {
    words(said, Type::Mono, ink).single_line()
}

/// A press in the status line.
pub(super) fn status_link(key: &str, said: String, t: &Tokens) -> El {
    Button::new(said)
        .size(Size::Compact)
        .look(Look::Quiet)
        .build(t)
        .key(key.to_owned())
        .into()
}

fn status(state: &State, counts: Vec<El>, t: &Tokens) -> El {
    let ledger = &state.window.ledger;
    let copied = ledger
        .copied
        .as_ref()
        .map(|_| status_text("copied".into(), t.success).key("ledger.copied"));
    let legend = ledger.legend.then(|| legend(t));
    Inline::loose()
        .key("ledger.status")
        .role("status")
        .w_full()
        .children(counts)
        .children(copied)
        .child(el().grow(1.0))
        .children(legend)
        .child(
            keyed("ledger.keys", "keys", "?", Look::Quiet, t)
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
    Inline::loose()
        .key("ledger.legend")
        .role("dialog")
        .label("Keys")
        .children(KEYS.map(|(keys, does)| {
            let caps: Vec<&str> = keys.split(' ').collect();
            Inline::tight()
                .label(format!("{keys} {does}"))
                .child(keycap::Keys::ledged(&caps).build(t).a11y_hidden())
                .child(words(does, Type::Small, t.text_tertiary).a11y_hidden())
        }))
}

fn open_menu(state: &mut State, id: &str) {
    super::select(state, id);
    let ledger = &mut state.window.ledger;
    ledger.menu = Some(id.to_owned());
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
    match state.window.ledger.tab {
        Tab::Conversations => return conversations::panel_key(state, ev, tx),
        Tab::Worktrees => return worktrees::panel_key(state, ev, tx),
        Tab::Sessions => {}
    }
    let chosen = {
        let rows = super::rows(state);
        super::selected(state, &rows)
            .map(|r| (r.entry.id.clone(), r.verbs.len(), r.entry.directory.clone()))
    };
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
            Some((id, verbs, _)) if verbs > 1 => open_menu(state, &id),
            _ => return false,
        },
        Key::Text(typed) => match (typed.as_str(), chosen) {
            ("j", _) => super::step(state, 1),
            ("k", _) => super::step(state, -1),
            (".", Some((id, verbs, _))) if verbs > 1 => open_menu(state, &id),
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

/// Escape backs out one layer: the keys legend, then the list.
fn escape(state: &mut State, tx: &mut Tx) {
    let backed_out = match state.window.ledger.tab {
        Tab::Conversations => conversations::back_out(state),
        Tab::Worktrees => worktrees::back_out(state),
        Tab::Sessions => false,
    };
    if backed_out {
        return;
    }
    let ledger = &mut state.window.ledger;
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

/// A button that shows its key beside its word.
fn keyed(key: &str, said: &'static str, cap: &'static str, look: Look, t: &Tokens) -> El {
    El::from(
        Button::new(said)
            .size(Size::Compact)
            .look(look)
            .trailing(keycap::Keys::ledged(&[cap]).build(t))
            .build(t)
            .key(key.to_owned()),
    )
    .self_start()
}

/// The longest token that keeps its whole chip: a date bound such as `from:2026-09-01` is 15.
const SHORT_TOKEN: usize = 16;

/// A path the user copies with y or with the copy button beside it, marked once copied.
pub(super) fn copy_path(
    key: &str,
    path: &str,
    copied: bool,
    t: &Tokens,
    copy: impl Fn(&mut State, &mut Tx) + 'static,
) -> El {
    Inline::tight()
        .w_full()
        .child(
            words(
                format!("{path}{}", if copied { "  copied" } else { "" }),
                Type::Mono,
                t.text_primary,
            )
            .key(format!("{key}.text"))
            .wrap()
            .grow(1.0)
            .min_w(0.0),
        )
        .child(
            icon_button(key, "Copy the path (y)", glyph::COPY, IconSize::Small)
                .on_press_tx(copy)
                .build(t)
                .into(),
        )
}
