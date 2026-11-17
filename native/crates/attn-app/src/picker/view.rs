use attn_ui::colors::mix_srgb;
use attn_ui::keycap;
use attn_ui::type_;
use attn_ui::{GlyphButton, Tokens, icon};
use rune::tok::space;
use rune::{El, Tx, col, el, row, text};
use rune_kit::{Button, ButtonVariant, Dialog, TextInput};

use super::repo::{self, Chooser, Delete, Start, Zone};
use super::{Catalog, Choice, Offer, Opened, Picker, Which, agents, listed, numbered, paths};
use crate::State;

/// The New Session dialog over the app.
pub(crate) fn dialog(state: &State, t: &Tokens) -> Option<El> {
    let frame = match state.window.launching.dialog.as_ref()? {
        Opened::Classic(picker) => {
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
                });
            rune_kit::popover::raised(
                attn_ui::kit(t),
                frame.w_full().max_w(DIALOG_W).padding(space::S3),
                8.0,
            )
        }
        Opened::Launcher(launcher) => crate::launcher::framed(state, launcher, t)
            .key("new-session")
            .role("dialog")
            .label("New agent")
            .modal_tx(false, |state: &mut State, tx: &mut Tx| {
                crate::launcher::escape(state, tx)
            }),
    };
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
                    .backdrop_blur(2.0)
                    .fill(t.shadow.alpha(0.5))
                    .a11y_hidden()
                    .on_press_tx(|state: &mut State, tx: &mut Tx| {
                        if state.window.launching.launcher(Which::Dialog).is_some() {
                            crate::launcher::dismiss(state, tx)
                        } else {
                            super::close(state, tx)
                        }
                    }),
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
    let frame = match launching.shown_inline()? {
        Opened::Classic(picker) => frame(state, picker, t)
            .w_full()
            .max_w(640.0)
            .radius(8.0)
            .fill(t.bg_panel)
            .border(1.0, t.border_subtle)
            .padding(space::S3),
        Opened::Launcher(launcher) => crate::launcher::framed(state, launcher, t),
    };
    Some(
        col()
            .key("launcher.area")
            .w_full()
            .h_full()
            .items_center()
            .pt(80.0)
            .child(frame.key("launcher").role("group").label("New agent")),
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
        .child(controls(
            state,
            which,
            &picker.choice,
            &Offer::here(state),
            t,
        ))
        .children(body)
}

/// One harness the toggle offers, as both launchers show it.
pub(crate) struct AgentOption {
    pub(crate) agent: String,
    pub(crate) label: String,
    pub(crate) selectable: bool,
    pub(crate) chosen: bool,
    /// The key that picks it with ⌥: `T` for Terminal, its number for the first nine others.
    pub(crate) key: Option<String>,
    /// Struck through: the machine the start aims at lacks it.
    pub(crate) absent: bool,
    pub(crate) checking: bool,
    pub(crate) title: Option<String>,
}

pub(crate) fn agent_options(state: &State, choice: &Choice, offer: &Offer) -> Vec<AgentOption> {
    let launching = &state.window.launching;
    let agents = &offer.agents;
    let current = choice.agent_among(agents);
    let numbered = numbered(agents, &launching.catalog);
    listed(agents, &launching.catalog)
        .into_iter()
        .map(|agent| {
            let label = state
                .facts
                .settings
                .get(&format!("{agent}_display_name"))
                .filter(|name| !name.trim().is_empty())
                .cloned()
                .unwrap_or_else(|| agents::label(&agent));
            let availability = agents.of(&agent);
            let selectable = availability != agents::Availability::Unavailable;
            let key = if !selectable {
                None
            } else if agent == agents::TERMINAL {
                Some("T".to_owned())
            } else {
                numbered
                    .iter()
                    .position(|a| *a == agent)
                    .filter(|n| *n < 9)
                    .map(|n| (n + 1).to_string())
            };
            let absent = offer
                .missing
                .as_ref()
                .filter(|(_, lacking)| lacking.contains(&agent))
                .map(|(machine, _)| machine);
            let checking = availability == agents::Availability::Checking
                && !offer.outpost
                && absent.is_none();
            let title = if !selectable {
                Some(format!("{label} is not available on this machine"))
            } else if let Some(machine) = absent {
                Some(format!("{label} is not installed on {machine}"))
            } else {
                checking.then(|| format!("Checking for {label} on this machine"))
            };
            AgentOption {
                chosen: current == agent,
                absent: absent.is_some(),
                agent,
                label,
                selectable,
                key,
                checking,
                title,
            }
        })
        .collect()
}

