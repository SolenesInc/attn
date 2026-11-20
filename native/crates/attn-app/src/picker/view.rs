use attn_ui::blocks::{Actions, Block, DialogSize, Field as FieldBlock, Inline, Notice, Stack};
use attn_ui::finder::{self, FinderSize, Hint};
use attn_ui::system::{
    GroupHead, IconSize, ListRow, Look, Part, Said, Segment, Segmented, Size, Type, icon,
    icon_button, press_area, words,
};
use attn_ui::{Dialog, Tokens, glyph};
use rune::{El, Tx, col, el};
use rune_kit::ScrollBar;
use rune_kit::TextInput;

use super::repo::{self, Chooser, Delete, Start, Zone};
use super::{Catalog, Choice, Offer, Opened, Picker, Which, agents, listed, numbered, paths};
use crate::State;

/// The New Session dialog over the app: its backdrop and the dialog, each a layer of its own.
pub(crate) fn dialog(state: &State, t: &Tokens) -> Vec<El> {
    let Some(opened) = state.window.launching.dialog.as_ref() else {
        return Vec::new();
    };
    let frame = match opened {
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
            finder::framed(t, frame, FinderSize::Wide)
        }
        Opened::Launcher(launcher) => crate::launcher::framed(state, launcher, t)
            .key("new-session")
            .role("dialog")
            .label("New agent")
            .modal_tx(false, |state: &mut State, tx: &mut Tx| {
                crate::launcher::escape(state, tx)
            }),
    };
    vec![
        attn_ui::Backdrop::modal(t)
            .draw("new-session.scrim")
            .on_press_tx(|state: &mut State, tx: &mut Tx| {
                if state.window.launching.launcher(Which::Dialog).is_some() {
                    crate::launcher::dismiss(state, tx)
                } else {
                    super::close(state, tx)
                }
            }),
        finder::placed(frame, FinderSize::Wide).key("new-session.layer"),
    ]
}

/// The launcher an empty desktop shows in place of its tiles.
pub(crate) fn inline(state: &State, t: &Tokens) -> Option<El> {
    let launching = &state.window.launching;
    if let Some(starting) = launching.shown_starting() {
        let message = format!("Starting {}…", starting.name);
        return Some(
            col().key("launcher").w_full().h_full().center().child(
                words(message.clone(), Type::Body, t.text_secondary)
                    .key("launcher.starting")
                    .role("status")
                    .label(message),
            ),
        );
    }
    let frame = match launching.shown_inline()? {
        Opened::Classic(picker) => finder::framed(t, frame(state, picker, t), FinderSize::Wide),
        Opened::Launcher(launcher) => crate::launcher::framed(state, launcher, t),
    };
    Some(
        col()
            .key("launcher.area")
            .w_full()
            .h_full()
            .items_center()
            .child(el().h(EMPTY_DESKTOP_DROP * crate::shell::TITLE_BAR_HEIGHT))
            .child(el().h_frac(EMPTY_DESKTOP_DROP))
            .child(frame.key("launcher").role("group").label("New agent")),
    )
}

/// How far down the window an empty desktop's launcher starts, as a share of the window's
/// height: `.empty-desktop-launcher`'s `12vh`. The desktop starts under the title bar, so the
/// title bar's share comes first and the desktop's share follows.
const EMPTY_DESKTOP_DROP: f32 = 0.12;

fn frame(state: &State, picker: &Picker, t: &Tokens) -> El {
    let which = picker.which;
    let body = match &picker.chooser {
        Some(chooser) => chooser_view(picker, chooser, t),
        None => path_view(state, picker, t),
    };
    col()
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
    pub(crate) checking: bool,
    /// Installed here, but not on the machine the start goes to.
    pub(crate) absent: bool,
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
            let label = agents::named(&state.facts.settings, &agent);
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
                agent,
                label,
                selectable,
                key,
                checking,
                absent: absent.is_some(),
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
            agents::no_yolo(&current)
        },
    }
}

