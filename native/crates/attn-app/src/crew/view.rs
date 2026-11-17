use attn_protocol::{CrewMember, Seed};
use attn_ui::Tokens;
use rune::tok::{space, type_};
use rune::{El, Key, KeyEvent, Tx, col, row, text};
use rune_kit::{Button, ButtonVariant, ControlSize, Dialog, TextArea, TextInput};

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
const SEED_FIND: &str = "crew.seeds.find";

fn caption(key: impl Into<String>, words: impl Into<String>, t: &Tokens) -> El {
    text(words.into())
        .key(key.into())
        .font(type_::LABEL)
        .color(t.text_tertiary)
}

fn kicker(words: &str, t: &Tokens) -> El {
    text(words.to_owned())
        .font(type_::SECTION)
        .color(t.text_tertiary)
}

fn status(key: &str, words: impl Into<String>, color: rune::Color) -> El {
    let words = words.into();
    text(words.clone())
        .key(key.to_owned())
        .role("status")
        .label(words)
        .font(type_::LABEL)
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

fn button(
    key: impl Into<String>,
    label: &str,
    disabled: bool,
    t: &Tokens,
    press: impl Fn(&mut State, &mut Tx) + 'static,
) -> El {
    Button::new(label)
        .control_size(ControlSize::Small)
        .disabled(disabled)
        .on_press_tx(press)
        .build(attn_ui::kit(t))
        .key(key.into())
}

/// The Manage crew panel, while it is open, with the model picker of its launch settings.
pub(crate) fn panel(state: &State, t: &Tokens, layers: &mut Vec<El>) {
    let k = attn_ui::kit(t);
    Dialog::new("crew", "Manage crew")
        .description("Crew")
        .open(true)
        .width(960.0)
        .max_height(720.0)
        .on_close_tx(|state: &mut State, tx: &mut Tx| super::close(state, tx))
        .child(
            row()
                .key("crew.shell")
                .w_full()
                .grow(1.0)
                .min_h(0.0)
                .child(roster(state, t))
                .child(detail(state, t)),
        )
        .build(k, layers);
    crate::settings::route_overlay(state, t, layers);
    crate::launch_desktop::overlay(state, t, layers);
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
        .w(240.0)
        .h_full()
        .gap(space::S2)
        .padding(space::S3)
        .border_sides([0.0, 1.0, 0.0, 0.0], t.border)
        .child(
            row()
                .w_full()
                .child(kicker("Members", t).grow(1.0))
                .child(caption("crew.roster.count", members.len().to_string(), t)),
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
        .gap(2.0)
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
        .map(|s| launch::status(&s));
    let name = member.name.clone();
    let desktop = member
        .launch_desktop
        .as_ref()
        .and_then(|d| d.label.clone())
        .unwrap_or_default();
    let presence = presence(member);
    let lines = if desktop.is_empty() { 1 } else { 2 };
    let (primary, tertiary, warning) = (t.text_primary, t.text_tertiary, t.warning);
    Button::new(format!("{name} {presence}"))
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
                        .child(text(name).font(font).color(primary))
                        .children(
                            (!desktop.is_empty())
                                .then(|| text(desktop).font(type_::LABEL).color(tertiary)),
                        )
                        .child(text(presence).font(type_::LABEL).color(tertiary)),
                )
                .children(save.map(|s| text(s).font(type_::LABEL).color(warning)))
        })
        .on_press_tx(move |state: &mut State, tx: &mut Tx| {
            super::navigate(state, Nav::Member(id.clone()), tx)
        })
        .build(attn_ui::kit(t))
        .key(format!("crew.roster.{}", member.id))
        .w_full()
        .h(row_height(lines, t))
        .focus_selected(is_selected)
        .checked(is_selected)
}

fn detail(state: &State, t: &Tokens) -> El {
    let main = col()
        .key("crew.detail")
        .grow(1.0)
        .min_w(0.0)
        .h_full()
        .gap(space::S3)
        .padding(space::S3)
        .scroll(rune::ScrollSpec {
            stick_bottom: false,
        });
    let Some(member) = super::selected(state) else {
        return main.child(caption("crew.empty", "No crew members.", t));
    };
    let tab = state.window.crew.place.tab;
    main.child(heading(state, member, t))
        .child(tabs(tab, t))
        .children(match tab {
            Tab::Launch => launch_tab(state, member, t),
            Tab::Charter => vec![charter_tab(state, member, t)],
            Tab::Handoffs => vec![handoffs_tab(state, member, t)],
            Tab::Seeds => vec![seeds_tab(state, member, t)],
        })
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
                        .font(type_::TITLE)
                        .color(t.text_primary),
                )
                .child(
                    Button::new("Rename")
                        .variant(ButtonVariant::Ghost)
                        .control_size(ControlSize::Small)
                        .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                            super::begin_rename(state, &id, tx)
                        })
                        .build(attn_ui::kit(t))
                        .key(RENAME),
                )
        }
    };
    row()
        .w_full()
        .items_center()
        .gap(space::S3)
        .child(
            col()
                .grow(1.0)
                .child(name)
                .child(caption("crew.presence", presence(member), t)),
        )
}

