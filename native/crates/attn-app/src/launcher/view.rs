use attn_ui::colors::mix_srgb;
use attn_ui::{Tokens, icon};
use rune::{Color, El, KeyPlatform, Span, TextCase, TextStyle, Tx, col, el, row, text};
use rune_kit::TextInput;

use super::{
    Address, Kind, Launcher, Listing, Memory, Row, WhereRow, address_of, completion, machine_name,
    machine_of, machine_prefix, memory, outpost, outposts, reachable, split_browse,
};
use crate::State;
use crate::picker::{
    DIALOG_W, TERMINAL, Which, agent_option, agent_options, catalog_note, checking_mark,
    desktop_label, paths, yolo, yolo_switch,
};

const TITLE: TextStyle = TextStyle::new(10.0, 500.0).mono().tracking(1.5);
const FIELD: TextStyle = TextStyle::new(14.0, 400.0).mono();
const CHIP: TextStyle = TextStyle::new(12.0, 400.0).mono();
const SECTION: TextStyle = TextStyle::new(10.0, 600.0).mono().tracking(1.2);
const SECTION_WORDS: TextStyle = TextStyle::new(10.0, 400.0).mono();
const SECTION_NAME: TextStyle = TextStyle::new(10.0, 600.0).mono();
const NAME: TextStyle = TextStyle::new(13.0, 500.0);
const PATH: TextStyle = TextStyle::new(11.0, 400.0).mono();
const BADGE: TextStyle = TextStyle::new(10.5, 400.0).mono();
const OPTION: TextStyle = TextStyle::new(11.5, 400.0).mono();
const OPTION_CHOSEN: TextStyle = TextStyle::new(11.5, 600.0).mono();
const SHORTCUT: TextStyle = TextStyle::new(10.0, 400.0).mono();
const TOGGLE: TextStyle = TextStyle::new(11.0, 400.0).mono();
const CAP: TextStyle = TextStyle::new(10.5, 400.0).mono();
const HINT: TextStyle = TextStyle::new(11.5, 400.0);
const MESSAGE: TextStyle = TextStyle::new(12.5, 400.0);
const STATUS: TextStyle = TextStyle::new(11.0, 400.0).mono();
const ICON_SIZE: f32 = 14.0;
const RADIUS: f32 = 10.0;

/// The launcher's own shades of the color scheme, as Tauri's `--launcher-*` variables mix them.
struct Look {
    accent: Color,
    accent_text: Color,
    on_accent: Color,
    bg_input: Color,
    bg_tile: Color,
    border_tile: Color,
    border_input: Color,
    hairline: Color,
    footer: Color,
    frame: Color,
    text_quiet: Color,
    text_path: Color,
    text_section: Color,
    text_faint: Color,
    worktree: Color,
}

impl Look {
    fn of(t: &Tokens) -> Look {
        let (black, white) = (t.shadow, t.highlight);
        let dark = t.dark;
        Look {
            accent: t.brand,
            accent_text: if dark {
                mix_srgb(t.brand, white, 0.2)
            } else {
                mix_srgb(t.brand, black, 0.17)
            },
            on_accent: mix_srgb(t.brand, black, 0.89),
            bg_input: if dark {
                mix_srgb(t.bg_input, black, 0.3)
            } else {
                t.bg_input
            },
            bg_tile: if dark {
                mix_srgb(t.bg_elevated, t.bg_panel, 0.64)
            } else {
                t.bg_elevated
            },
            border_tile: if dark {
                mix_srgb(t.border, t.border_subtle, 0.67)
            } else {
                t.border_subtle
            },
            border_input: t.border,
            hairline: if dark {
                mix_srgb(t.border_subtle, black, 0.11)
            } else {
                t.border_subtle
            },
            footer: black.alpha(if dark { 0.28 } else { 0.03 }),
            frame: if dark {
                mix_srgb(t.border, t.border_subtle, 0.3)
            } else {
                t.border_subtle
            },
            text_quiet: if dark {
                t.text_tertiary
            } else {
                t.text_secondary
            },
            text_path: if dark {
                mix_srgb(t.text_tertiary, t.text_muted, 0.49)
            } else {
                t.text_tertiary
            },
            text_section: if dark { t.text_muted } else { t.text_tertiary },
            text_faint: if dark {
                mix_srgb(t.text_muted, t.text_dimmed, 0.35)
            } else {
                t.text_muted
            },
            worktree: if dark {
                mix_srgb(t.success, white, 0.5)
            } else {
                t.success
            },
        }
    }
}

