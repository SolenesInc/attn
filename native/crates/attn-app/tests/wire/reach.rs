use std::collections::BTreeSet;

use attn_testkit::{Daemon, Target, World};

use super::sidebar::{desktop, pane, session, state};

/// The buttons on screen that Tab never reaches: the keys of every enabled button, less those
/// that held the keyboard while Tab walked the window once round.
pub(super) fn unreached_by_tab(world: &mut World) -> Vec<String> {
    let buttons: BTreeSet<String> = world
        .screen()
        .all(Target::role("button"))
        .into_iter()
        .filter(|n| !n.is("disabled"))
        .filter_map(|n| n.key)
        .collect();
    let mut reached = BTreeSet::new();
    let mut first = None;
    for _ in 0..(buttons.len() * 3 + 20) {
        world.gesture(|app| app.press("tab"));
        let Some(key) = world.screen().focused().and_then(|n| n.key) else {
            continue;
        };
        if first.as_ref() == Some(&key) {
            break;
        }
        first.get_or_insert_with(|| key.clone());
        reached.insert(key);
    }
    buttons.difference(&reached).cloned().collect()
}

/// The focus ring a focused control draws: around it or around the box it sits in, in the dark
/// or light appearance, or none, as a finder's field.
pub(super) enum Ring {
    Drawn { dark: bool },
    None,
}

/// Whether the focused element and its `ring` show whole inside the element keyed `viewport`.
pub(super) fn focus_shows_whole(world: &World, viewport: &str, ring: Ring) -> Result<(), String> {
    let runtime = world.runtime();
    let all = runtime.elements();
    let focused = all.iter().find(|e| e.focused).ok_or("nothing has focus")?;
    let [vx, vy, vw, vh] = all
        .iter()
        .find(|e| e.key.as_deref() == Some(viewport))
        .ok_or_else(|| format!("no {viewport}"))?
        .rect;
    let inside = |[x, y, w, h]: [f32; 4]| {
        x >= vx - 0.5 && y >= vy - 0.5 && x + w <= vx + vw + 0.5 && y + h <= vy + vh + 0.5
    };
    let line = focused.rect;
    if !inside(line) {
        return Err(format!(
            "{:?} at {line:?} is outside {viewport} at {:?}",
            focused.key,
            [vx, vy, vw, vh]
        ));
    }
    let Ring::Drawn { dark } = ring else {
        return Ok(());
    };
    let t = attn_ui::colors::scheme_for(None).colors(dark).ui;
    let warm = [t.warning.r, t.warning.g, t.warning.b].map(rune::theme::srgb_encode);
    let to_pixels = runtime.zoom() * runtime.window_scale();
    let [x, y, w, h] = line.map(|v| v * to_pixels);
    let ring = runtime
        .instances
        .iter()
        .find(|i| {
            let [ix, iy, iw, ih] = i.rect;
            ix <= x
                && iy <= y
                && ix + iw >= x + w
                && iy + ih >= y + h
                && i.params[0] > 0.0
                && (0..3).all(|c| (i.border[c] - warm[c]).abs() < 0.01)
        })
        .ok_or_else(|| format!("{:?} draws no ring", focused.key))?;
    let [cx, cy, cr, cb] = ring.clip;
    let [rx, ry, rw, rh] = ring.rect;
    let unclipped = rx >= cx - 0.5 && ry >= cy - 0.5 && rx + rw <= cr + 0.5 && ry + rh <= cb + 0.5;
    let ring_points = ring.rect.map(|v| v / to_pixels);
    if !(unclipped && inside(ring_points)) {
        return Err(format!(
            "{:?}'s ring at {ring_points:?} is cut: {viewport} at {:?}",
            focused.key,
            [vx, vy, vw, vh]
        ));
    }
    Ok(())
}

/// The keys Tab reaches going once round from where focus is, each checked shown with its whole
/// focus ring inside the element `within` names for it.
pub(super) fn tab_round_in_view(world: &mut World, within: impl Fn(&str) -> String) -> Vec<String> {
    const RING_REACH: f64 = 4.0;
    let mut reached: Vec<String> = Vec::new();
    for _ in 0..200 {
        world.gesture(|app| app.press("tab"));
        let Some(key) = world.screen().focused().and_then(|n| n.key) else {
            continue;
        };
        if reached.first() == Some(&key) {
            break;
        }
        let region = within(&key);
        let [x, y, w, h] = world.rect(&key);
        let [rx, ry, rw, rh] = world.rect(&region);
        assert!(
            x - RING_REACH >= rx
                && y - RING_REACH >= ry
                && x + w + RING_REACH <= rx + rw
                && y + h + RING_REACH <= ry + rh,
            "{key}'s ring around {:?} is cut by {region} at {:?}",
            [x, y, w, h],
            [rx, ry, rw, rh]
        );
        reached.push(key);
    }
    reached
}

fn two_desktops_with_agents() -> attn_protocol::InitialStateMessage {
    let mut desktops = vec![
        desktop("d1", "a", "API", vec![pane("d1", "p1", "api")]),
        desktop("d2", "b", "Web", vec![pane("d2", "p2", "web")]),
    ];
    for (slot, d) in desktops.iter_mut().enumerate() {
        d.shortcut_slot = Some(slot as i64 + 1);
    }
    state(
        vec![session("api", "working"), session("web", "waiting_input")],
        desktops,
    )
}

#[test]
fn tab_reaches_every_button_at_home() {
    let mut world = World::start(Daemon::scripted().state(two_desktops_with_agents()));

    assert_eq!(unreached_by_tab(&mut world), Vec::<String>::new());
}

