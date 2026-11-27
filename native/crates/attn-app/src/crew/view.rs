use std::collections::HashMap;
use std::rc::Rc;
use std::sync::Arc;

use attn_protocol::{CrewMember, Seed};
use attn_ui::Tokens;
use attn_ui::blocks::{
    Actions, Block, Field, Head, Inline, List, Notice, Page, Prose, Refusal, Section, Sheet, Stack,
};
use attn_ui::system::{
    Button, DotShape, DotSize, FieldState, ListRow, Look, Size, Tone, Type, area_box, dot,
    line_box, tag, words,
};
use rune::{El, GrowthSpec, Key, KeyEvent, Tx, col, row};
use rune_kit::{TextArea, TextInput};

use super::autosave::Phase;
use super::handoffs::Reading;
use super::seeds::{self, SeedFilter};
use super::{Nav, Tab, charter, handoffs, launch};
use crate::State;
use crate::settings::RouteTarget;

pub(crate) const RENAME: &str = "crew.rename";
pub(crate) const RENAME_FIELD: &str = "crew.rename.field";
/// A member's presence mark, as large as a row's state mark.
const FIND: &str = "crew.find";
const CHARTER: &str = "crew.charter.editor";
/// The charter editor's height: about twenty lines of a charter show at once, and a longer
/// charter scrolls inside it.
const CHARTER_HEIGHT: f32 = 390.0;
const SEED_FIND: &str = "crew.seeds.find";
/// The width of the list of a member's letters: a row gives its words all but its 16 of insets,
/// and its longest line, "Latest · 09:58 UTC", measures 107 in `Small`.
const INDEX_WIDTH: f32 = 150.0;
/// The narrowest the member's details get beside the roster: its tabs on one line measure
/// 303 (crew.tabs, Control at 12), and the page's sides take 32 each. Narrower, the roster
/// stands above the details.
const DETAIL_FLOOR: f32 = 368.0;
/// The tallest the roster stands above the details: three rows of two lines and its heading.
const STACKED_ROSTER: f32 = 200.0;
/// The roster's width: a row gives its words all but 56 (the initial, the insets and the
/// presence mark), and the longest line a member shows here, "Between sessions · Default ›
/// assistant", measures 216 in `Small`.
const ROSTER_WIDTH: f32 = 272.0;
pub(crate) const PAGE: &str = "crew.page";

/// Manage crew, while it is open: a page in place of the window's main area, beside the
/// sidebar, that holds the keyboard until it closes.
pub(crate) fn page(state: &State, t: &Tokens) -> El {
    let close = El::from(
        Button::new("Close")
            .size(Size::Compact)
            .look(Look::Quiet)
            .trailing(attn_ui::keycap::Keys::ledged(&["esc"]).build(t))
            .on_press_tx(|state: &mut State, tx: &mut Tx| super::close(state, tx))
            .build(t)
            .key("crew.close-page"),
    );
    let stacked = state
        .window
        .crew
        .width
        .is_some_and(|w| w < ROSTER_WIDTH + DETAIL_FLOOR);
    let shell = if stacked {
        col()
            .child(roster(state, t, true))
            .child(detail(state, t).grow(1.0).min_h(0.0))
    } else {
        row().child(roster(state, t, false)).child(detail(state, t))
    };
    let shell = shell
        .key("crew.shell")
        .w_full()
        .h_full()
        .min_h(0.0)
        .on_layout_tx(|state: &mut State, [_, _, w, _], tx: &mut Tx| {
            if state.window.crew.width == Some(w) {
                tx.live_only();
                return;
            }
            state.window.crew.width = Some(w);
        });
    Sheet::new("crew.sheet", "Manage crew")
        .close(close)
        .fill(shell)
        .build(t)
        .key(PAGE)
        .role("dialog")
        .label("Manage crew")
        .modal_tx(false, |state: &mut State, tx: &mut Tx| {
            super::close(state, tx)
        })
        .grow(1.0)
        .min_w(0.0)
        .fill(t.bg_panel)
        .children(crate::desktop::header_pop::in_sheet(state, t))
}

/// The pickers Manage crew opens over itself.
pub(crate) fn overlays(state: &State, t: &Tokens, layers: &mut Vec<El>) {
    crate::settings::route_overlay(state, t, layers);
    crate::launch_desktop::overlay(state, t, layers);
}

/// The member's initial, leading its row.
fn avatar(name: &str, t: &Tokens) -> El {
    let initial: String = name.chars().take(1).flat_map(char::to_uppercase).collect();
    words(initial, Type::Name, t.success).a11y_hidden()
}

fn presence_mark(member: &CrewMember, t: &Tokens) -> El {
    if member.binding_session.is_some() {
        dot(t.success, DotShape::Filled, DotSize::Regular)
    } else {
        dot(t.text_dimmed, DotShape::Hollow, DotSize::Regular)
    }
}

