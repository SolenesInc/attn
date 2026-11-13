use attn_protocol::{Worktree, WorktreeSweepEntry};
use attn_ui::Tokens;
use rune::tok::{space, type_};
use rune::{El, Key, KeyEvent, Tx, col, row, text, text_input};

use super::super::query::{
    base_name, full_stamp, name_ids, relative_stamp, tilde_path, until_stamp,
};
use super::super::view::{INSPECTOR_WIDTH, body, button, chip_button, label, muted, title};
use super::super::{LIST, Note, QUERY};
use super::{Refresh, Scope, Verb, live_sessions, refreshing, verbs};
use crate::State;
use crate::sidebar::queue::epoch_ms;

pub(crate) fn toolbar(state: &State, t: &Tokens) -> El {
    let ledger = &state.window.ledger.worktrees;
    let field = text_input(&ledger.query, type_::MONO_CAPTION)
        .key(QUERY)
        .single_line()
        .label("Filter")
        .placeholder("repo:attn  branch  dirty  merged  words", t.text_muted)
        .on_edit(|state: &mut State| &mut state.window.ledger.worktrees.query)
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
            row()
                .role("radiogroup")
                .label("Which worktrees")
                .gap(2.0)
                .children(Scope::ALL.map(|scope| {
                    chip_button(
                        &format!("ledger.scope.{}", scope.label().to_lowercase()),
                        scope.label(),
                        scope == ledger.scope,
                        t,
                    )
                    .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                        super::set_scope(state, scope, tx)
                    })
                })),
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

pub(crate) fn list(state: &State, t: &Tokens) -> El {
    let ledger = &state.window.ledger.worktrees;
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
    let chosen = super::selected_key(state);
    let mut out: Vec<El> = Vec::new();
    let mut group: Option<&str> = None;
    match ledger.scope {
        Scope::Active => {
            for w in super::active_rows(state) {
                if group != Some(w.main_repo.as_str()) {
                    out.push(group_heading(state, &w.main_repo, t));
                    group = Some(&w.main_repo);
                }
                out.push(worktree_row(
                    state,
                    w,
                    chosen.as_deref() == Some(&w.path),
                    t,
                ));
            }
        }
        Scope::Removed => {
            for e in super::removed_rows(state) {
                if group != Some(e.main_repo.as_str()) {
                    out.push(group_heading(state, &e.main_repo, t));
                    group = Some(&e.main_repo);
                }
                out.push(swept_row(state, e, chosen.as_deref() == Some(&e.id), t));
            }
        }
    }
    if out.is_empty() {
        let words = empty_words(state);
        return list.child(
            text(words)
                .key("ledger.empty")
                .role("note")
                .label(words)
                .font(type_::BODY)
                .color(t.text_tertiary)
                .wrap()
                .padding(space::S6),
        );
    }
    list.children(out)
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
    row()
        .key(format!("ledger.group.{main_repo}"))
        .role("heading")
        .label(base_name(main_repo).to_owned())
        .w_full()
        .items_center()
        .gap(space::S2)
        .px(space::S3)
        .pt(space::S2)
        .child(
            text(base_name(main_repo).to_owned())
                .font(type_::LABEL)
                .color(t.text_secondary),
        )
        .child(
            text(meta.join("  ·  "))
                .key(format!("ledger.group.{main_repo}.meta"))
                .font(type_::MONO_CAPTION)
                .color(t.text_tertiary)
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
    meta.push(sweep_word(w, state.epoch_ms));
    if refreshing(state, &w.path) {
        meta.push("refreshing…".into());
    }
    if let Some(at) = w.last_activity_at.as_deref().and_then(epoch_ms) {
        meta.push(format!("idle {}", relative_stamp(at, state.epoch_ms)));
    }
    meta
}

fn note_line(key: &str, note: Option<&Note>, t: &Tokens) -> Option<El> {
    match note {
        Some(Note::Refused(words)) => Some(
            text(words.clone())
                .key(key.to_owned())
                .role("status")
                .label(words.clone())
                .font(type_::LABEL)
                .color(t.warning)
                .wrap(),
        ),
        _ => None,
    }
}

fn worktree_row(state: &State, w: &Worktree, selected: bool, t: &Tokens) -> El {
    let ledger = &state.window.ledger.worktrees;
    let path = w.path.clone();
    let note = ledger.note(&path);
    let busy = matches!(note, Some(Note::Busy(_)));
    let verbs = verbs(state, w);
    let menu_open = ledger.menu.as_deref() == Some(path.as_str());
    let name = base_name(&path).to_owned();
    let primary = verbs.first().map(|verb| {
        let (p, verb) = (path.clone(), verb.clone());
        let words = match note {
            Some(Note::Busy(busy)) => busy.clone(),
            _ => verb.label(),
        };
        button(&format!("ledger.row.{path}.verb"), &words, t).on_press_tx(
            move |state: &mut State, tx: &mut Tx| super::run(state, &p, verb.clone(), tx),
        )
    });
    let more = (verbs.len() > 1 && !busy).then(|| {
        let p = path.clone();
        button(&format!("ledger.row.{path}.more"), "···", t)
            .label(format!("More for {name}"))
            .a11y_value(if menu_open { "expanded" } else { "" })
            .on_press_tx(move |state: &mut State, _: &mut Tx| toggle_menu(state, &p))
    });
    let menu = menu_open.then(|| verb_menu(&path, &verbs, t));
    let chosen = path.clone();
    row()
        .key(format!("ledger.row.{path}"))
        .role("option")
        .label(name.clone())
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
                    text(name)
                        .font(type_::BODY)
                        .color(t.text_primary)
                        .single_line()
                        .truncate(),
                )
                .child(
                    text(row_meta(state, w).join("  ·  "))
                        .key(format!("ledger.row.{path}.meta"))
                        .font(type_::LABEL)
                        .color(t.text_tertiary)
                        .single_line()
                        .truncate(),
                )
                .children(note_line(&format!("ledger.row.{path}.note"), note, t)),
        )
        .children(primary)
        .children(more)
        .children(menu)
}

