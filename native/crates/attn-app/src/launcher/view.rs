use attn_ui::{Tokens, icon};
use rune::tok::{space, type_};
use rune::{El, Tx, col, row, text};
use rune_kit::TextInput;

use super::{Kind, Launcher, Listing, Memory, Mode, Row, WhereRow, mode, remembered, split_browse};
use crate::State;
use crate::picker::{TERMINAL, Which, controls, desktop_label, footer, paths, section};

/// Launcher 2.0's body, which the dialog and the empty desktop each frame.
pub(crate) fn frame(state: &State, launcher: &Launcher, t: &Tokens) -> El {
    let which = launcher.which;
    let terminal = launcher.choice.agent(state) == TERMINAL;
    let body = match &launcher.place {
        Some(place) => where_list(state, launcher, place, t),
        None => locations(state, launcher, terminal, t),
    };
    col()
        .gap(space::S3)
        .on_key(move |state: &mut State, ev: &rune::KeyEvent, tx: &mut Tx| {
            super::on_key(state, which, ev, tx)
        })
        .child(controls(state, which, &launcher.choice, t))
        .child(header(state, launcher, terminal, t))
        .child(body)
        .children(launcher.problem.as_ref().map(|problem| {
            text(problem.clone())
                .key(format!("{}.error", which.key()))
                .role("alert")
                .label(problem.clone())
                .font(type_::BODY)
                .color(t.error)
        }))
        .children(launcher.busy.then(|| {
            text("Starting…")
                .key(format!("{}.progress", which.key()))
                .role("status")
                .label("Starting…")
                .font(type_::LABEL)
                .color(t.text_secondary)
        }))
        .child(hints(state, launcher, terminal, t))
}

fn header(state: &State, launcher: &Launcher, terminal: bool, t: &Tokens) -> El {
    let which = launcher.which;
    let w = which.key();
    let home = &state.window.launching.home;
    let title = match (terminal, which) {
        (true, _) => "New terminal".to_owned(),
        (false, Which::Dialog) => "New agent".to_owned(),
        (false, Which::Inline) => format!(
            "New agent on {}",
            desktop_label(state, launcher.desktop.as_ref())
        ),
    };
    let (edit, placeholder) = match &launcher.place {
        Some(place) => (&place.branch, "name a new worktree, or pick below"),
        None => (&launcher.query, "Project or path"),
    };
    let ghost = launcher
        .place
        .is_none()
        .then(|| ghost(state, launcher))
        .flatten();
    let chip = launcher.place.as_ref().map(|place| {
        text(format!("{} ›", paths::basename(&place.repo)))
            .key(format!("{w}.repo-chip"))
            .font(type_::BODY)
            .color(t.text_secondary)
    });
    let field = row()
        .w_full()
        .items_center()
        .gap(space::S1)
        .children(chip)
        .child(
            TextInput::new(edit)
                .key(launcher.field_key())
                .plain()
                .text_style(type_::BODY)
                .height(type_::BODY.line_px())
                .width(None)
                .label("Path")
                .placeholder(placeholder)
                .on_edit(move |state: &mut State| super::field_mut(state, which))
                .on_change(move |state: &mut State, tx: &mut Tx| super::edited(state, which, tx))
                .on_key(move |state: &mut State, ev: &rune::KeyEvent, tx: &mut Tx| {
                    super::on_key(state, which, ev, tx)
                })
                .build(attn_ui::kit(t))
                .grow(1.0),
        )
        .children(ghost.map(|g| {
            text(g)
                .key(format!("{w}.ghost"))
                .font(type_::BODY)
                .color(t.text_muted)
        }));
    let breadcrumb = match (&launcher.place, mode(&launcher.query.text)) {
        (None, Mode::Browse(path)) => match launcher.listing(&split_browse(path).0, home) {
            Some(Listing::Listed { directory, .. }) => Some(paths::display(directory, home)),
            _ => None,
        },
        _ => None,
    };
    col()
        .gap(space::S1)
        .child(
            text(title.clone())
                .key(format!("{w}.title"))
                .role("heading")
                .label(title)
                .font(type_::TITLE)
                .color(t.text_primary),
        )
        .child(field)
        .children(breadcrumb.map(|dir| {
            text(dir.clone())
                .key(format!("{w}.breadcrumb"))
                .label(dir)
                .font(type_::LABEL)
                .color(t.text_tertiary)
        }))
}