fn roster(state: &State, t: &Tokens, stacked: bool) -> El {
    let members = super::members(state);
    let selected = super::selected(state).map(|m| m.id.clone());
    let filter = state
        .window
        .crew
        .place
        .roster_filter
        .text
        .trim()
        .to_lowercase();
    let count = members.len();
    let find = (count > 5).then(|| {
        let input = TextInput::new(&state.window.crew.place.roster_filter)
            .key(FIND)
            .label("Find a member")
            .placeholder("Find a member…")
            .on_edit(|state: &mut State| &mut state.window.crew.place.roster_filter);
        El::from(line_box(input, FieldState::Ready, t))
    });
    let visible: Vec<&CrewMember> = members
        .into_iter()
        .filter(|m| filter.is_empty() || m.name.to_lowercase().contains(&filter))
        .collect();
    let ids: Vec<String> = visible.iter().map(|m| m.id.clone()).collect();
    let list = visible
        .iter()
        .fold(
            List::new("crew.roster.list", "Members")
                .heading()
                .count(count)
                .find(find),
            |list, m| list.row(roster_row(state, m, selected.as_deref(), t)),
        )
        .on_key(move |state: &mut State, ev: &KeyEvent, tx: &mut Tx| {
            let step = match ev.key {
                Key::Down => 1,
                Key::Up => -1,
                _ => return false,
            };
            let current = super::selected(state)
                .and_then(|m| ids.iter().position(|id| *id == m.id))
                .unwrap_or(0) as i64;
            let next = (current + step).clamp(0, ids.len() as i64 - 1);
            if let Some(id) = ids.get(next as usize) {
                super::navigate(state, Nav::Member(id.clone()), tx);
            }
            true
        })
        .scrolls_as("crew.roster", "complementary", "Crew roster")
        .scrolling(t);
    let list = if stacked {
        list.shrink().min_h(0.0)
    } else {
        list.grow(1.0).min_h(0.0)
    };
    let shown = Stack::tight().child(list);
    if stacked {
        shown
            .w_full()
            .max_h(STACKED_ROSTER)
            .border_sides([0.0, 0.0, 1.0, 0.0], t.border_subtle)
    } else {
        shown
            .w(ROSTER_WIDTH)
            .h_full()
            .border_sides([0.0, 1.0, 0.0, 0.0], t.border_subtle)
    }
}

fn presence(member: &CrewMember) -> &'static str {
    if member.binding_session.is_some() {
        "In session"
    } else {
        "Between sessions"
    }
}

fn roster_row(state: &State, member: &CrewMember, selected: Option<&str>, t: &Tokens) -> El {
    let id = member.id.clone();
    let is_selected = selected == Some(member.id.as_str());
    let save = launch::shown(state, &member.id)
        .filter(|s| !s.saved)
        .map(|s| (launch::status(&s), s.error.is_some()));
    let desktop = member
        .launch_desktop
        .as_ref()
        .and_then(|d| d.label.clone())
        .filter(|d| !d.is_empty());
    let presence = presence(member);
    let line = match desktop {
        Some(desktop) => format!("{presence} · {desktop}"),
        None => presence.to_owned(),
    };
    let mut row = ListRow::new(
        format!("crew.roster.{}", member.id),
        format!("{} {presence}", member.name),
    )
    .lead(avatar(&member.name, t))
    .title(member.name.clone())
    .subtitle(line, t)
    .aside(presence_mark(member, t))
    .selected(is_selected);
    if let Some((said, failed)) = save {
        row = row.tag(
            tag(
                format!("crew.roster.{}.save", member.id),
                if failed { "Not saved" } else { "Saving" },
                if failed { Tone::Bad } else { Tone::Neutral },
                t,
            )
            .label(said),
        );
    }
    row.build(t)
        .on_press_tx(move |state: &mut State, tx: &mut Tx| {
            tx.instant();
            super::navigate(state, Nav::Member(id.clone()), tx)
        })
        .focus_selected(is_selected)
        .checked(is_selected)
}

fn detail(state: &State, t: &Tokens) -> El {
    let Some(member) = super::selected(state) else {
        return Page::new()
            .push(
                Prose::lead("crew.empty", "No crew members.")
                    .quiet(true)
                    .build(t),
            )
            .build("crew.detail", t);
    };
    let tab = state.window.crew.place.tab;
    let blocks = match tab {
        Tab::Launch => launch_tab(state, member, t),
        Tab::Charter => charter_tab(state, member, t),
        Tab::Handoffs => handoffs_tab(state, member, t),
        Tab::Seeds => seeds_tab(state, member, t),
    };
    Page::new()
        .head(heading(state, member, t))
        .push_all(rename(state, member, t))
        .push(Block::control("crew.tabs.block", tabs(tab, t)))
        .push_all(blocks)
        .build("crew.detail", t)
}

