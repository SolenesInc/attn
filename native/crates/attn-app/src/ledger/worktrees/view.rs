use attn_protocol::{Worktree, WorktreeSweepEntry};
use attn_ui::Tokens;
use attn_ui::blocks::inspector::{aside, lines, value};
use attn_ui::blocks::{Block, Facts, Inline, Inspector, List};
use attn_ui::system::{IconSize, ListRow, Look, Reveal, Type, icon_button, words};
use rune::{El, Key, KeyEvent, Tx};

use super::super::mark::{Glyph, mark};
use super::super::query::{
    base_name, full_stamp, name_ids, relative_stamp, tilde_path, until_stamp,
};
use super::super::view::{
    chosen as chosen_button, kicker_line, link, query_field, row_verb, status_link, status_text,
    toolbar_line, value_with, verb_button,
};
use super::super::{LIST, Note, QUERY};
use super::{Refresh, Scope, Verb, live_sessions, refreshing, verbs};
use crate::State;
use crate::sidebar::queue::epoch_ms;
use attn_ui::system::DotSize;

pub(crate) fn toolbar(state: &State, t: &Tokens) -> El {
    let ledger = &state.window.ledger.worktrees;
    let field = query_field(
        &ledger.query,
        "repo:attn  branch  dirty  merged  words",
        |state: &mut State| &mut state.window.ledger.worktrees.query,
        |state: &mut State, _: &mut Tx| super::query_edited(state),
        query_key,
        t,
    );
    let scopes = Inline::tight()
        .key("ledger.scope")
        .role("radiogroup")
        .label("Which worktrees")
        .children(Scope::ALL.map(|scope| {
            chosen_button(
                format!("ledger.scope.{}", scope.label().to_lowercase()),
                scope.label(),
                scope == ledger.scope,
                attn_ui::system::Size::Compact,
                t,
            )
            .on_press_tx(move |state: &mut State, tx: &mut Tx| super::set_scope(state, scope, tx))
        }));
    toolbar_line(vec![scopes, field], Vec::new(), t)
}

pub(crate) fn list(state: &State, t: &Tokens) -> El {
    let ledger = &state.window.ledger.worktrees;
    let chosen = super::selected_key(state);
    let mut list =
        List::new(LIST, "Rows").chooser(chosen.as_ref().map(|k| format!("ledger.row.{k}")));
    let mut group: Option<&str> = None;
    match ledger.scope {
        Scope::Active => {
            for w in super::active_rows(state) {
                if group != Some(w.main_repo.as_str()) {
                    list = list.row(group_heading(state, &w.main_repo, t));
                    group = Some(&w.main_repo);
                }
                let (row, under) = worktree_row(state, w, chosen.as_deref() == Some(&w.path), t);
                list = list.row_with(row, under);
            }
        }
        Scope::Removed => {
            for e in super::removed_rows(state) {
                if group != Some(e.main_repo.as_str()) {
                    list = list.row(group_heading(state, &e.main_repo, t));
                    group = Some(&e.main_repo);
                }
                list = list.row(swept_row(state, e, chosen.as_deref() == Some(&e.id), t));
            }
        }
    }
    list.empty("ledger.empty", empty_words(state), None)
        .scrolling(t)
}

fn empty_words(state: &State) -> &'static str {
    let ledger = &state.window.ledger.worktrees;
    if !ledger.loaded() {
        "Reading the registry…"
    } else if ledger.scope == Scope::Removed {
        if ledger.sweep_log().is_empty() {
            "Nothing removed yet."
        } else {
            "Nothing removed matches the query."
        }
    } else if ledger.all().is_empty() {
        "No worktrees tracked yet. Refresh once a repository has one."
    } else {
        "Nothing matches the query."
    }
}

fn group_heading(state: &State, main_repo: &str, t: &Tokens) -> El {
    let ledger = &state.window.ledger.worktrees;
    let mut meta = Vec::new();
    if let Some(branch) = ledger.integration(main_repo) {
        meta.push(format!("merges into {branch}"));
    }
    if refreshing(state, main_repo) {
        meta.push("refreshing…".into());
    }
    Inline::loose()
        .key(format!("ledger.group.{main_repo}"))
        .role("heading")
        .label(base_name(main_repo).to_owned())
        .w_full()
        .child(words(
            base_name(main_repo).to_owned(),
            Type::Label,
            t.text_secondary,
        ))
        .child(
            words(meta.join("  ·  "), Type::Mono, t.text_tertiary)
                .key(format!("ledger.group.{main_repo}.meta"))
                .single_line()
                .truncate(),
        )
}

