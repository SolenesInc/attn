use attn_ui::Tokens;
use rune::tok::{space, type_};
use rune::{El, Tx, col, el, row, text, text_input};

use super::repo::{self, Chooser, Delete, Start, Zone};
use super::{Catalog, Picker, Which, agents, listed, numbered, paths};
use crate::State;
use crate::keys::FIELD_KEY_SUFFIX;

/// The New Session dialog over the app.
pub(crate) fn dialog(state: &State, t: &Tokens) -> Option<El> {
    let picker = state.window.launching.dialog.as_ref()?;
    let frame = frame(state, picker, t)
        .key("new-session")
        .role("dialog")
        .label("New Session Location")
        .modal_tx(false, |state: &mut State, tx: &mut Tx| {
            super::on_key(
                state,
                Which::Dialog,
                &rune::KeyEvent {
                    key: rune::Key::Escape,
                    mods: rune::Mods::default(),
                },
                tx,
            );
        })
        .w_full()
        .max_w(640.0)
        .radius(8.0)
        .fill(t.bg_elevated)
        .border(1.0, t.border)
        .padding(space::S3);
    Some(
        el().key("new-session.layer")
            .absolute(0.0, 0.0)
            .w_full()
            .h_full()
            .child(
                el().key("new-session.scrim")
                    .absolute(0.0, 0.0)
                    .w_full()
                    .h_full()
                    .fill(t.bg_overlay)
                    .a11y_hidden()
                    .on_press_tx(|state: &mut State, tx: &mut Tx| super::close(state, tx)),
            )
            .child(col().w_full().h_full().pt(80.0).items_center().child(frame)),
    )
}

/// The launcher an empty desktop shows in place of its tiles.
pub(crate) fn inline(state: &State, t: &Tokens) -> Option<El> {
    let launching = &state.window.launching;
    if let Some(starting) = launching.shown_starting() {
        let message = format!("Starting {}…", starting.name);
        return Some(
            col().key("launcher").w_full().h_full().center().child(
                text(message.clone())
                    .key("launcher.starting")
                    .role("status")
                    .label(message)
                    .font(type_::BODY)
                    .color(t.text_secondary),
            ),
        );
    }
    let picker = launching.shown_inline()?;
    Some(
        col()
            .key("launcher.area")
            .w_full()
            .h_full()
            .items_center()
            .pt(80.0)
            .child(
                frame(state, picker, t)
                    .key("launcher")
                    .role("group")
                    .label("New agent")
                    .w_full()
                    .max_w(640.0)
                    .radius(8.0)
                    .fill(t.bg_panel)
                    .border(1.0, t.border_subtle)
                    .padding(space::S3),
            ),
    )
}

fn frame(state: &State, picker: &Picker, t: &Tokens) -> El {
    let which = picker.which;
    let body = match &picker.chooser {
        Some(chooser) => chooser_view(picker, chooser, t),
        None => path_view(state, picker, t),
    };
    col()
        .gap(space::S3)
        .on_key(move |state: &mut State, ev: &rune::KeyEvent, tx: &mut Tx| {
            super::on_key(state, which, ev, tx)
        })
        .child(controls(state, picker, t))
        .children(body)
}

