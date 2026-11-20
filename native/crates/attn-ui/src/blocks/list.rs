use rune::{El, KeyEvent, Tx, col, row};
use rune_kit::ScrollBar;

use super::block::{Block, Kind, inner_gap, spaced};
use crate::Tokens;
use crate::system::{ListRow, Radius, Said, Size, Space, Type, door, words};

/// One thing a list holds: a row as built, or a row with what sits under it, which the row's
/// item holds too.
enum Item {
    Built(El),
    Holding(Box<ListRow>, Option<String>, Vec<Block>),
}

/// A list of things, one [`crate::system::ListRow`] each with what it says under it, set apart
/// by rules, bare on its surface or on a card; or what to say when there are none. Bare, a row's
/// words start on the column's line and its box reaches out past it by the row's own inset.
pub struct List {
    key: String,
    label: Said,
    card: bool,
    heading: bool,
    count: Option<usize>,
    chooser: Option<Option<String>>,
    roving: bool,
    follows: bool,
    listbox: bool,
    failed: bool,
    keys: Option<Box<dyn FnOnce(El) -> El>>,
    find: Option<El>,
    led: bool,
    region: Option<(String, &'static str, String)>,
    items: Vec<Item>,
    empty: Option<(String, Said, Option<El>)>,
}

impl List {
    /// `key` names the list, which assistive tools read as `label`.
    pub fn new(key: impl Into<String>, label: impl Into<Said>) -> List {
        List {
            key: key.into(),
            label: label.into(),
            card: false,
            heading: false,
            count: None,
            chooser: None,
            roving: false,
            follows: false,
            listbox: false,
            failed: false,
            keys: None,
            find: None,
            led: false,
            region: None,
            items: Vec::new(),
            empty: None,
        }
    }

    /// The list lies on a card of its own, apart from the surface around it.
    pub fn card(mut self) -> Self {
        self.card = true;
        self
    }

    /// Its label stands above the rows as their heading.
    pub fn heading(mut self) -> Self {
        self.heading = true;
        self
    }

    /// The list holds the keyboard and its rows are options, the one keyed `chosen` read as
    /// the active one while the keyboard stays on the list.
    pub fn chooser(mut self, chosen: Option<String>) -> Self {
        self.chooser = Some(chosen);
        self
    }

    /// Its rows are options of a list box that the keyboard does not stop on, as when the panel
    /// around it has its own keys for moving between them.
    pub fn listbox(mut self) -> Self {
        self.listbox = true;
        self
    }

    /// The list is one stop of Tab, and the arrows move between its rows, each of which
    /// Return presses.
    pub fn roving(mut self) -> Self {
        self.roving = true;
        self
    }

    /// On a roving list, the arrows, Home and End choose the row they reach, as a list box whose
    /// selection follows its focus does.
    pub fn follows(mut self) -> Self {
        self.follows = true;
        self
    }

    /// The list is one stop of Tab and answers the keys itself, such as Up and Down choosing
    /// the next row.
    pub fn on_key<S: 'static>(
        mut self,
        keys: impl Fn(&mut S, &KeyEvent, &mut Tx) -> bool + 'static,
    ) -> Self {
        self.keys = Some(Box::new(move |list: El| list.focusable(true).on_key(keys)));
        self
    }

    /// The column that scrolls the list is itself keyed `key` and read as a `role` named
    /// `label`, such as the navigation landmark a file tree is, so what finds it can scroll it.
    pub fn scrolls_as(
        mut self,
        key: impl Into<String>,
        role: &'static str,
        label: impl Into<String>,
    ) -> Self {
        self.region = Some((key.into(), role, label.into()));
        self
    }

    /// Its rows lead with a mark, such as a folder's chevron, so what it says when it has none
    /// starts where their words do.
    pub fn led(mut self) -> Self {
        self.led = true;
        self
    }

    /// A field above the rows that narrows them, where there is one: on the rows' line, staying
    /// while they scroll.
    pub fn find(mut self, field: Option<El>) -> Self {
        self.find = field;
        self
    }