/// The harness toggle: the harnesses as segments, each with the letter that picks it with
/// Alt, the Alt before them, and what the toggle says while the list loads or after it failed
/// to. The letters choose, so the segments take no Tab stop.
pub(crate) fn harness_toggle(
    state: &State,
    which: Which,
    options: Vec<AgentOption>,
    t: &Tokens,
) -> El {
    let w = which.key();
    let legend = rune::Chord::parse("alt+t")
        .expect("the Alt chord parses")
        .keycaps_on(rune::KeyPlatform::current())
        .swap_remove(0);
    let segments = options.into_iter().map(|option| {
        let picked = option.agent.clone();
        let key = format!("{w}.agent.{}", option.agent);
        let mark = Inline::tight()
            .children(option.key.as_ref().map(|k| {
                words(k.clone(), Type::Mono, t.text_muted)
                    .key(format!("{key}.keys"))
                    .label(spoken(&format!("alt+{}", k.to_lowercase())))
            }))
            .children(option.checking.then(|| {
                words("…", Type::Small, t.text_tertiary)
                    .key(format!("{key}.checking"))
                    .role("img")
                    .label("checking")
            }));
        let mut segment = Segment::new(key, option.label.clone())
            .chosen(option.chosen)
            .disabled(!option.selectable)
            .missing(option.absent)
            .trailing(mark)
            .on_press_tx(move |state: &mut State, _: &mut Tx| {
                super::choose_agent(state, which, &picked)
            });
        if let Some(title) = option.title {
            segment = segment.help(title);
        }
        segment
    });
    Inline::loose()
        .children(catalog_note(state, which, t))
        .child(words(legend, Type::Mono, t.text_muted).a11y_hidden())
        .child(
            Segmented::new(format!("{w}.agents"), "Session agent")
                .without_tab_stops()
                .options(segments)
                .build(t)
                .into(),
        )
}

/// The YOLO switch and the keys that flip it. Its keys flip it, so it takes no Tab stop.
/// `keys` is the chord as the bar shows it.
pub(crate) fn yolo_control(which: Which, yolo: &Yolo, keys: String, t: &Tokens) -> El {
    let switch = El::from(attn_ui::system::switch(
        format!("{}.yolo", which.key()),
        "YOLO",
        yolo.on,
        attn_ui::system::Beside::Label,
        !yolo.supported,
        t,
        move |state: &mut State, _: &mut Tx| super::toggle_yolo(state, which),
    ))
    .focusable(false)
    .description(yolo.title.clone())
    .help(yolo.title.clone());
    Inline::loose()
        .child(switch)
        .children(yolo.supported.then(|| {
            words(keys, Type::Mono, t.text_muted)
                .key(format!("{}.yolo.keys", which.key()))
                .label(spoken("alt+q"))
        }))
}

/// `spec` as this platform names the chord.
fn spoken(spec: &str) -> String {
    rune::Chord::parse(spec)
        .expect("a picker chord parses")
        .label_on(rune::KeyPlatform::current())
}

/// What the toggle says while the harness list loads or after it failed to.
pub(crate) fn catalog_note(state: &State, which: Which, t: &Tokens) -> Option<El> {
    let w = which.key();
    match &state.window.launching.catalog {
        Catalog::Unasked | Catalog::Loading => Some(
            words("Loading harnesses…", Type::Small, t.text_tertiary)
                .key(format!("{w}.agents.loading"))
                .role("status"),
        ),
        Catalog::Failed(error) => Some(
            Inline::tight()
                .key(format!("{w}.agents.error"))
                .role("alert")
                .label(error.clone())
                .child(words(error.clone(), Type::Small, t.text_secondary).a11y_hidden())
                .child(
                    attn_ui::system::Button::new("Retry")
                        .look(Look::Quiet)
                        .size(Size::Compact)
                        .on_press_tx(super::retry_catalog)
                        .build(t)
                        .key(format!("{w}.agents.retry"))
                        .into(),
                ),
        ),
        Catalog::Loaded(_) => None,
    }
}

/// The classic picker's two bars, Tauri's `.picker-agent-bar` and `.picker-chief-bar`: each
/// names its control at the left and holds it at the right edge.
fn controls(state: &State, which: Which, choice: &Choice, offer: &Offer, t: &Tokens) -> El {
    let toggle = harness_toggle(state, which, agent_options(state, choice, offer), t);
    let yolo = yolo_control(which, &yolo(state, choice, offer), spoken("alt+q"), t);
    attn_ui::finder::bar(t, vec![toggle, yolo])
}