fn swept_row(state: &State, e: &WorktreeSweepEntry, selected: bool, t: &Tokens) -> El {
    let mut meta: Vec<String> = e.branch.iter().cloned().collect();
    meta.push(e.action.clone());
    meta.extend(e.reason.iter().cloned());
    if let Some(at) = epoch_ms(&e.at) {
        meta.push(relative_stamp(at, state.epoch_ms));
    }
    let id = e.id.clone();
    row()
        .key(format!("ledger.row.{}", e.id))
        .role("option")
        .label(base_name(&e.path).to_owned())
        .a11y_value(if selected { "selected" } else { "" })
        .w_full()
        .px(space::S3)
        .py(space::S2)
        .opacity(0.6)
        .fill(if selected {
            t.bg_selected
        } else {
            rune::Color::TRANSPARENT
        })
        .hover(t.bg_elevated)
        .on_press_tx(move |state: &mut State, tx: &mut Tx| {
            super::select(state, &id);
            tx.focus(LIST, false);
        })
        .child(
            col()
                .grow(1.0)
                .min_w(0.0)
                .gap(2.0)
                .child(
                    text(base_name(&e.path).to_owned())
                        .font(type_::BODY)
                        .color(t.text_primary)
                        .single_line()
                        .truncate(),
                )
                .child(
                    text(meta.join("  ·  "))
                        .key(format!("ledger.row.{}.meta", e.id))
                        .font(type_::LABEL)
                        .color(t.text_tertiary)
                        .single_line()
                        .truncate(),
                ),
        )
}

fn verb_menu(path: &str, verbs: &[Verb], t: &Tokens) -> El {
    col()
        .key(format!("ledger.row.{path}.menu"))
        .role("menu")
        .gap(2.0)
        .padding(space::S1)
        .radius(6.0)
        .fill(t.bg_elevated)
        .border(1.0, t.border)
        .children(verbs.iter().enumerate().map(|(at, verb)| {
            let (p, verb) = (path.to_owned(), verb.clone());
            text(format!("{}  {}", at + 1, verb.label()))
                .key(format!("ledger.row.{path}.menu.{at}"))
                .role("menuitem")
                .label(verb.label())
                .font(type_::LABEL)
                .color(t.text_primary)
                .hover(t.bg_selected)
                .radius(4.0)
                .px(space::S2)
                .py(2.0)
                .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                    super::run(state, &p, verb.clone(), tx)
                })
        }))
}

fn copy_path(path: &str, copied: bool, t: &Tokens) -> El {
    let p = path.to_owned();
    text(format!(
        "{}{}",
        tilde_path(path),
        if copied { "  copied" } else { "" }
    ))
    .key("ledger.inspector.path")
    .role("button")
    .label("Copy the path (y)")
    .font(type_::MONO_CAPTION)
    .color(t.text_primary)
    .wrap()
    .on_press_tx(move |state: &mut State, tx: &mut Tx| super::super::copy(state, p.clone(), tx))
}