fn controls(state: &State, picker: &Picker, t: &Tokens) -> El {
    let which = picker.which;
    let w = which.key();
    let launching = &state.window.launching;
    let agents = picker.agents(state);
    let current = picker.agent(state);
    let numbered = numbered(&agents, &launching.catalog);
    let options = listed(&agents, &launching.catalog)
        .into_iter()
        .map(|agent| {
            let label = agents::label(&agent);
            let availability = agents.of(&agent);
            let selectable = availability != agents::Availability::Unavailable;
            let keys = if !selectable {
                None
            } else if agent == agents::TERMINAL {
                Some("⌥T".to_owned())
            } else {
                numbered
                    .iter()
                    .position(|a| *a == agent)
                    .filter(|n| *n < 9)
                    .map(|n| format!("⌥{}", n + 1))
            };
            let title = match availability {
                agents::Availability::Unavailable => {
                    Some(format!("{label} is not available on this machine"))
                }
                agents::Availability::Checking => {
                    Some(format!("Checking for {label} on this machine"))
                }
                agents::Availability::Available => None,
            };
            let chosen = current == agent;
            let picked = agent.clone();
            let mut option = row()
                .key(format!("{w}.agent.{agent}"))
                .role("radio")
                .label(label.clone())
                .checked(chosen)
                .disabled(!selectable)
                .gap(space::S1)
                .padding(space::S1)
                .radius(4.0)
                .fill(if chosen { t.bg_selected } else { t.bg_panel })
                .on_press_tx(move |state: &mut State, _: &mut Tx| {
                    super::choose_agent(state, which, &picked)
                })
                .child(text(label).font(type_::BODY).color(if selectable {
                    t.text_primary
                } else {
                    t.text_muted
                }));
            if let Some(title) = title {
                option = option.description(title.clone()).help(title);
            }
            if availability == agents::Availability::Checking {
                option = option.child(
                    text("…")
                        .key(format!("{w}.agent.{agent}.checking"))
                        .role("img")
                        .label("checking")
                        .color(t.text_tertiary),
                );
            }
            option.children(keys.map(|k| text(k).font(type_::MONO_CAPTION).color(t.text_tertiary)))
        });
    let catalog_note = match &launching.catalog {
        Catalog::Unasked | Catalog::Loading => Some(
            text("Loading harnesses…")
                .key(format!("{w}.agents.loading"))
                .role("status")
                .label("Loading harnesses…")
                .font(type_::LABEL)
                .color(t.text_tertiary),
        ),
        Catalog::Failed(error) => Some(
            row()
                .key(format!("{w}.agents.error"))
                .role("alert")
                .label(error.clone())
                .gap(space::S1)
                .child(
                    text(error.clone())
                        .font(type_::LABEL)
                        .color(t.text_secondary),
                )
                .child(
                    text("Retry")
                        .key(format!("{w}.agents.retry"))
                        .role("button")
                        .label("Retry")
                        .font(type_::LABEL)
                        .color(t.accent)
                        .on_press_tx(|state: &mut State, _: &mut Tx| super::retry_catalog(state)),
                ),
        ),
        Catalog::Loaded(_) => None,
    };
    let supported = agents::supports_yolo(&state.facts.settings, &current);
    let yolo = super::yolo_on(state, picker);
    let yolo_title = if supported {
        "Run the agent without asking for permission".to_owned()
    } else {
        format!(
            "{} does not support yolo mode",
            agents::yolo_label(&current)
        )
    };
    col()
        .gap(space::S2)
        .child(
            row()
                .key(format!("{w}.agents"))
                .role("radiogroup")
                .label("Session agent")
                .gap(space::S2)
                .items_center()
                .child(section("SESSION AGENT", t))
                .children(options)
                .children(catalog_note),
        )
        .child(
            row()
                .gap(space::S2)
                .items_center()
                .child(section("YOLO", t))
                .child(
                    row()
                        .key(format!("{w}.yolo"))
                        .role("switch")
                        .label("YOLO")
                        .checked(yolo)
                        .disabled(!supported)
                        .description(yolo_title.clone())
                        .help(yolo_title)
                        .gap(space::S1)
                        .on_press_tx(move |state: &mut State, _: &mut Tx| {
                            super::toggle_yolo(state, which)
                        })
                        .child(
                            text(if yolo { "On" } else { "Off" })
                                .font(type_::BODY)
                                .color(t.text_primary),
                        )
                        .children(
                            supported.then(|| {
                                text("⌥Q").font(type_::MONO_CAPTION).color(t.text_tertiary)
                            }),
                        ),
                ),
        )
}