fn branch_words(w: &Worktree) -> String {
    if w.detached.unwrap_or_default() {
        let sha = w.head_sha.as_deref().unwrap_or_default();
        format!("detached {}", sha.get(..8).unwrap_or(sha))
    } else if w.branch.is_empty() {
        "—".into()
    } else {
        w.branch.clone()
    }
}

fn state_words(w: &Worktree) -> Vec<String> {
    let mut out = Vec::new();
    if w.prunable.unwrap_or_default() {
        out.push("stale".into());
    }
    if w.dirty.unwrap_or_default() {
        out.push(format!("dirty {}", w.dirty_files.unwrap_or_default()));
    }
    if let Some(n) = w.stashes.filter(|n| *n > 0) {
        out.push(format!("{n} stashed"));
    }
    if let Some(n) = w.unpushed.filter(|n| *n > 0) {
        out.push(format!("{n} ahead"));
    }
    if let Some(signal) = w.merged_signal.as_deref().filter(|s| !s.is_empty()) {
        out.push(format!("merged · {signal}"));
    }
    if let Some(error) = w.refresh_error.as_deref().filter(|e| !e.is_empty()) {
        out.push(error.to_owned());
    }
    out
}

fn sweep_word(w: &Worktree, now_ms: i64) -> String {
    if w.pinned.unwrap_or_default() {
        return "kept".into();
    }
    match w.sweep_status.as_deref().unwrap_or_default() {
        "" => "not decided".into(),
        "scheduled" => match w.sweep_at.as_deref().and_then(epoch_ms) {
            Some(at) => format!("removing in {}", until_stamp(at, now_ms)),
            None => "scheduled".into(),
        },
        status => status.replace('_', " "),
    }
}

fn row_meta(state: &State, w: &Worktree) -> Vec<String> {
    let mut meta = vec![branch_words(w)];
    meta.extend(state_words(w));
    let live = live_sessions(state, &w.path).len();
    if live > 0 {
        meta.push(format!(
            "{live} live {}",
            if live == 1 { "session" } else { "sessions" }
        ));
    }
    meta.push(sweep_word(w, state.window.now_ms));
    if refreshing(state, &w.path) {
        meta.push("refreshing…".into());
    }
    if let Some(at) = w.last_activity_at.as_deref().and_then(epoch_ms) {
        meta.push(format!("idle {}", relative_stamp(at, state.window.now_ms)));
    }
    meta
}

fn note_line(key: &str, note: Option<&Note>, t: &Tokens) -> Option<El> {
    match note {
        Some(Note::Refused(said)) => Some(
            words(said.clone(), Type::Small, t.warning)
                .key(key.to_owned())
                .role("status")
                .label(said.clone())
                .wrap(),
        ),
        _ => None,
    }
}

/// A worktree's mark, as the Tauri app's `worktreeGlyph`.
fn glyph(state: &State, w: &Worktree) -> Glyph {
    if refreshing(state, &w.path) {
        Glyph::Refreshing
    } else if w.refresh_error.as_deref().is_some_and(|e| !e.is_empty())
        || w.prunable.unwrap_or_default()
    {
        Glyph::Error
    } else if w.pinned.unwrap_or_default() {
        Glyph::Pinned
    } else if w.sweep_status.as_deref() == Some("scheduled") {
        Glyph::Scheduled
    } else if w.dirty.unwrap_or_default()
        || w.unpushed.is_some_and(|n| n > 0)
        || w.stashes.is_some_and(|n| n > 0)
    {
        Glyph::Dirty
    } else {
        Glyph::Clean
    }
}