fn heading(state: &State, member: &CrewMember, t: &Tokens) -> Head {
    let renaming = state
        .window
        .crew
        .rename
        .as_ref()
        .is_some_and(|r| r.member == member.id);
    let in_session = member.binding_session.is_some();
    let presence_tag = El::from(tag(
        "crew.presence.pill",
        presence(member),
        if in_session {
            Tone::Good
        } else {
            Tone::Neutral
        },
        t,
    ))
    .a11y_hidden();
    let head = Head::new("crew.heading", member.name.clone())
        .note(
            words(presence(member), Type::Small, t.text_tertiary)
                .key("crew.presence")
                .label(presence(member)),
        )
        .aside(presence_tag);
    if renaming {
        return head;
    }
    let id = member.id.clone();
    head.aside(
        Button::new("Rename")
            .size(Size::Compact)
            .look(Look::Quiet)
            .on_press_tx(move |state: &mut State, tx: &mut Tx| super::begin_rename(state, &id, tx))
            .build(t)
            .key(RENAME),
    )
}

/// The field that renames the member, across the details under its name, while renaming.
fn rename(state: &State, member: &CrewMember, t: &Tokens) -> Option<Block> {
    let rename = state
        .window
        .crew
        .rename
        .as_ref()
        .filter(|r| r.member == member.id)?;
    let input = TextInput::new(&rename.edit)
        .key(RENAME_FIELD)
        .label(format!("New name for {}", member.name))
        .on_edit(|state: &mut State| {
            let rename = state
                .window
                .crew
                .rename
                .as_mut()
                .expect("the rename field shows only while renaming");
            &mut rename.edit
        })
        .on_change(|state: &mut State, _: &mut Tx| {
            if let Some(rename) = state.window.crew.rename.as_mut() {
                rename.error = None;
            }
        })
        .on_submit(super::submit_rename)
        .on_key(|state: &mut State, ev: &KeyEvent, tx: &mut Tx| {
            if ev.key != Key::Escape {
                return false;
            }
            if state.window.crew.rename.as_ref().is_some_and(|r| !r.saving) {
                super::cancel_rename(state, tx);
            }
            true
        });
    let field = line_box(
        input,
        if rename.saving {
            FieldState::Disabled
        } else {
            FieldState::failed_if(rename.error.is_some())
        },
        t,
    );
    Some(
        Field::new(RENAME_FIELD, "New name")
            .control(field)
            .refusal(
                rename
                    .error
                    .as_ref()
                    .map(|why| Refusal::new("crew.rename.error", why.clone()).build(t)),
            )
            .build(t),
    )
}

/// The member's details: one button per tab, the shown one raised.
fn tabs(shown: Tab, t: &Tokens) -> El {
    Inline::tight()
        .key("crew.tabs")
        .role("navigation")
        .label("Member details")
        .children(Tab::ALL.into_iter().map(|tab| {
            tab_button(
                format!("crew.tab.{}", tab.id()),
                tab.label(),
                None,
                tab == shown,
                t,
            )
            .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                tx.instant();
                super::navigate(state, Nav::Tab(tab), tx)
            })
        }))
}

/// A tab's button, `said` and a `count` after it, raised and read as chosen while shown.
fn tab_button(
    key: String,
    said: &'static str,
    count: Option<String>,
    chosen: bool,
    t: &Tokens,
) -> El {
    let label = match &count {
        Some(n) => format!("{said} {n}"),
        None => said.to_owned(),
    };
    let mut button =
        Button::new(said)
            .size(Size::Compact)
            .look(if chosen { Look::Plain } else { Look::Quiet });
    if let Some(n) = count {
        button = button.trailing(words(n, Type::Mono, t.text_dimmed));
    }
    El::from(button.build(t).key(key).label(label)).checked(chosen)
}

fn status_line(key: &str, said: impl Into<String>, ink: rune::Color) -> El {
    let said = said.into();
    words(said.clone(), Type::Small, ink)
        .key(key.to_owned())
        .role("status")
        .label(said)
        .wrap()
}

