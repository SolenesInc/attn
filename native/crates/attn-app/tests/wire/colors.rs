use std::collections::{BTreeMap, BTreeSet};

use attn_protocol::{InitialStateMessage, SettingsUpdatedMessage};
use attn_testkit::{Daemon, Target, World};
use serde_json::Value;

use super::queue::{one_desktop, queue, turn};
use super::terminal::one_terminal;

const ATTN: &str = include_str!("../../../../../internal/colors/schemes/attn.json");
const CATPPUCCIN: &str = include_str!("../../../../../internal/colors/schemes/catppuccin.json");

fn dark(scheme: &str, group: &str, name: &str) -> String {
    let file: Value = serde_json::from_str(scheme).unwrap();
    file["dark"][group][name].as_str().unwrap().to_owned()
}

fn attn(group: &str, name: &str) -> String {
    dark(ATTN, group, name)
}

fn catppuccin(group: &str, name: &str) -> String {
    dark(CATPPUCCIN, group, name)
}

fn update(world: &mut World, settings: &[(&str, &str)]) {
    let settings: BTreeMap<String, String> = settings
        .iter()
        .map(|(k, v)| ((*k).to_owned(), (*v).to_owned()))
        .collect();
    world.daemon.emit(SettingsUpdatedMessage {
        settings: Some(settings),
        ..SettingsUpdatedMessage::new()
    });
    world.settle();
}

fn window_background(world: &World) -> String {
    let [r, g, b, _] = world.runtime().clear_color();
    let byte = |c: f32| (c * 255.0).round() as u8;
    format!("#{:02x}{:02x}{:02x}", byte(r), byte(g), byte(b))
}

fn terminal_background(world: &World) -> String {
    let themes = world.daemon.sent_of("set_terminal_theme");
    let last = themes.last().expect("the daemon heard the terminal colors");
    last["background"].as_str().unwrap().to_owned()
}

#[test]
fn a_scheme_and_an_override_recolor_the_window_and_its_terminals_as_they_arrive() {
    let mut world = World::start(one_terminal());
    world.open_desktop();
    assert_eq!(window_background(&world), attn("ui", "bg_app"));
    assert_eq!(terminal_background(&world), attn("terminal", "bg"));

    update(&mut world, &[("color_scheme", "catppuccin")]);
    assert_eq!(window_background(&world), catppuccin("ui", "bg_app"));
    assert_eq!(terminal_background(&world), catppuccin("terminal", "bg"));

    update(
        &mut world,
        &[
            ("color_scheme", "catppuccin"),
            (
                "color_overrides",
                r##"{"dark":{"ui":{"bg_app":"#24273a"},"terminal":{"bg":"#303446"}},"light":{"ui":{"bg_app":"#ffffff"}}}"##,
            ),
        ],
    );
    assert_eq!(window_background(&world), "#24273a");
    assert_eq!(terminal_background(&world), "#303446");

    update(&mut world, &[]);
    assert_eq!(window_background(&world), attn("ui", "bg_app"));
    assert_eq!(terminal_background(&world), attn("terminal", "bg"));
}

#[test]
fn a_saved_scheme_attn_no_longer_has_draws_attn_and_the_other_settings_still_apply() {
    let mut world = World::start(one_terminal());
    world.open_desktop();
    update(&mut world, &[("color_scheme", "night"), ("theme", "light")]);
    let light = |group: &str, name: &str| {
        let file: Value = serde_json::from_str(ATTN).unwrap();
        file["light"][group][name].as_str().unwrap().to_owned()
    };
    assert_eq!(window_background(&world), light("ui", "bg_app"));
    assert_eq!(terminal_background(&world), light("terminal", "bg"));
}

fn menu_fill(world: &World, key: &str) -> String {
    let menu = world
        .runtime()
        .elements()
        .into_iter()
        .find(|e| e.key.as_deref() == Some(key))
        .unwrap_or_else(|| panic!("no {key} on screen"));
    menu.fill.hex()[..7].to_owned()
}

#[test]
fn a_standard_menu_fills_with_the_scheme_s_panel_and_its_override_as_they_arrive() {
    const MENU: &str = "snooze.menu.snooze-menu";
    let mut world =
        World::start(Daemon::scripted().state(queue(one_desktop(vec![turn("t-old", 30)]))));
    world.gesture(|app| app.click(Target::role("button").name("Snooze t-old")));
    assert_eq!(menu_fill(&world, MENU), attn("ui", "bg_panel"));

    let queue_mode = ("queue_mode_enabled", "true");
    update(&mut world, &[queue_mode, ("color_scheme", "catppuccin")]);
    assert_eq!(menu_fill(&world, MENU), catppuccin("ui", "bg_panel"));

    update(
        &mut world,
        &[
            queue_mode,
            ("color_scheme", "catppuccin"),
            (
                "color_overrides",
                r##"{"dark":{"ui":{"bg_panel":"#2a2b3c"}}}"##,
            ),
        ],
    );
    assert_eq!(menu_fill(&world, MENU), "#2a2b3c");
}