fn path_view(state: &State, picker: &Picker, t: &Tokens) -> Vec<El> {
    let which = picker.which;
    let w = which.key();
    let launching = &state.window.launching;
    let home = &launching.home;
    let title = match which {
        Which::Dialog => "New Session Location".to_owned(),
        Which::Inline => format!("New agent on {}", desktop_label(state, picker)),
    };
    let field_key = picker.field_key();
    debug_assert!(field_key.ends_with(FIELD_KEY_SUFFIX));
    let rows = picker.rows(launching);
    let ghost = picker
        .ghost(&rows)
        .filter(|g| g.starts_with(&picker.input.text))
        .map(|g| g[picker.input.text.len()..].to_owned());
    let field = row()
        .w_full()
        .items_center()
        .child(
            text_input(&picker.input, type_::BODY)
                .key(field_key)
                .single_line()
                .label("Path")
                .placeholder("Type path (e.g., ~/projects) or search...", t.text_muted)
                .on_edit(move |state: &mut State| state.window.launching.input_mut(which))
                .on_input_change(move |state: &mut State, tx: &mut Tx| {
                    super::edited(state, which, tx)
                })
                .on_input_key(move |state: &mut State, ev: &rune::KeyEvent, tx: &mut Tx| {
                    super::on_key(state, which, ev, tx)
                })
                .grow(1.0)
                .color(t.text_primary),
        )
        .children(ghost.map(|g| {
            text(g)
                .key(format!("{w}.ghost"))
                .font(type_::BODY)
                .color(t.text_muted)
        }));
    let mut out = vec![
        text(title.clone())
            .key(format!("{w}.title"))
            .role("heading")
            .label(title)
            .font(type_::TITLE)
            .color(t.text_primary),
        field,
    ];
    if let Some(dir) = &picker.browsing {
        out.push(
            text(format!("Browsing: {dir}"))
                .key(format!("{w}.browsing"))
                .font(type_::LABEL)
                .color(t.text_tertiary),
        );
    }
    if let Some((operation, path)) = &picker.operation {
        out.push(
            row()
                .key(format!("{w}.operation"))
                .role("status")
                .label(operation.label())
                .gap(space::S1)
                .child(
                    text(operation.label())
                        .font(type_::LABEL)
                        .color(t.text_secondary),
                )
                .child(text(path.clone()).font(type_::LABEL).color(t.text_tertiary)),
        );
    }
    if let Some(error) = &picker.error {
        out.push(
            text(error.clone())
                .key(format!("{w}.error"))
                .role("alert")
                .label(error.clone())
                .font(type_::BODY)
                .color(t.error),
        );
    }
    let recents: Vec<El> = rows
        .iter()
        .enumerate()
        .filter(|(_, r)| r.recent)
        .map(|(i, r)| location_row(picker, i, r, home, t))
        .collect();
    let directories: Vec<El> = rows
        .iter()
        .enumerate()
        .filter(|(_, r)| !r.recent)
        .map(|(i, r)| location_row(picker, i, r, home, t))
        .collect();
    let empty = (rows.is_empty()).then(|| {
        let message = if picker.input.text.is_empty() {
            "Type a path to browse directories"
        } else {
            "No matches. Press Enter to use path directly."
        };
        text(message)
            .key(format!("{w}.empty"))
            .label(message)
            .font(type_::BODY)
            .color(t.text_tertiary)
    });
    let group = |name: &str, title: &str, rows: Vec<El>| {
        (!rows.is_empty()).then(|| {
            col()
                .key(format!("{w}.{}", name.to_lowercase()))
                .role("group")
                .label(name)
                .gap(2.0)
                .child(section(title, t))
                .children(rows)
        })
    };
    out.push(
        col()
            .key(format!("{w}.locations"))
            .role("listbox")
            .label("Locations")
            .w_full()
            .max_h(360.0)
            .scroll(rune::ScrollSpec {
                stick_bottom: false,
            })
            .gap(space::S2)
            .children(group("Recent", "RECENT", recents))
            .children(group("Directories", "DIRECTORIES", directories))
            .children(empty),
    );
    out.push(footer(
        &[
            "↑↓ navigate",
            "Tab autocomplete",
            "Enter select",
            "Esc cancel",
        ],
        t,
    ));
    out
}