fn keycaps(spec: &str) -> Vec<String> {
    rune::Chord::parse(spec)
        .expect("a launcher chord parses")
        .keycaps_on(KeyPlatform::current())
}

/// `spec` as this platform writes the chord: `⌥Q` on a Mac, `Alt+Q` elsewhere.
fn combo(spec: &str) -> String {
    let joint = if KeyPlatform::current() == KeyPlatform::Mac {
        ""
    } else {
        "+"
    };
    keycaps(spec).join(joint)
}

/// Launcher 2.0 in the box the dialog and the empty desktop show it in.
pub(crate) fn framed(state: &State, launcher: &Launcher, t: &Tokens) -> El {
    let look = Look::of(t);
    rune_kit::popover::raised(attn_ui::kit(t), frame(state, launcher, t, &look), RADIUS)
        .w_full()
        .max_w(DIALOG_W)
        .fill(t.bg_app)
        .border(1.0, look.frame)
        .clip_rounded(RADIUS)
}

fn frame(state: &State, launcher: &Launcher, t: &Tokens, look: &Look) -> El {
    let which = launcher.which;
    let w = which.key();
    let terminal = launcher.agent(state) == TERMINAL;
    let body = match &launcher.place {
        Some(place) => where_list(state, launcher, place, t, look),
        None => locations(state, launcher, terminal, t, look),
    };
    col()
        .on_key(move |state: &mut State, ev: &rune::KeyEvent, tx: &mut Tx| {
            super::on_key(state, which, ev, tx)
        })
        .child(controls(state, launcher, terminal, t, look))
        .child(header(state, launcher, terminal, t, look))
        .child(body)
        .children(launcher.problem.as_ref().map(|problem| {
            let ink = mix_srgb(t.error, t.highlight, 0.6);
            col()
                .key(format!("{w}.error"))
                .role("alert")
                .label(problem.message.clone())
                .mx(16.0)
                .my(10.0)
                .px(10.0)
                .py(8.0)
                .gap(2.0)
                .radius(6.0)
                .fill(t.error.alpha(0.1))
                .border(1.0, t.error.alpha(0.3))
                .child(text(problem.message.clone()).font(MESSAGE).color(ink))
                .children(problem.reason.as_ref().map(|reason| {
                    text(reason.clone())
                        .key(format!("{w}.error.reason"))
                        .label(reason.clone())
                        .font(MESSAGE)
                        .color(ink.alpha(0.75))
                }))
        }))
        .children(launcher.busy.then(|| progress(state, launcher, t)))
        .child(hints(state, launcher, terminal, look))
}