fn launch_tab(state: &State, member: &CrewMember, t: &Tokens) -> Vec<Block> {
    let Some(shown) = launch::shown(state, &member.id) else {
        return Vec::new();
    };
    let connected = state.facts.connection == attn_client::Connection::Connected;
    let in_session = member.binding_session.is_some();
    let mut tab = Vec::new();
    if let Some(session) = &member.binding_session {
        let running = state
            .facts
            .sessions
            .iter()
            .find(|s| &s.id == session)
            .map(|s| s.agent.clone())
            .unwrap_or_default();
        let harness = if running.is_empty() {
            "Harness not reported".to_owned()
        } else {
            crate::harness::catalog::name(&state.facts.harnesses, &running)
        };
        let short = attn_protocol::session_id::short(session);
        let said = format!("{harness} · Model not reported · Effort not reported · {short}");
        tab.push(
            Section::new("crew.running", "Running now")
                .control(words(short.into_owned(), Type::Mono, t.text_muted))
                .push(Block::custom(
                    "crew.running.route.block",
                    words(said.clone(), Type::Body, t.text_primary)
                        .key("crew.running.route")
                        .label(said)
                        .wrap(),
                ))
                .build(t),
        );
    }
    let follows = launch::harness(&shown);
    let unavailable = state.facts.harnesses_loaded
        && !follows.is_empty()
        && !crate::settings::harnesses::listed(state)
            .iter()
            .any(|h| h.id == follows && h.available());
    let warning = unavailable.then(|| "This harness is unavailable on this daemon.".to_owned());
    let id = member.id.clone();
    let ack = &shown.acknowledged;
    let (agent, model, effort) = (
        ack.resolved_agent.as_str(),
        ack.resolved_model.as_deref().unwrap_or_default(),
        ack.resolved_effort.as_deref().unwrap_or_default(),
    );
    let acknowledged = route_words(state, agent, model, effort);
    let acknowledged_route = crate::settings::route::Route::new(agent, model, effort);
    let save = launch::status(&shown);
    let save_ink = if shown.error.is_some() {
        t.error
    } else if shown.saving {
        t.brand
    } else {
        t.text_muted
    };
    let desktop = crate::launch_desktop::Field {
        key: format!("crew.launch.{}", member.id),
        kind: "crew".into(),
        item_id: member.id.clone(),
        profile_id: member.profile_id.clone(),
        default_name: if member.name.is_empty() {
            member.id.clone()
        } else {
            member.name.clone()
        },
        value: shown.draft.launch_desktop.clone(),
        disabled: false,
        on_change: launch::choose_desktop,
    };
    let (choice, unread) = crate::launch_desktop::trigger(state, t, &desktop);
    let mut launch_section = Section::new("crew.launch", "Launch settings")
        .description("The next session starts with these.")
        .control(status_line("crew.launch.status", save, save_ink));
    if shown.error.is_some() {
        let id = id.clone();
        launch_section = launch_section.control(
            Button::new("Retry")
                .size(Size::Compact)
                .on_press_tx(move |state: &mut State, tx: &mut Tx| launch::pump(state, &id, tx))
                .build(t)
                .key("crew.launch.retry"),
        );
    }
    launch_section = launch_section
        .push(
            Field::new(desktop.select_key(), "Desktop")
                .control(choice)
                .error(unread)
                .build(t),
        )
        .push(
            Field::new("crew.launch.route", "Runs as")
                .control(crate::settings::route_chip(
                    state,
                    t,
                    route_target(&member.id),
                    "Crew launch model",
                ))
                .build(t),
        );
    if let Some(warning) = warning {
        let notice = Notice::warn("crew.launch.warning", warning);
        launch_section = launch_section.push(notice.build(t));
    }
    launch_section = launch_section.push(Block::custom(
        "crew.acknowledged.block",
        Inline::loose()
            .key("crew.acknowledged")
            .role("group")
            .label(format!("Acknowledged next session {acknowledged}"))
            .w_full()
            .child(
                words("Acknowledged next session", Type::Small, t.text_tertiary)
                    .key("crew.acknowledged.title"),
            )
            .child(crate::settings::route::badge(state, t, &acknowledged_route).a11y_hidden()),
    ));
    if let Some(why) = shown.error.clone() {
        launch_section = launch_section.push(Notice::error("crew.launch.error", why).build(t));
    }
    tab.push(launch_section.build(t));
    if in_session {
        let applying = state.window.crew.applying.as_deref() == Some(member.id.as_str());
        let applied = state
            .window
            .crew
            .applied
            .as_ref()
            .filter(|a| a.member == member.id);
        let id = member.id.clone();
        let mut apply = Section::new("crew.apply.section", "Apply settings now")
            .description("The same harness reloads in place and keeps the conversation. A new harness starts after the member files its letter.")
            .control(
                Button::new(if applying { "Applying…" } else { "Apply" })
                    .size(Size::Compact)
                    .disabled(!connected || !shown.saved || applying)
                    .on_press_tx(move |state: &mut State, tx: &mut Tx| super::apply(state, &id, tx))
                    .build(t)
                    .key("crew.apply"),
            );
        if member.switch_pending == Some(true) {
            apply = apply.push(Block::custom(
                "crew.switch-pending.block",
                status_line(
                    "crew.switch-pending",
                    format!(
                        "Waiting for {}’s letter to continue on {}.",
                        if member.name.is_empty() {
                            &member.id
                        } else {
                            &member.name
                        },
                        member.resolved_agent
                    ),
                    t.text_secondary,
                ),
            ));
        }
        if let Some(a) = applied {
            apply = apply.push(Block::custom(
                "crew.apply.outcome.block",
                status_line(
                    "crew.apply.outcome",
                    a.text.clone(),
                    if a.failed { t.error } else { t.text_secondary },
                ),
            ));
        }
        tab.push(apply.build(t));
    }
    let id = member.id.clone();
    let (key, label, disabled) = if in_session {
        ("crew.close", "Close", !connected)
    } else {
        ("crew.start", "Start", !connected || !shown.saved)
    };
    tab.push(
        Section::new(
            "crew.session",
            if in_session {
                "Close session"
            } else {
                "Start session"
            },
        )
        .control(
            Button::new(label)
                .size(Size::Compact)
                .look(if in_session {
                    Look::Plain
                } else {
                    Look::Primary
                })
                .disabled(disabled)
                .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                    if in_session {
                        super::ask_to_close(state, &id, tx)
                    } else {
                        super::start(state, &id, tx)
                    }
                })
                .build(t)
                .key(key),
        )
        .build(t),
    );
    tab
}