fn group(key: &str, name: &str, t: &Tokens) -> El {
    col()
        .key(format!("ledger.inspector.{key}"))
        .role("group")
        .label(name.to_owned())
        .gap(2.0)
        .child(label(name, t))
}

fn session_label(state: &State, id: &str) -> Option<String> {
    state
        .facts
        .sessions
        .iter()
        .find(|s| s.id == id)
        .map(crate::sidebar::model::display_name)
}

pub(crate) fn inspector(state: &State, t: &Tokens) -> El {
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
    let ledger = &state.window.ledger.worktrees;
    let chosen = super::selected_key(state);
    let copied = state.window.ledger.copied.as_deref();
    match ledger.scope {
        Scope::Active => {
            if let Some(w) = chosen.and_then(|k| ledger.all().iter().find(|w| w.path == k)) {
                return worktree_inspector(state, aside, w, copied, t);
            }
        }
        Scope::Removed => {
            if let Some(e) = chosen.and_then(|k| ledger.sweep_log().iter().find(|e| e.id == k)) {
                let mut aside = aside
                    .child(title(base_name(&e.path), t))
                    .child(muted(&e.action, t))
                    .child(group("path", "Path", t).child(copy_path(
                        &e.path,
                        copied == Some(&e.path),
                        t,
                    )))
                    .child(
                        group("repository", "Repository", t)
                            .child(body(&tilde_path(&e.main_repo), t)),
                    );
                if let Some(branch) = &e.branch {
                    aside = aside.child(group("branch", "Branch", t).child(body(branch, t)));
                }
                if let Some(at) = epoch_ms(&e.at) {
                    aside = aside.child(group("when", "When", t).child(body(&full_stamp(at), t)));
                }
                if let Some(reason) = &e.reason {
                    aside = aside.child(group("why", "Why", t).child(body(reason, t)));
                }
                return aside;
            }
        }
    }
    aside
        .child(title("Nothing selected", t))
        .child(muted("Pick a row to read it here.", t))
}