/// The harness toggle with one ⌥ before its bare key letters, and YOLO on the same row.
fn controls(state: &State, launcher: &Launcher, terminal: bool, t: &Tokens, look: &Look) -> El {
    let which = launcher.which;
    let w = which.key();
    let offer = launcher.offer(state);
    let options = agent_options(state, &launcher.choice, &offer)
        .into_iter()
        .map(|option| {
            let name = text(option.label.clone())
                .font(if option.chosen { OPTION_CHOSEN } else { OPTION })
                .color(if option.chosen {
                    look.on_accent
                } else {
                    look.text_quiet
                });
            let name = if option.absent {
                name.strike(0, option.label.len(), None)
            } else {
                name
            };
            agent_option(which, &option)
                .gap(7.0)
                .px(10.0)
                .py(4.0)
                .radius(6.0)
                .fill(if option.chosen {
                    look.accent
                } else {
                    Color::TRANSPARENT
                })
                .opacity(if option.selectable { 1.0 } else { 0.45 })
                .child(name)
                .children(option.key.as_ref().map(|key| {
                    text(key.clone()).font(SHORTCUT).color(if option.chosen {
                        look.on_accent.alpha(0.6)
                    } else {
                        look.text_faint
                    })
                }))
                .children(checking_mark(which, &option, t))
        });
    let legend = keycaps("alt+t").swap_remove(0);
    let agents = row()
        .items_center()
        .flex_wrap()
        .gap(6.0)
        .children(catalog_note(state, which, t))
        .child(text(legend).font(CAP).color(look.text_faint).a11y_hidden())
        .child(
            row()
                .key(format!("{w}.agents"))
                .role("radiogroup")
                .label("Session agent")
                .items_center()
                .flex_wrap()
                .shrink()
                .gap(2.0)
                .padding(2.0)
                .radius(8.0)
                .fill(look.bg_input)
                .border(1.0, look.border_tile)
                .children(options),
        );
    let yolo = (!terminal).then(|| {
        let yolo = yolo(state, &launcher.choice, &offer);
        let dot = el().w(7.0).h(7.0).radius(999.0);
        let dot = if yolo.on {
            dot.fill(look.accent)
        } else {
            dot.border(1.5, look.text_faint)
        };
        yolo_switch(which, &yolo)
            .gap(6.0)
            .px(8.0)
            .py(4.0)
            .opacity(if yolo.supported { 1.0 } else { 0.45 })
            .child(dot)
            .child(text("YOLO").font(TOGGLE).color(if yolo.on {
                look.accent_text
            } else {
                look.text_quiet
            }))
            .children(
                yolo.supported
                    .then(|| text(combo("alt+q")).font(SHORTCUT).color(look.text_faint)),
            )
    });
    row()
        .w_full()
        .items_center()
        .justify_between()
        .flex_wrap()
        .gaps(8.0, 14.0)
        .px(16.0)
        .py(10.0)
        .fill(t.highlight.alpha(0.015))
        .border_sides([0.0, 0.0, 1.0, 0.0], look.hairline)
        .child(agents)
        .children(yolo)
}