fn label_ink(world: &World, label: &str) -> String {
    let text = world
        .runtime()
        .elements()
        .into_iter()
        .find(|e| e.text.as_deref() == Some(label))
        .unwrap_or_else(|| panic!("no {label} on screen"));
    text.text_color.expect("a label has ink").hex()[..7].to_owned()
}

#[test]
fn a_standard_menu_draws_the_row_the_keys_reach_in_primary_ink_and_the_rest_in_secondary() {
    let mut world =
        World::start(Daemon::scripted().state(queue(one_desktop(vec![turn("t-old", 30)]))));
    world.gesture(|app| app.click(Target::role("button").name("Snooze t-old")));
    let (primary, secondary) = (attn("ui", "text_primary"), attn("ui", "text_secondary"));
    assert_eq!(label_ink(&world, "For 30 minutes"), primary);
    assert_eq!(label_ink(&world, "For an hour"), secondary);

    world.gesture(|app| app.key(super::queue::plain(rune::Key::Down)));
    assert_eq!(label_ink(&world, "For 30 minutes"), secondary);
    assert_eq!(label_ink(&world, "For an hour"), primary);
}

/// The contrast of every word drawn in a `fill` inside the element keyed `within`, the element
/// itself included.
fn words_on(world: &World, within: &str, fill: rune::Color) -> Vec<f32> {
    let all = world.runtime().elements();
    let inside = |e: &rune::ElementInfo, outer: usize| {
        std::iter::successors(Some(e), |e| e.parent.map(|p| &all[p]))
            .any(|a| a.path == all[outer].path)
    };
    let root = all
        .iter()
        .position(|e| e.key.as_deref() == Some(within))
        .unwrap_or_else(|| panic!("no {within} on screen"));
    all.iter()
        .enumerate()
        .filter(|(_, e)| e.fill == fill && inside(e, root))
        .flat_map(|(i, _)| {
            all.iter()
                .filter(move |w| w.text_color.is_some() && inside(w, i))
                .map(move |w| attn_ui::colors::contrast(w.text_color.unwrap(), fill))
        })
        .collect()
}

type Status = fn(&attn_ui::Tokens) -> rune::Color;

/// Each place in `world` that draws words on a status fill, as what it is, the element it lies
/// in and the fill, measured in every scheme on both sides with `settings` kept.
fn unread_in_every_scheme(
    world: &mut World,
    settings: &[(&str, &str)],
    places: &[(&str, &str, Status)],
) -> Vec<String> {
    let mut unread = Vec::new();
    for scheme in attn_ui::colors::SCHEMES.iter() {
        for (side, dark) in [("dark", true), ("light", false)] {
            let mut all = vec![("color_scheme", scheme.id), ("theme", side)];
            all.extend_from_slice(settings);
            update(world, &all);
            let t = scheme.colors(dark).ui;
            for &(place, within, fill) in places {
                let reads = words_on(world, within, fill(&t));
                assert!(!reads.is_empty(), "{place} draws no words on its fill");
                for read in reads.into_iter().filter(|&r| r < 4.5) {
                    unread.push(format!("{}, {side}: {place} reads {read:.2}:1", scheme.id));
                }
            }
        }
    }
    unread
}

#[test]
fn a_count_or_mark_on_a_status_fill_reads_at_4_5_to_1_in_every_scheme_dark_and_light() {
    let mut bar = World::start(Daemon::scripted().state(queue(super::queue::crowding_desktops(3))));
    bar.gesture(|app| app.click(Target::role("button").name("Collapse sidebar")));
    bar.resize((1400.0, 900.0));
    let mut unread = unread_in_every_scheme(
        &mut bar,
        &[("queue_mode_enabled", "true")],
        &[
            ("the queue bar's desktop count", "queue-bar.desktops", |t| {
                t.warning
            }),
        ],
    );

    let mut asks = super::asks::start(
        vec![super::asks::cache_store(), super::asks::release_day()],
        vec![super::asks::api_working(), super::asks::web_waiting()],
    );
    asks.open_desktop();
    super::asks::open_drawer(&mut asks);
    unread.extend(unread_in_every_scheme(
        &mut asks,
        &[],
        &[
            ("the asks drawer's count", "asks.count", |t| t.accent),
            ("an asking agent's mark", "sidebar.session.web.ask", |t| {
                t.accent
            }),
        ],
    ));

    let mut vents = super::settings::analytics::triage();
    unread.extend(unread_in_every_scheme(
        &mut vents,
        &[("analytics.enabled", "true")],
        &[(
            "the vents tab's count",
            "settings.analytics.views.vents",
            |t| t.brand,
        )],
    ));

    assert!(
        unread.is_empty(),
        "words under 4.5:1 on a status fill: {unread:#?}"
    );
}

