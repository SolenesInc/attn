use rune::{El, el, row};

use super::{
    IconButton, IconSize, Radius, Said, Size, Space, Type, disabled, door, focus, layers, words,
};
use crate::Tokens;

/// The heading of a group in a list, such as a desktop's in the sidebar or a section of the
/// queue: a mark, the group's name, a hairline to the end, and the keys that go to it. Pressing
/// it goes to the group, or opens and closes it, unless it is only a heading.
pub struct GroupHead {
    key: String,
    label: String,
    mark: Option<El>,
    name: Option<Name>,
    asides: Vec<El>,
    keys: Option<String>,
    trailing: Vec<El>,
    controls: Vec<IconButton>,
    open: Option<bool>,
    heading: bool,
    disabled: bool,
}

enum Name {
    Small(Said),
    Section(Said),
    Built(El),
}

impl GroupHead {
    pub fn new(key: impl Into<String>, label: impl Into<String>) -> GroupHead {
        GroupHead {
            key: key.into(),
            label: label.into(),
            mark: None,
            name: None,
            asides: Vec::new(),
            keys: None,
            trailing: Vec::new(),
            controls: Vec::new(),
            open: None,
            heading: false,
            disabled: false,
        }
    }

    /// How tall every group heading stands.
    pub fn height() -> f32 {
        Size::Compact.height()
    }

    pub fn mark(mut self, mark: El) -> Self {
        self.mark = Some(mark);
        self
    }

    /// The group's own name, such as a desktop's.
    pub fn name(mut self, name: impl Into<Said>) -> Self {
        self.name = Some(Name::Small(name.into()));
        self
    }

    /// The name of a kind of thing the group holds, such as Crew or Automations.
    pub fn section(mut self, name: impl Into<Said>) -> Self {
        self.name = Some(Name::Section(name.into()));
        self
    }

    /// A name built by its feature, such as a count in the warning colour.
    pub fn name_el(mut self, name: El) -> Self {
        self.name = Some(Name::Built(name));
        self
    }

    /// Something said after the name, such as how many the group holds.
    pub fn aside(mut self, aside: El) -> Self {
        self.asides.push(aside);
        self
    }

    pub fn keys(mut self, keys: Option<String>) -> Self {
        self.keys = keys;
        self
    }

    /// A control that always shows at the heading's end, such as Manage.
    pub fn trailing(mut self, control: El) -> Self {
        self.trailing.push(control);
        self
    }

    /// Controls that show at the heading's end while the pointer or the keyboard is on it, on
    /// its own ground, what they lie over ending in a fade before them. A heading has these or
    /// [`Self::trailing`] controls, not both.
    pub fn controls(mut self, controls: impl IntoIterator<Item = IconButton>) -> Self {
        self.controls.extend(controls);
        self
    }

    /// The heading opens and closes its group, led by a caret that says which it is.
    pub fn open(mut self, open: bool) -> Self {
        self.open = Some(open);
        self
    }

    /// The heading only names its group and takes no press.
    pub fn heading(mut self) -> Self {
        self.heading = true;
        self
    }

    pub fn disabled(mut self, disabled: bool) -> Self {
        self.disabled = disabled;
        self
    }

    pub fn build(self, t: &Tokens) -> El {
        debug_assert!(self.controls.is_empty() || self.trailing.is_empty());
        let keys_key = format!("{}.keys", self.key);
        let reach = self.controls.iter().map(IconButton::width).sum::<f32>()
            + Space::S2.pt() * self.controls.len().saturating_sub(1) as f32
            + Space::S4.pt();
        let hovering = !self.controls.is_empty();
        let controls = hovering.then(|| {
            let controls = door::gap(row(), Space::S2)
                .key(format!("{}.controls", self.key))
                .group_opacity(0.0, 1.0)
                .pin(None, Some(0.0), Some(0.0), Some(0.0))
                .items_center()
                .children(self.controls.into_iter().map(|b| El::from(b.build(t))));
            door::sides(controls, Space::S0, Space::S4)
        });
        let caret = self.open.map(|open| {
            let glyph = if open {
                crate::glyph::CARET_DOWN
            } else {
                crate::glyph::CARET_RIGHT
            };
            super::glyph(glyph, IconSize::Small, t.text_tertiary)
        });
        let name = self.name.map(|name| match name {
            Name::Small(name) => words(name, Type::Small, t.text_muted),
            Name::Section(name) => words(name, Type::Label, t.text_tertiary),
            Name::Built(name) => name,
        });
        let content = row()
            .grow(1.0)
            .min_w(0.0)
            .h_full()
            .items_center()
            .children(caret)
            .children(self.mark)
            .children(name.map(|name| name.single_line().truncate().shrink()))
            .children(self.asides)
            .child(el().grow(1.0).h(1.0).fill(t.border_subtle))
            .children(self.keys.map(|keys| {
                words(keys, Type::Mono, t.text_dimmed)
                    .key(keys_key)
                    .single_line()
                    .truncate()
                    .shrink()
            }));
        let content = door::gap(content, Space::S6);
        let content = if hovering {
            door::fade_before_hover(content, Space::S16, reach)
        } else {
            content
        };
        let head = row()
            .key(self.key)
            .label(self.label)
            .w_full()
            .min_w(0.0)
            .h(Self::height())
            .items_center()
            .child(content)
            .children(self.trailing)
            .hover_group()
            .children(controls);
        let head = door::gap(door::inset(head, Space::S4, Space::S0), Space::S6);
        let head = door::corner(head, Radius::Tag);
        if self.heading {
            return head.role("heading");
        }
        let head = head.role("button").focusable(!self.disabled);
        let head = match self.open {
            Some(open) => head.expanded(open),
            None => head,
        };
        let head = focus::ring_on_edge(layers::pressable(head, t.bg_panel, t), t);
        if self.disabled {
            disabled::dim(head.disabled(true))
        } else {
            head
        }
    }
}