/// What a start under way says: where it starts, and how long a stalled outpost has not
/// answered.
fn progress(state: &State, launcher: &Launcher, t: &Tokens) -> El {
    let host = launcher.starting.as_ref().and_then(|s| s.host.as_deref());
    let mut message = match host {
        Some(_) => format!("Starting on {}…", machine_name(outposts(state), host)),
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
    text(message.clone())
        .key(format!("{}.progress", launcher.which.key()))
        .role("status")
        .label(message)
        .font(STATUS)
        .color(if stalled.is_some() {
            t.warning
        } else {
            t.text_secondary
        })
        .px(16.0)
        .py(10.0)
}

fn header(state: &State, launcher: &Launcher, terminal: bool, t: &Tokens, look: &Look) -> El {
    let which = launcher.which;
    let w = which.key();
    let outposts = outposts(state);
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
        None if outposts.is_empty() => (&launcher.query, "Project or path"),
        None => (&launcher.query, "Project, path, or machine:path"),
    };
    let ghost = launcher
        .place
        .is_none()
        .then(|| ghost(state, launcher))
        .flatten();
    let chip = launcher.place.as_ref().map(|place| {
        text(format!("{} ›", paths::basename(&place.repo)))
            .key(format!("{w}.repo-chip"))
            .font(CHIP)
            .color(t.text_secondary)
            .px(8.0)
            .py(3.0)
            .radius(6.0)
            .fill(look.bg_tile)
            .border(1.0, look.border_input)
    });
    let prefix = match launcher.place {
        Some(_) => "",
        None => machine_prefix(&launcher.query.text, outposts),
    };
    let accent = (!prefix.is_empty())
        .then(|| vec![Span::new(0, prefix.len()).color(mix_srgb(t.accent, t.highlight, 0.33))]);
    let mut input = TextInput::new(edit)
        .key(launcher.field_key())
        .plain()
        .text_style(FIELD)
        .height(40.0)
        .width(None)
        .label("Path")
        .placeholder(placeholder)
        .on_edit(move |state: &mut State| super::field_mut(state, which))
        .on_change(move |state: &mut State, tx: &mut Tx| super::edited(state, which, tx))
        .on_key(move |state: &mut State, ev: &rune::KeyEvent, tx: &mut Tx| {
            super::on_key(state, which, ev, tx)
        });
    if let Some(spans) = accent {
        input = input.spans(spans);
    }
    let field = row()
        .w_full()
        .h(44.0)
        .px(12.0)
        .items_center()
        .gap(8.0)
        .radius(8.0)
        .fill(look.bg_input)
        .border(1.0, look.border_input)
        .focus_within_ring(1.0, -1.0, look.accent.alpha(0.55))
        .children(chip)
        .child(input.build(attn_ui::kit(t)).grow(1.0))
        .children(ghost.map(|g| {
            text(g.clone())
                .key(format!("{w}.ghost"))
                .label(g)
                .font(FIELD)
                .color(t.text_muted)
        }));
    let address = launcher.address(state);
    let status = launcher
        .place
        .is_none()
        .then(|| status_line(state, launcher, &address))
        .flatten();
    col()
        .w_full()
        .px(16.0)
        .pt(14.0)
        .pb(12.0)
        .border_sides([0.0, 0.0, 1.0, 0.0], look.hairline)
        .child(
            text(title.clone())
                .key(format!("{w}.title"))
                .role("heading")
                .label(title)
                .font(TITLE)
                .text_case(TextCase::Uppercase)
                .color(look.accent)
                .ml(2.0)
                .mb(9.0),
        )
        .child(field)
        .children(status.map(|(line, bad)| {
            text(line.clone())
                .key(format!("{w}.status"))
                .role("status")
                .label(line)
                .font(STATUS)
                .color(if bad {
                    mix_srgb(t.error, t.highlight, 0.47)
                } else {
                    look.text_quiet
                })
                .mx(2.0)
                .mt(8.0)
        }))
}

/// A list's section header: letter-spaced capitals.
fn section(title: &str, look: &Look) -> El {
    text(title.to_owned())
        .font(SECTION)
        .text_case(TextCase::Uppercase)
        .color(look.text_section)
        .px(10.0)
        .pt(10.0)
        .pb(6.0)
}

/// The scrolling list under the field.
fn results(key: String, label: &str, children: Vec<El>) -> El {
    col()
        .key(key)
        .role("listbox")
        .label(label)
        .w_full()
        .max_h(340.0)
        .scroll(rune::ScrollSpec {
            stick_bottom: false,
        })
        .px(8.0)
        .pt(6.0)
        .pb(8.0)
        .children(children)
}

/// A row's glyph in its tile, which turns to the accent while the row is highlighted.
fn tile(glyph: &'static str, highlighted: bool, look: &Look) -> El {
    row()
        .w(28.0)
        .h(28.0)
        .center()
        .radius(6.0)
        .fill(if highlighted {
            look.accent.alpha(0.14)
        } else {
            look.bg_tile
        })
        .border(
            1.0,
            if highlighted {
                look.accent.alpha(0.35)
            } else {
                look.border_tile
            },
        )
        .child(
            text(glyph)
                .font(TextStyle::new(ICON_SIZE, 400.0))
                .color(if highlighted {
                    look.accent_text
                } else {
                    look.text_quiet
                }),
        )
}