fn location_row(picker: &Picker, i: usize, r: &super::Row, _home: &str, t: &Tokens) -> El {
    let which = picker.which;
    let highlighted = picker.highlight.is_some_and(|h| h.index == i);
    let path = r.path.clone();
    let mut option = col()
        .key(format!("{}.row.{i}", which.key()))
        .role("option")
        .label(r.name.clone())
        .focus_selected(highlighted)
        .w_full()
        .padding(space::S1)
        .radius(4.0)
        .fill(if highlighted {
            t.bg_selected
        } else {
            t.bg_elevated
        })
        .on_press_tx(move |state: &mut State, _: &mut Tx| super::open_path(state, which, &path))
        .child(
            row()
                .gap(space::S1)
                .child(text(if r.recent { "🕐" } else { "📁" }).font(type_::BODY))
                .child(text(r.name.clone()).font(type_::BODY).color(t.text_primary)),
        );
    if let Some(shown) = &r.shown_path {
        option = option.description(shown.clone()).child(
            text(shown.clone())
                .font(type_::LABEL)
                .color(t.text_tertiary),
        );
    }
    option
}

fn chooser_view(picker: &Picker, chooser: &Chooser, t: &Tokens) -> Vec<El> {
    let now = chooser.read_at;
    let which = picker.which;
    let w = which.key();
    let destinations = chooser.destinations();
    let in_rows = chooser.zone == Zone::Destinations;
    let rows = destinations.iter().enumerate().map(|(i, d)| {
        let committed = in_rows && chooser.committed == i;
        let prompt = chooser
            .delete
            .as_ref()
            .filter(|del| del.path() == d.path)
            .map(|del| delete_prompt(w, del, t));
        let name = if d.branch.is_empty() {
            paths::basename(&d.path).to_owned()
        } else {
            d.branch.clone()
        };
        let detail = repo::row_detail(chooser, d, now);
        col()
            .key(format!("{w}.repo.row.{i}"))
            .role("option")
            .label(name.clone())
            .description(detail.clone())
            .focus_selected(committed)
            .w_full()
            .padding(space::S1)
            .radius(4.0)
            .fill(if committed {
                t.bg_selected
            } else {
                t.bg_elevated
            })
            .on_press_tx(move |state: &mut State, tx: &mut Tx| repo::activate(state, which, i, tx))
            .child(match prompt {
                Some(prompt) => prompt,
                None => row()
                    .gap(space::S1)
                    .child(text(if d.main { "●" } else { "◎" }).font(type_::BODY))
                    .child(text(name).font(type_::BODY).color(t.text_primary).grow(1.0))
                    .child(
                        text((i + 1).to_string())
                            .font(type_::MONO_CAPTION)
                            .color(t.text_tertiary),
                    ),
            })
            .child(text(detail).font(type_::LABEL).color(t.text_tertiary))
    });
    let start = |kind: Start, label: String| {
        let chosen = chooser.start == kind;
        let suffix = match kind {
            Start::Default => "default",
            Start::Current => "current",
        };
        row()
            .key(format!("{w}.repo.start.{suffix}"))
            .role("radio")
            .label(label.clone())
            .checked(chosen)
            .gap(space::S1)
            .on_press_tx(move |state: &mut State, _: &mut Tx| {
                if let Some(chooser) = state
                    .window
                    .launching
                    .picker_mut(which)
                    .and_then(|p| p.chooser.as_mut())
                {
                    chooser.start = kind;
                }
            })
            .child(text(if chosen { "◉" } else { "○" }).font(type_::BODY))
            .child(text(label).font(type_::LABEL).color(t.text_secondary))
    };
    let form = col()
        .key(format!("{w}.repo.form"))
        .gap(space::S1)
        .on_press_tx(move |state: &mut State, tx: &mut Tx| {
            repo::enter_form(state, which);
            tx.focus(repo::name_key(which), false);
        })
        .child(
            row()
                .gap(space::S1)
                .items_center()
                .child(text("+").font(type_::BODY).color(t.text_secondary))
                .child(
                    text_input(&chooser.name, type_::BODY)
                        .key(repo::name_key(which))
                        .single_line()
                        .label("Branch name")
                        .placeholder("Branch name...", t.text_muted)
                        .on_edit(move |state: &mut State| name_edit(state, which))
                        .on_input_key(move |state: &mut State, ev: &rune::KeyEvent, tx: &mut Tx| {
                            repo::enter_form(state, which);
                            super::on_key(state, which, ev, tx)
                        })
                        .grow(1.0)
                        .color(t.text_primary),
                )
                .child(
                    text("⟳")
                        .key(format!("{w}.repo.reroll"))
                        .role("button")
                        .label("Pick another name")
                        .help("Pick another name (⌃R)")
                        .font(type_::BODY)
                        .on_press_tx(move |state: &mut State, _: &mut Tx| reroll(state, which)),
                ),
        )
        .child(start(
            Start::Default,
            format!("Start from {}", chooser.info.default_branch),
        ))
        .child(start(
            Start::Current,
            format!("Start from {}", chooser.current_branch()),
        ))
        .child(
            text(
                "Enter to create • Tab to toggle • ⌃R for another name • ↓ to open an existing one",
            )
            .font(type_::LABEL)
            .color(t.text_tertiary),
        );
    let refreshing = chooser.refreshing.then(|| {
        text("Refreshing repo options")
            .key(format!("{w}.repo.refreshing"))
            .role("status")
            .label("Refreshing repo options")
            .font(type_::LABEL)
            .color(t.text_tertiary)
    });
    let on_worktree = destinations.get(chooser.committed).is_some_and(|d| !d.main);
    let hints: Vec<&str> = match chooser.zone {
        Zone::Create => vec!["Enter Create worktree", "↓ Open existing", "Esc Back"],
        Zone::Destinations => {
            let mut hints = vec!["↑↓ Navigate", "Enter Open"];
            if on_worktree {
                hints.push("D Delete worktree");
            }
            hints.push(if chooser.refreshing {
                "R Refresh ..."
            } else {
                "R Refresh"
            });
            hints.push("Esc Back");
            hints
        }
    };
    vec![
        col()
            .key(repo::root_key(which))
            .role("group")
            .label("Repository options")
            .focusable(true)
            .gap(space::S2)
            .children(refreshing)
            .child(section("NEW WORKTREE", t))
            .child(form)
            .child(section("OPEN EXISTING", t))
            .child(
                col()
                    .key(format!("{w}.repo.rows"))
                    .role("listbox")
                    .label("Open existing")
                    .gap(2.0)
                    .children(rows),
            ),
        footer(&hints, t),
    ]
}

