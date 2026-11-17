use attn_protocol::{CrewMember, Seed};
use attn_ui::Tokens;
use attn_ui::type_;
use rune::tok::space;
use rune::{Color, El, Key, KeyEvent, Tx, col, row, text};
use rune_kit::{Button, ButtonVariant, TextArea, TextInput};

use super::autosave::Phase;
use super::handoffs::Reading;
use super::seeds::{self, SeedFilter};
use super::{Nav, Tab, charter, handoffs, launch};
use crate::State;
use crate::settings::RouteTarget;

pub(crate) const RENAME: &str = "crew.rename";
pub(crate) const RENAME_FIELD: &str = "crew.rename.field";
const FIND: &str = "crew.find";
const CHARTER: &str = "crew.charter.editor";
/// The charter editor's height, as the Tauri app's `.crew-charter-editor`; a longer charter
/// scrolls inside it.
const CHARTER_HEIGHT: f32 = 390.0;
const SEED_FIND: &str = "crew.seeds.find";
pub(crate) const PAGE: &str = "crew.page";

/// The Tauri app's small print in Manage crew, its 9–11 px regular text, at the native app's
/// 12 px.
const NOTE: rune::TextStyle = rune::TextStyle::new(12.0, 400.0);
const TITLE: rune::TextStyle = rune::TextStyle::new(16.0, 650.0).tracking(-0.3);
const ROSTER_NAME: rune::TextStyle = rune::TextStyle::new(13.0, 580.0);
const AVATAR: rune::TextStyle = rune::TextStyle::new(13.0, 650.0);
const LARGE_AVATAR: rune::TextStyle = rune::TextStyle::new(19.0, 650.0);
const RENAME_TYPE: rune::TextStyle = rune::TextStyle::new(12.0, 500.0);
const NAME: rune::TextStyle = rune::TextStyle::new(22.0, 650.0).tracking(-0.55);
const SECTION: rune::TextStyle = rune::TextStyle::new(14.0, 620.0);
const DOCUMENT: rune::TextStyle = rune::TextStyle::new(15.0, 620.0);
const SEEDS: rune::TextStyle = rune::TextStyle::new(16.0, 620.0);
/// The widest the launch card, the start row and the seeds run, as the Tauri app's
/// `min(720px, 100%)`; the charter and the handoffs run to `WIDE`.
const NARROW: f32 = 720.0;
const WIDE: f32 = 900.0;

fn caption(key: impl Into<String>, words: impl Into<String>, t: &Tokens) -> El {
    text(words.into())
        .key(key.into())
        .font(NOTE)
        .color(t.text_muted)
}

fn kicker(words: &str, t: &Tokens) -> El {
    type_::caps(words).color(t.text_muted)
}

/// The file bar joined to the top of the charter editor, as the Tauri app's
/// `.crew-charter-meta`: the file at its left and its language at its right.
fn charter_bar(t: &Tokens) -> El {
    row()
        .key("crew.charter.meta")
        .label("CHARTER.md · Markdown")
        .w_full()
        .h(28.0)
        .items_center()
        .px(space::S3)
        .fill(t.bg_elevated)
        .border_sides([1.0, 1.0, 0.0, 1.0], t.border)
        .corners([7.0, 7.0, 0.0, 0.0])
        .child(
            text("CHARTER.md")
                .font(type_::MONO_CAPTION)
                .color(t.text_muted)
                .grow(1.0),
        )
        .child(
            text("Markdown")
                .font(type_::MONO_CAPTION)
                .color(t.text_muted),
        )
}

fn status(key: &str, words: impl Into<String>, color: rune::Color) -> El {
    let words = words.into();
    text(words.clone())
        .key(key.to_owned())
        .role("status")
        .label(words)
        .font(NOTE)
        .color(color)
}

fn alert(key: &str, words: impl Into<String>, t: &Tokens) -> El {
    let words = words.into();
    text(words.clone())
        .key(key.to_owned())
        .role("alert")
        .label(words)
        .font(type_::LABEL)
        .color(t.error)
        .wrap()
        .w_full()
}

/// Manage crew, while it is open: a page in place of the window's main area, beside the
/// sidebar, that holds the keyboard until it closes, as the Tauri app's `.crew-panel`.
pub(crate) fn page(state: &State, t: &Tokens) -> El {
    col()
        .key(PAGE)
        .role("dialog")
        .label("Manage crew")
        .modal_tx(false, |state: &mut State, tx: &mut Tx| {
            super::close(state, tx)
        })
        .grow(1.0)
        .min_w(0.0)
        .h_full()
        .min_h(0.0)
        .fill(t.bg_panel)
        .child(bar(t))
        .child(
            row()
                .key("crew.shell")
                .w_full()
                .grow(1.0)
                .min_h(0.0)
                .child(roster(state, t))
                .child(detail(state, t)),
        )
}

/// The pickers Manage crew opens over itself.
pub(crate) fn overlays(state: &State, t: &Tokens, layers: &mut Vec<El>) {
    crate::settings::route_overlay(state, t, layers);
    crate::launch_desktop::overlay(state, t, layers);
}