fn worktree_row(state: &State, w: &Worktree, selected: bool, t: &Tokens) -> (ListRow, Vec<Block>) {
    let ledger = &state.window.ledger.worktrees;
    let path = w.path.clone();
    let note = ledger.note(&path);
    let busy = matches!(note, Some(Note::Busy(_)));
    let verbs = verbs(state, w);
    let menu_open = ledger.menu.as_deref() == Some(path.as_str());
    let name = base_name(&path).to_owned();
    let primary = verbs.first().map(|verb| {
        let (p, verb) = (path.clone(), verb.clone());
        let said = match note {
            Some(Note::Busy(busy)) => busy.clone(),
            _ => verb.label(),
        };
        row_verb(format!("ledger.row.{path}.verb"), said, t).on_press_tx(
            move |state: &mut State, tx: &mut Tx| super::run(state, &p, verb.clone(), tx),
        )
    });
    let more = (verbs.len() > 1 && !busy).then(|| {
        let p = path.clone();
        icon_button(
            format!("ledger.row.{path}.more"),
            format!("More for {name}"),
            "···",
            IconSize::Small,
        )
        .expanded(menu_open)
        .on_press_tx(move |state: &mut State, _: &mut Tx| open_menu(state, &p))
    });
    let chosen = path.clone();
    let row = ListRow::new(format!("ledger.row.{path}"), name.clone())
        .lead(mark(glyph(state, w), DotSize::Regular, t))
        .title(name)
        .subtitle_keyed(
            format!("ledger.row.{path}.meta"),
            row_meta(state, w).join("  ·  "),
            t,
        )
        .controls(primary, Reveal::Quiet)
        .hover_controls(more)
        .in_view(selected)
        .role("option")
        .unfocusable()
        .value(if selected { "selected" } else { "" })
        .on_press_tx(move |state: &mut State, tx: &mut Tx| {
            super::select(state, &chosen);
            tx.focus(LIST, false);
        });
    let under = note_line(&format!("ledger.row.{path}.note"), note, t)
        .map(|line| Block::custom(format!("ledger.row.{path}.note.block"), line))
        .into_iter()
        .collect();
    (row, under)
}

fn swept_row(state: &State, e: &WorktreeSweepEntry, selected: bool, t: &Tokens) -> El {
    let mut meta: Vec<String> = e.branch.iter().cloned().collect();
    meta.push(e.action.clone());
    meta.extend(e.reason.iter().cloned());
    let id = e.id.clone();
    let name = base_name(&e.path).to_owned();
    let mut row = ListRow::new(format!("ledger.row.{}", e.id), name.clone())
        .lead(mark(Glyph::Removed, DotSize::Regular, t))
        .title(name)
        .subtitle_keyed(format!("ledger.row.{}.meta", e.id), meta.join("  ·  "), t)
        .dimmed(true)
        .in_view(selected)
        .role("option")
        .unfocusable();
    if let Some(at) = epoch_ms(&e.at) {
        row = row.aside(words(
            relative_stamp(at, state.window.now_ms),
            Type::Mono,
            t.text_dimmed,
        ));
    }
    row.build(t)
        .a11y_value(if selected { "selected" } else { "" })
        .on_press_tx(move |state: &mut State, tx: &mut Tx| {
            super::select(state, &id);
            tx.focus(LIST, false);
        })
}

fn copy_path(path: &str, copied: bool, t: &Tokens) -> El {
    let p = path.to_owned();
    super::super::view::copy_path(
        "ledger.inspector.path.copy",
        &tilde_path(path),
        copied,
        t,
        move |state: &mut State, tx: &mut Tx| super::super::copy(state, p.clone(), tx),
    )
}

fn session_label(state: &State, id: &str) -> Option<String> {
    state
        .facts
        .sessions
        .iter()
        .find(|s| s.id == id)
        .map(crate::sidebar::model::display_name)
}

pub(crate) fn row_menu(state: &State, t: &Tokens) -> Vec<El> {
    let ledger = &state.window.ledger.worktrees;
    let Some(path) = ledger.menu.as_deref() else {
        return Vec::new();
    };
    let Some(w) = ledger.all().iter().find(|w| w.path == path) else {
        return Vec::new();
    };
    let verbs = verbs(state, w)
        .into_iter()
        .map(|v| (v.label(), v))
        .collect();
    super::super::view::verb_menu(
        path,
        base_name(path),
        verbs,
        super::run,
        |state| state.window.ledger.worktrees.menu = None,
        t,
    )
}