    /// The list could not be read: what stands in its place says why, as an alert.
    pub fn failed(mut self, failed: bool) -> Self {
        self.failed = failed;
        self
    }

    /// How many things the list holds, after its heading.
    pub fn count(mut self, count: usize) -> Self {
        self.count = Some(count);
        self
    }

    pub fn row(mut self, row: impl Into<El>) -> Self {
        self.items.push(Item::Built(row.into()));
        self
    }

    pub fn rows(mut self, rows: impl IntoIterator<Item = El>) -> Self {
        self.items.extend(rows.into_iter().map(Item::Built));
        self
    }

    /// A row with blocks under it that belong to it, such as what went wrong with it, starting
    /// where its words start. The row's list item holds them, so assistive tools find each
    /// control under the row inside the item the row names.
    pub fn row_with(mut self, row: ListRow, under: Vec<Block>) -> Self {
        self.items.push(Item::Holding(Box::new(row), None, under));
        self
    }

    /// [`List::row_with`], with what is under the row keyed `key`, as one part a reader finds.
    pub fn row_with_keyed(
        mut self,
        row: ListRow,
        key: impl Into<String>,
        under: Vec<Block>,
    ) -> Self {
        self.items
            .push(Item::Holding(Box::new(row), Some(key.into()), under));
        self
    }

    /// What stands in the list's place while it has no rows, keyed `key`: what is missing, and
    /// the button that adds the first one.
    pub fn empty(
        mut self,
        key: impl Into<String>,
        said: impl Into<Said>,
        action: Option<El>,
    ) -> Self {
        self.empty = Some((key.into(), said.into(), action));
        self
    }

    pub fn build(self, t: &Tokens) -> Block {
        let (heading, list, semantics) = self.laid(t);
        let list = semantics.onto(list);
        let list = match heading {
            Some(heading) => door::gap(col().w_full(), Space::S8)
                .child(heading)
                .child(list),
            None => list,
        };
        Block::new(Kind::List, list)
    }

    /// The list as a column of its own that scrolls, filling the height it is given, as beside
    /// an inspector. Its key, role and name sit on the node that scrolls, with its heading and
    /// find field above it, staying while the rows scroll.
    pub fn scrolling(mut self, t: &Tokens) -> El {
        let find = self
            .find
            .take()
            .map(|find| door::frame(find, Space::S8, Space::S16, Space::S0));
        let region = self.region.take();
        let (heading, content, semantics) = self.laid(t);
        let heading = heading.map(|heading| door::frame(heading, Space::S8, Space::S16, Space::S0));
        let (content, rows) = match region {
            Some((key, role, label)) => (
                semantics.onto(content),
                col().key(key).role(role).label(label),
            ),
            None => (content, semantics.onto(col())),
        };
        let rows = rows
            .w_full()
            .min_w(0.0)
            .scroll(rune::ScrollSpec {
                stick_bottom: false,
            })
            .child(door::frame(content, Space::S8, Space::S16, Space::S8))
            .scroll_bar_styled(crate::scroll_bar(t));
        if find.is_none() && heading.is_none() {
            return rows.h_full();
        }
        col()
            .w_full()
            .h_full()
            .min_w(0.0)
            .min_h(0.0)
            .children(find)
            .children(heading)
            .child(rows.shrink().min_h(0.0))
    }

    /// The list as a column of its own as tall as its rows, up to `tallest`, past which it
    /// scrolls, as above what the rows choose between, in a band that already keeps the
    /// sheet's sides. Its key, role and name sit on the node that scrolls.
    pub fn scrolling_up_to(self, tallest: f32, t: &Tokens) -> El {
        let (heading, content, semantics) = self.laid(t);
        let rows = semantics
            .onto(col())
            .w_full()
            .max_h(tallest)
            .min_w(0.0)
            .scroll(rune::ScrollSpec {
                stick_bottom: false,
            })
            .child(door::frame(content, Space::S8, Space::S0, Space::S8))
            .scroll_bar_styled(crate::scroll_bar(t));
        match heading {
            Some(heading) => col().w_full().min_w(0.0).child(heading).child(rows),
            None => rows,
        }
    }