fn bar(t: &Tokens) -> El {
    row()
        .key("crew.bar")
        .w_full()
        .min_h(53.0)
        .items_center()
        .gap(16.0)
        .px(22.0)
        .py(10.0)
        .border_sides([0.0, 0.0, 1.0, 0.0], t.border)
        .child(
            col()
                .grow(1.0)
                .gap(1.0)
                .child(kicker("Crew", t))
                .child(text("Manage crew").font(TITLE).color(t.text_primary)),
        )
        .child(
            bordered("crew.close-page", t)
                .on_press_tx(|state: &mut State, tx: &mut Tx| super::close(state, tx))
                .label("Close")
                .gap(7.0)
                .px(9.0)
                .py(6.0)
                .radius(7.0)
                .child(text("Close").font(type_::BODY).color(t.text_secondary))
                .child(text("Esc").font(type_::MONO_CAPTION).color(t.text_muted)),
        )
}

/// The Tauri app's small bordered buttons in Manage crew: the elevated fill, a hairline and
/// secondary ink.
fn bordered(key: impl Into<String>, t: &Tokens) -> El {
    row()
        .key(key.into())
        .role("button")
        .focusable(true)
        .focus_ring(2.0, 2.0, t.brand)
        .items_center()
        .radius(6.0)
        .fill(t.bg_elevated)
        .hover(t.bg_button)
        .border(1.0, t.border)
}

/// A [`bordered`] button that says `label`, unusable while `disabled`.
fn button(
    key: impl Into<String>,
    label: &str,
    disabled: bool,
    t: &Tokens,
    press: impl Fn(&mut State, &mut Tx) + 'static,
) -> El {
    let shown = bordered(key, t)
        .label(label.to_owned())
        .px(8.0)
        .py(5.0)
        .child(text(label.to_owned()).font(NOTE).color(t.text_secondary));
    if disabled {
        shown.disabled(true).opacity(0.45)
    } else {
        shown.on_press_tx(press)
    }
}

/// The member's initial on its green tile, as the Tauri app's `.crew-avatar`.
fn avatar(name: &str, side: f32, corner: f32, font: rune::TextStyle, t: &Tokens) -> El {
    let initial: String = name.chars().take(1).flat_map(char::to_uppercase).collect();
    col()
        .size(side, side)
        .center()
        .radius(corner)
        .fill(attn_ui::colors::mix_srgb(t.bg_elevated, t.success, 0.14))
        .border(1.0, t.text_primary.alpha(0.08))
        .child(text(initial).font(font).color(attn_ui::colors::mix_srgb(
            t.text_primary,
            t.success,
            0.72,
        )))
        .a11y_hidden()
}

fn presence_dot(member: &CrewMember, t: &Tokens) -> El {
    rune::el()
        .size(6.0, 6.0)
        .radius(3.0)
        .fill(if member.binding_session.is_some() {
            t.success
        } else {
            t.text_dimmed
        })
        .a11y_hidden()
}

fn roster(state: &State, t: &Tokens) -> El {
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
    let mut aside = col()
        .key("crew.roster")
        .role("complementary")
        .label("Crew roster")
        .w(226.0)
        .h_full()
        .gap(12.0)
        .px(10.0)
        .py(18.0)
        .border_sides([0.0, 1.0, 0.0, 0.0], t.border)
        .scroll(rune::ScrollSpec {
            stick_bottom: false,
        })
        .child(
            row()
                .w_full()
                .px(9.0)
                .child(kicker("Members", t).grow(1.0))
                .child(
                    text(members.len().to_string())
                        .key("crew.roster.count")
                        .font(type_::CAPS)
                        .color(t.text_muted),
                ),
        );
    if members.len() > 5 {
        aside = aside.child(
            TextInput::new(&state.window.crew.place.roster_filter)
                .key(FIND)
                .label("Find a member")
                .placeholder("Find a member…")
                .width(None)
                .on_edit(|state: &mut State| &mut state.window.crew.place.roster_filter)
                .build(attn_ui::kit(t)),
        );
    }
    let visible: Vec<&CrewMember> = members
        .into_iter()
        .filter(|m| filter.is_empty() || m.name.to_lowercase().contains(&filter))
        .collect();
    let ids: Vec<String> = visible.iter().map(|m| m.id.clone()).collect();
    let list = col()
        .key("crew.roster.list")
        .role("list")
        .label("Members")
        .w_full()
        .gap(5.0)
        .focusable(true)
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
        .children(
            visible
                .iter()
                .map(|m| roster_row(state, m, selected.as_deref(), t)),
        );
    aside.child(list)
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
    let identity = col()
        .grow(1.0)
        .min_w(0.0)
        .gap(3.0)
        .child(
            text(member.name.clone())
                .font(ROSTER_NAME)
                .color(t.text_primary)
                .single_line()
                .truncate()
                .mb(3.0),
        )
        .children(desktop.map(|d| text(d).font(NOTE).color(t.text_muted)))
        .child(
            row()
                .gap(6.0)
                .items_center()
                .child(presence_dot(member, t))
                .child(text(presence).font(NOTE).color(t.text_muted)),
        );
    let mut shown = row()
        .key(format!("crew.roster.{}", member.id))
        .role("button")
        .label(format!("{} {presence}", member.name))
        .focusable(true)
        .focus_ring(2.0, 2.0, t.brand)
        .w_full()
        .items_start()
        .gap(10.0)
        .px(9.0)
        .py(11.0)
        .radius(7.0)
        .border(1.0, Color::TRANSPARENT)
        .child(avatar(&member.name, 30.0, 8.0, AVATAR, t))
        .child(identity)
        .children(save.map(|(words, failed)| {
            rune::el()
                .label(words)
                .size(6.0, 6.0)
                .mt(4.0)
                .radius(3.0)
                .fill(if failed { t.error } else { t.brand })
        }))
        .on_press_tx(move |state: &mut State, tx: &mut Tx| {
            super::navigate(state, Nav::Member(id.clone()), tx)
        })
        .focus_selected(is_selected)
        .checked(is_selected);
    shown = if is_selected {
        shown
            .border(1.0, attn_ui::colors::mix_srgb(t.border, t.brand, 0.32))
            .fill(attn_ui::colors::mix_srgb(t.bg_elevated, t.brand, 0.09))
            .box_shadow(rune::Shadow {
                x: 2.0,
                color: t.brand,
                inset: true,
                ..rune::Shadow::default()
            })
    } else {
        shown.hover(t.text_primary.alpha(0.05))
    };
    shown
}