pub(crate) fn inspector(state: &State, t: &Tokens) -> El {
    let ledger = &state.window.ledger.worktrees;
    let chosen = super::selected_key(state);
    let copied = state.window.ledger.copied.as_deref();
    match ledger.scope {
        Scope::Active => {
            if let Some(w) = chosen.and_then(|k| ledger.all().iter().find(|w| w.path == k)) {
                return worktree_inspector(state, w, copied, t);
            }
        }
        Scope::Removed => {
            if let Some(e) = chosen.and_then(|k| ledger.sweep_log().iter().find(|e| e.id == k)) {
                let mut facts = Facts::new("ledger.inspector.facts")
                    .control(
                        "ledger.inspector.path",
                        "Path",
                        copy_path(&e.path, copied == Some(&e.path), t),
                    )
                    .fact(
                        "ledger.inspector.repository",
                        "Repository",
                        tilde_path(&e.main_repo),
                    );
                if let Some(branch) = &e.branch {
                    facts = facts.fact("ledger.inspector.branch", "Branch", branch);
                }
                if let Some(at) = epoch_ms(&e.at) {
                    facts = facts.fact("ledger.inspector.when", "When", full_stamp(at));
                }
                if let Some(reason) = &e.reason {
                    facts = facts.fact("ledger.inspector.why", "Why", reason);
                }
                return Inspector::new("ledger.inspector", base_name(&e.path).to_owned())
                    .kicker(kicker_line(Glyph::Removed, &e.action, t))
                    .block(facts.build(t))
                    .build(t);
            }
        }
    }
    super::super::view::nothing_selected("Pick a row to read it here.", t)
}

