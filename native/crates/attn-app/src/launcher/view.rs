use attn_ui::blocks::{Block, Inline, Notice, Prose, Status};
use attn_ui::finder::{self, FinderSize, Hint};
use attn_ui::system::{
    DotShape, DotSize, GroupHead, IconSize, ListRow, Part, Said, Tone, Type, dot, icon, tag, words,
};
use attn_ui::{Tokens, icon};
use rune::{El, Platform, Span, Tx, col, row};
use rune_kit::ScrollBar;
use rune_kit::TextInput;

use super::{
    Address, Kind, Launcher, Listing, Memory, Row, WhereRow, address_of, completion, machine_name,
    machine_of, machine_prefix, memory, offered, outpost, outposts, reachable, split_browse,
};
use crate::State;
use crate::picker::{
    TERMINAL, Which, agent_options, desktop_label, harness_toggle, paths, yolo, yolo_control,
};

fn keycaps(spec: &str) -> Vec<String> {
    rune::Chord::parse(spec)
        .expect("a launcher chord parses")
        .keycaps_on(Platform::current())
}

/// `spec` as this platform writes the chord: `⌥Q` on a Mac, `Alt+Q` elsewhere.
fn combo(spec: &str) -> String {
    let joint = if Platform::current() == Platform::Mac {
        ""
    } else {
        "+"
    };
    keycaps(spec).join(joint)
}

/// The launcher in the box the dialog and the empty desktop show it in.
pub(crate) fn framed(state: &State, launcher: &Launcher, t: &Tokens) -> El {
    finder::framed(t, frame(state, launcher, t), FinderSize::Wide)
}

fn frame(state: &State, launcher: &Launcher, t: &Tokens) -> El {
    let which = launcher.which;
    let w = which.key();
    let terminal = launcher.agent(state) == TERMINAL;
    let body = match &launcher.place {
        Some(place) => where_list(state, launcher, place, t),
        None => locations(state, launcher, terminal, t),
    };
    let mut notices = Vec::new();
    if let Some(problem) = &launcher.problem {
        notices.push(Notice::error(format!("{w}.error"), problem.message.clone()).build(t));
        if let Some(reason) = &problem.reason {
            notices.push(Prose::lead(format!("{w}.error.reason"), reason.clone()).build(t));
        }
    }
    if launcher.busy {
        notices.push(progress(state, launcher, t));
    }
    col()
        .w_full()
        .on_key(move |state: &mut State, ev: &rune::KeyEvent, tx: &mut Tx| {
            super::on_key(state, which, ev, tx)
        })
        .children(
            launcher
                .choosing
                .is_none()
                .then(|| controls(state, launcher, terminal, t)),
        )
        .child(header(state, launcher, terminal, t))
        .child(body)
        .children((!notices.is_empty()).then(|| finder::notices(notices)))
        .child(hints(state, launcher, terminal, t))
}

/// The harness toggle, and YOLO on the same row.
fn controls(state: &State, launcher: &Launcher, terminal: bool, t: &Tokens) -> El {
    let which = launcher.which;
    let offer = launcher.offer(state);
    let toggle = harness_toggle(
        state,
        which,
        agent_options(state, &launcher.choice, &offer),
        launcher.choice.asked,
        t,
    );
    let yolo = (!terminal).then(|| {
        yolo_control(
            which,
            &yolo(state, &launcher.choice, &offer),
            combo("alt+q"),
            t,
        )
    });
    attn_ui::finder::bar(
        t,
        format!("{}.controls", which.key()),
        std::iter::once(toggle).chain(yolo).collect(),
    )
}