/// The YOLO switch: whether it is on, whether the chosen harness has the mode, and what its
/// tooltip says.
pub(crate) struct Yolo {
    pub(crate) on: bool,
    pub(crate) supported: bool,
    pub(crate) title: String,
}

pub(crate) fn yolo(state: &State, choice: &Choice, offer: &Offer) -> Yolo {
    let current = choice.agent_among(&offer.agents);
    let supported = agents::supports_yolo(&state.facts.settings, &current);
    Yolo {
        on: choice.yolo_for(state, &current),
        supported,
        title: if supported {
            "Run the agent without asking for permission".to_owned()
        } else {
            format!(
                "{} does not support yolo mode",
                agents::yolo_label(&current)
            )
        },
    }
}

/// The harness option's row: its role, name, state and the press that picks it.
pub(crate) fn agent_option(which: Which, option: &AgentOption) -> El {
    let picked = option.agent.clone();
    let mut el = row()
        .key(format!("{}.agent.{}", which.key(), option.agent))
        .role("radio")
        .label(option.label.clone())
        .checked(option.chosen)
        .disabled(!option.selectable)
        .items_center()
        .on_press_tx(move |state: &mut State, _: &mut Tx| {
            super::choose_agent(state, which, &picked)
        });
    if let Some(title) = &option.title {
        el = el.description(title.clone()).help(title.clone());
    }
    el
}

pub(crate) fn checking_mark(which: Which, option: &AgentOption, t: &Tokens) -> Option<El> {
    option.checking.then(|| {
        text("…")
            .key(format!("{}.agent.{}.checking", which.key(), option.agent))
            .role("img")
            .label("checking")
            .color(t.text_tertiary)
    })
}

/// The YOLO switch's row: its role, name, state and the press that flips it.
pub(crate) fn yolo_switch(which: Which, yolo: &Yolo) -> El {
    row()
        .key(format!("{}.yolo", which.key()))
        .role("switch")
        .label("YOLO")
        .checked(yolo.on)
        .disabled(!yolo.supported)
        .description(yolo.title.clone())
        .help(yolo.title.clone())
        .items_center()
        .on_press_tx(move |state: &mut State, _: &mut Tx| super::toggle_yolo(state, which))
}

/// What the toggle says while the harness list loads or after it failed to.
pub(crate) fn catalog_note(state: &State, which: Which, t: &Tokens) -> Option<El> {
    let w = which.key();
    match &state.window.launching.catalog {
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
                    link("Retry", type_::LABEL, t, super::retry_catalog)
                        .key(format!("{w}.agents.retry")),
                ),
        ),
        Catalog::Loaded(_) => None,
    }
}

