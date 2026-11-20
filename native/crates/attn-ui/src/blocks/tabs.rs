use rune::{El, Tx, row};

use crate::Tokens;
use crate::system::{Radius, Space, Type, door, layers, words};

/// The widest a tab stands before its name gives way.
const TAB_MEASURE: f32 = 220.0;
/// How much of the brand tints the tab shown, and its edge.
const SHOWN: f32 = 0.12;
const SHOWN_EDGE: f32 = 0.38;

/// A strip of tabs across the top of a panel, one for each thing it can show, the shown one
/// tinted with the brand; a strip too narrow for them scrolls.
pub struct Tabs {
    key: String,
    label: String,
    tabs: Vec<El>,
}

impl Tabs {
    pub fn new(key: impl Into<String>, label: impl Into<String>) -> Tabs {
        Tabs {
            key: key.into(),
            label: label.into(),
            tabs: Vec::new(),
        }
    }

    /// A tab keyed `key` named `name`, `shown` or not, that `press` shows.
    pub fn tab<S: 'static>(
        mut self,
        key: impl Into<String>,
        name: impl Into<String>,
        shown: bool,
        t: &Tokens,
        press: impl Fn(&mut S, &mut Tx) + 'static,
    ) -> Self {
        let name = name.into();
        let (ink, fill, edge) = if shown {
            (
                t.text_primary,
                t.brand.alpha(SHOWN),
                t.brand.alpha(SHOWN_EDGE),
            )
        } else {
            (
                t.text_secondary,
                rune::Color::TRANSPARENT,
                rune::Color::TRANSPARENT,
            )
        };
        let tab = words(name.clone(), Type::Control, ink)
            .key(key.into())
            .role("tab")
            .label(name)
            .checked(shown)
            .single_line()
            .truncate()
            .max_w(TAB_MEASURE)
            .fill(fill)
            .border(1.0, edge)
            .hover(if shown {
                fill
            } else {
                layers::hover_layer(t.bg_elevated, t)
            })
            .on_press_tx(press);
        self.tabs.push(door::corner(
            door::inset(tab, Space::S8, Space::S4),
            Radius::Control,
        ));
        self
    }

    pub fn build(self, t: &Tokens) -> El {
        let strip = row()
            .key(self.key)
            .role("tablist")
            .label(self.label)
            .w_full()
            .items_center()
            .fill(t.bg_elevated)
            .border_sides([0.0, 0.0, 1.0, 0.0], t.border)
            .scroll_x()
            .children(self.tabs);
        door::gap(door::inset(strip, Space::S12, Space::S6), Space::S4)
    }
}