/// A list row: the accent border and tint while highlighted, a faint wash under the pointer.
fn option(key: String, name: &str, highlighted: bool, look: &Look, t: &Tokens) -> El {
    row()
        .key(key)
        .role("option")
        .label(name.to_owned())
        .focus_selected(highlighted)
        .w_full()
        .items_center()
        .gap(12.0)
        .px(10.0)
        .py(7.0)
        .radius(7.0)
        .border(
            1.0,
            if highlighted {
                look.accent.alpha(0.22)
            } else {
                Color::TRANSPARENT
            },
        )
        .fill(if highlighted {
            look.accent.alpha(0.075)
        } else {
            Color::TRANSPARENT
        })
        .hover(if highlighted {
            look.accent.alpha(0.075)
        } else {
            t.highlight.alpha(0.025)
        })
}

/// A row's name over its path.
fn info(name: String, detail: Option<String>, highlighted: bool, t: &Tokens, look: &Look) -> El {
    col()
        .grow(1.0)
        .shrink()
        .min_w(0.0)
        .child(
            text(name)
                .font(NAME)
                .color(t.text_primary)
                .single_line()
                .truncate(),
        )
        .children(detail.map(|d| {
            text(d)
                .font(PATH)
                .color(if highlighted {
                    look.text_quiet
                } else {
                    look.text_path
                })
                .single_line()
                .truncate()
                .mt(2.0)
        }))
}

fn badge(word: String, look: &Look) -> El {
    text(word.clone())
        .label(word)
        .font(BADGE)
        .color(look.text_faint)
}

fn age_badge(age: String, look: &Look) -> El {
    row()
        .w(30.0)
        .justify_end()
        .child(text(age).font(BADGE).color(look.text_faint))
}

fn locations(state: &State, launcher: &Launcher, terminal: bool, t: &Tokens, look: &Look) -> El {
    let w = launcher.which.key();
    let outposts = outposts(state);
    let rows = launcher.rows(state);
    let selected = launcher.selected(state, &rows);
    let address = launcher.address(state);
    let browsed = match &address {
        Address::Browse { path, machine, .. } => {
            let host = machine.host.as_deref();
            Some((launcher.listing(state, host, &split_browse(path).0), host))
        }
        _ => None,
    };
    let folder_title = match browsed {
        Some((Some(Listing::Listed { directory, .. }), host)) => format!(
            "Folders in {}/{}",
            launcher
                .display(state, directory, host)
                .trim_end_matches('/'),
            if outposts.is_empty() {
                String::new()
            } else {
                format!(" on {}", machine_name(outposts, host))
            }
        ),
        _ => "Folders".to_owned(),
    };
    let mut children = Vec::new();
    for (i, r) in rows.iter().enumerate() {
        if i == 0 || rows[i - 1].kind != r.kind {
            let title = match r.kind {
                Kind::Recent => "Recent".to_owned(),
                Kind::Machine => "Browse a machine".to_owned(),
                Kind::Elsewhere => "Found elsewhere".to_owned(),
                Kind::Folder => folder_title.clone(),
            };
            children.push(section(&title, look).key(format!("{w}.section.{i}")));
        }
        children.push(location_row(
            state,
            launcher,
            i,
            r,
            selected == Some(i),
            terminal,
            t,
            look,
        ));
    }
    if rows.is_empty() {
        let empty = match browsed {
            None => Some("No recent project matches. Type ~/ to browse folders."),
            Some((listing, host)) => (matches!(listing, Some(Listing::Listed { .. }))
                || !reachable(outposts, host))
            .then_some("No folders match. Enter uses the path as typed."),
        };
        children.extend(empty.map(|message| {
            text(message)
                .key(format!("{w}.empty"))
                .label(message)
                .font(MESSAGE)
                .color(t.text_dimmed)
                .px(12.0)
                .py(16.0)
        }));
    }
    results(format!("{w}.locations"), "Locations", children)
}