fn worktree_inspector(state: &State, w: &Worktree, copied: Option<&str>, t: &Tokens) -> El {
    let ledger = &state.window.ledger.worktrees;
    let now = state.window.now_ms;
    let path = w.path.clone();
    let note = ledger.note(&path);
    let busy = matches!(note, Some(Note::Busy(_)));
    let confirming = ledger.confirming.as_deref() == Some(path.as_str());
    let sweep = sweep_word(w, now);
    let mut kicker = vec![sweep.clone()];
    if refreshing(state, &path) {
        kicker.push("refreshing…".into());
    }
    let repository = lines(
        std::iter::once(value(tilde_path(&w.main_repo), t)).chain(
            ledger
                .integration(&w.main_repo)
                .map(|branch| aside(format!("merges into {branch}"), t)),
        ),
    );
    let sha = w.head_sha.as_deref().unwrap_or_default();
    let short = sha.get(..12).unwrap_or(sha);
    let detached = w.detached.unwrap_or_default();
    let branch = lines(
        std::iter::once(value(
            &if detached {
                format!("detached at {short}")
            } else if w.branch.is_empty() {
                "—".into()
            } else {
                w.branch.clone()
            },
            t,
        ))
        .chain((!detached && !sha.is_empty()).then(|| aside(short.to_owned(), t))),
    );
    let words = state_words(w);
    let observed = match w.observed_at.as_deref().and_then(epoch_ms) {
        Some(at) => format!("observed {}", relative_stamp(at, now)),
        None => "not refreshed yet".into(),
    };
    let state_lines = lines([
        value(
            &if words.is_empty() {
                "clean".into()
            } else {
                words.join("  ·  ")
            },
            t,
        ),
        aside(&observed, t),
    ]);
    let activity = w.last_activity_at.as_deref().and_then(epoch_ms).map(|at| {
        value_with(
            &full_stamp(at),
            &format!("({})", relative_stamp(at, now)),
            t,
        )
    });
    let reason = w
        .sweep_reason
        .as_deref()
        .filter(|r| !r.is_empty())
        .map(|r| {
            aside(name_ids(r, |id| session_label(state, id)), t)
                .key("ledger.inspector.sweep-reason")
        });
    let kept_since = w
        .pinned
        .unwrap_or_default()
        .then(|| w.pinned_at.as_deref().and_then(epoch_ms))
        .flatten()
        .map(|at| aside(format!("kept since {}", full_stamp(at)), t));
    let sweep_lines = lines(
        std::iter::once(value(&sweep, t))
            .chain(reason)
            .chain(kept_since),
    );
    let live = live_sessions(state, &path);
    let none_live = live.is_empty().then(|| aside("none live here", t));
    let led_by_link = none_live.is_none();
    let sessions = lines(
        none_live
            .into_iter()
            .chain(live.into_iter().map(|s| {
                let (p, id, name) = (path.clone(), s.id.clone(), s.name.clone());
                link(
                    &format!("ledger.inspector.session.{}", s.id),
                    &s.name,
                    t,
                    move |state: &mut State, tx: &mut Tx| {
                        super::run(
                            state,
                            &p,
                            Verb::GoTo {
                                id: id.clone(),
                                name: name.clone(),
                            },
                            tx,
                        )
                    },
                )
            }))
            .chain(std::iter::once({
                let p = path.clone();
                link(
                    "ledger.inspector.sessions-here",
                    "every session that ran here →",
                    t,
                    move |state: &mut State, tx: &mut Tx| {
                        super::run(state, &p, Verb::SessionsHere, tx)
                    },
                )
            })),
    );
    let actions: Vec<Verb> = if confirming {
        vec![
            Verb::DeleteNow {
                losing_changes: w.dirty.unwrap_or_default(),
            },
            Verb::Cancel,
        ]
    } else {
        vec![Verb::Keep(!w.pinned.unwrap_or_default()), Verb::Delete]
    };
    let actions = Inline::tight().children(actions.into_iter().enumerate().map(|(at, verb)| {
        let words = match (&verb, note) {
            (Verb::Keep(_), Some(Note::Busy(busy))) => busy.clone(),
            _ => verb.label(),
        };
        let look = match verb {
            Verb::Delete | Verb::DeleteNow { .. } => Look::Danger,
            Verb::Keep(_) => Look::Primary,
            _ => Look::Quiet,
        };
        let el = verb_button(&format!("ledger.inspector.action.{at}"), words, look, t)
            .label(verb.label());
        if busy && verb != Verb::Cancel {
            el.a11y_value("disabled").opacity(0.5)
        } else {
            let p = path.clone();
            el.on_press_tx(move |state: &mut State, tx: &mut Tx| {
                super::run(state, &p, verb.clone(), tx)
            })
        }
    }));
    let mut facts = Facts::new("ledger.inspector.facts")
        .control(
            "ledger.inspector.path",
            "Path",
            copy_path(&path, copied == Some(path.as_str()), t),
        )
        .item("ledger.inspector.repository", "Repository", repository)
        .item("ledger.inspector.branch", "Branch", branch)
        .item("ledger.inspector.state", "State", state_lines);
    if let Some(activity) = activity {
        facts = facts.item("ledger.inspector.last-activity", "Last activity", activity);
    }
    facts = facts.item("ledger.inspector.sweep", "Sweep", sweep_lines);
    facts = if led_by_link {
        facts.control("ledger.inspector.sessions", "Sessions", sessions)
    } else {
        facts.item("ledger.inspector.sessions", "Sessions", sessions)
    };
    note_line("ledger.inspector.note", note, t)
        .into_iter()
        .chain([actions])
        .fold(
            Inspector::new("ledger.inspector", base_name(&path).to_owned())
                .kicker(kicker_line(glyph(state, w), &kicker.join(" · "), t))
                .block(facts.build(t)),
            Inspector::push,
        )
        .build(t)
}