    /// What the list draws, and what it is to a reader and the keyboard, apart: a list that
    /// scrolls puts the second on the node that scrolls.
    fn laid(self, t: &Tokens) -> (Option<El>, El, Semantics) {
        let empty = match self.empty {
            Some((key, said, action)) if self.items.is_empty() => {
                let (role, ink) = if self.failed {
                    ("alert", t.error)
                } else {
                    ("note", t.text_tertiary)
                };
                let said = door::gap(col().w_full().items_start(), Space::S12)
                    .key(key)
                    .role(role)
                    .label(said.text())
                    .child(words(said, Type::Body, ink).wrap().a11y_hidden())
                    .children(action);
                Some(if self.led {
                    door::inset_start(col().w_full().child(said), ListRow::lead_room())
                } else {
                    said
                })
            }
            _ => None,
        };
        let last = self.items.len().saturating_sub(1);
        let hangs = !self.card;
        let items = self.items.into_iter().enumerate().map(|(at, item)| {
            let item = match item {
                Item::Built(row) => col().child(row),
                Item::Holding(row, key, under) => {
                    let under = (!under.is_empty()).then(|| {
                        let under = door::frame(
                            spaced(under, inner_gap, None),
                            Space::S4,
                            Space::S8,
                            Space::S12,
                        );
                        match key {
                            Some(key) => under.key(key),
                            None => under,
                        }
                    });
                    match under {
                        Some(under) => row.build_holding(under, t),
                        None => col().child(row.build(t)),
                    }
                }
            };
            let item = if hangs {
                door::hang(item, Size::Compact.inset())
            } else {
                item.w_full()
            };
            if at < last {
                item.border_sides([0.0, 0.0, 1.0, 0.0], t.border_subtle)
            } else {
                item
            }
        });
        let count_key = format!("{}.count", self.key);
        let heading = self.heading.then(|| {
            let line = door::gap(row().w_full().items_center(), Space::S8)
                .child(
                    words(self.label.clone(), Type::Label, t.text_tertiary)
                        .a11y_hidden()
                        .grow(1.0),
                )
                .children(self.count.map(|n| {
                    words(n.to_string(), Type::Label, t.text_tertiary).key(count_key.clone())
                }));
            if hangs {
                line
            } else {
                door::sides(line, Size::Compact.inset(), Size::Compact.inset())
            }
        });
        let semantics = Semantics {
            key: self.key,
            role: if self.chooser.is_some() || self.listbox {
                "listbox"
            } else {
                "list"
            },
            label: self.label.text(),
            roving: self.roving,
            follows: self.follows,
            keys: self.keys,
            chooser: self.chooser,
        };
        let list = col().w_full().children(items).children(empty);
        let list = if self.card {
            door::corner(
                door::inset(list, Space::S4, Space::S4).border(1.0, t.border_subtle),
                Radius::Card,
            )
        } else {
            list
        };
        (heading, list, semantics)
    }
}

/// What a list is to a reader and the keyboard: its key, role and name, and how it takes the
/// keyboard, all on one node.
struct Semantics {
    key: String,
    role: &'static str,
    label: String,
    roving: bool,
    follows: bool,
    keys: Option<Box<dyn FnOnce(El) -> El>>,
    chooser: Option<Option<String>>,
}

impl Semantics {
    fn onto(self, node: El) -> El {
        let node = node.key(self.key).role(self.role).label(self.label);
        let node = match (self.roving, self.follows) {
            (true, true) => node.focus_group().focus_follows(),
            (true, false) => node.focus_group(),
            (false, _) => node,
        };
        let node = match self.keys {
            Some(keys) => keys(node),
            None => node,
        };
        match self.chooser {
            Some(Some(chosen)) => node.focusable(true).active_descendant(chosen),
            Some(None) => node.focusable(true),
            None => node,
        }
    }
}
