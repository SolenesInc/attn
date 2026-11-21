use rune::{El, col};

use super::actions::Actions;
use super::block::{Block, inner_gap, spaced};
use crate::system::{Space, door};
use crate::{Frame, Tokens};

/// A menu's width: a profile's name and its rename and delete buttons, or a mailbox's name,
/// its state and its keys, keep one line.
const WIDTH: f32 = 260.0;

/// A list of options opened from a [`crate::system::Trigger`] and anchored to it, such as a
/// select's values or a switcher's profiles: its rows, or a small form in their place, and a
/// line of actions under a rule. Whoever opens it anchors it, holds its keys and says what
/// closes it.
pub struct Menu {
    key: String,
    label: String,
    role: &'static str,
    rows: Vec<El>,
    form: Vec<Block>,
    footer: Option<Actions>,
    active: Option<String>,
    focusable: bool,
}

impl Menu {
    /// A menu keyed `key`, read as `label`.
    pub fn new(key: impl Into<String>, label: impl Into<String>) -> Menu {
        Menu {
            key: key.into(),
            label: label.into(),
            role: "menu",
            rows: Vec::new(),
            form: Vec::new(),
            footer: None,
            active: None,
            focusable: true,
        }
    }

    /// Its rows are the values of a list box rather than the commands of a menu.
    pub fn listbox(mut self) -> Self {
        self.role = "listbox";
        self
    }

    /// The keyboard stays in what opened it, such as a field that names the row it is on, so the
    /// menu takes no focus of its own, even when an assistive client asks it to, and a press on
    /// a row reaches the gestures of the layer under it.
    pub fn takes_no_focus(mut self) -> Self {
        self.focusable = false;
        self
    }

    /// A row, such as a [`crate::system::ListRow`] with its role.
    pub fn row(mut self, row: impl Into<El>) -> Self {
        self.rows.push(row.into());
        self
    }

    pub fn rows(mut self, rows: impl IntoIterator<Item = El>) -> Self {
        self.rows.extend(rows);
        self
    }

    /// Blocks after the rows, or in their place, such as a name to type, keyed `{key}.form`.
    pub fn form(mut self, blocks: impl IntoIterator<Item = Block>) -> Self {
        self.form.extend(blocks);
        self
    }

    pub fn footer(mut self, actions: Actions) -> Self {
        self.footer = Some(actions);
        self
    }

    /// The row the keyboard is on while focus stays on the menu, keyed `key`.
    pub fn active(mut self, key: Option<String>) -> Self {
        self.active = key;
        self
    }

    pub fn build(self, t: &Tokens) -> El {
        let form_key = format!("{}.form", self.key);
        let form = (!self.form.is_empty()).then(|| {
            door::inset(
                spaced(self.form, inner_gap, None).key(form_key),
                Space::S8,
                Space::S8,
            )
        });
        let footer = self.footer.map(|actions| {
            door::rule_above(
                door::frame(
                    col().w_full().child(actions.build().el),
                    Space::S4,
                    Space::S0,
                    Space::S0,
                ),
                t.border_subtle,
            )
        });
        let mut menu = door::gap(
            col()
                .key(self.key)
                .role(self.role)
                .label(self.label)
                .w(WIDTH)
                .focusable(self.focusable)
                .no_focus_ring(),
            Space::S2,
        )
        .children(self.rows)
        .children(form)
        .children(footer);
        if let Some(active) = self.active {
            menu = menu.active_descendant(active);
        }
        Frame::menu(t).draw(door::inset(menu, Space::S4, Space::S4))
    }
}