/// What Tab would complete the typed path to, past what is typed: the highlighted folder,
/// or the first one while none is highlighted.
fn ghost(state: &State, launcher: &Launcher) -> Option<String> {
    let Mode::Browse(_) = mode(&launcher.query.text) else {
        return None;
    };
    let rows = launcher.rows(state);
    let row = match launcher.selected(&rows) {
        Some(i) => rows.get(i).filter(|r| r.kind == Kind::Folder),
        None => rows.iter().find(|r| r.kind == Kind::Folder),
    }?;
    let full = format!(
        "{}/",
        paths::display(&row.path, &state.window.launching.home).trim_end_matches('/')
    );
    let typed = &launcher.query.text;
    paths::starts_with_ignoring_case(&full, typed)
        .then(|| full.get(typed.len()..).map(str::to_owned))
        .flatten()
        .filter(|rest| !rest.is_empty())
}

fn locations(state: &State, launcher: &Launcher, terminal: bool, t: &Tokens) -> El {
    let w = launcher.which.key();
    let home = &state.window.launching.home;
    let rows = launcher.rows(state);
    let selected = launcher.selected(&rows);
    let browsing = mode(&launcher.query.text);
    let folder_title = match browsing {
        Mode::Browse(path) => match launcher.listing(&split_browse(path).0, home) {
            Some(Listing::Listed { directory, .. }) => format!(
                "Folders in {}/",
                paths::display(directory, home).trim_end_matches('/')
            ),
            _ => "Folders".to_owned(),
        },
        Mode::Search(_) => String::new(),
    };
    let mut children = Vec::new();
    for (i, r) in rows.iter().enumerate() {
        if i == 0 || rows[i - 1].kind != r.kind {
            let title = match r.kind {
                Kind::Recent => "Recent".to_owned(),
                Kind::Folder => folder_title.clone(),
            };
            children.push(section(&title, t).key(format!("{w}.section.{i}")));
        }
        children.push(location_row(
            state,
            launcher,
            i,
            r,
            selected == Some(i),
            terminal,
            t,
        ));
    }
    if rows.is_empty() {
        let empty = match browsing {
            Mode::Search(_) => Some("No recent project matches. Type ~/ to browse folders."),
            Mode::Browse(path) => matches!(
                launcher.listing(&split_browse(path).0, home),
                Some(Listing::Listed { .. })
            )
            .then_some("No folders match. Enter uses the path as typed."),
        };
        children.extend(empty.map(|message| {
            text(message)
                .key(format!("{w}.empty"))
                .label(message)
                .font(type_::BODY)
                .color(t.text_tertiary)
        }));
    }
    col()
        .key(format!("{w}.locations"))
        .role("listbox")
        .label("Locations")
        .w_full()
        .max_h(360.0)
        .scroll(rune::ScrollSpec {
            stick_bottom: false,
        })
        .gap(2.0)
        .children(children)
}

fn location_row(
    state: &State,
    launcher: &Launcher,
    i: usize,
    r: &Row,
    highlighted: bool,
    terminal: bool,
    t: &Tokens,
) -> El {
    let which = launcher.which;
    let home = &state.window.launching.home;
    let name = match r.kind {
        Kind::Folder => format!("{}/", r.name),
        Kind::Recent => r.name.clone(),
    };
    let shown = (r.kind == Kind::Recent).then(|| paths::display(&r.path, home));
    let badge = r.repo_root.as_ref().and_then(|root| {
        if !terminal && (r.kind == Kind::Recent || highlighted) {
            Some(match remembered(state, root) {
                Memory::NewWorktree => "⎇ new worktree",
                Memory::MainRepo => "main checkout",
            })
        } else {
            (r.kind == Kind::Folder).then_some("repo")
        }
    });
    let age = r
        .last_seen
        .as_deref()
        .map(|seen| age(seen, state.window.now_ms));
    let picked = r.clone();
    let mut option = row()
        .key(format!("{}.row.{i}", which.key()))
        .role("option")
        .label(name.clone())
        .focus_selected(highlighted)
        .w_full()
        .items_center()
        .gap(space::S2)
        .padding(space::S1)
        .radius(4.0)
        .fill(if highlighted {
            t.bg_selected
        } else {
            t.bg_elevated
        })
        .on_press_tx(move |state: &mut State, tx: &mut Tx| {
            super::pick(state, which, i, picked.clone(), tx)
        })
        .child(
            text(match r.kind {
                Kind::Recent => icon::CLOCK,
                Kind::Folder => icon::FOLDER,
            })
            .font(type_::BODY),
        )
        .child(
            col()
                .grow(1.0)
                .shrink()
                .min_w(0.0)
                .child(text(name).font(type_::BODY).color(t.text_primary))
                .children(
                    shown
                        .clone()
                        .map(|s| text(s).font(type_::LABEL).color(t.text_tertiary)),
                ),
        )
        .children(badge.map(|b| {
            text(b)
                .key(format!("{}.row.{i}.badge", which.key()))
                .label(b)
                .font(type_::LABEL)
                .color(t.text_secondary)
        }))
        .children(
            age.filter(|a| !a.is_empty())
                .map(|a| text(a).font(type_::LABEL).color(t.text_tertiary)),
        );
    if let Some(shown) = shown {
        option = option.description(shown);
    }
    option
}