fn name_edit(state: &mut State, which: Which) -> &mut rune::TextEdit {
    let launching = &mut state.window.launching;
    let has_chooser = launching.picker(which).is_some_and(|p| p.chooser.is_some());
    if !has_chooser {
        return launching.input_mut(which);
    }
    let picker = launching.picker_mut(which).expect("the picker is open");
    &mut picker.chooser.as_mut().expect("the chooser shows").name
}

fn reroll(state: &mut State, which: Which) {
    if let Some(chooser) = state
        .window
        .launching
        .picker_mut(which)
        .and_then(|p| p.chooser.as_mut())
    {
        chooser.reroll();
    }
}

fn delete_prompt(w: &str, delete: &Delete, t: &Tokens) -> El {
    let name = paths::basename(delete.path()).to_owned();
    let (first, second, role, label) = match delete {
        Delete::Asking { .. } => (
            format!("Delete {name}? (y/n)"),
            None,
            "alert",
            format!("Delete {name}?"),
        ),
        Delete::Deleting { .. } => (
            format!("Deleting {name}"),
            Some("Removing worktree and refreshing repo options".to_owned()),
            "status",
            format!("Deleting {name}"),
        ),
        Delete::Failed {
            error, forceable, ..
        } => (
            format!("Delete failed: {error}"),
            Some(if *forceable {
                "Force delete local worktree and branch? (y/n)".to_owned()
            } else {
                "Press Esc to dismiss".to_owned()
            }),
            "alert",
            format!("Delete failed: {error}"),
        ),
    };
    col()
        .key(format!("{w}.repo.delete"))
        .role(role)
        .label(label)
        .child(text(first).font(type_::BODY).color(t.text_primary))
        .children(second.map(|s| text(s).font(type_::LABEL).color(t.text_secondary)))
}