fn detail(state: &State, t: &Tokens) -> El {
    let main = col()
        .key("crew.detail")
        .grow(1.0)
        .min_w(0.0)
        .h_full()
        .min_h(0.0)
        .fill(t.bg_app)
        .scroll(rune::ScrollSpec {
            stick_bottom: false,
        });
    let Some(member) = super::selected(state) else {
        return main.child(
            caption("crew.empty", "No crew members.", t)
                .px(28.0)
                .py(40.0),
        );
    };
    let tab = state.window.crew.place.tab;
    main.child(heading(state, member, t))
        .child(tabs(tab, t))
        .child(
            col()
                .key("crew.tab")
                .w_full()
                .px(28.0)
                .pt(22.0)
                .pb(32.0)
                .children(match tab {
                    Tab::Launch => launch_tab(state, member, t),
                    Tab::Charter => vec![charter_tab(state, member, t)],
                    Tab::Handoffs => vec![handoffs_tab(state, member, t)],
                    Tab::Seeds => vec![seeds_tab(state, member, t)],
                }),
        )
}

fn heading(state: &State, member: &CrewMember, t: &Tokens) -> El {
    let renaming = state
        .window
        .crew
        .rename
        .as_ref()
        .filter(|r| r.member == member.id);
    let name: El = match renaming {
        Some(rename) => col()
            .gap(space::S1)
            .child(
                TextInput::new(&rename.edit)
                    .key(RENAME_FIELD)
                    .label(format!("New name for {}", member.name))
                    .disabled(rename.saving)
                    .width(Some(240.0))
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
                    })
                    .build(attn_ui::kit(t)),
            )
            .children(
                rename
                    .error
                    .as_ref()
                    .map(|why| alert("crew.rename.error", why.clone(), t)),
            ),
        None => {
            let id = member.id.clone();
            row()
                .gap(space::S2)
                .items_center()
                .child(
                    text(member.name.clone())
                        .key("crew.heading")
                        .role("heading")
                        .label(member.name.clone())
                        .font(NAME)
                        .color(t.text_primary),
                )
                .child(
                    bordered(RENAME, t)
                        .label("Rename")
                        .ml(10.0)
                        .px(7.0)
                        .py(2.0)
                        .child(text("Rename").font(RENAME_TYPE).color(t.text_secondary))
                        .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                            super::begin_rename(state, &id, tx)
                        }),
                )
        }
    };
    let in_session = member.binding_session.is_some();
    let pill = row()
        .key("crew.presence.pill")
        .px(8.0)
        .py(4.0)
        .radius(999.0);
    let pill = if in_session {
        pill.fill(t.success.alpha(0.08))
            .border(1.0, attn_ui::colors::mix_srgb(t.border, t.success, 0.35))
            .child(text(presence(member)).font(NOTE).color(t.success))
    } else {
        pill.border(1.0, t.border)
            .child(text(presence(member)).font(NOTE).color(t.text_muted))
    };
    row()
        .key("crew.member")
        .w_full()
        .items_center()
        .gap(18.0)
        .px(28.0)
        .pt(20.0)
        .pb(18.0)
        .fill(t.bg_app)
        .linear_gradient(
            110f32.to_radians(),
            [
                (0.0, attn_ui::colors::mix_srgb(t.bg_panel, t.brand, 0.05)),
                (0.68, t.bg_app),
            ],
        )
        .child(
            row()
                .grow(1.0)
                .min_w(0.0)
                .items_center()
                .gap(13.0)
                .child(avatar(&member.name, 43.0, 11.0, LARGE_AVATAR, t))
                .child(col().min_w(0.0).gap(4.0).child(name).child(caption(
                    "crew.presence",
                    presence(member),
                    t,
                ))),
        )
        .child(pill.a11y_hidden())
}