fn where_list(state: &State, launcher: &Launcher, place: &super::Where, t: &Tokens) -> El {
    let which = launcher.which;
    let w = which.key();
    let home = &state.window.launching.home;
    if place.info.is_none() {
        let reading = format!("Reading {}…", paths::basename(&place.repo));
        return col()
            .key(format!("{w}.where"))
            .children(launcher.problem.is_none().then(|| {
                text(reading.clone())
                    .key(format!("{w}.reading"))
                    .role("status")
                    .label(reading)
                    .font(type_::BODY)
                    .color(t.text_tertiary)
            }));
    }
    let rows = place.rows();
    let name = launcher.worktree_name();
    let typed = !place.branch.text.trim().is_empty();
    let memory = remembered(state, &place.repo);
    let mut children = Vec::new();
    for (i, r) in rows.iter().enumerate() {
        let after_new = i > 0 && matches!(rows[i - 1], WhereRow::New { .. });
        if i == 0 {
            let title = if typed {
                format!("New worktree · {name}")
            } else {
                format!("New worktree · {name} · type to rename")
            };
            children.push(section(&title, t).key(format!("{w}.where.new")));
        } else if after_new && !matches!(r, WhereRow::New { .. }) {
            children.push(section("Open existing", t).key(format!("{w}.where.existing")));
        }
        children.push(where_row(
            launcher,
            i,
            r,
            place.highlight == Some(i),
            memory,
            &place.repo,
            home,
            state.window.now_ms,
            t,
        ));
    }
    col()
        .key(format!("{w}.where"))
        .role("listbox")
        .label("Where")
        .w_full()
        .max_h(360.0)
        .scroll(rune::ScrollSpec {
            stick_bottom: false,
        })
        .gap(2.0)
        .children(children)
}