fn route_target(member: &str) -> RouteTarget {
    RouteTarget {
        id: format!("crew.{member}"),
        read: |state, id| launch::route(state, id.strip_prefix("crew.")?),
        write: |state, id, route, tx| {
            if let Some(member) = id.strip_prefix("crew.") {
                launch::write_route(state, member, route, tx)
            }
        },
    }
}

fn route_words(state: &State, agent: &str, model: &str, effort: &str) -> String {
    let harness = if agent.is_empty() {
        "Harness not reported".to_owned()
    } else {
        crate::harness::catalog::name(&state.facts.harnesses, agent)
    };
    let model = if model.is_empty() {
        "default".to_owned()
    } else {
        model.to_owned()
    };
    let mut words = format!("{harness} · {model}");
    if !effort.is_empty() {
        words.push_str(&format!(" · {effort}"));
    }
    words
}

fn charter_tab(state: &State, member: &CrewMember, t: &Tokens) -> Vec<Block> {
    let charters = &state.window.crew.charters;
    let edit = charters.edits.get(&member.id);
    let id = member.id.clone();
    let Some(edit) = edit.filter(|e| e.phase != Phase::Loading || e.acknowledged.is_some()) else {
        return vec![
            Prose::lead("crew.charter.loading", "Loading charter…")
                .quiet(true)
                .build(t),
        ];
    };
    if edit.acknowledged.is_none() {
        return vec![
            Notice::error(
                "crew.charter.load-error",
                edit.error
                    .clone()
                    .unwrap_or_else(|| "The charter could not be loaded.".into()),
            )
            .action(
                Button::new("Retry")
                    .size(Size::Compact)
                    .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                        charter::load(state, &id, tx)
                    })
                    .build(t)
                    .key("crew.charter.load-retry"),
            )
            .build(t),
        ];
    }
    let field = charters
        .fields
        .get(&member.id)
        .cloned()
        .unwrap_or_else(|| rune::TextEdit::new(edit.draft.clone()));
    let edited = member.id.clone();
    let editor = TextArea::new(&field)
        .key(CHARTER)
        .label(format!("Charter for {}", member.name))
        .rows(usize::MAX)
        .max_height(CHARTER_HEIGHT)
        .resizable()
        .spellcheck(true)
        .on_edit(move |state: &mut State| state.window.crew.charters.field_mut(&edited))
        .on_change({
            let id = member.id.clone();
            move |state: &mut State, tx: &mut Tx| charter::edited(state, &id, tx)
        })
        .on_blur({
            let id = member.id.clone();
            move |state: &mut State, tx: &mut Tx| charter::flush(state, &id, tx)
        });
    let save_ink = match edit.phase {
        Phase::Saving | Phase::Dirty => t.brand,
        Phase::Error | Phase::Conflict => t.error,
        Phase::Loading | Phase::Saved => t.text_muted,
    };
    let file = Inline::loose()
        .key("crew.charter.meta")
        .label("CHARTER.md · Markdown")
        .w_full()
        .child(words("CHARTER.md", Type::Mono, t.text_muted).grow(1.0))
        .child(words("Markdown", Type::Mono, t.text_muted));
    let mut section = Section::new("crew.charter", "Charter")
        .control(status_line(
            "crew.charter.status",
            charter::status(edit.phase),
            save_ink,
        ))
        .push(Block::custom(
            "crew.charter.editor.block",
            Stack::tight()
                .w_full()
                .child(file)
                .child(area_box(editor, FieldState::Ready, t).into()),
        ));
    if edit.phase == Phase::Error {
        let id = member.id.clone();
        section = section.push(
            Notice::error(
                "crew.charter.error",
                edit.error.clone().unwrap_or_else(|| {
                    "The charter was not saved. Your edit is still here.".into()
                }),
            )
            .action(
                Button::new("Retry")
                    .size(Size::Compact)
                    .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                        charter::flush(state, &id, tx)
                    })
                    .build(t)
                    .key("crew.charter.retry"),
            )
            .build(t),
        );
    }
    if edit.phase == Phase::Conflict && edit.conflict.is_some() {
        let (file, mine) = (member.id.clone(), member.id.clone());
        section = section
            .push(
                Notice::warn(
                    "crew.charter.conflict",
                    "The file changed outside this editor. Your edit is still here. Choose which version should become canonical.",
                )
                .build(t),
            )
            .push(
                Actions::new("crew.charter.conflict.actions")
                    .push(
                        Button::new("Use file version")
                            .size(Size::Compact)
                            .on_press(move |state: &mut State| charter::use_file(state, &file))
                            .build(t)
                            .key("crew.charter.use-file"),
                    )
                    .push(
                        Button::new("Keep my edit")
                            .size(Size::Compact)
                            .look(Look::Primary)
                            .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                                charter::keep_mine(state, &mine, tx)
                            })
                            .build(t)
                            .key("crew.charter.keep-mine"),
                    )
                    .build(),
            );
    }
    vec![section.build(t)]
}