/// The dot before a machine's name: green while connected, amber while stalled, red once lost
/// or refused, a ring for this machine and any other state.
fn machine_dot(state: Option<&str>, t: &Tokens, look: &Look) -> El {
    let dot = el().w(6.0).h(6.0).radius(999.0);
    match state {
        Some("connected") => dot.fill(t.success),
        Some("stalled") => dot.fill(t.warning),
        Some("lost" | "refused") => dot.fill(t.error),
        _ => dot.border(1.5, look.text_faint),
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
    look: &Look,
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
        Kind::Recent => Some(launcher.display(state, &r.path, host)),
        Kind::Machine => Some(format!(
            "{} · Tab browses {}",
            host.and_then(|h| outpost(outposts, h))
                .map(|o| o.state.clone())
                .unwrap_or_default(),
            address_of(&machine_of(outposts, host), "~/")
        )),
        Kind::Elsewhere => Some(format!(
            "Enter browses {}",
            address_of(&machine_of(outposts, host), &format!("{}/", r.path))
        )),
        Kind::Folder => None,
    };
    let badge_el = r.repo_root.as_ref().and_then(|root| {
        if !terminal && (r.kind == Kind::Recent || highlighted) {
            Some(match memory(state, root, host) {
                Memory::NewWorktree => {
                    let word = "⎇ new worktree";
                    text(word)
                        .label(word)
                        .font(BADGE)
                        .color(look.worktree)
                        .px(6.0)
                        .py(1.0)
                        .radius(4.0)
                        .fill(t.success.alpha(0.09))
                }
                Memory::MainRepo => badge("main checkout".to_owned(), look).color(look.text_quiet),
            })
        } else {
            (r.kind == Kind::Folder).then(|| badge("repo".to_owned(), look))
        }
    });
    let badge_el = badge_el.map(|b| b.key(format!("{w}.row.{i}.badge")));
    let reach = host.map(|h| outpost(outposts, h).map(|o| o.reach.clone()));
    let machine =
        (matches!(r.kind, Kind::Recent | Kind::Elsewhere) && !outposts.is_empty()).then(|| {
            let reach_word = match &reach {
                None => "local".to_owned(),
                Some(reach) => reach.clone().unwrap_or_else(|| "unreachable".to_owned()),
            };
            let name = machine_name(outposts, host);
            let shown = host.map(|h| outpost(outposts, h).map_or("lost", |o| o.state.as_str()));
            row()
                .key(format!("{w}.row.{i}.machine"))
                .label(name.clone())
                .description(reach_word)
                .items_center()
                .gap(5.0)
                .child(machine_dot(shown, t, look))
                .child(badge(name, look))
        });
    let unreachable = r.kind != Kind::Machine
        && reach
            .as_ref()
            .is_some_and(|reach| reach.as_deref().is_none_or(|r| r == "unreachable"));
    let age = r
        .last_seen
        .as_deref()
        .map(|seen| age(seen, state.window.now_ms));
    let glyph = match r.kind {
        Kind::Recent => icon::CLOCK,
        Kind::Folder if r.repo_root.is_some() => icon::REPO,
        Kind::Folder => icon::FOLDER,
        Kind::Machine => icon::DESKTOP,
        Kind::Elsewhere => icon::SEARCH,
    };
    let picked = r.clone();
    let mut row_el = option(format!("{w}.row.{i}"), &name, highlighted, look, t)
        .on_press_tx(move |state: &mut State, tx: &mut Tx| {
            super::pick(state, which, i, picked.clone(), tx)
        })
        .child(tile(glyph, highlighted, look))
        .child(info(name.clone(), detail.clone(), highlighted, t, look))
        .children(badge_el)
        .children(machine)
        .children(age.filter(|a| !a.is_empty()).map(|a| age_badge(a, look)));
    if unreachable {
        row_el = row_el.opacity(0.55);
    }
    if let Some(detail) = detail {
        row_el = row_el.description(detail);
    }
    row_el
}