/// What a start under way says: where it starts, and how long a stalled outpost has not
/// answered.
fn progress(state: &State, launcher: &Launcher, t: &Tokens) -> Block {
    let host = launcher.starting.as_ref().and_then(|s| s.host.as_deref());
    let mut message = match host {
        Some(_) => format!(
            "Starting on {}…",
            machine_name(&offered(state, &outposts(state)), host)
        ),
        None => "Starting…".to_owned(),
    };
    let stalled = launcher.stalled(state);
    if let (Some(outpost), Some(starting)) = (stalled, &launcher.starting) {
        let since = crate::calendar::seconds_of_rfc3339(&outpost.state_since)
            .map_or(0, |s| (s * 1000.0) as i64);
        let waited = (launcher.now_ms - starting.at_ms.max(since)).max(0);
        message.push_str(&format!(
            " no answer for {}s",
            (waited as f64 / 1000.0).round() as i64
        ));
    }
    let key = format!("{}.progress", launcher.which.key());
    if stalled.is_some() {
        Notice::warn(key, message).build(t)
    } else {
        Status::new(key, message, true).build(t)
    }
}

fn header(state: &State, launcher: &Launcher, terminal: bool, t: &Tokens) -> El {
    let which = launcher.which;
    let w = which.key();
    let outposts = outposts(state);
    let title = match (terminal, which) {
        _ if let Some(chooser) = &launcher.choosing => format!(
            "{} on {}",
            chooser.purpose.title(),
            machine_name(&offered(state, &outposts), None)
        ),
        (true, _) => "New terminal".to_owned(),
        (false, Which::Dialog) => "New agent".to_owned(),
        (false, Which::Inline) => format!(
            "New agent on {}",
            desktop_label(state, launcher.desktop.as_ref())
        ),
    };
    let (edit, placeholder) = match &launcher.place {
        Some(place) => (&place.branch, "name a new worktree, or pick below"),
        None if launcher.choosing.is_some() => (&launcher.query, "Folder"),
        None if outposts.is_empty() => (&launcher.query, "Project or path"),
        None => (&launcher.query, "Project, path, or machine:path"),
    };
    let ghost = launcher
        .place
        .is_none()
        .then(|| ghost(state, launcher))
        .flatten();
    let chip = launcher.place.as_ref().map(|place| {
        finder::chip(
            t,
            format!("{w}.repo-chip"),
            format!("{} ›", paths::basename(&place.repo)),
        )
    });
    let prefix = match launcher.place {
        Some(_) => "",
        None => machine_prefix(&launcher.query.text, &offered(state, &outposts)),
    };
    let accent = (!prefix.is_empty()).then(|| vec![Span::new(0, prefix.len()).color(t.accent)]);
    let mut input = finder::query_field(
        TextInput::new(edit)
            .key(launcher.field_key())
            .plain()
            .width(None)
            .label("Path")
            .placeholder(placeholder)
            .active_descendant(launcher.active_row_key(state))
            .on_edit(move |state: &mut State| super::field_mut(state, which))
            .on_change(move |state: &mut State, tx: &mut Tx| super::edited(state, which, tx))
            .on_key(move |state: &mut State, ev: &rune::KeyEvent, tx: &mut Tx| {
                super::on_key(state, which, ev, tx)
            }),
    );
    if let Some(spans) = accent {
        input = input.spans(spans);
    }
    let field = finder::query_box(
        t,
        chip,
        input.build(attn_ui::kit(t)),
        ghost.map(|g| finder::ghost(format!("{w}.ghost"), &launcher.query.text, g, t.text_muted)),
    );
    let address = launcher.address(state);
    let status = launcher
        .place
        .is_none()
        .then(|| status_line(state, launcher, &address))
        .flatten();
    finder::query(
        t,
        finder::title(t, format!("{w}.title"), &title).label(title.clone()),
        field,
        status.map(|(line, bad)| {
            words(
                line.clone(),
                Type::Mono,
                if bad { t.error } else { t.text_tertiary },
            )
            .key(format!("{w}.status"))
            .role("status")
            .label(line)
            .wrap()
        }),
    )
}

/// A list's section heading.
fn section(key: String, title: &str, t: &Tokens) -> El {
    finder::heading(
        GroupHead::new(key, title.to_owned())
            .section(title.to_owned())
            .heading()
            .build(t),
    )
}