fn day_and_time(iso: &str) -> (String, String) {
    let (day, rest) = iso.split_once('T').unwrap_or((iso, ""));
    (day.to_owned(), rest.chars().take(5).collect())
}

fn handoffs_tab(state: &State, member: &CrewMember, t: &Tokens) -> Vec<Block> {
    let history = state.window.crew.handoffs.by_member.get(&member.id);
    let id = member.id.clone();
    let Some(history) = history.filter(|h| h.reading != Reading::Loading) else {
        return vec![
            Prose::lead("crew.handoffs.loading", "Loading handoffs…")
                .quiet(true)
                .build(t),
        ];
    };
    if history.reading == Reading::Failed {
        return vec![
            Notice::error(
                "crew.handoffs.error",
                history.error.clone().unwrap_or_default(),
            )
            .action(
                Button::new("Retry")
                    .size(Size::Compact)
                    .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                        handoffs::load(state, &id, true, tx)
                    })
                    .build(t)
                    .key("crew.handoffs.retry"),
            )
            .build(t),
        ];
    }
    let refresh_id = member.id.clone();
    let mut section = Section::new("crew.handoffs", "Handoffs");
    if !history.handoffs.is_empty() {
        section = section.control(El::from(tag(
            "crew.handoffs.read-only",
            "Read only",
            Tone::Neutral,
            t,
        )));
    }
    section = section.control(
        Button::new("Refresh")
            .size(Size::Compact)
            .look(Look::Quiet)
            .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                handoffs::load(state, &refresh_id, true, tx)
            })
            .build(t)
            .key("crew.handoffs.refresh"),
    );
    let Some(shown) = history.shown() else {
        return vec![
            section
                .push(
                    Prose::lead("crew.handoffs.empty", "No handoffs recorded.")
                        .quiet(true)
                        .build(t),
                )
                .build(t),
        ];
    };
    let count = history.handoffs.len();
    let letters = history.handoffs.iter().enumerate().map(|(at, h)| {
        let (day, time) = day_and_time(&h.occurred_at);
        let latest = if at == 0 { "Latest · " } else { "" };
        let (member, file) = (member.id.clone(), h.filename.clone());
        let current = h.filename == shown.filename;
        ListRow::new(
            format!("crew.handoff.{at}"),
            format!("{day} {latest}{time} UTC"),
        )
        .title(day)
        .subtitle(format!("{latest}{time} UTC"), t)
        .selected(current)
        .build(t)
        .on_press_tx(move |state: &mut State, tx: &mut Tx| {
            tx.instant();
            handoffs::select(state, &member, &file, tx)
        })
        .checked(current)
    });
    let index = Stack::tight()
        .key("crew.handoffs.index")
        .role("navigation")
        .label(format!("{} handoffs", member.name))
        .w(INDEX_WIDTH)
        .border_sides([0.0, 1.0, 0.0, 0.0], t.border_subtle)
        .child(
            words(
                format!("{count} {}", if count == 1 { "letter" } else { "letters" }),
                Type::Label,
                t.text_muted,
            )
            .key("crew.handoffs.count"),
        )
        .children(letters);
    let letter = history
        .letter
        .as_ref()
        .filter(|l| l.filename == shown.filename);
    let (day, time) = day_and_time(&shown.occurred_at);
    let body = match letter {
        Some(l) if l.reading == Reading::Ready && l.document.is_some() => {
            let document = l.document.as_ref().expect("checked");
            let view = format!("crew.handoff.{}.{}", member.id, document.filename);
            crate::markdown::reader(
                &document.content,
                crate::markdown::Document {
                    path: String::new(),
                    session: None,
                    away: crate::markdown::Away::of(
                        &crate::machine::Machines::of(&state.facts),
                        None,
                    ),
                    view: view.clone(),
                    opens_seed: Some(super::open_seed),
                    seed: None,
                    part: None,
                    ids: state.readings.ids.clone(),
                    placed: Vec::new(),
                },
                std::rc::Rc::default(),
                state.readings.kept(&view),
                None,
                crate::markdown::look(t),
                t,
            )
            .key("crew.handoff.letter")
        }
        Some(l) if l.reading == Reading::Failed => {
            let (member, file) = (member.id.clone(), l.filename.clone());
            Refusal::new(
                "crew.handoff.letter-error",
                l.error
                    .clone()
                    .unwrap_or_else(|| "The letter could not be read.".into()),
            )
            .retry(
                Button::new("Retry")
                    .size(Size::Compact)
                    .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                        handoffs::load_letter(state, &member, &file, tx)
                    })
                    .build(t)
                    .key("crew.handoff.letter-retry"),
            )
            .build(t)
        }
        _ => words("Loading letter…", Type::Small, t.text_muted).key("crew.handoff.loading"),
    };
    let reader = Stack::tight()
        .key("crew.handoff.reader")
        .role("article")
        .label(shown.filename.clone())
        .grow(1.0)
        .min_w(0.0)
        .child(
            words(
                format!("{day} {time} UTC · {}", shown.filename),
                Type::Small,
                t.text_secondary,
            )
            .key("crew.handoff.date"),
        )
        .child(body);
    vec![
        section
            .push(Block::custom(
                "crew.handoffs.letters.block",
                Inline::loose()
                    .key("crew.handoffs.letters")
                    .w_full()
                    .items_start()
                    .child(index)
                    .child(reader),
            ))
            .build(t),
    ]
}