/// The member's details, as the Tauri app's `.crew-member-tabs`: underlined words on the panel,
/// the open one in primary ink over the brand.
fn tabs(shown: Tab, t: &Tokens) -> El {
    row()
        .key("crew.tabs")
        .role("navigation")
        .label("Member details")
        .w_full()
        .gap(24.0)
        .px(28.0)
        .fill(t.bg_panel)
        .border_sides([0.0, 0.0, 1.0, 0.0], t.border)
        .children(Tab::ALL.into_iter().map(|tab| {
            underlined(
                format!("crew.tab.{}", tab.id()),
                tab.label().to_owned(),
                None,
                tab == shown,
                [11.0, 12.0],
                2.0,
                t,
            )
            .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                super::navigate(state, Nav::Tab(tab), tx)
            })
        }))
}

/// One of the Tauri app's underline tabs: `words` and a dimmed `count`, muted until chosen,
/// when primary ink sits over a `rule`-thick brand line. `pad` is its top and bottom padding.
fn underlined(
    key: String,
    words: String,
    count: Option<String>,
    chosen: bool,
    [top, bottom]: [f32; 2],
    rule: f32,
    t: &Tokens,
) -> El {
    let label = match &count {
        Some(n) => format!("{words} {n}"),
        None => words.clone(),
    };
    row()
        .key(key)
        .role("button")
        .label(label)
        .focusable(true)
        .focus_ring(2.0, 2.0, t.brand)
        .items_end()
        .gap(4.0)
        .pt(top)
        .pb(bottom - rule)
        .border_sides(
            [0.0, 0.0, rule, 0.0],
            if chosen { t.brand } else { Color::TRANSPARENT },
        )
        .mb(-1.0)
        .child(
            text(words)
                .font(NOTE)
                .color(if chosen { t.text_primary } else { t.text_muted }),
        )
        .children(count.map(|n| text(n).font(type_::MONO_CAPTION).color(t.text_dimmed)))
        .checked(chosen)
}

/// A bordered card on the panel, as the Tauri app's `.crew-running`.
fn card(key: &str, t: &Tokens) -> El {
    col()
        .key(key.to_owned())
        .w_full()
        .max_w(NARROW)
        .gap(space::S2)
        .px(16.0)
        .py(13.0)
        .radius(8.0)
        .fill(t.bg_panel)
        .border(1.0, t.border)
}

/// A row under a hairline: what it does on the left and its button on the right, as the
/// Tauri app's `.crew-session-action`.
fn action_row(key: &str, t: &Tokens) -> El {
    row()
        .key(key.to_owned())
        .w_full()
        .max_w(NARROW)
        .items_center()
        .gap(16.0)
        .mt(16.0)
        .pt(14.0)
        .border_sides([1.0, 0.0, 0.0, 0.0], t.border_subtle)
}