/// The classic picker's harness toggle and YOLO switch.
fn controls(state: &State, which: Which, choice: &Choice, offer: &Offer, t: &Tokens) -> El {
    let w = which.key();
    let options = agent_options(state, choice, offer)
        .into_iter()
        .map(|option| {
            let name = text(option.label.clone())
                .font(OPTION_TYPE)
                .color(if option.chosen {
                    t.bg_app
                } else if option.selectable {
                    t.text_tertiary
                } else {
                    t.text_muted
                });
            let name = if option.absent {
                name.strike(0, option.label.len(), None)
            } else {
                name
            };
            agent_option(which, &option)
                .gap(space::S2)
                .px(12.0)
                .py(6.0)
                .radius(999.0)
                .fill(if option.chosen { t.brand } else { t.bg_input })
                .child(name)
                .children(checking_mark(which, &option, t))
                .children(option.key.as_ref().map(|k| {
                    keycaps(
                        format!("{w}.agent.{}.keys", option.agent),
                        &format!("alt+{}", k.to_lowercase()),
                        option.chosen,
                        t,
                    )
                }))
        });
    let yolo = yolo(state, choice, offer);
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
                .child(toggle(options, t))
                .children(catalog_note(state, which, t)),
        )
        .child(
            row()
                .gap(space::S2)
                .items_center()
                .child(section("YOLO", t))
                .child(
                    yolo_switch(which, &yolo)
                        .gap(space::S2)
                        .px(12.0)
                        .py(6.0)
                        .radius(999.0)
                        .fill(if yolo.on { t.brand } else { t.bg_input })
                        .child(
                            text(if yolo.on { "On" } else { "Off" })
                                .font(OPTION_TYPE)
                                .color(if yolo.on {
                                    t.bg_app
                                } else if yolo.supported {
                                    t.text_tertiary
                                } else {
                                    t.text_muted
                                }),
                        )
                        .children(
                            yolo.supported
                                .then(|| keycaps(format!("{w}.yolo.keys"), "alt+q", yolo.on, t)),
                        ),
                ),
        )
}

/// Tauri's agent toggle: a pill track holding the options.
fn toggle(options: impl IntoIterator<Item = El>, t: &Tokens) -> El {
    row()
        .items_center()
        .flex_wrap()
        .shrink()
        .gap(4.0)
        .padding(3.0)
        .radius(16.0)
        .fill(t.bg_input)
        .border(1.0, t.border)
        .children(options)
}

/// The chord `spec` that picks an option, as one chip, Tauri's `.agent-shortcut`: muted on a
/// resting option, dark ink on a pale chip on the chosen orange pill.
fn keycaps(key: String, spec: &str, chosen: bool, t: &Tokens) -> El {
    let chord = rune::Chord::parse(spec)
        .expect("a picker chord parses")
        .label_on(rune::KeyPlatform::current());
    let (ink, edge, fill) = if chosen {
        (
            mix_srgb(t.brand, rune::Color::BLACK, 0.77),
            t.shadow.alpha(0.2),
            t.highlight.alpha(0.45),
        )
    } else {
        (t.text_muted, t.border, t.highlight.alpha(0.04))
    };
    keycap::Keys::outlined(&[chord])
        .colors(ink, edge, fill)
        .build(t)
        .key(key)
}