fn tabs(shown: Tab, t: &Tokens) -> El {
    row()
        .key("crew.tabs")
        .role("navigation")
        .label("Member details")
        .gap(space::S1)
        .children(Tab::ALL.into_iter().map(|tab| {
            Button::new(tab.label())
                .variant(if tab == shown {
                    ButtonVariant::Neutral
                } else {
                    ButtonVariant::Ghost
                })
                .control_size(ControlSize::Small)
                .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                    super::navigate(state, Nav::Tab(tab), tx)
                })
                .build(attn_ui::kit(t))
                .key(format!("crew.tab.{}", tab.id()))
                .checked(tab == shown)
        }))
}

fn card(key: &str, t: &Tokens) -> El {
    col()
        .key(key.to_owned())
        .w_full()
        .gap(space::S2)
        .padding(space::S3)
        .radius(8.0)
        .border(1.0, t.border)
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
        let words = format!(
            "{harness} · Model not reported · Effort not reported · {}",
            session.chars().take(8).collect::<String>()
        );
        tab.push(
            card("crew.running", t)
                .role("group")
                .label("Running now")
                .child(kicker("Running now", t))
                .child(
                    text(words.clone())
                        .key("crew.running.route")
                        .label(words)
                        .font(type_::BODY)
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
    let acknowledged = route_words(
        state,
        &shown.acknowledged.resolved_agent,
        shown
            .acknowledged
            .resolved_model
            .as_deref()
            .unwrap_or_default(),
        shown
            .acknowledged
            .resolved_effort
            .as_deref()
            .unwrap_or_default(),
    );
    let save = launch::status(&shown);
    let mut launch_card = card("crew.launch", t)
        .role("group")
        .label("Launch settings")
        .child(
            row()
                .w_full()
                .items_center()
                .child(
                    col().grow(1.0).child(kicker("Next session", t)).child(
                        text("Launch settings")
                            .role("heading")
                            .font(type_::TITLE)
                            .color(t.text_primary),
                    ),
                )
                .child(status("crew.launch.status", save, t.text_secondary))
                .children(shown.error.is_some().then(|| {
                    let id = id.clone();
                    button("crew.launch.retry", "Retry", false, t, move |state, tx| {
                        launch::pump(state, &id, tx)
                    })
                })),
        )
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
        ))
        .child(crate::settings::route_chip(
            state,
            t,
            route_target(&member.id),
            "Crew launch model",
        ));
    if let Some(warning) = warning {
        launch_card = launch_card.child(
            row()
                .gap(space::S2)
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
                .gap(space::S2)
                .child(caption(
                    "crew.acknowledged.title",
                    "Acknowledged next session",
                    t,
                ))
                .child(
                    text(acknowledged)
                        .font(type_::LABEL)
                        .color(t.text_secondary),
                ),
        )
        .children(
            shown
                .error
                .clone()
                .map(|why| alert("crew.launch.error", why, t)),
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
            card("crew.apply.section", t)
                .child(text("Apply settings now").role("heading").font(type_::TITLE).color(t.text_primary))
                .child(
                    text("The same harness reloads in place and keeps the conversation. A new harness starts after the member files its letter.")
                        .font(type_::LABEL)
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
                }))
                .child(button(
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
        card("crew.session", t)
            .child(
                text(if in_session {
                    "Close session"
                } else {
                    "Start session"
                })
                .role("heading")
                .font(type_::TITLE)
                .color(t.text_primary),
            )
            .child(button(key, label, disabled, t, move |state, tx| {
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
        .rows(16)
        .spellcheck(true)
        .width(None)
        .on_edit(move |state: &mut State| state.window.crew.charters.field_mut(&edited))
        .on_change({
            let id = member.id.clone();
            move |state: &mut State, tx: &mut Tx| charter::edited(state, &id, tx)
        })
        .build(attn_ui::kit(t));
    let mut section = card("crew.charter", t)
        .role("group")
        .label("Charter editor")
        .child(
            row()
                .w_full()
                .items_center()
                .child(
                    text("Charter")
                        .role("heading")
                        .font(type_::TITLE)
                        .color(t.text_primary)
                        .grow(1.0),
                )
                .child(status(
                    "crew.charter.status",
                    charter::status(edit.phase),
                    t.text_secondary,
                )),
        )
        .child(caption("crew.charter.meta", "CHARTER.md · Markdown", t))
        .child(editor);
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
    let head = row()
        .w_full()
        .items_center()
        .gap(space::S2)
        .child(
            text("Handoffs")
                .role("heading")
                .font(type_::TITLE)
                .color(t.text_primary)
                .grow(1.0),
        )
        .children(
            (!history.handoffs.is_empty())
                .then(|| caption("crew.handoffs.read-only", "Read only", t)),
        )
        .child(button(
            "crew.handoffs.refresh",
            "Refresh",
            false,
            t,
            move |state, tx| handoffs::load(state, &refresh_id, true, tx),
        ));
    let section = col()
        .key("crew.handoffs")
        .w_full()
        .gap(space::S2)
        .child(head);
    let Some(shown) = history.shown() else {
        return section.child(caption("crew.handoffs.empty", "No handoffs recorded.", t));
    };
    let count = history.handoffs.len();
    let index = col()
        .key("crew.handoffs.index")
        .role("navigation")
        .label(format!("{} handoffs", member.name))
        .w(180.0)
        .gap(2.0)
        .child(caption(
            "crew.handoffs.count",
            format!("{count} {}", if count == 1 { "letter" } else { "letters" }),
            t,
        ))
        .children(history.handoffs.iter().enumerate().map(|(at, h)| {
            let (day, time) = day_and_time(&h.occurred_at);
            let latest = if at == 0 { "Latest · " } else { "" };
            let (member, file) = (member.id.clone(), h.filename.clone());
            Button::new(format!("{day} {latest}{time} UTC"))
                .variant(ButtonVariant::Ghost)
                .control_size(ControlSize::Small)
                .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                    handoffs::select(state, &member, &file, tx)
                })
                .build(attn_ui::kit(t))
                .key(format!("crew.handoff.{at}"))
                .checked(h.filename == shown.filename)
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
                None,
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
        .gap(space::S2)
        .child(caption(
            "crew.handoff.date",
            format!("{day} {time} UTC · {}", shown.filename),
            t,
        ))
        .child(body);
    section.child(
        row()
            .w_full()
            .gap(space::S3)
            .items_start()
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
        .gap(space::S1)
        .children(
            [
                (SeedFilter::Tending, "tending", "Tending", tended.len()),
                (SeedFilter::Planted, "planted", "Planted", planted.len()),
            ]
            .into_iter()
            .map(|(f, id, label, count)| {
                Button::new(format!("{label} {count}{suffix}"))
                    .variant(if f == filter {
                        ButtonVariant::Neutral
                    } else {
                        ButtonVariant::Ghost
                    })
                    .control_size(ControlSize::Small)
                    .on_press_tx(move |state: &mut State, _: &mut Tx| {
                        state.window.crew.place.seed_filter = f
                    })
                    .build(attn_ui::kit(t))
                    .key(format!("crew.seeds.{id}"))
                    .checked(f == filter)
            }),
        );
    let mut section = col()
        .key("crew.seeds")
        .role("group")
        .label("Seeds")
        .w_full()
        .gap(space::S2)
        .child(kicker("Garden", t))
        .child(
            text("Seeds")
                .role("heading")
                .font(type_::TITLE)
                .color(t.text_primary),
        )
        .child(filters);
    if capped {
        section = section.child(status(
            "crew.seeds.cap",
            format!(
                "Showing matches in the newest {} of {} seeds.",
                all.len(),
                garden.total
            ),
            t.text_tertiary,
        ));
    }
    let rows = match filter {
        SeedFilter::Tending => &tended,
        SeedFilter::Planted => &planted,
    };
    if rows.is_empty() {
        return section
            .child(caption(
                "crew.seeds.empty",
                seeds::empty(&member.name, filter, capped),
                t,
            ))
            .children(
                (filter == SeedFilter::Tending && !planted.is_empty()).then(|| {
                    button(
                        "crew.seeds.see-planted",
                        "See planted seeds",
                        false,
                        t,
                        |state, _| state.window.crew.place.seed_filter = SeedFilter::Planted,
                    )
                }),
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