/// A section heading in parts on one line, those marked to give way truncating first, such as
/// the folder in "Folders in ~/a/long/path/ on a machine".
fn section_of(key: String, parts: &[(String, bool)], t: &Tokens) -> El {
    let label = parts
        .iter()
        .map(|(said, _)| said.as_str())
        .collect::<Vec<_>>()
        .join(" ");
    let line = parts.iter().fold(Inline::joined(), |line, (said, gives)| {
        let part = words(said.clone(), Type::Label, t.text_tertiary).single_line();
        line.child(if *gives {
            part.truncate().shrink().min_w(0.0)
        } else {
            part.truncate()
        })
    });
    finder::heading(GroupHead::new(key, label).name_el(line).heading().build(t))
}

/// How tall the list under the field gets before it scrolls: seven rows of two lines.
const LIST_H: f32 = 340.0;

/// The age column: "12mo", the longest age, in `Mono`.
const AGE_W: f32 = 36.0;

/// A row's glyph, in the accent while the row is highlighted.
fn glyph(glyph: &'static rune::Icon, highlighted: bool, t: &Tokens) -> El {
    icon(
        glyph,
        IconSize::Regular,
        if highlighted {
            t.accent
        } else {
            t.text_tertiary
        },
    )
}

/// A row of a launcher's list: its glyph, its name over its detail, and what it says at its
/// end, on the selected surface while it is the keyboard's.
fn list_row(key: String, name: &str, highlighted: bool) -> ListRow {
    ListRow::new(key, name.to_owned())
        .role("option")
        .unfocusable()
        .cursor(highlighted)
        .selected(highlighted)
        .cut_middle()
        .title(name.to_owned())
}

pub(crate) fn quiet(word: String, t: &Tokens) -> El {
    words(word.clone(), Type::Small, t.text_tertiary)
        .label(word)
        .single_line()
}

pub(crate) fn age_column(age: String, t: &Tokens) -> El {
    row()
        .w(AGE_W)
        .justify_end()
        .child(words(age, Type::Mono, t.text_tertiary).single_line())
}