/// The action row's button: the bordered button in primary ink.
fn action_button(
    key: &str,
    label: &str,
    disabled: bool,
    t: &Tokens,
    press: impl Fn(&mut State, &mut Tx) + 'static,
) -> El {
    let shown = bordered(key, t)
        .label(label.to_owned())
        .px(10.0)
        .py(7.0)
        .radius(7.0)
        .child(text(label.to_owned()).font(NOTE).color(t.text_primary));
    if disabled {
        shown.disabled(true).opacity(0.45)
    } else {
        shown.on_press_tx(press)
    }
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

fn harness_name(state: &State, id: &str) -> String {
    crate::settings::listed_harnesses(state)
        .into_iter()
        .find(|h| h.id == id)
        .map(|h| h.name)
        .filter(|n| !n.is_empty())
        .unwrap_or_else(|| id.to_owned())
}

fn route_words(state: &State, agent: &str, model: &str, effort: &str) -> String {
    let harness = if agent.is_empty() {
        "Harness not reported".to_owned()
    } else {
        harness_name(state, agent)
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

fn launch_tab(state: &State, member: &CrewMember, t: &Tokens) -> Vec<El> {
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
            harness_name(state, &running)
        };
        let short: String = session.chars().take(8).collect();
        let words = format!("{harness} · Model not reported · Effort not reported · {short}");
        tab.push(
            card("crew.running", t)
                .role("group")
                .label("Running now")
                .mb(15.0)
                .child(
                    row()
                        .w_full()
                        .child(kicker("Running now", t).grow(1.0))
                        .child(text(short).font(type_::MONO_CAPTION).color(t.text_muted)),
                )
                .child(
                    text(words.clone())
                        .key("crew.running.route")
                        .label(words)
                        .font(NOTE)
                        .color(t.text_primary),
                ),
        );
    }
    let catalog_error = crate::settings::harnesses_error(state);
    let follows = launch::followed_harness(&shown);
    let unavailable = !follows.is_empty()
        && !crate::settings::listed_harnesses(state)
            .iter()
            .any(|h| h.id == follows && h.available);
    let warning = catalog_error
        .clone()
        .or_else(|| unavailable.then(|| "This harness is unavailable on this daemon.".to_owned()));
    let id = member.id.clone();
    let ack = &shown.acknowledged;
    let (agent, model, effort) = (
        ack.resolved_agent.as_str(),
        ack.resolved_model.as_deref().unwrap_or_default(),
        ack.resolved_effort.as_deref().unwrap_or_default(),
    );
    let acknowledged = route_words(state, agent, model, effort);
    let acknowledged_route = crate::settings::route::Route::stored(agent, model, effort);
    let save = launch::status(&shown);
    let save_ink = if shown.error.is_some() {
        t.error
    } else if shown.saving {
        t.brand
    } else {
        t.text_muted
    };
    let mut launch_card = col()
        .key("crew.launch")
        .role("group")
        .label("Launch settings")
        .w_full()
        .max_w(NARROW)
        .radius(9.0)
        .fill(t.bg_panel)
        .border(1.0, attn_ui::colors::mix_srgb(t.border, t.brand, 0.25))
        .box_shadow(rune::Shadow {
            y: 5.0,
            blur: 24.0,
            color: t.shadow.alpha(0.08),
            ..rune::Shadow::default()
        })
        .child(
            row()
                .w_full()
                .items_center()
                .gap(space::S2)
                .px(17.0)
                .pt(14.0)
                .child(
                    col()
                        .grow(1.0)
                        .gap(3.0)
                        .child(kicker("Next session", t))
                        .child(
                            text("Launch settings")
                                .role("heading")
                                .font(SECTION)
                                .color(t.text_primary),
                        ),
                )
                .child(status("crew.launch.status", save, save_ink))
                .children(shown.error.is_some().then(|| {
                    let id = id.clone();
                    button("crew.launch.retry", "Retry", false, t, move |state, tx| {
                        launch::pump(state, &id, tx)
                    })
                })),
        )
        .child(
            row()
                .key("crew.launch.fields")
                .w_full()
                .gap(13.0)
                .px(17.0)
                .py(16.0)
                .items_start()
                .child(
                    col()
                        .grow(1.0)
                        .w(0.0)
                        .min_w(0.0)
                        .child(crate::launch_desktop::field(
                            state,
                            t,
                            &crate::launch_desktop::Field {
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
                            },
                        )),
                )
                .child(
                    col()
                        .grow(1.0)
                        .w(0.0)
                        .min_w(0.0)
                        .child(crate::settings::route_chip(
                            state,
                            t,
                            route_target(&member.id),
                            "Crew launch model",
                        )),
                )
                .child(col().grow(1.0).w(0.0).min_w(0.0)),
        );
    if let Some(warning) = warning {
        launch_card = launch_card.child(
            row()
                .w_full()
                .gap(12.0)
                .px(17.0)
                .pb(11.0)
                .items_center()
                .child(alert("crew.launch.warning", warning, t))
                .children(catalog_error.is_some().then(|| {
                    button(
                        "crew.launch.rediscover",
                        "Retry harness discovery",
                        false,
                        t,
                        crate::settings::read_harnesses,
                    )
                })),
        );
    }
    launch_card = launch_card
        .child(
            row()
                .key("crew.acknowledged")
                .role("group")
                .label(format!("Acknowledged next session {acknowledged}"))
                .w_full()
                .items_center()
                .gap(12.0)
                .px(17.0)
                .py(11.0)
                .fill(t.bg_app.alpha(0.45))
                .border_sides([1.0, 0.0, 0.0, 0.0], t.border_subtle)
                .corners([0.0, 0.0, 9.0, 9.0])
                .child(caption("crew.acknowledged.title", "Acknowledged next session", t).grow(1.0))
                .child(crate::settings::route::badge(state, t, &acknowledged_route).a11y_hidden()),
        )
        .children(
            shown
                .error
                .clone()
                .map(|why| alert("crew.launch.error", why, t).px(17.0).pb(11.0)),
        );
    tab.push(launch_card);
    if in_session {
        let applying = state.window.crew.applying.as_deref() == Some(member.id.as_str());
        let applied = state
            .window
            .crew
            .applied
            .as_ref()
            .filter(|a| a.member == member.id);
        let id = member.id.clone();
        tab.push(
            action_row("crew.apply.section", t)
                .child(
                    col()
                        .grow(1.0)
                        .min_w(0.0)
                        .gap(4.0)
                        .child(text("Apply settings now").role("heading").font(SECTION).color(t.text_primary))
                        .child(
                            text("The same harness reloads in place and keeps the conversation. A new harness starts after the member files its letter.")
                                .font(NOTE)
                                .color(t.text_secondary)
                                .wrap()
                                .w_full(),
                        )
                        .children((member.switch_pending == Some(true)).then(|| {
                            status(
                                "crew.switch-pending",
                                format!(
                                    "Waiting for {}’s letter to continue on {}.",
                                    if member.name.is_empty() { &member.id } else { &member.name },
                                    member.resolved_agent
                                ),
                                t.text_secondary,
                            )
                        }))
                        .children(applied.map(|a| {
                            status(
                                "crew.apply.outcome",
                                a.text.clone(),
                                if a.failed { t.error } else { t.text_secondary },
                            )
                        })),
                )
                .child(action_button(
                    "crew.apply",
                    if applying { "Applying…" } else { "Apply" },
                    !connected || !shown.saved || applying,
                    t,
                    move |state, tx| super::apply(state, &id, tx),
                )),
        );
    }
    let id = member.id.clone();
    let (key, label, disabled) = if in_session {
        ("crew.close", "Close", !connected)
    } else {
        ("crew.start", "Start", !connected || !shown.saved)
    };
    tab.push(
        action_row("crew.session", t)
            .child(
                text(if in_session {
                    "Close session"
                } else {
                    "Start session"
                })
                .role("heading")
                .font(SECTION)
                .color(t.text_primary)
                .grow(1.0),
            )
            .child(action_button(key, label, disabled, t, move |state, tx| {
                if in_session {
                    super::ask_to_close(state, &id, tx)
                } else {
                    super::start(state, &id, tx)
                }
            })),
    );
    tab
}

fn charter_tab(state: &State, member: &CrewMember, t: &Tokens) -> El {
    let charters = &state.window.crew.charters;
    let edit = charters.edits.get(&member.id);
    let id = member.id.clone();
    let Some(edit) = edit.filter(|e| e.phase != Phase::Loading || e.acknowledged.is_some()) else {
        return caption("crew.charter.loading", "Loading charter…", t);
    };
    if edit.acknowledged.is_none() {
        return row()
            .gap(space::S2)
            .items_center()
            .child(alert(
                "crew.charter.load-error",
                edit.error
                    .clone()
                    .unwrap_or_else(|| "The charter could not be loaded.".into()),
                t,
            ))
            .child(button(
                "crew.charter.load-retry",
                "Retry",
                false,
                t,
                move |state, tx| charter::load(state, &id, tx),
            ));
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
        .spellcheck(true)
        .width(None)
        .on_edit(move |state: &mut State| state.window.crew.charters.field_mut(&edited))
        .on_change({
            let id = member.id.clone();
            move |state: &mut State, tx: &mut Tx| charter::edited(state, &id, tx)
        })
        .build(attn_ui::kit(t));
    let save_ink = match edit.phase {
        Phase::Saving | Phase::Dirty => t.brand,
        Phase::Error | Phase::Conflict => t.error,
        Phase::Loading | Phase::Saved => t.text_muted,
    };
    let mut section = col()
        .key("crew.charter")
        .role("group")
        .label("Charter editor")
        .w_full()
        .max_w(WIDE)
        .child(document_heading("Charter", t).child(status(
            "crew.charter.status",
            charter::status(edit.phase),
            save_ink,
        )))
        .child(col().w_full().child(charter_bar(t)).child(editor));
    if edit.phase == Phase::Error {
        let id = member.id.clone();
        section = section.child(
            row()
                .gap(space::S2)
                .items_center()
                .child(alert(
                    "crew.charter.error",
                    edit.error.clone().unwrap_or_else(|| {
                        "The charter was not saved. Your edit is still here.".into()
                    }),
                    t,
                ))
                .child(button(
                    "crew.charter.retry",
                    "Retry",
                    false,
                    t,
                    move |state, tx| charter::flush(state, &id, tx),
                )),
        );
    }
    if edit.phase == Phase::Conflict && edit.conflict.is_some() {
        let (file, mine) = (member.id.clone(), member.id.clone());
        section = section.child(
            col()
                .key("crew.charter.conflict")
                .role("alert")
                .label("The file changed outside this editor.")
                .gap(space::S1)
                .child(
                    text("The file changed outside this editor.")
                        .font(type_::TITLE)
                        .color(t.text_primary),
                )
                .child(
                    text("Your edit is still here. Choose which version should become canonical.")
                        .font(type_::LABEL)
                        .color(t.text_secondary),
                )
                .child(
                    row()
                        .gap(space::S2)
                        .child(button(
                            "crew.charter.use-file",
                            "Use file version",
                            false,
                            t,
                            move |state, _| charter::use_file(state, &file),
                        ))
                        .child(button(
                            "crew.charter.keep-mine",
                            "Keep my edit",
                            false,
                            t,
                            move |state, tx| charter::keep_mine(state, &mine, tx),
                        )),
                ),
        );
    }
    section
}

/// Saves the charter the focus just left; whether the focus moved.
pub(crate) fn leave_charter(state: &mut State, tx: &mut Tx) -> bool {
    let editing = (super::is_open(state) && tx.focused() == Some(CHARTER))
        .then(|| super::selected(state).map(|m| m.id.clone()))
        .flatten();
    if state.window.crew.charter_focus == editing {
        return false;
    }
    let left = std::mem::replace(&mut state.window.crew.charter_focus, editing);
    if let Some(member) = left {
        charter::flush(state, &member, tx);
    }
    true
}

/// A tab's document heading, as the Tauri app's `.crew-document-heading`: its title on the
/// left and what goes with it on the right.
fn document_heading(title: &str, t: &Tokens) -> El {
    row().w_full().items_start().gap(18.0).mb(16.0).child(
        text(title.to_owned())
            .role("heading")
            .font(DOCUMENT)
            .color(t.text_primary)
            .grow(1.0),
    )
}

fn day_and_time(iso: &str) -> (String, String) {
    let (day, rest) = iso.split_once('T').unwrap_or((iso, ""));
    (day.to_owned(), rest.chars().take(5).collect())
}

fn handoffs_tab(state: &State, member: &CrewMember, t: &Tokens) -> El {
    let history = state.window.crew.handoffs.by_member.get(&member.id);
    let id = member.id.clone();
    let Some(history) = history.filter(|h| h.reading != Reading::Loading) else {
        return caption("crew.handoffs.loading", "Loading handoffs…", t);
    };
    if history.reading == Reading::Failed {
        return row()
            .gap(space::S2)
            .items_center()
            .child(alert(
                "crew.handoffs.error",
                history.error.clone().unwrap_or_default(),
                t,
            ))
            .child(button(
                "crew.handoffs.retry",
                "Retry",
                false,
                t,
                move |state, tx| handoffs::load(state, &id, true, tx),
            ));
    }
    let refresh_id = member.id.clone();
    let head = document_heading("Handoffs", t).child(
        row()
            .gap(9.0)
            .items_center()
            .children((!history.handoffs.is_empty()).then(|| {
                type_::caps("Read only")
                    .key("crew.handoffs.read-only")
                    .color(t.text_muted)
            }))
            .child(button(
                "crew.handoffs.refresh",
                "Refresh",
                false,
                t,
                move |state, tx| handoffs::load(state, &refresh_id, true, tx),
            )),
    );
    let section = col().key("crew.handoffs").w_full().max_w(WIDE).child(head);
    let Some(shown) = history.shown() else {
        return section.child(
            col()
                .key("crew.handoffs.none")
                .w_full()
                .items_center()
                .px(15.0)
                .py(60.0)
                .radius(8.0)
                .border(1.0, t.border)
                .child(caption("crew.handoffs.empty", "No handoffs recorded.", t)),
        );
    };
    let count = history.handoffs.len();
    let index = col()
        .key("crew.handoffs.index")
        .role("navigation")
        .label(format!("{} handoffs", member.name))
        .w(150.0)
        .gap(2.0)
        .px(8.0)
        .py(13.0)
        .border_sides([0.0, 1.0, 0.0, 0.0], t.border)
        .child(
            type_::upper(
                &format!("{count} {}", if count == 1 { "letter" } else { "letters" }),
                type_::MONO_CAPTION,
            )
            .key("crew.handoffs.count")
            .color(t.text_muted)
            .px(7.0)
            .pb(8.0),
        )
        .children(history.handoffs.iter().enumerate().map(|(at, h)| {
            let (day, time) = day_and_time(&h.occurred_at);
            let latest = if at == 0 { "Latest · " } else { "" };
            let (member, file) = (member.id.clone(), h.filename.clone());
            let current = h.filename == shown.filename;
            let entry = col()
                .key(format!("crew.handoff.{at}"))
                .role("button")
                .label(format!("{day} {latest}{time} UTC"))
                .focusable(true)
                .focus_ring(2.0, 2.0, t.brand)
                .w_full()
                .gap(4.0)
                .px(8.0)
                .py(9.0)
                .radius(6.0)
                .child(text(day).font(NOTE).color(t.text_secondary))
                .child(
                    text(format!("{latest}{time} UTC"))
                        .font(NOTE)
                        .color(t.text_muted),
                )
                .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                    handoffs::select(state, &member, &file, tx)
                })
                .checked(current);
            if current {
                entry
                    .fill(t.brand.alpha(0.08))
                    .border(1.0, attn_ui::colors::mix_srgb(t.border, t.brand, 0.28))
            } else {
                entry.border(1.0, Color::TRANSPARENT)
            }
        }));
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
                    outpost: None,
                    view: view.clone(),
                    opens_seed: Some(super::open_seed),
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
            row()
                .gap(space::S2)
                .child(alert(
                    "crew.handoff.letter-error",
                    l.error
                        .clone()
                        .unwrap_or_else(|| "The letter could not be read.".into()),
                    t,
                ))
                .child(button(
                    "crew.handoff.letter-retry",
                    "Retry",
                    false,
                    t,
                    move |state, tx| handoffs::load_letter(state, &member, &file, tx),
                ))
        }
        _ => caption("crew.handoff.loading", "Loading letter…", t),
    };
    let reader = col()
        .key("crew.handoff.reader")
        .role("article")
        .label(shown.filename.clone())
        .grow(1.0)
        .min_w(0.0)
        .child(
            row()
                .w_full()
                .gap(12.0)
                .px(15.0)
                .py(11.0)
                .border_sides([0.0, 0.0, 1.0, 0.0], t.border)
                .child(
                    caption(
                        "crew.handoff.date",
                        format!("{day} {time} UTC · {}", shown.filename),
                        t,
                    )
                    .color(t.text_secondary),
                ),
        )
        .child(col().w_full().padding(15.0).child(body));
    section.child(
        row()
            .key("crew.handoffs.letters")
            .w_full()
            .min_h(420.0)
            .items_stretch()
            .radius(8.0)
            .fill(t.bg_panel)
            .border(1.0, t.border)
            .child(index)
            .child(reader),
    )
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