const MARKED: [&str; 7] = [
    "working",
    "waiting_input",
    "pending_approval",
    "scheduled",
    "idle",
    "recoverable",
    "unknown",
];

/// A state mark on screen, by its key, in the ink it is drawn in and the ground the frame paints
/// under it: a dot's fill, or a harness logo's glyphs.
struct Mark {
    key: String,
    ink: rune::Color,
    ground: rune::Color,
}

fn marks(world: &World) -> Vec<Mark> {
    let rt = world.runtime();
    let scale = rt.env.scale;
    let all = rt.elements();
    let device = |r: [f32; 4]| r.map(|v| v * scale);
    let holds = |r: [f32; 4], [x, y]: [f32; 2]| {
        r[0] <= x && x <= r[0] + r[2] && r[1] <= y && y <= r[1] + r[3]
    };
    let centre = |r: [f32; 4]| [r[0] + r[2] / 2.0, r[1] + r[3] / 2.0];
    all.iter()
        .enumerate()
        .filter(|(_, e)| e.role == "img" && e.key.as_deref().is_some_and(|k| k.ends_with(".state")))
        .map(|(i, mark)| {
            let key = mark.key.clone().unwrap();
            let box_ = device(mark.rect);
            let within = |d: &rune::ElementInfo| {
                std::iter::successors(d.parent, |&p| all[p].parent).any(|p| p == i)
            };
            let (first, ink) = match all.iter().find(|d| within(d) && d.fill.a > 0.999) {
                Some(dot) => {
                    let drawn = device(dot.rect);
                    let at = rt
                        .instances
                        .iter()
                        .position(|p| {
                            p.params[1] == rune::render::KIND_RECT
                                && p.fill[3] > 0.999
                                && (0..4).all(|c| (p.rect[c] - drawn[c]).abs() <= 1.0)
                        })
                        .unwrap_or_else(|| panic!("{key}'s dot is not drawn"));
                    (at, dot.fill)
                }
                None => {
                    let ink = std::iter::once(mark)
                        .chain(all.iter().filter(|d| within(d)))
                        .find_map(|e| e.text_color)
                        .unwrap_or_else(|| panic!("{key} draws neither a dot nor a glyph"));
                    let at = rt
                        .instances
                        .iter()
                        .position(|p| {
                            p.params[1] == rune::render::KIND_GLYPH && holds(box_, centre(p.rect))
                        })
                        .unwrap_or_else(|| panic!("{key}'s glyph is not drawn"));
                    (at, ink)
                }
            };
            let under = centre(box_);
            let [r, g, b, _] = rt.clear_color();
            let [r, g, b] = rt.instances[..first]
                .iter()
                .filter(|p| {
                    let [x0, y0, x1, y1] = p.clip;
                    p.params[1] == rune::render::KIND_RECT
                        && p.params[2] == 0.0
                        && holds(p.rect, under)
                        && holds([x0, y0, x1 - x0, y1 - y0], under)
                })
                .fold([r, g, b], |below, p| {
                    [0, 1, 2].map(|c| p.fill[c] + below[c] * (1.0 - p.fill[3]))
                })
                .map(rune::theme::srgb_decode);
            Mark {
                key,
                ink,
                ground: rune::Color { r, g, b, a: 1.0 },
            }
        })
        .collect()
}

fn centre_of(world: &World, key: &str) -> (f32, f32) {
    let [x, y, w, h] = world
        .screen()
        .bounds(Target::key(key))
        .unwrap_or_else(|| panic!("{key} is laid out"));
    (x + w / 2.0, y + h / 2.0)
}

/// A state mark reads on its ground at the 3:1 a control's edge keeps.
const MARK_READS: f32 = 3.0;
/// Idle and unknown, both grey, stand at least this far apart in luminance, as they measured
/// when they were set at least 10 apart in OKLab lightness.
const GREYS_APART: f32 = 1.5;