fn locations(state: &State, launcher: &Launcher, terminal: bool, t: &Tokens) -> El {
    let w = launcher.which.key();
    let outposts = outposts(state);
    let rows = launcher.rows(state);
    let selected = launcher.selected(&rows);
    let address = launcher.address(state);
    let browsed = match &address {
        Address::Browse { path, machine, .. } => {
            let host = machine.host.as_deref();
            Some((launcher.listing(state, host, &split_browse(path).0), host))
        }
        _ => None,
    };
    let folder_parts = match browsed {
        Some((Some(Listing::Listed { directory, .. }), host)) => vec![
            ("Folders in".to_owned(), false),
            (
                format!(
                    "{}/",
                    launcher
                        .display(state, directory, host)
                        .trim_end_matches('/')
                ),
                true,
            ),
        ]
        .into_iter()
        .chain(offered(state, &outposts).several().then(|| {
            (
                format!("on {}", machine_name(&offered(state, &outposts), host)),
                false,
            )
        }))
        .collect(),
        _ => vec![("Folders".to_owned(), false)],
    };
    let mut children = Vec::new();
    for (i, r) in rows.iter().enumerate() {
        if i == 0 || rows[i - 1].kind != r.kind {
            let parts = match r.kind {
                Kind::Recent => vec![("Recent".to_owned(), false)],
                Kind::Machine => vec![("Browse a machine".to_owned(), false)],
                Kind::Elsewhere => vec![("Found elsewhere".to_owned(), false)],
                Kind::Folder => folder_parts.clone(),
            };
            children.push(section_of(format!("{w}.section.{i}"), &parts, t));
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
        let empty = match browsed {
            None => Some("No recent project matches. Type ~/ to browse folders."),
            Some((listing, host)) => (matches!(listing, Some(Listing::Listed { .. }))
                || !reachable(outposts, host))
            .then_some("No folders match. Enter uses the path as typed."),
        };
        children.extend(empty.map(|message| finder::empty(t, format!("{w}.empty"), message)));
    }
    finder::grouped(launcher.list_key(), "Locations", children, LIST_H)
        .drawn_rect_into(&launcher.drawn.list)
        .scroll_bar_styled(attn_ui::scroll_bar(t))
}

/// A machine's dot and name, keyed `key`, read as its name and how the home reaches it.
pub(crate) fn machine_tag(
    key: String,
    machines: &crate::machine::Machines,
    host: Option<&str>,
    t: &Tokens,
) -> El {
    let outposts = machines.outposts;
    let reach_word = match host {
        None if machines.on_app_machine(None) => "local".to_owned(),
        None => "reachable".to_owned(),
        Some(h) => {
            outpost(outposts, h).map_or_else(|| "unreachable".to_owned(), |o| o.reach.clone())
        }
    };
    let name = machine_name(machines, host);
    let shown = host.map(|h| outpost(outposts, h).map_or("lost", |o| o.state.as_str()));
    Inline::joined()
        .key(key)
        .label(name.clone())
        .description(reach_word)
        .shrink()
        .child(machine_dot(shown, t))
        .child(quiet(name, t).truncate().shrink().min_w(0.0))
}

/// The dot before a machine's name: green while connected, amber while stalled, red once lost
/// or refused, a ring for this machine and any other state.
fn machine_dot(state: Option<&str>, t: &Tokens) -> El {
    match state {
        Some("connected") => dot(t.success, DotShape::Filled, DotSize::Small),
        Some("stalled") => dot(t.warning, DotShape::Filled, DotSize::Small),
        Some("lost" | "refused") => dot(t.error, DotShape::Filled, DotSize::Small),
        _ => dot(t.text_tertiary, DotShape::Hollow, DotSize::Small),
    }
}

#[allow(clippy::too_many_arguments)]
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
    let w = which.key();
    let outposts = outposts(state);
    let host = r.host.as_deref();
    let name = match r.kind {
        Kind::Folder => format!("{}/", r.name),
        _ => r.name.clone(),
    };
    let detail = match r.kind {
        Kind::Recent => Some(Said::parts([Part::Code(
            launcher.display(state, &r.path, host),
        )])),
        Kind::Machine => Some(Said::parts([
            Part::Text(format!(
                "{} · Tab browses ",
                host.and_then(|h| outpost(outposts, h))
                    .map(|o| o.state.clone())
                    .unwrap_or_default()
            )),
            Part::Code(address_of(
                &machine_of(&offered(state, &outposts), host),
                "~/",
            )),
        ])),
        Kind::Elsewhere => Some(Said::parts([
            Part::Text("Enter browses ".to_owned()),
            Part::Code(address_of(
                &machine_of(&offered(state, &outposts), host),
                &format!("{}/", r.path),
            )),
        ])),
        Kind::Folder => None,
    };
    let badge_key = format!("{w}.row.{i}.badge");
    let badge = r.repo_root.as_ref().and_then(|root| {
        if !terminal && (r.kind == Kind::Recent || highlighted) {
            Some(match memory(state, root, host) {
                Memory::NewWorktree => tag(badge_key.clone(), "new worktree", Tone::Good, t).into(),
                Memory::MainRepo => quiet("main checkout".to_owned(), t).key(badge_key.clone()),
            })
        } else {
            (r.kind == Kind::Folder).then(|| quiet("repo".to_owned(), t).key(badge_key.clone()))
        }
    });
    let reach = host.map(|h| outpost(&outposts, h).map(|o| o.reach.clone()));
    let machine = (matches!(r.kind, Kind::Recent | Kind::Elsewhere)
        && offered(state, &outposts).several())
    .then(|| {
        machine_tag(
            format!("{w}.row.{i}.machine"),
            &offered(state, &outposts),
            host,
            t,
        )
    });
    let unreachable = r.kind != Kind::Machine
        && reach
            .as_ref()
            .is_some_and(|reach| reach.as_deref().is_none_or(|r| r == "unreachable"));
    let age = r
        .last_seen
        .as_deref()
        .map(|seen| age(seen, state.window.now_ms))
        .filter(|a| !a.is_empty());
    let mark = match r.kind {
        Kind::Recent => &icon::RECENT,
        Kind::Folder if r.repo_root.is_some() => &icon::REPO,
        Kind::Folder => &icon::FOLDER,
        Kind::Machine => &icon::MACHINE,
        Kind::Elsewhere => &icon::ELSEWHERE,
    };
    let mut line = list_row(format!("{w}.row.{i}"), &name, highlighted)
        .lead(glyph(mark, highlighted, t))
        .dimmed(unreachable);
    if let Some(detail) = &detail {
        line = line.subtitle(detail.clone(), t);
    }
    for aside in badge.into_iter().chain(machine) {
        line = line.aside(aside);
    }
    if let Some(age) = age {
        line = line.aside(age_column(age, t));
    }
    let picked = r.clone();
    let row_el = line
        .build(t)
        .on_press_tx(move |state: &mut State, tx: &mut Tx| {
            super::pick(state, which, i, picked.clone(), tx)
        });
    let row_el = match detail {
        Some(detail) => row_el.description(detail.text()),
        None => row_el,
    };
    if highlighted {
        row_el.drawn_rect_into(&launcher.drawn.row)
    } else {
        row_el
    }
}