fn seeds_tab(state: &State, member: &CrewMember, t: &Tokens) -> El {
    let garden = &state.facts.garden;
    let all = garden.of(&state.facts.profiles.selected);
    let tended = seeds::tended(all, member);
    let planted = seeds::planted(all, member);
    let place = &state.window.crew.place;
    let filter = place.seed_filter;
    let capped = garden.total > all.len() as i64;
    let suffix = if capped { "+" } else { "" };
    let filters = row()
        .key("crew.seeds.filters")
        .role("group")
        .label("Seed ownership")
        .w_full()
        .gap(20.0)
        .border_sides([0.0, 0.0, 1.0, 0.0], t.border_subtle)
        .children(
            [
                (SeedFilter::Tending, "tending", "Tending", tended.len()),
                (SeedFilter::Planted, "planted", "Planted", planted.len()),
            ]
            .into_iter()
            .map(|(f, id, label, count)| {
                underlined(
                    format!("crew.seeds.{id}"),
                    label.to_owned(),
                    Some(format!("{count}{suffix}")),
                    f == filter,
                    [8.0, 10.0],
                    1.0,
                    t,
                )
                .px(1.0)
                .on_press_tx(move |state: &mut State, _: &mut Tx| {
                    state.window.crew.place.seed_filter = f
                })
            }),
        );
    let mut section = col()
        .key("crew.seeds")
        .role("group")
        .label("Seeds")
        .w_full()
        .max_w(NARROW)
        .child(
            col()
                .gap(3.0)
                .mt(3.0)
                .mb(12.0)
                .child(kicker("Garden", t))
                .child(
                    text("Seeds")
                        .role("heading")
                        .font(SEEDS)
                        .color(t.text_primary),
                ),
        )
        .child(filters);
    if capped {
        section = section.child(
            status(
                "crew.seeds.cap",
                format!(
                    "Showing matches in the newest {} of {} seeds.",
                    all.len(),
                    garden.total
                ),
                t.warning,
            )
            .mt(10.0),
        );
    }
    let rows = match filter {
        SeedFilter::Tending => &tended,
        SeedFilter::Planted => &planted,
    };
    if rows.is_empty() {
        return section.child(
            col()
                .key("crew.seeds.none")
                .w_full()
                .items_center()
                .gap(10.0)
                .px(12.0)
                .py(40.0)
                .child(caption(
                    "crew.seeds.empty",
                    seeds::empty(&member.name, filter, capped),
                    t,
                ))
                .children(
                    (filter == SeedFilter::Tending && !planted.is_empty()).then(|| {
                        text("See planted seeds")
                            .key("crew.seeds.see-planted")
                            .role("button")
                            .label("See planted seeds")
                            .focusable(true)
                            .font(NOTE)
                            .color(t.brand)
                            .on_press_tx(|state: &mut State, _: &mut Tx| {
                                state.window.crew.place.seed_filter = SeedFilter::Planted
                            })
                    }),
                ),
        );
    }
    let query = place.seed_query.text.clone();
    let visible: Vec<&&Seed> = rows.iter().filter(|s| seeds::matches(s, &query)).collect();
    section = section.child(
        TextInput::new(&place.seed_query)
            .key(SEED_FIND)
            .label("Find a seed")
            .placeholder("Find a seed…")
            .width(None)
            .on_edit(|state: &mut State| &mut state.window.crew.place.seed_query)
            .build(attn_ui::kit(t)),
    );
    if visible.is_empty() {
        return section.child(caption("crew.seeds.no-match", "No matching seeds.", t));
    }
    section.child(
        col()
            .key("crew.seeds.list")
            .role("list")
            .focus_group()
            .label(match filter {
                SeedFilter::Tending => "Tended seeds",
                SeedFilter::Planted => "Planted seeds",
            })
            .w_full()
            .gap(2.0)
            .children(visible.into_iter().map(|seed| seed_row(seed, all, t))),
    )
}