/// One agent in each state on one desktop in attn's colours on `side`, with the pane of
/// `MARKED[chosen]` the active one.
fn every_state(side: &str, chosen: Option<usize>) -> InitialStateMessage {
    let sessions = MARKED
        .iter()
        .map(|state| super::sidebar::session(state, state))
        .collect();
    let mut message = one_desktop(sessions);
    message.settings = Some(BTreeMap::from([
        ("color_scheme".to_owned(), "attn".to_owned()),
        ("theme".to_owned(), side.to_owned()),
    ]));
    if let Some(i) = chosen {
        message.desktops.as_mut().unwrap()[0].active_pane_id = format!("p{i}");
    }
    message
}

fn ground_of(world: &World, key: &str) -> rune::Color {
    marks(world)
        .into_iter()
        .find(|m| m.key == key)
        .unwrap_or_else(|| panic!("{key} is not drawn"))
        .ground
}

/// Every grey state mark, idle's, recoverable's or unknown's, that `world` draws under 3:1 on
/// its ground, with each agent's row at rest, hovered, chosen, and chosen and hovered, on Home,
/// in the sidebar and in the tile headers.
fn faint_marks(world: &mut World, side: &str) -> BTreeSet<String> {
    let t = attn_ui::colors::scheme_for(Some("attn"))
        .colors(side == "dark")
        .ui;
    let grey = |ink: rune::Color| {
        [t.state_idle, t.state_unknown].iter().any(|g| {
            [g.r - ink.r, g.g - ink.g, g.b - ink.b]
                .iter()
                .all(|d| d.abs() < 1e-3)
        })
    };
    let mut faint = BTreeSet::new();
    let mut measure = |world: &World, how: &str| {
        for m in marks(world).into_iter().filter(|m| grey(m.ink)) {
            let read = attn_ui::colors::contrast(m.ink, m.ground);
            if read < MARK_READS {
                faint.insert(format!("{side}, {how}: {} reads {read:.2}:1", m.key));
            }
        }
    };
    let away = (700.0, 880.0);
    world.daemon.emit(every_state(side, None));
    world.settle();
    world.gesture(|app| app.click(Target::key("sidebar.home")));
    world.gesture(|app| app.move_to(away));
    assert_eq!(
        marks(world).len(),
        2 * MARKED.len(),
        "every agent has a mark in the sidebar and on Home"
    );
    let at_rest: Vec<_> = MARKED
        .iter()
        .map(|state| ground_of(world, &format!("sidebar.session.{state}.state")))
        .collect();
    measure(world, "at rest");
    for state in MARKED {
        for row in [
            format!("home.session.{state}"),
            format!("sidebar.session.{state}"),
        ] {
            let at = centre_of(world, &row);
            world.gesture(|app| app.move_to(at));
            assert_ne!(
                ground_of(world, &format!("{row}.state")),
                at_rest[0],
                "{row} lights under the pointer"
            );
            measure(world, "hovered");
        }
    }
    world.open_desktop();
    for (i, state) in MARKED.iter().enumerate() {
        let key = format!("sidebar.session.{state}");
        world.daemon.emit(every_state(side, Some(i)));
        world.gesture(|app| app.move_to(away));
        let chosen = ground_of(world, &format!("{key}.state"));
        assert_ne!(
            chosen, at_rest[i],
            "{key} lights when its pane is the active one"
        );
        measure(world, "chosen");
        let at = centre_of(world, &key);
        world.gesture(|app| app.move_to(at));
        assert_ne!(
            ground_of(world, &format!("{key}.state")),
            chosen,
            "{key} lights further under the pointer"
        );
        measure(world, "chosen and hovered");
    }
    faint
}

#[test]
fn attns_grey_state_marks_read_at_3_to_1_on_every_ground_and_idle_stands_apart_from_unknown() {
    let mut world = World::start(Daemon::scripted().state(every_state("dark", None)));
    world.resize((1400.0, 900.0));
    let mut faint = BTreeSet::new();
    for side in ["dark", "light"] {
        faint.extend(faint_marks(&mut world, side));
        let ink = |state: &str| {
            marks(&world)
                .into_iter()
                .find(|m| m.key == format!("sidebar.session.{state}.state"))
                .unwrap_or_else(|| panic!("no {state} mark in the sidebar"))
                .ink
        };
        let apart = attn_ui::colors::contrast(ink("idle"), ink("unknown"));
        if apart < GREYS_APART {
            faint.insert(format!("{side}: idle stands {apart:.2}:1 from unknown"));
        }
    }
    assert!(
        faint.is_empty(),
        "attn's grey state marks too faint or too alike: {faint:#?}"
    );
}