fn path_view(state: &State, picker: &Picker, t: &Tokens) -> Vec<El> {
    let which = picker.which;
    let w = which.key();
    let launching = &state.window.launching;
    let title = match which {
        Which::Dialog => "New Session Location".to_owned(),
        Which::Inline => format!(
            "New agent on {}",
            desktop_label(state, picker.desktop.as_ref())
        ),
    };
    let rows = picker.rows(launching);
    let ghost = picker
        .ghost(&rows)
        .filter(|g| g.starts_with(&picker.input.text))
        .map(|g| g[picker.input.text.len()..].to_owned());
    let input = finder::query_field(
        TextInput::new(&picker.input)
            .key(picker.field_key())
            .plain()
            .width(None)
            .label("Path")
            .placeholder("Type path (e.g., ~/projects) or search...")
            .active_descendant(picker.highlight.map(|h| row_key(which, h.index)))
            .on_edit(move |state: &mut State| state.window.launching.input_mut(which))
            .on_change(move |state: &mut State, tx: &mut Tx| super::edited(state, which, tx))
            .on_key(move |state: &mut State, ev: &rune::KeyEvent, tx: &mut Tx| {
                super::on_key(state, which, ev, tx)
            }),
    )
    .build(attn_ui::kit(t));
    let field = finder::query_box(
        t,
        None,
        input,
        ghost.map(|g| finder::ghost(format!("{w}.ghost"), &picker.input.text, g, t.text_muted)),
    );
    let mut said = Vec::new();
    if let Some(dir) = &picker.browsing {
        said.push(
            words(
                Said::parts([Part::Text("Browsing: ".to_owned()), Part::Code(dir.clone())]),
                Type::Small,
                t.text_tertiary,
            )
            .key(format!("{w}.browsing")),
        );
    }
    if let Some((operation, path)) = &picker.operation {
        said.push(
            words(
                Said::parts([
                    Part::Text(format!("{} ", operation.label())),
                    Part::Code(path.clone()),
                ]),
                Type::Small,
                t.text_secondary,
            )
            .key(format!("{w}.operation"))
            .role("status")
            .label(operation.label()),
        );
    }
    if let Some(error) = &picker.error {
        said.push(
            words(error.clone(), Type::Small, t.error)
                .key(format!("{w}.error"))
                .role("alert")
                .label(error.clone())
                .wrap(),
        );
    }
    let header = finder::query(
        t,
        finder::title(t, format!("{w}.title"), &title)
            .label(title.clone())
            .single_line()
            .truncate()
            .help_when_truncated(title),
        field,
        (!said.is_empty()).then(|| Stack::tight().w_full().children(said)),
    );
    let group = |name: &str, recent: bool| {
        let rows: Vec<El> = rows
            .iter()
            .enumerate()
            .filter(|(_, r)| r.recent == recent)
            .map(|(i, r)| location_row(picker, i, r, t))
            .collect();
        (!rows.is_empty()).then(|| {
            Stack::tight()
                .key(format!("{w}.{}", name.to_lowercase()))
                .role("group")
                .label(name)
                .w_full()
                .child(finder::heading(
                    GroupHead::new(format!("{w}.{}.head", name.to_lowercase()), name.to_owned())
                        .section(name.to_owned())
                        .heading()
                        .build(t),
                ))
                .children(rows)
        })
    };
    let empty = rows.is_empty().then(|| {
        let message = if picker.input.text.is_empty() {
            "Type a path to browse directories"
        } else {
            "No matches. Press Enter to use path directly."
        };
        finder::empty(t, format!("{w}.empty"), message)
    });
    let mut hints = vec![
        Hint::keys("", "↑↓", " navigate"),
        Hint::keys("", "Tab", " autocomplete"),
        Hint::keys("", "Enter", " select"),
    ];
    if which == Which::Dialog {
        hints.push(Hint::keys("", "Esc", " cancel").at_end());
    }
    let list = finder::grouped(
        format!("{w}.locations"),
        "Locations",
        group("Recent", true)
            .into_iter()
            .chain(group("Directories", false))
            .chain(empty)
            .collect(),
        LIST_H,
    )
    .scroll_bar_styled(attn_ui::scroll_bar(t));
    vec![header, list, footer(w, hints, t)]
}

/// How tall a picker's list gets before it scrolls.
const LIST_H: f32 = 340.0;

fn row_key(which: Which, i: usize) -> String {
    format!("{}.row.{i}", which.key())
}

fn location_row(picker: &Picker, i: usize, r: &super::Row, t: &Tokens) -> El {
    let which = picker.which;
    let highlighted = picker.highlight.is_some_and(|h| h.index == i);
    let path = r.path.clone();
    let mut line = ListRow::new(row_key(which, i), r.name.clone())
        .role("option")
        .unfocusable()
        .cursor(highlighted)
        .selected(highlighted)
        .lead(icon(
            if r.recent {
                &attn_ui::icon::RECENT
            } else {
                &attn_ui::icon::FOLDER
            },
            IconSize::Regular,
            t.text_tertiary,
        ))
        .title(r.name.clone());
    if let Some(shown) = &r.shown_path {
        line = line.subtitle(Said::parts([Part::Code(shown.clone())]), t);
    }
    let option = line
        .build(t)
        .on_press_tx(move |state: &mut State, tx: &mut Tx| {
            super::open_path(state, which, &path, tx)
        });
    match &r.shown_path {
        Some(shown) => option.description(shown.clone()),
        None => option,
    }
}