fn state_label(status: &str) -> &'static str {
    match status {
        "planted" => "Planted",
        "growing" => "Growing",
        "parked" => "Parked",
        "harvested" => "Harvested",
        "withered" => "Withered",
        _ => "Unknown",
    }
}

fn seeds_tab(state: &State, member: &CrewMember, t: &Tokens) -> Vec<Block> {
    let profile = &state.facts.profiles.selected;
    let all = state.facts.garden.of(profile);
    let tended = seeds::tended(all, member);
    let planted = &state.window.crew.planted;
    let planted_count = planted.badge_count(profile, &member.id);
    let place = &state.window.crew.place;
    let filter = place.seed_filter;
    let filters = Inline::tight()
        .key("crew.seeds.filters")
        .role("group")
        .label("Seed ownership")
        .children(
            [
                (
                    SeedFilter::Tending,
                    "tending",
                    "Tending",
                    Some(tended.len().to_string()),
                ),
                (
                    SeedFilter::Planted,
                    "planted",
                    "Planted",
                    planted_count.map(|n| n.to_string()),
                ),
            ]
            .into_iter()
            .map(|(f, id, label, count)| {
                tab_button(format!("crew.seeds.{id}"), label, count, f == filter, t).on_press_tx(
                    move |state: &mut State, tx: &mut Tx| {
                        tx.instant();
                        state.window.crew.place.seed_filter = f;
                        seeds::sync(state, tx);
                    },
                )
            }),
        );
    let mut section = Section::new("crew.seeds", "Seeds")
        .push(Block::control("crew.seeds.filters.block", filters));
    match filter {
        SeedFilter::Tending => {
            if tended.is_empty() {
                section = section.push(
                    Prose::lead("crew.seeds.empty", seeds::empty(&member.name, filter))
                        .quiet(true)
                        .build(t),
                );
                if planted_count.unwrap_or(0) > 0 {
                    section = section.push(Block::control(
                        "crew.seeds.planted.block",
                        Button::new("See planted seeds")
                            .size(Size::Compact)
                            .look(Look::Quiet)
                            .on_press_tx(|state: &mut State, tx: &mut Tx| {
                                state.window.crew.place.seed_filter = SeedFilter::Planted;
                                seeds::sync(state, tx);
                            })
                            .build(t)
                            .key("crew.seeds.see-planted"),
                    ));
                }
                return vec![section.build(t)];
            }
            let query = place.seed_query.text.clone();
            let visible: Vec<&&Seed> = tended
                .iter()
                .filter(|s| seeds::matches(s, &query))
                .collect();
            let find = TextInput::new(&place.seed_query)
                .key(SEED_FIND)
                .label("Find a seed")
                .placeholder("Find a seed…")
                .on_edit(|state: &mut State| &mut state.window.crew.place.seed_query);
            section = section.push(Block::custom(
                "crew.seeds.find.block",
                line_box(find, FieldState::Ready, t).into(),
            ));
            if visible.is_empty() {
                return vec![
                    section
                        .push(
                            Prose::lead("crew.seeds.no-match", "No matching seeds.")
                                .quiet(true)
                                .build(t),
                        )
                        .build(t),
                ];
            }
            let by_id = seeds::by_id(all, &[]);
            let list = visible.into_iter().fold(
                List::new("crew.seeds.list", "Tended seeds").roving(),
                |list, seed| list.row(seed_row(seed, &by_id, t)),
            );
            vec![section.push(list.build(t)).build(t)]
        }
        SeedFilter::Planted => {
            let query = place.seed_query.text.trim();
            let has_any =
                planted_count.unwrap_or(0) > 0 || !planted.seeds.is_empty() || !query.is_empty();
            if has_any {
                let find = TextInput::new(&place.seed_query)
                    .key(SEED_FIND)
                    .label("Find a seed")
                    .placeholder("Find a seed…")
                    .on_edit(|state: &mut State| &mut state.window.crew.place.seed_query)
                    .on_change(seeds::sync);
                section = section.push(Block::custom(
                    "crew.seeds.find.block",
                    line_box(find, FieldState::Ready, t).into(),
                ));
            }
            if planted.seeds.is_empty() {
                if planted.loading {
                    return vec![
                        section
                            .push(
                                Prose::lead("crew.seeds.loading", "Loading planted seeds…")
                                    .quiet(true)
                                    .build(t),
                            )
                            .build(t),
                    ];
                }
                if let Some(error) = &planted.error {
                    return vec![
                        section
                            .push(
                                Notice::error("crew.seeds.error", error.clone())
                                    .action(
                                        Button::new("Retry")
                                            .size(Size::Compact)
                                            .on_press_tx(seeds::retry)
                                            .build(t)
                                            .key("crew.seeds.retry"),
                                    )
                                    .build(t),
                            )
                            .build(t),
                    ];
                }
                if !query.is_empty() {
                    return vec![
                        section
                            .push(
                                Prose::lead("crew.seeds.no-match", "No matching seeds.")
                                    .quiet(true)
                                    .build(t),
                            )
                            .build(t),
                    ];
                }
                return vec![
                    section
                        .push(
                            Prose::lead("crew.seeds.empty", seeds::empty(&member.name, filter))
                                .quiet(true)
                                .build(t),
                        )
                        .build(t),
                ];
            }
            let rows: Rc<[Arc<Seed>]> = planted.seeds.clone().into();
            let keys = rows.clone();
            let by_id = Rc::new(seeds::by_id(all, &planted.seeds));
            let tokens = *t;
            let list_el = col()
                .key("crew.seeds.list")
                .role("list")
                .label("Planted seeds")
                .focus_group()
                .self_stretch()
                .growth(GrowthSpec::default())
                .virtual_rows(
                    rows.len(),
                    ListRow::two_line_height(),
                    move |_: &State, i| format!("crew.seed.{}", keys[i].id).into(),
                    move |_: &State, i| seed_row(&by_id[&rows[i].id], &by_id, &tokens),
                );
            section = section.push(Block::custom("crew.seeds.list.block", list_el));
            if planted.loading_more {
                section = section.push(
                    Prose::lead("crew.seeds.more.loading", "Loading older seeds…")
                        .quiet(true)
                        .build(t),
                );
            } else if let Some(error) = &planted.error {
                section = section.push(
                    Notice::error("crew.seeds.error", error.clone())
                        .action(
                            Button::new("Retry")
                                .size(Size::Compact)
                                .on_press_tx(seeds::retry)
                                .build(t)
                                .key("crew.seeds.retry"),
                        )
                        .build(t),
                );
            } else if let Some(next) = &planted.next_before {
                let load: El = Button::new("Load older seeds")
                    .size(Size::Compact)
                    .look(Look::Quiet)
                    .on_press_tx(|state: &mut State, tx: &mut Tx| {
                        seeds::load_more(state, tx);
                    })
                    .build(t)
                    .key("crew.seeds.more")
                    .into();
                section = section.push(Block::custom(
                    "crew.seeds.more.block",
                    col()
                        .key(format!("crew.seeds.more.sentinel.{next}"))
                        .w_full()
                        .on_visible(|state: &mut State, shown: bool, tx: &mut Tx| {
                            if !shown || !seeds::load_more(state, tx) {
                                tx.live_only();
                            }
                        })
                        .child(load),
                ));
            }
            vec![section.build(t)]
        }
    }
}