pub(crate) fn status(state: &State, t: &Tokens) -> Vec<El> {
    let ledger = &state.window.ledger.worktrees;
    let mut counts = Vec::new();
    match ledger.scope {
        Scope::Active => {
            let rows = super::active_rows(state);
            let shown = rows.len();
            let kept = rows.iter().filter(|w| w.pinned.unwrap_or_default()).count();
            let dirty = rows.iter().filter(|w| w.dirty.unwrap_or_default()).count();
            let scheduled = rows
                .iter()
                .filter(|w| {
                    w.sweep_status.as_deref() == Some("scheduled") && !w.pinned.unwrap_or_default()
                })
                .count();
            counts.push(format!(
                "{shown} {}",
                if shown == 1 { "worktree" } else { "worktrees" }
            ));
            for (n, words) in [(kept, "kept"), (dirty, "dirty"), (scheduled, "scheduled")] {
                if n > 0 {
                    counts.push(format!("{n} {words}"));
                }
            }
            let hidden = ledger.all().len() - shown;
            if hidden > 0 {
                counts.push(format!("{hidden} hidden by the query"));
            }
        }
        Scope::Removed => counts.push(format!("{} removed", super::removed_rows(state).len())),
    }
    let mut out = vec![status_text(counts.join("  ·  "), t.text_tertiary).key("ledger.counts")];
    let asked = match ledger.refresh() {
        Some(Refresh::Asked(at)) => {
            format!(
                "refresh · asked {}",
                relative_stamp(*at, state.window.now_ms)
            )
        }
        _ => "refresh".into(),
    };
    out.push(
        status_link("ledger.refresh", asked, t)
            .on_press_tx(|state: &mut State, tx: &mut Tx| super::refresh(state, tx)),
    );
    let error = ledger
        .error()
        .map(str::to_owned)
        .or(match ledger.refresh() {
            Some(Refresh::Refused(why)) => Some(why.clone()),
            _ => None,
        });
    if let Some(error) = error {
        out.push(
            status_text(error.clone(), t.error)
                .key("ledger.error")
                .role("alert")
                .label(error),
        );
    }
    out
}

fn open_menu(state: &mut State, path: &str) {
    super::select(state, path);
    let ledger = &mut state.window.ledger.worktrees;
    ledger.menu = Some(path.to_owned());
}

/// The Worktrees list's own keys, outside the query field. Escape and the keys every list
/// shares stay with the ledger.
pub(crate) fn panel_key(state: &mut State, ev: &KeyEvent, tx: &mut Tx) -> bool {
    let chosen = super::selected_key(state);
    let ledger = &state.window.ledger.worktrees;
    let worktree = chosen
        .as_ref()
        .and_then(|k| ledger.all().iter().find(|w| w.path == *k))
        .filter(|_| ledger.scope == Scope::Active)
        .cloned();
    let yank = match ledger.scope {
        Scope::Active => worktree.as_ref().map(|w| w.path.clone()),
        Scope::Removed => chosen
            .as_ref()
            .and_then(|k| ledger.sweep_log().iter().find(|e| e.id == *k))
            .map(|e| e.path.clone()),
    };
    let verb_count = worktree.as_ref().map_or(0, |w| verbs(state, w).len());
    let path = worktree.map(|w| w.path);
    match &ev.key {
        Key::Down => super::step(state, 1),
        Key::Up => super::step(state, -1),
        Key::Home => super::step(state, isize::MIN / 2),
        Key::End => super::step(state, isize::MAX / 2),
        Key::Enter => match path {
            Some(p) => super::run_nth(state, &p, 0, tx),
            None => return false,
        },
        Key::Right => match path {
            Some(p) if verb_count > 1 => open_menu(state, &p),
            _ => return false,
        },
        Key::Text(typed) => match (typed.as_str(), path) {
            ("j", _) => super::step(state, 1),
            ("k", _) => super::step(state, -1),
            (".", Some(p)) if verb_count > 1 => open_menu(state, &p),
            ("y", _) if yank.is_some() => super::super::copy(state, yank.unwrap_or_default(), tx),
            (digit, Some(p)) if digit.len() == 1 && ("1"..="9").contains(&digit) => {
                let index = usize::from(digit.as_bytes()[0] - b'1');
                super::run_nth(state, &p, index, tx);
            }
            _ => return false,
        },
        _ => return false,
    }
    true
}

/// Backs out of a waiting delete.
pub(crate) fn back_out(state: &mut State) -> bool {
    let ledger = &mut state.window.ledger.worktrees;
    ledger.confirming.take().is_some()
}

fn query_key(state: &mut State, ev: &KeyEvent, tx: &mut Tx) -> bool {
    if ev.key != Key::Escape {
        return false;
    }
    let ledger = &mut state.window.ledger.worktrees;
    if ledger.query.text.is_empty() {
        tx.focus(LIST, false);
    } else {
        ledger.query = rune::TextEdit::default();
        super::query_edited(state);
        tx.focus(QUERY, false);
    }
    true
}