/// The corner prompt for an agent starting in a new worktree, and the dialog it opens when the
/// worktree could not be made.
pub(crate) fn worktree_setup(state: &State, t: &Tokens) -> Option<El> {
    let setup = state.window.launching.worktree.as_ref()?;
    if let (Some(error), true) = (&setup.failed, setup.opened) {
        let dismiss = text("Dismiss")
            .key("worktree-setup.dismiss")
            .role("button")
            .label("Dismiss")
            .font(type_::BODY)
            .color(t.accent)
            .on_press_tx(|state: &mut State, _: &mut Tx| state.window.launching.worktree = None);
        return Some(
            el().key("worktree-setup.layer")
                .absolute(0.0, 0.0)
                .w_full()
                .h_full()
                .child(
                    col().w_full().h_full().center().child(
                        col()
                            .key("worktree-setup")
                            .role("dialog")
                            .label("Session was not created")
                            .modal(true, |state: &mut State| {
                                state.window.launching.worktree = None
                            })
                            .max_w(480.0)
                            .padding(space::S3)
                            .gap(space::S2)
                            .radius(8.0)
                            .fill(t.bg_elevated)
                            .border(1.0, t.border)
                            .child(
                                text("Action needed")
                                    .font(type_::LABEL)
                                    .color(t.text_tertiary),
                            )
                            .child(
                                text("Session was not created")
                                    .font(type_::TITLE)
                                    .color(t.text_primary),
                            )
                            .child(
                                col()
                                    .key("worktree-setup.error")
                                    .role("alert")
                                    .label(format!("Create failed: {error}"))
                                    .child(text("Create failed").font(type_::BODY).color(t.error))
                                    .child(
                                        text(error.clone())
                                            .font(type_::BODY)
                                            .color(t.text_secondary),
                                    ),
                            )
                            .child(dismiss),
                    ),
                ),
        );
    }
    let (title, detail) = match &setup.failed {
        Some(_) => (
            "Create failed".to_owned(),
            format!("{} failed", setup.branch),
        ),
        None => ("Creating worktree".to_owned(), setup.branch.clone()),
    };
    let mut corner = col()
        .key("worktree-setup")
        .role(if setup.failed.is_some() {
            "button"
        } else {
            "status"
        })
        .label(format!("{title}: {detail}"))
        .padding(space::S2)
        .radius(6.0)
        .fill(t.bg_elevated)
        .border(1.0, t.border)
        .child(
            text("Session setup")
                .font(type_::LABEL)
                .color(t.text_tertiary),
        )
        .child(text(title).font(type_::BODY).color(t.text_primary))
        .child(text(detail).font(type_::LABEL).color(t.text_secondary));
    if setup.failed.is_some() {
        corner = corner
            .child(
                text("open to resolve")
                    .font(type_::LABEL)
                    .color(t.text_tertiary),
            )
            .on_press_tx(|state: &mut State, _: &mut Tx| {
                if let Some(setup) = &mut state.window.launching.worktree {
                    setup.opened = true;
                }
            });
    } else {
        corner = corner.child(
            text(format!(
                "git worktree add · {}",
                paths::tildify(&setup.repo)
            ))
            .font(type_::LABEL)
            .color(t.text_tertiary),
        );
    }
    Some(
        el().key("worktree-setup.corner")
            .absolute(0.0, 0.0)
            .w_full()
            .h_full()
            .child(
                col()
                    .w_full()
                    .h_full()
                    .items_end()
                    .justify_end()
                    .padding(space::S3)
                    .child(corner),
            ),
    )
}

fn desktop_label(state: &State, picker: &Picker) -> String {
    let Some(desktop) = picker
        .desktop
        .as_ref()
        .and_then(|id| state.facts.desktops.iter().find(|d| d.id == *id))
    else {
        return "this desktop".into();
    };
    let name = desktop.name.trim();
    if !name.is_empty() {
        return name.into();
    }
    let number = match desktop.shortcut_slot {
        Some(slot) if slot > 0 => slot as usize,
        _ => {
            let extra = state
                .facts
                .desktops
                .iter()
                .filter(|d| d.shortcut_slot.is_none_or(|s| s == 0))
                .position(|d| d.id == desktop.id)
                .unwrap_or(0);
            9 + extra + 1
        }
    };
    format!("Desktop {number}")
}

fn section(title: &str, t: &Tokens) -> El {
    text(title).font(type_::SECTION).color(t.text_tertiary)
}

fn footer(hints: &[&str], t: &Tokens) -> El {
    row().gap(space::S3).children(
        hints
            .iter()
            .map(|h| text(*h).font(type_::LABEL).color(t.text_tertiary)),
    )
}
