use rune::{Chord, GesturePhase, Key, KeyEvent, Mods, PhysicalInput, Runtime, Tx, WheelEvent};
use serde::Deserialize;
use serde_json::{Value, json};

use crate::target::{Found, Target};

/// What the app adds to the protocol: whether it is ready to be driven, its actions by id,
/// and named slices of its state.
pub trait Hooks<S>: Send + Sync + 'static {
    fn ready(&self, state: &S) -> bool;
    fn run(&self, state: &mut S, name: &str, tx: &mut Tx) -> Result<(), String>;
    /// A named slice, or `None` while it is not there yet, such as a terminal the window has
    /// not drawn.
    fn state(&self, state: &S, of: &str) -> Result<Option<Value>, String>;
}

/// One request, as the Tauri app's automation server reads it, so one Node client drives
/// both apps.
#[derive(Clone, Debug, Deserialize)]
pub struct Request {
    #[serde(default)]
    pub id: Value,
    #[serde(default)]
    pub token: String,
    pub action: String,
    #[serde(default)]
    pub payload: Value,
}

/// One attempt at a request on the runtime.
#[derive(Debug)]
pub enum Attempt {
    Done(Value),
    /// The request waits for the screen: its element is missing, still moving, or scrolling
    /// into view. The text says what it waits for.
    Wait(String),
    Fail(String),
}

#[derive(Deserialize)]
struct Aimed {
    target: Target,
    /// Where in the element, as fractions of its box from the top left; the center by default.
    #[serde(default = "center")]
    at: (f32, f32),
}

fn center() -> (f32, f32) {
    (0.5, 0.5)
}

#[derive(Deserialize)]
struct Drag {
    #[serde(flatten)]
    from: Aimed,
    #[serde(default)]
    to: Option<Target>,
    /// Points from the start, when `to` is not given.
    #[serde(default)]
    by: Option<(f32, f32)>,
}

#[derive(Deserialize)]
struct Scroll {
    #[serde(flatten)]
    over: Aimed,
    dy: f32,
}

#[derive(Deserialize)]
struct Chorded {
    chord: String,
}

#[derive(Deserialize)]
struct Held {
    mods: String,
}

#[derive(Deserialize)]
struct Typed {
    text: String,
    #[serde(default)]
    target: Option<Target>,
}

#[derive(Deserialize)]
struct Named {
    name: String,
}

#[derive(Deserialize)]
struct Of {
    of: String,
    /// Waits until a string in the slice contains this text.
    #[serde(default)]
    includes: Option<String>,
}

#[derive(Deserialize)]
struct Waited {
    target: Target,
    #[serde(default)]
    gone: bool,
}

fn parse<T: for<'de> Deserialize<'de>>(action: &str, payload: &Value) -> Result<T, Attempt> {
    serde_json::from_value(payload.clone())
        .map_err(|e| Attempt::Fail(format!("{action}: bad payload {payload}: {e}")))
}

macro_rules! args {
    ($action:expr, $payload:expr) => {
        match parse($action, $payload) {
            Ok(args) => args,
            Err(fail) => return fail,
        }
    };
}