fn worktree_inspector(
    state: &State,
    aside: El,
    w: &Worktree,
    copied: Option<&str>,
    t: &Tokens,
) -> El {
    let ledger = &state.window.ledger.worktrees;
    let now = state.epoch_ms;
    let path = w.path.clone();
    let note = ledger.note(&path);
    let busy = matches!(note, Some(Note::Busy(_)));
    let confirming = ledger.confirming.as_deref() == Some(path.as_str());
    let sweep = sweep_word(w, now);
    let mut kicker = vec![sweep.clone()];
    if refreshing(state, &path) {
        kicker.push("refreshing…".into());
    }
    let mut repository =
        group("repository", "Repository", t).child(body(&tilde_path(&w.main_repo), t));
    if let Some(branch) = ledger.integration(&w.main_repo) {
        repository = repository.child(muted(&format!("merges into {branch}"), t));
    }
    let sha = w.head_sha.as_deref().unwrap_or_default();
    let short = sha.get(..12).unwrap_or(sha);
    let detached = w.detached.unwrap_or_default();
    let branch = group("branch", "Branch", t)
        .child(body(
            &if detached {
                format!("detached at {short}")
            } else if w.branch.is_empty() {
                "—".into()
            } else {
                w.branch.clone()
            },
            t,
        ))
        .children((!detached && !sha.is_empty()).then(|| muted(short, t)));
    let words = state_words(w);
    let observed = match w.observed_at.as_deref().and_then(epoch_ms) {
        Some(at) => format!("observed {}", relative_stamp(at, now)),
        None => "not refreshed yet".into(),
    };
    let state_group = group("state", "State", t)
        .child(body(
            &if words.is_empty() {
                "clean".into()
            } else {
                words.join("  ·  ")
            },
            t,
        ))
        .child(muted(&observed, t));
    let activity = w.last_activity_at.as_deref().and_then(epoch_ms).map(|at| {
        group("last-activity", "Last activity", t).child(body(
            &format!("{} ({})", full_stamp(at), relative_stamp(at, now)),
            t,
        ))
    });
    let reason = w
        .sweep_reason
        .as_deref()
        .filter(|r| !r.is_empty())
        .map(|r| {
            muted(&name_ids(r, |id| session_label(state, id)), t)
                .key("ledger.inspector.sweep-reason")
        });
    let kept_since = w
        .pinned
        .unwrap_or_default()
        .then(|| w.pinned_at.as_deref().and_then(epoch_ms))
        .flatten()
        .map(|at| muted(&format!("kept since {}", full_stamp(at)), t));
    let sweep_group = group("sweep", "Sweep", t)
        .child(body(&sweep, t))
        .children(reason)
        .children(kept_since);
    let live = live_sessions(state, &path);
    let sessions = group("sessions", "Sessions", t)
        .children(live.is_empty().then(|| muted("none live here", t)))
        .children(live.into_iter().map(|s| {
            let (p, id, name) = (path.clone(), s.id.clone(), s.name.clone());
            button(&format!("ledger.inspector.session.{}", s.id), &s.name, t).on_press_tx(
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
        .child({
            let p = path.clone();
            button(
                "ledger.inspector.sessions-here",
                "every session that ran here →",
                t,
            )
            .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                super::run(state, &p, Verb::SessionsHere, tx)
            })
        });
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
    aside
        .child(title(base_name(&path), t))
        .child(muted(&kicker.join(" · "), t).key("ledger.inspector.kicker"))
        .child(group("path", "Path", t).child(copy_path(&path, copied == Some(path.as_str()), t)))
        .child(repository)
        .child(branch)
        .child(state_group)
        .children(activity)
        .child(sweep_group)
        .child(sessions)
        .children(note_line("ledger.inspector.note", note, t))
        .child(
            row()
                .gap(space::S2)
                .children(actions.into_iter().enumerate().map(|(at, verb)| {
                    let words = match (&verb, note) {
                        (Verb::Keep(_), Some(Note::Busy(busy))) => busy.clone(),
                        _ => verb.label(),
                    };
                    let el = button(&format!("ledger.inspector.action.{at}"), &words, t)
                        .label(verb.label());
                    if busy && verb != Verb::Cancel {
                        el.a11y_value("disabled").opacity(0.5)
                    } else {
                        let p = path.clone();
                        el.on_press_tx(move |state: &mut State, tx: &mut Tx| {
                            super::run(state, &p, verb.clone(), tx)
                        })
                    }
                })),
        )
}

pub(crate) fn status(state: &State, t: &Tokens) -> Vec<El> {
    let ledger = &state.window.ledger.worktrees;
    let mut parts = Vec::new();
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
            parts.push(format!(
                "{shown} {}",
                if shown == 1 { "worktree" } else { "worktrees" }
            ));
            for (n, words) in [(kept, "kept"), (dirty, "dirty"), (scheduled, "scheduled")] {
                if n > 0 {
                    parts.push(format!("{n} {words}"));
                }
            }
            let hidden = ledger.all().len() - shown;
            if hidden > 0 {
                parts.push(format!("{hidden} hidden by the query"));
            }
        }
        Scope::Removed => parts.push(format!("{} removed", super::removed_rows(state).len())),
    }
    let mut out = vec![
        text(parts.join("  ·  "))
            .key("ledger.counts")
            .font(type_::LABEL)
            .color(t.text_secondary),
    ];
    let asked = match ledger.refresh() {
        Some(Refresh::Asked(at)) => {
            format!("refresh · asked {}", relative_stamp(*at, state.epoch_ms))
        }
        _ => "refresh".into(),
    };
    out.push(
        button("ledger.refresh", &asked, t)
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
            text(error.clone())
                .key("ledger.error")
                .role("alert")
                .label(error)
                .font(type_::LABEL)
                .color(t.error),
        );
    }
    out
}

fn toggle_menu(state: &mut State, path: &str) {
    super::select(state, path);
    let ledger = &mut state.window.ledger.worktrees;
    ledger.menu = match ledger.menu {
        Some(_) => None,
        None => Some(path.to_owned()),
    };
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
    let menu_open = state.window.ledger.worktrees.menu.is_some();
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
            Some(p) if verb_count > 1 && !menu_open => toggle_menu(state, &p),
            _ => return false,
        },
        Key::Left if menu_open => state.window.ledger.worktrees.menu = None,
        Key::Text(typed) => match (typed.as_str(), path) {
            ("j", _) => super::step(state, 1),
            ("k", _) => super::step(state, -1),
            (".", Some(p)) if verb_count > 1 => toggle_menu(state, &p),
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

/// Backs out of the Worktrees list's own layers: a waiting delete, then the verb menu.
pub(crate) fn back_out(state: &mut State) -> bool {
    let ledger = &mut state.window.ledger.worktrees;
    ledger.confirming.take().is_some() || ledger.menu.take().is_some()
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