fn where_list(
    state: &State,
    launcher: &Launcher,
    place: &super::Where,
    t: &Tokens,
    look: &Look,
) -> El {
    let which = launcher.which;
    let w = which.key();
    let host = place.host.as_deref();
    if place.info.is_none() {
        let reading = format!("Reading {}…", paths::basename(&place.repo));
        return col()
            .key(format!("{w}.where"))
            .children(launcher.problem.is_none().then(|| {
                text(reading.clone())
                    .key(format!("{w}.reading"))
                    .role("status")
                    .label(reading)
                    .font(MESSAGE)
                    .color(t.text_dimmed)
                    .px(16.0)
                    .py(16.0)
            }));
    }
    let rows = place.rows();
    let name = launcher.worktree_name();
    let typed = !place.branch.text.trim().is_empty();
    let memory = memory(state, &place.repo, host);
    let shown = |path: &str| launcher.display(state, path, host);
    let mut children = Vec::new();
    for (i, r) in rows.iter().enumerate() {
        let after_new = i > 0 && matches!(rows[i - 1], WhereRow::New { .. });
        if i == 0 {
            children.push(new_worktree_section(&name, typed, look).key(format!("{w}.where.new")));
        } else if after_new && !matches!(r, WhereRow::New { .. }) {
            children.push(section("Open existing", look).key(format!("{w}.where.existing")));
        }
        children.push(where_row(
            launcher,
            i,
            r,
            place.highlight == Some(i),
            memory,
            &shown(&place.repo),
            &shown,
            state.window.now_ms,
            t,
            look,
        ));
    }
    results(format!("{w}.where"), "Where", children)
}

/// "NEW WORKTREE · name · type to rename", the name in green and as typed.
fn new_worktree_section(name: &str, typed: bool, look: &Look) -> El {
    let label = if typed {
        format!("New worktree · {name}")
    } else {
        format!("New worktree · {name} · type to rename")
    };
    row()
        .label(label)
        .items_center()
        .px(10.0)
        .pt(10.0)
        .pb(6.0)
        .child(
            text("New worktree")
                .font(SECTION)
                .text_case(TextCase::Uppercase)
                .color(look.text_section),
        )
        .child(text(" · ").font(SECTION_WORDS).color(look.text_section))
        .child(
            text(name.to_owned())
                .font(SECTION_NAME)
                .color(look.worktree),
        )
        .children((!typed).then(|| {
            text(" · type to rename")
                .font(SECTION_WORDS)
                .color(look.text_section)
        }))
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
    look: &Look,
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
            Some(badge("last used".to_owned(), look))
        }
        WhereRow::New { current: false, .. } if memory == Memory::NewWorktree => {
            Some(badge("last used".to_owned(), look))
        }
        WhereRow::Existing { .. } if deleting.is_some() => Some(badge("y / n".to_owned(), look)),
        WhereRow::Existing { created_at, .. } => created_at
            .as_deref()
            .map(|at| age(at, now_ms))
            .filter(|a| !a.is_empty())
            .map(|a| age_badge(a, look)),
        _ => None,
    };
    let glyph = match (r, deleting) {
        (_, Some(_)) => icon::TRASH,
        (WhereRow::New { .. }, _) => icon::PLUS,
        (WhereRow::Main { .. }, _) => icon::COMMIT,
        (WhereRow::Existing { .. }, _) => icon::BRANCH,
    };
    let picked = r.clone();
    option(
        format!("{}.where.row.{i}", which.key()),
        &name,
        highlighted,
        look,
        t,
    )
    .description(detail.clone())
    .on_press_tx(move |state: &mut State, tx: &mut Tx| {
        super::pick_destination(state, which, i, picked.clone(), tx)
    })
    .child(tile(glyph, highlighted, look))
    .child(info(name, Some(detail), highlighted, t, look))
    .children(trailing)
}