#[test]
fn tab_reaches_every_button_of_the_desktop_sidebar_and_title_bar() {
    let mut world = World::start(Daemon::scripted().state(two_desktops_with_agents()));
    world.open_desktop();

    assert_eq!(unreached_by_tab(&mut world), Vec::<String>::new());
}

#[test]
fn tab_reaches_every_button_of_the_queue_flow() {
    let mut world = World::start(Daemon::scripted().state(two_desktops_with_agents()));
    world.gesture(|app| app.click(Target::role("button").name("Switch to queue flow")));

    assert_eq!(unreached_by_tab(&mut world), Vec::<String>::new());
}

#[test]
fn tab_reaches_every_button_of_the_collapsed_rail() {
    let mut world = World::start(Daemon::scripted().state(two_desktops_with_agents()));
    world.gesture(|app| app.click(Target::role("button").name("Collapse sidebar")));

    assert_eq!(unreached_by_tab(&mut world), Vec::<String>::new());
}

#[test]
fn space_on_the_home_row_reached_by_tab_goes_home() {
    let mut world = World::start(Daemon::scripted().state(two_desktops_with_agents()));
    world.open_desktop();
    let on_row = |world: &World| {
        world.screen().focused().and_then(|n| n.key).as_deref() == Some("sidebar.home")
    };
    for _ in 0..40 {
        if on_row(&world) {
            break;
        }
        world.gesture(|app| app.press("tab"));
    }
    assert!(on_row(&world), "Tab never reached the row");
    world.gesture(|app| app.type_text(" "));

    let row = world.screen().get(Target::key("sidebar.home"));
    assert_eq!(row.value.as_deref(), Some("current"));
}

/// Whether the focused control shows whole, and the focus ring drawn around it or around the box
/// it sits in, in the dark appearance, is cut by nothing.
fn focused_whole(world: &World) -> Result<String, String> {
    let runtime = world.runtime();
    let all = runtime.elements();
    let focused = all.iter().find(|e| e.focused).ok_or("nothing has focus")?;
    let name = focused.key.clone().unwrap_or_else(|| focused.path.clone());
    if (0..4).any(|i| (focused.visible[i] - focused.rect[i]).abs() > 0.5) {
        return Err(format!(
            "{name} shows {:?} of {:?}",
            focused.visible, focused.rect
        ));
    }
    let t = attn_ui::colors::scheme_for(None).colors(true).ui;
    let warm = [t.warning.r, t.warning.g, t.warning.b].map(rune::theme::srgb_encode);
    let to_pixels = runtime.zoom() * runtime.window_scale();
    let [x, y, w, h] = focused.rect.map(|v| v * to_pixels);
    let ring = runtime.instances.iter().find(|i| {
        let [ix, iy, iw, ih] = i.rect;
        ix <= x + 0.5
            && iy <= y + 0.5
            && ix + iw >= x + w - 0.5
            && iy + ih >= y + h - 0.5
            && i.params[0] > 0.0
            && (0..3).all(|c| (i.border[c] - warm[c]).abs() < 0.01)
    });
    if let Some(ring) = ring {
        let [cx, cy, cr, cb] = ring.clip;
        let [rx, ry, rw, rh] = ring.rect;
        if !(rx >= cx - 0.5 && ry >= cy - 0.5 && rx + rw <= cr + 0.5 && ry + rh <= cb + 0.5) {
            return Err(format!(
                "{name}'s ring {:?} is cut by {:?}",
                ring.rect.map(|v| v / to_pixels),
                ring.clip.map(|v| v / to_pixels)
            ));
        }
    }
    Ok(name)
}

fn focused_key(world: &World) -> Option<String> {
    world
        .runtime()
        .elements()
        .into_iter()
        .find(|e| e.focused)
        .map(|e| e.key.unwrap_or(e.path))
}

/// Whether the focused control sits in a list, tree or grid whose rows the arrows walk.
fn in_rows(world: &World) -> bool {
    let all = world.runtime().elements();
    let Some(focused) = all.iter().find(|e| e.focused) else {
        return false;
    };
    all.iter().any(|e| {
        matches!(e.role, "list" | "listbox" | "tree" | "grid")
            && focused.path.len() > e.path.len()
            && focused.path.starts_with(&e.path)
            && focused.path.as_bytes()[e.path.len()] == b'/'
    })
}

fn focus_inside(world: &World, scope: &str) -> bool {
    focused_key(world).is_some_and(|key| key.starts_with(scope))
}

/// Tabs once round from where focus is, passing over stops whose keys do not start with
/// `scope`, and at each stop in a list also presses End: every
/// control reached shows whole, with its ring cut by nothing. End that leaves focus where it was
/// scrolls the list, as a page's End does, and is not a stop. The controls reached, by key.
pub(super) fn tab_round_shows_whole(world: &mut World, scope: &str) -> Result<Vec<String>, String> {
    let mut reached: Vec<String> = Vec::new();
    for _ in 0..200 {
        world.gesture(|app| app.press("tab"));
        if world.runtime().elements().iter().all(|e| !e.focused) {
            continue;
        }
        if !focus_inside(world, scope) {
            continue;
        }
        let stop = focused_whole(world).map_err(|e| format!("{e} after {reached:?}"))?;
        if reached.first() == Some(&stop) {
            return Ok(reached);
        }
        reached.push(stop);
        if in_rows(world) {
            world.gesture(|app| app.press("end"));
            let last = focused_whole(world);
            match last {
                Ok(last) => reached.push(last),
                Err(_) if focused_key(world).as_ref() == reached.last() => {}
                Err(why) => return Err(why),
            }
        }
    }
    Ok(reached)
}