fn chooser_view(picker: &Picker, chooser: &Chooser, t: &Tokens) -> Vec<El> {
    let now = chooser.read_at;
    let which = picker.which;
    let w = which.key();
    let destinations = chooser.destinations();
    let in_rows = chooser.zone == Zone::Destinations;
    let rows: Vec<El> =
        destinations
            .iter()
            .enumerate()
            .map(|(i, d)| {
                let committed = in_rows && chooser.committed == i;
                let name = if d.branch.is_empty() {
                    paths::basename(&d.path).to_owned()
                } else {
                    d.branch.clone()
                };
                let detail = repo::row_detail(chooser, d, now);
                let line = ListRow::new(repo::row_key(which, i), name.clone())
                    .role("option")
                    .unfocusable()
                    .cursor(committed)
                    .selected(committed)
                    .lead(icon(
                        if d.main {
                            &attn_ui::icon::MAIN_BRANCH
                        } else {
                            &attn_ui::icon::BRANCH
                        },
                        IconSize::Regular,
                        t.text_tertiary,
                    ));
                let line = match chooser.delete.as_ref().filter(|del| del.path() == d.path) {
                    Some(del) => {
                        let (first, second, role, label) = delete_said(del);
                        line.title_el(
                            words(first, Type::Body, t.text_primary)
                                .key(format!("{w}.repo.delete"))
                                .role(role)
                                .label(label)
                                .single_line()
                                .truncate(),
                        )
                        .subtitle(second.unwrap_or_default(), t)
                    }
                    None => line.title(name).subtitle(detail.clone(), t).aside(words(
                        (i + 1).to_string(),
                        Type::Mono,
                        t.text_tertiary,
                    )),
                };
                line.build(t).description(detail).on_press_tx(
                    move |state: &mut State, tx: &mut Tx| repo::activate(state, which, i, tx),
                )
            })
            .collect();
    let start = |kind: Start, label: String, suffix: &str| {
        Segment::new(format!("{w}.repo.start.{suffix}"), label)
            .cut_middle()
            .chosen(chooser.start == kind)
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
    };
    let name = finder::query_field(
        TextInput::new(&chooser.name)
            .key(repo::name_key(which))
            .plain()
            .width(None)
            .label("Branch name")
            .placeholder("Branch name...")
            .on_edit(move |state: &mut State| name_edit(state, which))
            .on_key(move |state: &mut State, ev: &rune::KeyEvent, tx: &mut Tx| {
                repo::enter_form(state, which);
                super::on_key(state, which, ev, tx)
            }),
    )
    .build(attn_ui::kit(t));
    let reroll = icon_button(
        format!("{w}.repo.reroll"),
        "Pick another name",
        glyph::REFRESH,
        IconSize::Regular,
    )
    .help("Pick another name (⌃R)")
    .on_press(move |state: &mut State| reroll(state, which))
    .build(t);
    let form = Stack::tight()
        .key(format!("{w}.repo.form"))
        .w_full()
        .on_press_tx(move |state: &mut State, tx: &mut Tx| {
            repo::enter_form(state, which);
            tx.focus(repo::name_key(which), false);
        })
        .child(
            Inline::line()
                .w_full()
                .child(
                    finder::query_box(
                        t,
                        Some(words("+", Type::Body, t.text_secondary)),
                        name,
                        None,
                    )
                    .grow(1.0),
                )
                .child(El::from(reroll)),
        )
        .child(
            Segmented::new(format!("{w}.repo.start"), "Start from")
                .without_tab_stops()
                .stacked()
                .option(start(
                    Start::Default,
                    format!("Start from {}", chooser.info.default_branch),
                    "default",
                ))
                .option(start(
                    Start::Current,
                    format!("Start from {}", chooser.current_branch()),
                    "current",
                ))
                .build(t)
                .into(),
        )
        .child(
            words(
                "Enter to create • Tab to toggle • ⌃R for another name • ↓ to open an existing one",
                Type::Small,
                t.text_tertiary,
            )
            .wrap(),
        );
    let refreshing = chooser.refreshing.then(|| {
        words("Refreshing repo options", Type::Small, t.text_tertiary)
            .key(format!("{w}.repo.refreshing"))
            .role("status")
    });
    let on_worktree = destinations.get(chooser.committed).is_some_and(|d| !d.main);
    let hints: Vec<Hint> = match chooser.zone {
        Zone::Create => vec![
            Hint::keys("", "Enter", " Create worktree"),
            Hint::keys("", "↓", " Open existing"),
            Hint::keys("", "Esc", " Back").at_end(),
        ],
        Zone::Destinations => {
            let mut hints = vec![
                Hint::keys("", "↑↓", " Navigate"),
                Hint::keys("", "Enter", " Open"),
            ];
            if on_worktree {
                hints.push(Hint::keys("", "D", " Delete worktree"));
            }
            hints.push(Hint::keys(
                "",
                "R",
                if chooser.refreshing {
                    " Refresh ..."
                } else {
                    " Refresh"
                },
            ));
            hints.push(Hint::keys("", "Esc", " Back").at_end());
            hints
        }
    };
    let section = |key: &str, name: &str| {
        GroupHead::new(format!("{w}.repo.{key}"), name.to_owned())
            .section(name.to_owned())
            .heading()
            .build(t)
    };
    let body = vec![Block::custom(
        format!("{w}.repo.rows.block"),
        Stack::tight()
            .key(repo::root_key(which))
            .role("group")
            .label("Repository options")
            .focusable(true)
            .w_full()
            .children(refreshing)
            .child(section("new", "New worktree"))
            .child(form)
            .child(section("existing", "Open existing"))
            .child(
                Stack::tight()
                    .key(format!("{w}.repo.rows"))
                    .role("listbox")
                    .label("Open existing")
                    .w_full()
                    .children(rows),
            ),
    )];
    vec![
        finder::scrolling_notices(t, repo::body_key(which), body),
        footer(w, hints, t),
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

/// What a worktree's row says while it is being deleted: its line, the line under it, its
/// role and its name.
fn delete_said(delete: &Delete) -> (String, Option<String>, &'static str, String) {
    let name = paths::basename(delete.path()).to_owned();
    match delete {
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
    }
}

/// The corner prompt for an agent starting in a new worktree, and the dialog it opens when the
/// worktree could not be made.
pub(crate) fn worktree_setup(state: &State, t: &Tokens) -> Vec<El> {
    let Some(setup) = state.window.launching.worktree.as_ref() else {
        return Vec::new();
    };
    if let (Some(error), true) = (&setup.failed, setup.opened) {
        let mut layers = Vec::new();
        let folder = match &setup.step {
            repo::Step::SyncingRepo(path) | repo::Step::CreatingWorktree(path) => {
                Some(paths::tildify(path))
            }
            repo::Step::FetchingDetails => None,
        };
        Dialog::new("worktree-setup", "Session was not created")
            .lead(setup.subject.clone())
            .size(DialogSize::Form)
            .on_close(|state: &mut State| state.window.launching.worktree = None)
            .push_all(folder.map(|folder| {
                FieldBlock::new("worktree-setup.folder", "Folder")
                    .value(folder)
                    .build(t)
            }))
            .push(Notice::error("worktree-setup.error", format!("Create failed: {error}")).build(t))
            .actions(
                Actions::new("worktree-setup.actions").then_end().push(
                    attn_ui::system::Button::new("Dismiss")
                        .on_press(|state: &mut State| state.window.launching.worktree = None)
                        .build(t)
                        .key("worktree-setup.dismiss"),
                ),
            )
            .build(t, &mut layers);
        return layers;
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
    let lines = Stack::tight()
        .child(words("Session setup", Type::Label, t.text_tertiary))
        .child(words(title.clone(), Type::Body, t.text_primary))
        .child(words(detail.clone(), Type::Small, t.text_secondary))
        .child(words(last, Type::Small, t.text_tertiary));
    let name = format!("{title}: {detail}");
    let corner: El = if setup.failed.is_some() {
        press_area(
            "worktree-setup",
            &name,
            false,
            false,
            lines,
            t,
            |state: &mut State, _: &mut Tx| {
                if let Some(setup) = &mut state.window.launching.worktree {
                    setup.opened = true;
                }
            },
        )
        .into()
    } else {
        lines.key("worktree-setup").role("status").label(name)
    };
    vec![finder::corner_card(t, "worktree-setup.corner", corner)]
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

/// A picker's footer: its key hints over a rule, keyed `{w}.hints`.
fn footer(w: &str, hints: Vec<Hint>, t: &Tokens) -> El {
    finder::footer(t, w, hints)
        .key(format!("{w}.hints"))
        .border_sides([1.0, 0.0, 0.0, 0.0], t.border_subtle)
}