/// One footer hint: its keys as keycaps among its words.
enum Piece {
    Key(String),
    Words(&'static str),
}

fn hints(state: &State, launcher: &Launcher, terminal: bool, look: &Look) -> El {
    use Piece::{Key, Words};
    let accel = |key: &str| {
        let modifier = if KeyPlatform::current() == KeyPlatform::Mac {
            "⌘"
        } else {
            "Ctrl+"
        };
        format!("{modifier}{key}")
    };
    let mut hints: Vec<Vec<Piece>> = Vec::new();
    let mut back = None;
    if launcher.stalled(state).is_some() {
        hints.push(vec![
            Key("Esc".into()),
            Words("keep waiting in the background"),
        ]);
    } else if launcher.busy {
    } else if launcher.deletion.is_some() {
        hints.push(vec![Key("y".into()), Words("delete")]);
        hints.push(vec![
            Key("n".into()),
            Words("or"),
            Key("Esc".into()),
            Words("keep it"),
        ]);
    } else {
        let place = launcher.place.as_ref();
        if place.is_some() {
            hints.push(vec![Words("type to name it")]);
        }
        hints.push(vec![
            Key("↑↓".into()),
            Words(if place.is_some() { "choose" } else { "move" }),
        ]);
        hints.push(vec![
            Key("Enter".into()),
            Words(if place.is_none() && terminal {
                "open terminal"
            } else {
                "start"
            }),
        ]);
        if place.is_none() {
            let rows = launcher.rows(state);
            let selected = launcher.selected(state, &rows).and_then(|i| rows.get(i));
            let typed_repo = selected.is_none()
                && match launcher.address(state) {
                    Address::Browse { path, machine, .. } => {
                        launcher.known_repo(state, machine.host.as_deref(), &path) == Some(true)
                    }
                    _ => false,
                };
            let where_next =
                !terminal && (selected.is_some_and(|r| r.repo_root.is_some()) || typed_repo);
            hints.push(vec![
                Key("Tab".into()),
                Words(if where_next { "where…" } else { "complete" }),
            ]);
        }
        if place.is_some() || !terminal {
            hints.push(vec![Key(accel("Enter")), Words("terminal here")]);
        }
        if place.is_none() && !outposts(state).is_empty() {
            hints.push(vec![Key("name:".into()), Words("machine")]);
        }
        if let Some(place) = place
            && matches!(
                place.highlight.and_then(|i| place.rows().get(i).cloned()),
                Some(WhereRow::Existing { .. })
            )
        {
            hints.push(vec![Key(accel("⌫")), Words("delete")]);
        }
        back = Some(vec![
            Key("Esc".into()),
            Words(if place.is_some() { "back" } else { "close" }),
        ]);
    }
    row()
        .key(format!("{}.hints", launcher.which.key()))
        .w_full()
        .items_center()
        .flex_wrap()
        .gaps(6.0, 16.0)
        .px(16.0)
        .py(9.0)
        .fill(look.footer)
        .border_sides([1.0, 0.0, 0.0, 0.0], look.hairline)
        .children(hints.into_iter().map(|pieces| hint(pieces, look)))
        .children(back.map(|pieces| row().grow(1.0).justify_end().child(hint(pieces, look))))
}

fn hint(pieces: Vec<Piece>, look: &Look) -> El {
    let name = pieces
        .iter()
        .map(|p| match p {
            Piece::Key(key) => key.as_str(),
            Piece::Words(words) => words,
        })
        .collect::<Vec<_>>()
        .join(" ");
    row()
        .label(name)
        .items_center()
        .gap(5.0)
        .children(pieces.into_iter().map(|piece| {
            match piece {
                Piece::Key(key) => text(key)
                    .font(CAP)
                    .color(look.text_quiet)
                    .px(5.0)
                    .py(1.0)
                    .radius(4.0)
                    .fill(look.bg_tile)
                    .border_sides([1.0, 1.0, 2.0, 1.0], look.border_input)
                    .a11y_hidden(),
                Piece::Words(words) => text(words).font(HINT).color(look.text_faint).a11y_hidden(),
            }
        }))
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
    let selected = launcher.selected(state, &rows).and_then(|i| rows.get(i));
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