/// Runs one attempt at `action` on the runtime. Input it queues is handled by the host before
/// the next inspection; a pointer action's receipt says which element the press landed on.
pub fn act<S: 'static>(
    rt: &mut Runtime<S>,
    hooks: &dyn Hooks<S>,
    action: &str,
    payload: &Value,
) -> Attempt {
    match action {
        "ping" => Attempt::Done(json!({"pong": true, "frontendReady": hooks.ready(&rt.state)})),
        "tree" => Attempt::Done(rt.accessibility_json()),
        "find" => {
            let Aimed { target, .. } = args!(action, payload);
            Attempt::Done(serde_json::to_value(target.find(rt)).unwrap_or(Value::Null))
        }
        "wait" => {
            let Waited { target, gone } = args!(action, payload);
            match (target.find(rt), gone) {
                (None, true) => Attempt::Done(Value::Null),
                (Some(_), true) => Attempt::Wait(format!("{target:?} to go")),
                (Some(found), false) if !found.moving => Attempt::Done(json!(found)),
                (_, false) => Attempt::Wait(format!("{target:?} to appear and stop moving")),
            }
        }
        "press" => {
            let aimed: Aimed = args!(action, payload);
            pointer(rt, &aimed, |rt, (x, y)| {
                rt.pointer_move(x, y);
                rt.pointer_down(x, y);
                rt.pointer_up(x, y);
            })
        }
        "hover" => {
            let aimed: Aimed = args!(action, payload);
            pointer(rt, &aimed, |rt, (x, y)| rt.pointer_move(x, y))
        }
        "drag" => {
            let drag: Drag = args!(action, payload);
            let end = match (&drag.to, drag.by) {
                (Some(to), _) => match to
                    .find(rt)
                    .filter(|f| !f.moving)
                    .and_then(|f| f.point(center()))
                {
                    Some(end) => Some(end),
                    None => return Attempt::Wait(format!("{to:?} to show and stop moving")),
                },
                (None, Some(_)) => None,
                (None, None) => return Attempt::Fail("drag: give `to` or `by`".into()),
            };
            pointer(rt, &drag.from, |rt, (x, y)| {
                let (ex, ey) = end.unwrap_or_else(|| {
                    let (dx, dy) = drag.by.unwrap_or_default();
                    (x + dx, y + dy)
                });
                rt.pointer_move(x, y);
                rt.pointer_down(x, y);
                rt.pointer_move((x + ex) / 2.0, (y + ey) / 2.0);
                rt.pointer_move(ex, ey);
                rt.pointer_up(ex, ey);
            })
        }
        "scroll" => {
            let scroll: Scroll = args!(action, payload);
            pointer(rt, &scroll.over, |rt, (x, y)| {
                rt.wheel_event(
                    WheelEvent::new(0.0, -scroll.dy)
                        .at(x, y)
                        .phase(GesturePhase::Ended),
                );
            })
        }
        "key" => {
            let Chorded { chord } = args!(action, payload);
            match Chord::parse(&chord)
                .map_err(|e| e.to_string())
                .and_then(|c| crate::key_event(&c))
            {
                Ok(ev) => {
                    rt.key(ev);
                    Attempt::Done(json!({"handled": true}))
                }
                Err(why) => Attempt::Fail(format!("key: {chord:?}: {why}")),
            }
        }
        "hold" => {
            let Held { mods } = args!(action, payload);
            match crate::chord::held_mods(&mods) {
                Ok(held) => {
                    rt.physical_input(PhysicalInput::Modifiers(held));
                    Attempt::Done(json!({"held": mods}))
                }
                Err(why) => Attempt::Fail(format!("hold: {mods:?}: {why}")),
            }
        }
        "type" => {
            let Typed { text, target } = args!(action, payload);
            if let Some(target) = target {
                let Some(found) = target.find(rt) else {
                    return Attempt::Wait(format!("{target:?} to appear"));
                };
                if !rt.elements()[found.index].focused {
                    let aimed = Aimed {
                        target,
                        at: center(),
                    };
                    return match pointer(rt, &aimed, |rt, (x, y)| {
                        rt.pointer_down(x, y);
                        rt.pointer_up(x, y);
                    }) {
                        Attempt::Done(_) => Attempt::Wait("the field to take focus".into()),
                        other => other,
                    };
                }
            }
            for c in text.chars() {
                rt.key(KeyEvent {
                    key: Key::Text(c.to_string()),
                    mods: Mods::default(),
                });
            }
            Attempt::Done(json!({"handled": true}))
        }
        "run" => {
            let Named { name } = args!(action, payload);
            let mut result = Ok(());
            rt.update(|state, tx| result = hooks.run(state, &name, tx));
            match result {
                Ok(()) => Attempt::Done(json!({"handled": true})),
                Err(why) => Attempt::Fail(format!("run {name:?}: {why}")),
            }
        }
        "state" => {
            let Of { of, includes } = args!(action, payload);
            match (hooks.state(&rt.state, &of), includes) {
                (Err(why), _) => Attempt::Fail(format!("state {of:?}: {why}")),
                (Ok(None), Some(_)) => Attempt::Wait(format!("state {of:?} to be there")),
                (Ok(None), None) => Attempt::Fail(format!("state {of:?} is not there yet")),
                (Ok(Some(value)), Some(text)) if !holds(&value, &text) => {
                    Attempt::Wait(format!("state {of:?} to include {text:?}"))
                }
                (Ok(Some(value)), _) => Attempt::Done(value),
            }
        }
        other => Attempt::Fail(format!(
            "unknown action {other:?}; actions: ping, tree, find, wait, press, hover, drag, scroll, key, hold, type, run, state, native_windows, shot"
        )),
    }
}

/// Whether a string anywhere in `value` contains `text`.
fn holds(value: &Value, text: &str) -> bool {
    match value {
        Value::String(s) => s.contains(text),
        Value::Array(items) => items.iter().any(|v| holds(v, text)),
        Value::Object(fields) => fields.values().any(|v| holds(v, text)),
        _ => false,
    }
}

/// Aims at the element once it shows and stops moving, scrolling it into view first when
/// its ancestors clip it out, then sends `input` there and receipts where it landed.
fn pointer<S: 'static>(
    rt: &mut Runtime<S>,
    aimed: &Aimed,
    input: impl FnOnce(&mut Runtime<S>, (f32, f32)),
) -> Attempt {
    let Some(found) = aimed.target.find(rt) else {
        return Attempt::Wait(format!("{:?} to appear", aimed.target));
    };
    if found.moving {
        return Attempt::Wait(format!("{:?} to stop moving", aimed.target));
    }
    let Some(point) = found.point(aimed.at) else {
        return scroll_into_view(rt, &found);
    };
    let (landed, matched) = Target::landed(rt, &found, point);
    input(rt, point);
    Attempt::Done(json!({
        "handled": true,
        "matched": matched,
        "landed": landed,
        "target": found,
    }))
}

/// Wheels the nearest ancestor that shows anything by how far the element sits outside it.
fn scroll_into_view<S: 'static>(rt: &mut Runtime<S>, found: &Found) -> Attempt {
    let elements = rt.elements();
    let mut parent = elements[found.index].parent;
    while let Some(p) = parent {
        let [x, y, w, h] = elements[p].visible;
        if w > 0.0 && h > 0.0 {
            let [_, ty, _, th] = found.rect;
            let dy = if ty < y { y - ty } else { (y + h) - (ty + th) };
            rt.wheel(x + w / 2.0, y + h / 2.0, dy);
            return Attempt::Wait("the element to scroll into view".into());
        }
        parent = elements[p].parent;
    }
    Attempt::Fail(format!(
        "{:?} is laid out but nothing of it shows",
        found.key
    ))
}