#[allow(clippy::too_many_arguments)]
fn where_row(
    launcher: &Launcher,
    i: usize,
    r: &WhereRow,
    highlighted: bool,
    memory: Memory,
    repo: &str,
    home: &str,
    now_ms: i64,
    t: &Tokens,
) -> El {
    let which = launcher.which;
    let deleting = launcher
        .deletion
        .as_ref()
        .filter(|d| matches!(r, WhereRow::Existing { path, .. } if *path == d.path));
    let (name, detail) = match (r, deleting) {
        (WhereRow::Existing { branch, .. }, Some(d)) if d.force => (
            format!("{branch} has uncommitted changes"),
            "Force delete local worktree and branch? (y/n)".to_owned(),
        ),
        (WhereRow::Existing { branch, path, .. }, Some(_)) => (
            format!("Delete {branch}?"),
            format!("{} (y/n)", paths::display(path, home)),
        ),
        (WhereRow::New { current, branch }, _) => (
            format!("from {branch}"),
            if *current {
                "the branch the main checkout is on".to_owned()
            } else {
                "the default branch".to_owned()
            },
        ),
        (WhereRow::Main { branch }, _) => (
            "main checkout".to_owned(),
            format!("{} · on {branch}", paths::display(repo, home)),
        ),
        (WhereRow::Existing { branch, path, .. }, None) => {
            (branch.clone(), paths::display(path, home))
        }
    };
    let badge = match r {
        WhereRow::Main { .. } if memory == Memory::MainRepo => Some("last used".to_owned()),
        WhereRow::New { current: false, .. } if memory == Memory::NewWorktree => {
            Some("last used".to_owned())
        }
        WhereRow::Existing { .. } if deleting.is_some() => Some("y / n".to_owned()),
        WhereRow::Existing { created_at, .. } => created_at.as_deref().map(|at| age(at, now_ms)),
        _ => None,
    };
    let glyph = match (r, deleting) {
        (_, Some(_)) => icon::TRASH,
        (WhereRow::New { .. }, _) => icon::MERGE,
        (WhereRow::Main { .. }, _) => icon::CIRCLE,
        (WhereRow::Existing { .. }, _) => icon::CIRCLE_DOT,
    };
    let picked = r.clone();
    row()
        .key(format!("{}.where.row.{i}", which.key()))
        .role("option")
        .label(name.clone())
        .description(detail.clone())
        .focus_selected(highlighted)
        .w_full()
        .items_center()
        .gap(space::S2)
        .padding(space::S1)
        .radius(4.0)
        .fill(if highlighted {
            t.bg_selected
        } else {
            t.bg_elevated
        })
        .on_press_tx(move |state: &mut State, tx: &mut Tx| {
            super::pick_destination(state, which, i, picked.clone(), tx)
        })
        .child(text(glyph).font(type_::BODY))
        .child(
            col()
                .grow(1.0)
                .shrink()
                .min_w(0.0)
                .child(text(name).font(type_::BODY).color(t.text_primary))
                .child(text(detail).font(type_::LABEL).color(t.text_tertiary)),
        )
        .children(
            badge
                .filter(|b| !b.is_empty())
                .map(|b| text(b).font(type_::LABEL).color(t.text_secondary)),
        )
}

fn hints(state: &State, launcher: &Launcher, terminal: bool, t: &Tokens) -> El {
    let accel = if rune::KeyPlatform::current() == rune::KeyPlatform::Mac {
        "⌘"
    } else {
        "Ctrl+"
    };
    let hints: Vec<String> = if launcher.busy {
        Vec::new()
    } else if launcher.deletion.is_some() {
        vec!["y delete".into(), "n or Esc keep it".into()]
    } else {
        let mut hints = Vec::new();
        let place = launcher.place.as_ref();
        if place.is_some() {
            hints.push("type to name it".to_owned());
        }
        hints.push(format!(
            "↑↓ {}",
            if place.is_some() { "choose" } else { "move" }
        ));
        hints.push(format!(
            "Enter {}",
            if place.is_none() && terminal {
                "open terminal"
            } else {
                "start"
            }
        ));
        if place.is_none() {
            let rows = launcher.rows(state);
            let selected = launcher.selected(&rows).and_then(|i| rows.get(i));
            let typed_repo = selected.is_none()
                && match mode(&launcher.query.text) {
                    Mode::Browse(path) => {
                        launcher.known_repo(path, &state.window.launching.home) == Some(true)
                    }
                    Mode::Search(_) => false,
                };
            let where_next =
                !terminal && (selected.is_some_and(|r| r.repo_root.is_some()) || typed_repo);
            hints.push(format!(
                "Tab {}",
                if where_next { "where…" } else { "complete" }
            ));
        }
        if place.is_some() || !terminal {
            hints.push(format!("{accel}Enter terminal here"));
        }
        if let Some(place) = place
            && matches!(
                place.highlight.and_then(|i| place.rows().get(i).cloned()),
                Some(WhereRow::Existing { .. })
            )
        {
            hints.push(format!("{accel}⌫ delete"));
        }
        hints.push(format!(
            "Esc {}",
            if place.is_some() { "back" } else { "close" }
        ));
        hints
    };
    let hints: Vec<&str> = hints.iter().map(String::as_str).collect();
    footer(&hints, t).key(format!("{}.hints", launcher.which.key()))
}

/// How long ago an RFC 3339 time was, as the launcher says it: now, then minutes, hours, days.
fn age(time: &str, now_ms: i64) -> String {
    let Some(then) = crate::calendar::seconds_of_rfc3339(time) else {
        return String::new();
    };
    let minutes = ((now_ms as f64 / 1000.0 - then) / 60.0).floor().max(0.0) as i64;
    match minutes {
        0 => "now".into(),
        m if m < 60 => format!("{m}m"),
        m if m < 60 * 24 => format!("{}h", m / 60),
        m => format!("{}d", m / 60 / 24),
    }
}