fn where_list(state: &State, launcher: &Launcher, place: &super::Where, t: &Tokens) -> El {
    let which = launcher.which;
    let w = which.key();
    let host = place.host.as_deref();
    if place.info.is_none() {
        let reading = format!("Reading {}…", paths::basename(&place.repo));
        return col()
            .key(format!("{w}.where"))
            .children(launcher.problem.is_none().then(|| {
                finder::notices(vec![
                    Status::new(format!("{w}.reading"), reading, true).build(t),
                ])
            }));
    }
    let rows = place.rows();
    let name = launcher.worktree_name();
    let typed = !place.branch.text.trim().is_empty();
    let memory = memory(state, &place.repo, host);
    let shown = |path: &str| launcher.display(state, path, host);
    let highlighted = place.highlighted();
    let mut children = Vec::new();
    for (i, r) in rows.iter().enumerate() {
        let after_new = i > 0 && matches!(rows[i - 1], WhereRow::New { .. });
        if i == 0 {
            children.push(new_worktree_section(
                format!("{w}.where.new"),
                &name,
                typed,
                t,
            ));
        } else if after_new && !matches!(r, WhereRow::New { .. }) {
            children.push(section(format!("{w}.where.existing"), "Open existing", t));
        }
        children.push(where_row(
            launcher,
            i,
            r,
            highlighted.as_ref() == Some(r),
            memory,
            &shown(&place.repo),
            &shown,
            state.window.now_ms,
            t,
        ));
    }
    finder::grouped(launcher.list_key(), "Where", children, LIST_H)
        .drawn_rect_into(&launcher.drawn.list)
        .scroll_bar_styled(attn_ui::scroll_bar(t))
}

/// "NEW WORKTREE · name · type to rename", the name as typed.
fn new_worktree_section(key: String, name: &str, typed: bool, t: &Tokens) -> El {
    let label = if typed {
        format!("New worktree · {name}")
    } else {
        format!("New worktree · {name} · type to rename")
    };
    let said = Inline::joined()
        .child(words("New worktree", Type::Label, t.text_tertiary).single_line())
        .child(words("·", Type::Small, t.text_tertiary))
        .child(
            words(name.to_owned(), Type::Mono, t.success)
                .single_line()
                .truncate()
                .shrink()
                .min_w(0.0),
        )
        .children((!typed).then(|| {
            words("· type to rename", Type::Small, t.text_tertiary)
                .single_line()
                .truncate()
        }));
    GroupHead::new(key, label).name_el(said).heading().build(t)
}

