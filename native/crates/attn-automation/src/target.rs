use std::collections::HashMap;

use rune::{AccessibleElement, ElementInfo, Runtime};
use serde::{Deserialize, Serialize};

/// An element named the way a person or an assistive tool would: by its key, or by its role
/// and accessible name. A role is the one the accessibility tree prints, as `"list-box-option"`
/// for an option; a name is its label, its text, or the name the tree prints. Of several
/// matching elements, the first that shows wins, else the first; a key with a name matches only
/// while that element carries the name.
#[derive(Clone, Debug, PartialEq, Eq, Deserialize, Serialize)]
#[serde(untagged)]
pub enum Target {
    Key {
        key: String,
        #[serde(default, skip_serializing_if = "Option::is_none")]
        name: Option<String>,
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
    pub role: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub name: Option<String>,
    /// x, y, width, height in the window's points.
    pub rect: [f32; 4],
    #[serde(skip)]
    pub(crate) index: usize,
    #[serde(skip)]
    pub(crate) path: String,
    /// The path of the node that draws it: its own, or for a part of a text that Rune
    /// reports as its own element, as a link, that text's.
    #[serde(skip)]
    pub(crate) drawn_by: String,
    #[serde(skip)]
    pub(crate) visible: [f32; 4],
    #[serde(skip)]
    pub(crate) moving: bool,
}

impl Target {
    fn matches(&self, e: &ElementInfo, read: Option<&AccessibleElement>) -> bool {
        let named = |name: &Option<String>| {
            name.as_ref().is_none_or(|name| {
                accessible_name(e) == Some(name)
                    || read.is_some_and(|a| a.name.as_ref() == Some(name))
            })
        };
        match self {
            Target::Key { key, name } => e.key.as_deref() == Some(key) && named(name),
            Target::Role { role, name } => read.is_some_and(|a| a.role == *role) && named(name),
        }
    }

    /// What a request that cannot find it waits for, naming the tree's role when the target
    /// gives the role the view wrote instead.
    pub(crate) fn to_appear<S>(&self, rt: &Runtime<S>) -> String {
        let Target::Role { role, name } = self else {
            return format!("{self:?} to appear");
        };
        let read: HashMap<String, AccessibleElement> = rt
            .accessible_elements()
            .into_iter()
            .map(|a| (a.path.clone(), a))
            .collect();
        let tree_role = rt.elements().into_iter().find_map(|e| {
            let a = read.get(&e.path)?;
            let named = name.as_ref().is_none_or(|name| {
                accessible_name(&e) == Some(name) || a.name.as_ref() == Some(name)
            });
            (e.role == role && a.role != *role && named).then(|| a.role.clone())
        });
        match tree_role {
            Some(tree_role) => format!(
                "{self:?} to appear; the tree's role for an element the view calls {role:?} is {tree_role:?}"
            ),
            None => format!("{self:?} to appear"),
        }
    }

    /// The live element it names, the first that shows when several do, with whether it or an
    /// ancestor still moves.
    pub fn find<S>(&self, rt: &Runtime<S>) -> Option<Found> {
        let elements = rt.elements();
        let reads_tree = matches!(
            self,
            Target::Role { .. } | Target::Key { name: Some(_), .. }
        );
        let mut read: HashMap<String, AccessibleElement> = if reads_tree {
            rt.accessible_elements()
                .into_iter()
                .map(|a| (a.path.clone(), a))
                .collect()
        } else {
            HashMap::new()
        };
        let matching: Vec<usize> = (0..elements.len())
            .filter(|&i| {
                !elements[i].exiting && self.matches(&elements[i], read.get(&elements[i].path))
            })
            .collect();
        let shows = |&&i: &&usize| {
            let [_, _, w, h] = elements[i].visible;
            w > 0.0 && h > 0.0
        };
        let index = *matching.iter().find(shows).or(matching.first())?;
        let e = &elements[index];
        let mut moving = e.moving;
        let mut parent = e.parent;
        while let Some(p) = parent {
            moving |= elements[p].moving;
            parent = elements[p].parent;
        }
        let drawn_by = e
            .parent
            .map(|p| &elements[p].path)
            .filter(|text| {
                e.path
                    .strip_prefix(text.as_str())
                    .is_some_and(|rest| rest.starts_with('#'))
            })
            .unwrap_or(&e.path)
            .clone();
        let read = read.remove(&e.path);
        Some(Found {
            key: e.key.clone(),
            role: read.as_ref().map_or(e.role.to_owned(), |a| a.role.clone()),
            name: read
                .and_then(|a| a.name)
                .or_else(|| accessible_name(e).cloned()),
            rect: e.rect,
            index,
            path: e.path.clone(),
            drawn_by,
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
        if inert_over(rt, picked.path(), &found.drawn_by) && found.drawn_at(rt, (x, y)) {
            return (
                Some(found.key.clone().unwrap_or_else(|| found.path.clone())),
                true,
            );
        }
        let inside = picked.path() == found.drawn_by
            || picked
                .path()
                .strip_prefix(found.drawn_by.as_str())
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
    /// Whether window point `(x, y)` is inside its drawn, clipped box, whatever is drawn above.
    /// A part of a text has no box of its own, and its visible part is already clipped to the
    /// text's.
    pub(crate) fn drawn_at<S>(&self, rt: &Runtime<S>, (x, y): (f32, f32)) -> bool {
        self.drawn_by != self.path || rt.contains(&self.path, x, y)
    }

    /// The point `at` (fractions of the box, from its top left) inside the part of it its
    /// ancestors' clips leave visible; none when nothing of it shows.
    pub(crate) fn point(&self, at: (f32, f32)) -> Option<(f32, f32)> {
        let [x, y, w, h] = self.visible;
        (w > 0.0 && h > 0.0).then_some((x + w * at.0, y + h * at.1))
    }
}