fn seed_row(seed: &Seed, all: &[std::sync::Arc<Seed>], t: &Tokens) -> El {
    let title = if seed.title.trim().is_empty() {
        seed.id.clone()
    } else {
        seed.title.trim().to_owned()
    };
    let plot = seeds::plot(seed, all).map(|p| {
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
    let (primary, tertiary, secondary) = (t.text_primary, t.text_tertiary, t.text_secondary);
    Button::new(label)
        .variant(ButtonVariant::Ghost)
        .label_with(move |font, _| {
            row()
                .w_full()
                .gap(space::S2)
                .items_center()
                .child(
                    col()
                        .grow(1.0)
                        .min_w(0.0)
                        .child(text(title).font(font).color(primary))
                        .child(text(meta.join(" · ")).font(type_::LABEL).color(tertiary)),
                )
                .child(text(state_words).font(type_::LABEL).color(secondary))
        })
        .on_press_tx(move |state: &mut State, tx: &mut Tx| {
            super::navigate(state, Nav::Seed(id.clone()), tx)
        })
        .build(attn_ui::kit(t))
        .key(format!("crew.seed.{}", seed.id))
        .w_full()
        .h(row_height(1, t))
}

/// The height of a list row whose name sits over `notes` lines of small print.
fn row_height(notes: usize, t: &Tokens) -> f32 {
    attn_ui::kit(t).ty.ui_medium.line_px() + notes as f32 * type_::LABEL.line_px() + 2.0 * space::S2
}