#[allow(clippy::too_many_arguments)]
fn where_row(
    launcher: &Launcher,
    i: usize,
    r: &WhereRow,
    highlighted: bool,
    memory: Memory,
    repo: &str,
    shown: &dyn Fn(&str) -> String,
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
            format!("{} (y/n)", shown(path)),
        ),
        (WhereRow::New { current, branch }, _) => (
            format!("from {branch}"),
            if *current {
                "the branch the main checkout is on".to_owned()
            } else {
                "the default branch".to_owned()
            },
        ),
        (WhereRow::Main { branch }, _) => {
            ("main checkout".to_owned(), format!("{repo} · on {branch}"))
        }
        (WhereRow::Existing { branch, path, .. }, None) => (branch.clone(), shown(path)),
    };
    let trailing = match r {
        WhereRow::Main { .. } if memory == Memory::MainRepo => {
            Some(quiet("last used".to_owned(), t))
        }
        WhereRow::New { current: false, .. } if memory == Memory::NewWorktree => {
            Some(quiet("last used".to_owned(), t))
        }
        WhereRow::Existing { .. } if deleting.is_some() => Some(quiet("y / n".to_owned(), t)),
        WhereRow::Existing { created_at, .. } => created_at
            .as_deref()
            .map(|at| age(at, now_ms))
            .filter(|a| !a.is_empty())
            .map(|a| age_column(a, t)),
        _ => None,
    };
    let mark = match (r, deleting) {
        (_, Some(_)) => &icon::DELETE,
        (WhereRow::New { .. }, _) => &icon::PLUS,
        (WhereRow::Main { .. }, _) => &icon::MAIN_BRANCH,
        (WhereRow::Existing { .. }, _) => &icon::BRANCH,
    };
    let mut line = list_row(format!("{}.where.row.{i}", which.key()), &name, highlighted)
        .lead(glyph(mark, highlighted, t))
        .subtitle(detail.clone(), t);
    if let Some(trailing) = trailing {
        line = line.aside(trailing);
    }
    let picked = r.clone();
    let row =
        line.build(t)
            .description(detail)
            .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                super::pick_destination(state, which, i, picked.clone(), tx)
            });
    if highlighted {
        row.drawn_rect_into(&launcher.drawn.row)
    } else {
        row
    }
}

fn hints(state: &State, launcher: &Launcher, terminal: bool, t: &Tokens) -> El {
    let accel = |key: &str| {
        let modifier = if Platform::current() == Platform::Mac {
            "⌘"
        } else {
            "Ctrl+"
        };
        format!("{modifier}{key}")
    };
    let mut hints: Vec<Hint> = Vec::new();
    if launcher.stalled(state).is_some() {
        hints.push(Hint::keys("", "Esc", " keep waiting in the background").at_end());
    } else if launcher.busy {
    } else if launcher.choosing.is_some() {
        hints.push(Hint::keys("", "↑↓", " move"));
        hints.push(Hint::keys("", "Enter", " choose"));
        hints.push(Hint::keys("", "Tab", " open"));
        hints.push(Hint::keys("", "Esc", " close").at_end());
    } else if launcher.deletion.is_some() {
        hints.push(Hint::keys("", "y", " delete"));
        hints.push(Hint::pieces(&[
            ("n", true),
            ("or", false),
            ("Esc", true),
            ("keep it", false),
        ]));
    } else {
        let place = launcher.place.as_ref();
        if place.is_some() {
            hints.push(Hint::plain("type to name it"));
        }
        hints.push(Hint::keys(
            "",
            "↑↓",
            if place.is_some() { " choose" } else { " move" },
        ));
        hints.push(Hint::keys(
            "",
            "Enter",
            if place.is_none() && terminal {
                " open terminal"
            } else {
                " start"
            },
        ));
        if place.is_none() {
            let rows = launcher.rows(state);
            let selected = launcher.selected(&rows).and_then(|i| rows.get(i));
            let typed_repo = selected.is_none()
                && match launcher.address(state) {
                    Address::Browse { path, machine, .. } => {
                        launcher.known_repo(state, machine.host.as_deref(), &path) == Some(true)
                    }
                    _ => false,
                };
            let where_next =
                !terminal && (selected.is_some_and(|r| r.repo_root.is_some()) || typed_repo);
            hints.push(Hint::keys(
                "",
                "Tab",
                if where_next { " where…" } else { " complete" },
            ));
        }
        if place.is_some() || !terminal {
            hints.push(Hint::keys("", &accel("Enter"), " terminal here"));
        }
        if place.is_none() && !outposts(state).is_empty() {
            hints.push(Hint::keys("", "name:", " machine"));
        }
        if let Some(place) = place
            && matches!(place.highlighted(), Some(WhereRow::Existing { .. }))
        {
            hints.push(Hint::keys("", &accel("⌫"), " delete"));
        }
        hints
            .push(Hint::keys("", "Esc", if place.is_some() { " back" } else { " close" }).at_end());
    }
    let w = launcher.which.key();
    finder::footer(t, w, hints)
        .key(format!("{w}.hints"))
        .border_sides([1.0, 0.0, 0.0, 0.0], t.border_subtle)
}