fn path_view(state: &State, picker: &Picker, t: &Tokens) -> Vec<El> {
    let which = picker.which;
    let w = which.key();
    let launching = &state.window.launching;
    let home = &launching.home;
    let title = match which {
        Which::Dialog => "New Session Location".to_owned(),
        Which::Inline => format!(
            "New agent on {}",
            desktop_label(state, picker.desktop.as_ref())
        ),
    };
    let field_key = picker.field_key();
    let rows = picker.rows(launching);
    let ghost = picker
        .ghost(&rows)
        .filter(|g| g.starts_with(&picker.input.text))
        .map(|g| g[picker.input.text.len()..].to_owned());
    let field = row()
        .w_full()
        .items_center()
        .child(
            TextInput::new(&picker.input)
                .key(field_key)
                .plain()
                .text_style(type_::BODY)
                .height(type_::BODY.line_px())
                .width(None)
                .label("Path")
                .placeholder("Type path (e.g., ~/projects) or search...")
                .on_edit(move |state: &mut State| state.window.launching.input_mut(which))
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
    let mut out = vec![
        text(title.to_uppercase())
            .key(format!("{w}.title"))
            .role("heading")
            .label(title.clone())
            .font(TITLE)
            .color(t.brand)
            .single_line()
            .truncate()
            .w_full()
            .help_when_truncated(title),
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
            ("↑↓", "navigate"),
            ("Tab", "autocomplete"),
            ("Enter", "select"),
            ("Esc", "cancel"),
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
        .on_press_tx(move |state: &mut State, tx: &mut Tx| {
            super::open_path(state, which, &path, tx)
        })
        .child(
            row()
                .gap(space::S1)
                .child(text(if r.recent { icon::CLOCK } else { icon::FOLDER }).font(type_::BODY))
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
                    .child(
                        text(if d.main {
                            icon::CIRCLE
                        } else {
                            icon::CIRCLE_DOT
                        })
                        .font(type_::BODY),
                    )
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
            .child(
                text(if chosen {
                    icon::CIRCLE_DOT
                } else {
                    icon::CIRCLE_OPEN
                })
                .font(type_::BODY),
            )
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
                    TextInput::new(&chooser.name)
                        .key(repo::name_key(which))
                        .plain()
                        .text_style(type_::BODY)
                        .height(type_::BODY.line_px())
                        .width(None)
                        .label("Branch name")
                        .placeholder("Branch name...")
                        .on_edit(move |state: &mut State| name_edit(state, which))
                        .on_key(move |state: &mut State, ev: &rune::KeyEvent, tx: &mut Tx| {
                            repo::enter_form(state, which);
                            super::on_key(state, which, ev, tx)
                        })
                        .build(attn_ui::kit(t))
                        .grow(1.0),
                )
                .child(
                    GlyphButton::new(icon::REFRESH, "Pick another name")
                        .variant(ButtonVariant::Ghost)
                        .font(type_::BODY)
                        .side(type_::BODY.line_px())
                        .help("Pick another name (⌃R)")
                        .on_press(move |state: &mut State| reroll(state, which))
                        .build(attn_ui::kit(t))
                        .key(format!("{w}.repo.reroll")),
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
    let hints: Vec<(&str, &str)> = match chooser.zone {
        Zone::Create => vec![
            ("Enter", "Create worktree"),
            ("↓", "Open existing"),
            ("Esc", "Back"),
        ],
        Zone::Destinations => {
            let mut hints = vec![("↑↓", "Navigate"), ("Enter", "Open")];
            if on_worktree {
                hints.push(("D", "Delete worktree"));
            }
            hints.push((
                "R",
                if chooser.refreshing {
                    "Refresh ..."
                } else {
                    "Refresh"
                },
            ));
            hints.push(("Esc", "Back"));
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
    let picker = state
        .window
        .launching
        .picker_mut(which)
        .expect("the picker is open");
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
    let k = attn_ui::kit(t);
    if let (Some(error), true) = (&setup.failed, setup.opened) {
        let dismiss = link("Dismiss", type_::BODY, t, |state, _| {
            state.window.launching.worktree = None
        })
        .key("worktree-setup.dismiss");
        let failure = col()
            .key("worktree-setup.error")
            .role("alert")
            .label(format!("Create failed: {error}"))
            .child(text("Create failed").font(type_::BODY).color(t.error))
            .child(
                text(error.clone())
                    .font(type_::BODY)
                    .color(t.text_secondary),
            );
        let mut layers = Vec::new();
        Dialog::new("worktree-setup", "Session was not created")
            .description("Action needed")
            .open(true)
            .fit()
            .width(480.0)
            .on_close(|state: &mut State| state.window.launching.worktree = None)
            .child(
                col()
                    .w_full()
                    .gap(space::S2)
                    .px(16.0)
                    .pb(16.0)
                    .child(failure)
                    .child(dismiss),
            )
            .build(k, &mut layers);
        return Some(
            el().key("worktree-setup.layer")
                .absolute(0.0, 0.0)
                .w_full()
                .h_full()
                .children(layers),
        );
    }
    let (title, detail) = match &setup.failed {
        Some(_) => (
            "Create failed".to_owned(),
            format!("{} failed", setup.subject),
        ),
        None => (setup.step.title().to_owned(), setup.subject.clone()),
    };
    let last = match setup.failed {
        Some(_) => "open to resolve".to_owned(),
        None => setup.step.command(),
    };
    let lines = col()
        .items_start()
        .child(
            text("Session setup")
                .font(type_::LABEL)
                .color(t.text_tertiary),
        )
        .child(text(title.clone()).font(type_::BODY).color(t.text_primary))
        .child(
            text(detail.clone())
                .font(type_::LABEL)
                .color(t.text_secondary),
        )
        .child(text(last).font(type_::LABEL).color(t.text_tertiary));
    let name = format!("{title}: {detail}");
    let corner = if setup.failed.is_some() {
        let height = 3.0 * type_::LABEL.line_px() + type_::BODY.line_px() + 2.0 * space::S2;
        Button::new(name)
            .variant(ButtonVariant::Ghost)
            .on_press(|state: &mut State| {
                if let Some(setup) = &mut state.window.launching.worktree {
                    setup.opened = true;
                }
            })
            .label_with(move |_, _| lines)
            .build(k)
            .h(height)
            .px(space::S2)
    } else {
        lines.role("status").label(name).padding(space::S2)
    };
    let corner = rune_kit::popover::raised(k, corner.key("worktree-setup"), 6.0);
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

pub(crate) fn desktop_label(state: &State, desktop: Option<&String>) -> String {
    let Some(desktop) = desktop.and_then(|id| state.facts.desktops.iter().find(|d| d.id == *id))
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

const TITLE: rune::TextStyle = rune::TextStyle {
    size: type_::TITLE.size,
    ..type_::DRAWER_TITLE
};

const SECTION: rune::TextStyle = rune::TextStyle {
    tracking: 1.6,
    ..type_::CAPS
};

fn section(title: &str, t: &Tokens) -> El {
    type_::upper(title, SECTION).color(t.text_dimmed)
}

/// Tauri's `.picker-footer`: a darker band across the frame's foot, under a hairline, each
/// hint a keycap and its word.
fn footer(hints: &[(&str, &str)], t: &Tokens) -> El {
    row()
        .mx(-space::S3)
        .mb(-space::S3)
        .px(16.0)
        .py(10.0)
        .gap(20.0)
        .items_center()
        .flex_wrap()
        .corners([0.0, 0.0, 8.0, 8.0])
        .fill(t.shadow.alpha(0.3))
        .border_sides([1.0, 0.0, 0.0, 0.0], t.border_subtle)
        .children(hints.iter().map(|&(key, word)| {
            row()
                .label(format!("{key} {word}"))
                .gap(4.0)
                .items_center()
                .child(keycap::Keys::outlined(&[key]).build(t).a11y_hidden())
                .child(
                    text(word)
                        .font(type_::LABEL)
                        .color(t.text_dimmed)
                        .a11y_hidden(),
                )
        }))
}

/// A word in the accent colour that is pressed as a button named by it.
fn link(
    word: &'static str,
    font: rune::TextStyle,
    t: &Tokens,
    press: impl Fn(&mut State, &mut Tx) + 'static,
) -> El {
    let ink = t.accent;
    Button::new(word)
        .variant(ButtonVariant::GhostMuted)
        .on_press_tx(press)
        .label_with(move |_, _| text(word).font(font).color(ink))
        .build(attn_ui::kit(t))
        .h(font.line_px())
        .px(0.0)
}

pub(crate) const OPTION_TYPE: rune::TextStyle = rune::TextStyle::new(12.0, 500.0).mono();

/// Tauri's `.location-picker` width.
pub(crate) const DIALOG_W: f32 = 760.0;