fn seed_row(seed: &Seed, by_id: &HashMap<String, Arc<Seed>>, t: &Tokens) -> El {
    let title = if seed.title.trim().is_empty() {
        seed.id.clone()
    } else {
        seed.title.trim().to_owned()
    };
    let plot = seeds::plot(seed, by_id).map(|p| {
        let name = if p.title.trim().is_empty() {
            p.id.clone()
        } else {
            p.title.trim().to_owned()
        };
        let progress = seeds::progress(Some(p));
        if progress.is_empty() {
            name
        } else {
            format!("{name} · {progress}")
        }
    });
    let own = seeds::progress(Some(seed));
    let state_words = if own.is_empty() {
        state_label(&seed.status).to_owned()
    } else {
        own
    };
    let created = seed
        .created_at
        .split('T')
        .next()
        .unwrap_or_default()
        .to_owned();
    let meta: Vec<String> = [Some(seed.id.clone()), Some(created), plot]
        .into_iter()
        .flatten()
        .collect();
    let label = format!("{title} {} {state_words}", meta.join(" "));
    let id = seed.id.clone();
    ListRow::new(format!("crew.seed.{}", seed.id), label)
        .title(title)
        .subtitle(meta.join(" · "), t)
        .aside(words(state_words, Type::Small, t.text_secondary))
        .build(t)
        .on_press_tx(move |state: &mut State, tx: &mut Tx| {
            super::navigate(state, Nav::Seed(id.clone()), tx)
        })
}