/// How the machine the text aims at answers: several machines for one name, an outpost that
/// is slow, unreachable or lacks the folder. Whether it is bad news, too.
fn status_line(state: &State, launcher: &Launcher, address: &Address) -> Option<(String, bool)> {
    let outposts = outposts(state);
    match address {
        Address::Ambiguous { prefix, machines } => Some((
            format!(
                "{} names {} machines · pick one below",
                prefix.trim_end_matches(':'),
                machines.len()
            ),
            true,
        )),
        Address::Browse { path, machine, .. } if machine.host.is_some() => {
            let host = machine.host.as_deref();
            let (dir, _) = split_browse(path);
            let name = &machine.name;
            let listing = launcher.listing(state, host, &dir);
            if !reachable(outposts, host) {
                return Some((
                    match listing {
                        Some(Listing::Listed { at_ms, .. }) => format!(
                            "{name} unreachable · folders as of {}",
                            listed_age(*at_ms, state.window.now_ms.max(launcher.now_ms))
                        ),
                        _ => format!("{name} unreachable · no folders cached for {dir}"),
                    },
                    true,
                ));
            }
            match listing? {
                Listing::Pending { started_ms } => Some((
                    format!(
                        "asking {name} for {dir}/ · {} ms",
                        (launcher.now_ms - started_ms).max(0)
                    ),
                    false,
                )),
                Listing::Missing => Some((format!("{dir} is not on {name}"), true)),
                Listing::Failed(error) => {
                    Some((format!("{name} could not list {dir}: {error}"), true))
                }
                Listing::Listed { .. } => None,
            }
        }
        _ => None,
    }
}

fn listed_age(at_ms: i64, now_ms: i64) -> String {
    let minutes = (now_ms - at_ms).max(0) / 60_000;
    match minutes {
        0 => "just now".into(),
        m if m < 60 => format!("{m}m ago"),
        m if m < 60 * 24 => format!("{}h ago", m / 60),
        m => format!("{}d ago", m / 60 / 24),
    }
}

/// What Tab would complete the text to, past what is typed: while browsing, the highlighted
/// folder or the first one; while searching, the highlighted machine.
fn ghost(state: &State, launcher: &Launcher) -> Option<String> {
    let rows = launcher.rows(state);
    let selected = launcher.selected(&rows).and_then(|i| rows.get(i));
    let row = match launcher.address(state) {
        Address::Browse { .. } => match selected {
            Some(row) => Some(row).filter(|r| r.kind == Kind::Folder),
            None => rows.iter().find(|r| r.kind == Kind::Folder),
        },
        _ => selected.filter(|r| r.kind == Kind::Machine),
    }?;
    let full = completion(launcher, state, row);
    let typed = &launcher.query.text;
    paths::starts_with_ignoring_case(&full, typed)
        .then(|| full.get(typed.len()..).map(str::to_owned))
        .flatten()
        .filter(|rest| !rest.is_empty())
}

/// How long ago an RFC 3339 time was, as the launcher says it: now, then minutes, hours, days.
pub(crate) fn age(time: &str, now_ms: i64) -> String {
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
