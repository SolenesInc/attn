use rune::{ElementInfo, Runtime};
use serde::{Deserialize, Serialize};

/// An element named the way a person or an assistive tool would: by its key, or by its role
/// and accessible name. A role alone matches its first element.
#[derive(Clone, Debug, PartialEq, Eq, Deserialize, Serialize)]
#[serde(untagged)]
pub enum Target {
    Key {
        key: String,
    },
    Role {
        role: String,
        #[serde(default, skip_serializing_if = "Option::is_none")]
        name: Option<String>,
    },
}

/// The element a target matched on the last frame, as a pointer would aim at it.
#[derive(Clone, Debug, Serialize)]
pub struct Found {
    #[serde(skip_serializing_if = "Option::is_none")]
    pub key: Option<String>,
    pub role: &'static str,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub name: Option<String>,
    /// x, y, width, height in the window's points.
    pub rect: [f32; 4],
    #[serde(skip)]
    pub(crate) index: usize,
    #[serde(skip)]
    pub(crate) path: String,
    #[serde(skip)]
    pub(crate) visible: [f32; 4],
    #[serde(skip)]
    pub(crate) moving: bool,
}

impl Target {
    fn matches(&self, e: &ElementInfo) -> bool {
        match self {
            Target::Key { key } => e.key.as_deref() == Some(key),
            Target::Role { role, name } => {
                e.role == role
                    && name
                        .as_ref()
                        .is_none_or(|name| accessible_name(e) == Some(name))
            }
        }
    }

    /// The first live element it names, with whether it or an ancestor still moves.
    pub fn find<S>(&self, rt: &Runtime<S>) -> Option<Found> {
        let elements = rt.elements();
        let index = elements
            .iter()
            .position(|e| !e.exiting && self.matches(e))?;
        let e = &elements[index];
        let mut moving = e.moving;
        let mut parent = e.parent;
        while let Some(p) = parent {
            moving |= elements[p].moving;
            parent = elements[p].parent;
        }
        Some(Found {
            key: e.key.clone(),
            role: e.role,
            name: accessible_name(e).cloned(),
            rect: e.rect,
            index,
            path: e.path.clone(),
            visible: e.visible,
            moving,
        })
    }

    /// Which element the pointer at `(x, y)` lands on, by key or path, and whether that is
    /// the found element or inside it, as a press on a button's label presses the button. An
    /// inert layer drawn over its siblings, such as a ring over a tile or a status over the
    /// window, passes the pointer through to the found element among them.
    pub(crate) fn landed<S>(
        rt: &Runtime<S>,
        found: &Found,
        (x, y): (f32, f32),
    ) -> (Option<String>, bool) {
        let Some(picked) = rt.element_at(x, y) else {
            return (None, false);
        };
        if inert_over(rt, picked.path(), &found.path)
            && rt.contains(found.key.as_deref().unwrap_or(&found.path), x, y)
        {
            return (
                Some(found.key.clone().unwrap_or_else(|| found.path.clone())),
                true,
            );
        }
        let inside = picked.path() == found.path
            || picked
                .path()
                .strip_prefix(found.path.as_str())
                .is_some_and(|rest| rest.starts_with('/'));
        (
            Some(picked.key().unwrap_or(picked.path()).to_owned()),
            inside,
        )
    }
}

/// Whether the element the pointer at `(x, y)` lands on still moves, as a tile still sliding
/// back beside a sidebar that has already opened.
pub(crate) fn moving_at<S>(rt: &Runtime<S>, (x, y): (f32, f32)) -> bool {
    let Some(picked) = rt.element_at(x, y) else {
        return false;
    };
    rt.elements()
        .iter()
        .any(|e| e.path == picked.path() && e.moving)
}

/// Whether the element at `path` is part of an inert layer whose parent also holds `under`.
fn inert_over<S>(rt: &Runtime<S>, path: &str, under: &str) -> bool {
    let elements = rt.elements();
    let Some(mut layer) = elements.iter().position(|e| e.path == path) else {
        return false;
    };
    if !elements[layer].inert {
        return false;
    }
    while let Some(parent) = elements[layer].parent.filter(|&p| elements[p].inert) {
        layer = parent;
    }
    elements[layer].parent.is_some_and(|parent| {
        under
            .strip_prefix(elements[parent].path.as_str())
            .is_some_and(|rest| rest.starts_with('/'))
    })
}

fn accessible_name(e: &ElementInfo) -> Option<&String> {
    e.label.as_ref().or(e.text.as_ref())
}

impl Found {
    /// The point `at` (fractions of the box, from its top left) inside the part of it its
    /// ancestors' clips leave visible; none when nothing of it shows.
    pub(crate) fn point(&self, at: (f32, f32)) -> Option<(f32, f32)> {
        let [x, y, w, h] = self.visible;
        (w > 0.0 && h > 0.0).then_some((x + w * at.0, y + h * at.1))
    }
}
