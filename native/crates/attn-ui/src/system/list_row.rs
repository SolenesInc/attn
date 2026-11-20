use rune::{Color, El, Tx, col, el, row};

use super::{IconButton, Radius, Said, Size, Space, Type, door, focus, layers, words};
use crate::Tokens;

/// The column a row's lead stands in: a regular icon, the size beside `Body` text.
const LINE_GAP: Space = Space::S8;
const LEAD: f32 = 16.0;
/// The brand bar down the start of the row in view, as long as a regular icon.
const BAR: (f32, f32) = (2.0, 16.0);
/// How faint quiet controls stand on a row that is not selected.
const QUIET: f32 = 0.5;
/// How long the fade is that ends a row's words before its hover controls, and the room left
/// between the fade's end and them.
/// The narrowest a title gets before what follows it on its line gives way: "Nightly" in `Name`,
/// 46 points as the review of landing 1 measured it, so a name keeps its first word in the
/// sidebar at its narrowest.
const TITLE_FLOOR: f32 = 46.0;

/// How a row's always-shown controls show.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub enum Reveal {
    /// Whole, after its asides. A row may have these as well as controls on hover.
    #[default]
    Always,
    /// Always, after its asides, faint until the row is selected or the pointer is over it.
    Quiet,
}

/// How a row stands apart from the others in its list.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
enum Mark {
    #[default]
    None,
    /// The selected surface.
    Selected,
    /// The selected surface and the brand bar at its start: the row on screen.
    InView,
}

type Press = Box<dyn FnOnce(El) -> El>;

/// One line of a list, such as an agent, a tile, a place or a record, with an optional line
/// under it. Every row of one line is one height, every row of two lines another; its lead
/// stands in one column and its title at one inset, whatever list it is in. A row of two lines
/// sets its title in `Name` over a line in `Small`, its lead, asides and controls centred on the
/// title's line.
///
/// Its width: the inset each side, its lead, the title's floor of 46 points, its always-shown
/// controls and, while they show, its hover controls. A list narrower than their sum cuts the
/// title under its floor. The sidebar, 188 points inside at its narrowest, holds a lead and three
/// hover controls; the Automations drawer, 388 inside at its narrowest, holds a select row with
/// Run now and three. No sidebar row shows more than three: a queue row's band gives it Settle
/// and Snooze, or Ask to close, beside its actions, never all.
pub struct ListRow {
    key: String,
    label: String,
    lead: Option<El>,
    title: Option<Title>,
    cut_middle: bool,
    value: Option<String>,
    subtitle: Option<El>,
    subtitle_key: Option<String>,
    tags: Vec<El>,
    asides: Vec<El>,
    badges: Vec<El>,
    hover: Vec<IconButton>,
    inline: Vec<El>,
    quiet: bool,
    mark: Mark,
    current: bool,
    dimmed: bool,
    disabled: bool,
    cursor: bool,
    depth: u8,
    role: &'static str,
    focusable: bool,
    still: bool,
    wrap: bool,
    expanded: Option<bool>,
    checked: Option<bool>,
    description: Option<String>,
    select: Option<(String, String, Press)>,
    press: Option<Press>,
    item_value: Option<String>,
    select_role: &'static str,
    surface: Option<Color>,
    tint: Option<Color>,
}

/// How strongly a tinted row takes its tint, at rest and selected.
const TINT: (f32, f32) = (0.07, 0.12);

impl ListRow {
    /// A row keyed `key` that is one button, read as `label`.
    pub fn new(key: impl Into<String>, label: impl Into<String>) -> ListRow {
        ListRow {
            key: key.into(),
            label: label.into(),
            lead: None,
            title: None,
            cut_middle: false,
            value: None,
            subtitle: None,
            subtitle_key: None,
            tags: Vec::new(),
            asides: Vec::new(),
            badges: Vec::new(),
            hover: Vec::new(),
            inline: Vec::new(),
            quiet: false,
            mark: Mark::None,
            current: false,
            dimmed: false,
            disabled: false,
            cursor: false,
            depth: 0,
            role: "button",
            focusable: true,
            still: false,
            wrap: false,
            expanded: None,
            checked: None,
            description: None,
            select: None,
            press: None,
            item_value: None,
            select_role: "button",
            surface: None,
            tint: None,
        }
    }

    /// A column of list rows, inset from the panel it lies in so their corners and rings stand
    /// clear of its edges.
    pub fn column() -> El {
        door::rows(
            door::sides(col(), Space::S6, Space::S6),
            Space::S2,
            Space::S2,
        )
        .w_full()
    }

    /// A line of a list that is not a row, such as what an empty list says, its words on the line
    /// group headings start theirs on and as tall as they are, the keys that reach it, if any,
    /// at its end.
    pub fn note(
        key: impl Into<String>,
        said: impl Into<Said>,
        kind: Type,
        ink: Color,
        keys: Option<String>,
        t: &Tokens,
    ) -> El {
        let (key, said) = (key.into(), said.into());
        let keys_key = format!("{key}.keys");
        door::gap(
            door::sides(
                row()
                    .key(key)
                    .role("label")
                    .label(said.text())
                    .items_center(),
                super::group_head::INSET,
                super::group_head::INSET,
            ),
            Space::S6,
        )
        .child(
            words(said, kind, ink)
                .a11y_hidden()
                .single_line()
                .truncate()
                .shrink()
                .grow(1.0),
        )
        .children(keys.map(|keys| {
            words(keys, Type::Mono, t.text_dimmed)
                .key(keys_key)
                .single_line()
                .truncate()
                .shrink()
        }))
    }

    /// The room between the sections of one list.
    pub fn section_gap() -> f32 {
        Space::S16.pt()
    }

    /// How tall every list row of one line stands.
    pub fn height() -> f32 {
        Size::Regular.height()
    }

    /// How tall every list row of two lines stands: its title and the line under it, as far
    /// from its edges as a regular control's words.
    /// How far a row's words stand in from its line when it leads with a mark.
    pub(crate) fn lead_room() -> f32 {
        LEAD + Space::S8.pt()
    }

    /// `mark` in a row's lead room, so the words after it start where a led row's do, as a
    /// panel's header over a tree of rows.
    pub fn lead_slot(mark: El) -> El {
        row().w(Self::lead_room()).items_center().child(mark)
    }

    pub fn two_line_height() -> f32 {
        2.0 * Space::S6.pt() + Self::title_band() + Space::S2.pt() + door::line_height(Type::Small)
    }

    /// The line a row of two lines sets its title on: a line of `Name`, or a tag beside it when
    /// that is taller.
    fn title_band() -> f32 {
        door::line_height(Type::Name).max(super::tag::tag_height())
    }

    /// What leads the row, such as its kind's icon or its state's mark.
    pub fn lead(mut self, lead: El) -> Self {
        self.lead = Some(lead);
        self
    }

    /// The row's name.
    pub fn title(mut self, title: impl Into<Said>) -> Self {
        self.title = Some(Title::Words(title.into(), false));
        self
    }

    /// The row's name, secondary until the row is current: a place rather than a thing.
    pub fn quiet_title(mut self, title: impl Into<Said>) -> Self {
        self.title = Some(Title::Words(title.into(), true));
        self
    }

    /// The title and subtitle, cut short, keep their start and their end, so names that differ
    /// only at the end, such as two checkouts of one repository, stay apart.
    pub fn cut_middle(mut self) -> Self {
        self.cut_middle = true;
        self
    }

    /// The row's name built by its feature, such as a name led by a flag.
    pub fn title_el(mut self, title: El) -> Self {
        self.title = Some(Title::Built(title));
        self
    }

    /// A line under the title, such as where the thing is or what it last did. Copy written in
    /// the source sets what stands between backticks in `Mono`; a [`Said`] of parts sets its
    /// values in `Mono`.
    pub fn subtitle(mut self, subtitle: impl Into<Said>, t: &Tokens) -> Self {
        self.subtitle = Some(words(subtitle, Type::Small, t.text_tertiary));
        self
    }

    /// A line under the title built by its feature, such as facts with a part marked.
    pub fn subtitle_el(mut self, line: El) -> Self {
        self.subtitle = Some(line);
        self
    }

    /// [`Self::subtitle`] keyed `key` rather than the row's own key and `.subtitle`.
    pub fn subtitle_keyed(
        mut self,
        key: impl Into<String>,
        subtitle: impl Into<Said>,
        t: &Tokens,
    ) -> Self {
        self.subtitle_key = Some(key.into());
        self.subtitle(subtitle, t)
    }

    /// A tag or a badge right after the title, on its line.
    pub fn tag(mut self, tag: impl Into<El>) -> Self {
        self.tags.push(tag.into());
        self
    }

    /// Something the row says about itself at its end: a count, a mark, a time.
    pub fn aside(mut self, aside: impl Into<El>) -> Self {
        self.asides.push(aside.into());
        self
    }

    /// A mark the row keeps in view while its controls show, such as its role in a delegation,
    /// after its asides.
    pub fn badge(mut self, badge: impl Into<El>) -> Self {
        self.badges.push(badge.into());
        self
    }

    /// The keys that do what pressing the row does, at its end.
    pub fn keys(self, keys: Option<String>, t: &Tokens) -> Self {
        match keys {
            Some(keys) => {
                let key = format!("{}.keys", self.key);
                self.aside(words(keys, Type::Mono, t.text_dimmed).key(key))
            }
            None => self,
        }
    }

    /// Controls that show at the row's end, on its own ground, while the pointer or the keyboard
    /// is on it. Its asides and badges give way to them, its tags sit under its title, and a
    /// one-line title ends in a fade before them, and a wrapping row keeps their room for all its
    /// lines at rest too. Always-shown controls stay in view. They stand small and adjoin, so the
    /// words keep their room.
    pub fn hover_controls(mut self, controls: impl IntoIterator<Item = IconButton>) -> Self {
        self.hover
            .extend(controls.into_iter().map(|c| c.size(Size::Small)));
        self
    }

    /// Controls always at the row's end, after its asides, shown as `reveal` says.
    pub fn controls(mut self, controls: impl IntoIterator<Item = El>, reveal: Reveal) -> Self {
        match reveal {
            Reveal::Always => self.inline.extend(controls),
            Reveal::Quiet => {
                self.inline.extend(controls);
                self.quiet = true;
            }
        }
        self
    }

    /// The row is the one on screen: the selected surface and the brand bar at its start.
    pub fn in_view(mut self, in_view: bool) -> Self {
        if in_view {
            self.mark = Mark::InView;
        }
        self
    }

    /// The row is the chosen one of its list: the selected surface.
    pub fn selected(mut self, selected: bool) -> Self {
        if selected && self.mark == Mark::None {
            self.mark = Mark::Selected;
        }
        self
    }

    /// The row is where the window is: the selected surface, and its title in full.
    pub fn current(mut self, current: bool) -> Self {
        self.current = current;
        if current {
            self = self.selected(true);
        }
        self
    }

    /// The thing the row stands for is put away, such as a closed session: its title reads
    /// quieter, and it still takes a press.
    pub fn dimmed(mut self, dimmed: bool) -> Self {
        self.dimmed = dimmed;
        self
    }

    /// The keyboard's cursor of a list that holds the keyboard itself is on the row: it draws
    /// the focus ring while the list has the focus.
    pub fn cursor(mut self, cursor: bool) -> Self {
        self.cursor = cursor;
        self
    }

    /// The row takes no press for now, such as while what it starts is starting.
    pub fn disabled(mut self, disabled: bool) -> Self {
        self.disabled = disabled;
        self
    }

    /// How deep in a tree the row is: each level moves its lead one step in.
    pub fn depth(mut self, depth: u8) -> Self {
        self.depth = depth;
        self
    }

    /// The row only shows its thing and takes no press: an item of a list, not a button.
    pub fn still(mut self) -> Self {
        self.still = true;
        self.role = "listitem";
        self.focusable = false;
        self
    }

    /// The role of the button [`Self::select`] makes, when it is not a button, such as an item
    /// of a menu.
    pub fn select_role(mut self, role: &'static str) -> Self {
        self.select_role = role;
        self
    }

    /// The row's title and the line under it wrap rather than end in an ellipsis, and the row
    /// grows to hold them; its lead, asides and controls stay on the title's first line.
    pub fn wrap(mut self) -> Self {
        self.wrap = true;
        self
    }

    /// What pressing the row opens is open, or closed: said on the row, or on its words'
    /// button when it has [`Self::select`].
    pub fn expanded(mut self, open: bool) -> Self {
        self.expanded = Some(open);
        self
    }

    /// What the row's press turns on is on, or off, such as the automation whose runs show:
    /// said on the row, or on its words' button when it has [`Self::select`].
    pub fn checked(mut self, on: bool) -> Self {
        self.checked = Some(on);
        self
    }

    /// What the row is beyond its name, such as what starts an automation: read after the name
    /// on the row, or on its words' button when it has [`Self::select`].
    pub fn description(mut self, description: impl Into<String>) -> Self {
        self.description = Some(description.into());
        self
    }

    /// The row's role, when it is not a button: an option of a list box, an item of a list.
    pub fn role(mut self, role: &'static str) -> Self {
        self.role = role;
        self
    }

    /// The row takes no focus of its own, as an option of a list box whose list holds the
    /// keyboard.
    pub fn unfocusable(mut self) -> Self {
        self.focusable = false;
        self
    }

    /// What pressing the row does, for a row that is one button or option.
    pub fn on_press_tx<S: 'static>(mut self, press: impl Fn(&mut S, &mut Tx) + 'static) -> Self {
        self.press = Some(Box::new(move |e: El| e.on_press_tx(press)));
        self
    }

    /// The row is an item of a list, and its lead and words are its own button, keyed `key`
    /// and read as `label`, beside its controls rather than holding them.
    pub fn select<S: 'static>(
        mut self,
        key: impl Into<String>,
        label: impl Into<String>,
        press: impl Fn(&mut S, &mut Tx) + 'static,
    ) -> Self {
        self.role = "listitem";
        self.focusable = false;
        let attach: Press = Box::new(move |e: El| e.on_press_tx(press));
        self.select = Some((key.into(), label.into(), attach));
        self
    }

    /// What the row's list item says it is, beside its name, such as its severity and whether it
    /// is unread, on the item that holds the row and what lies under it.
    pub fn item_value(mut self, value: impl Into<String>) -> Self {
        self.item_value = Some(value.into());
        self
    }

    /// What the row's button reads as its value, such as "current" on the profile in use.
    pub fn value(mut self, value: impl Into<String>) -> Self {
        self.value = Some(value.into());
        self
    }

    /// The row's ground takes `tint`, such as the error colour while its machine is unreachable.
    pub fn tint(mut self, tint: Option<Color>) -> Self {
        self.tint = tint;
        self
    }

    /// The surface the row lies on, when it is not the panel.
    pub fn on(mut self, surface: Color) -> Self {
        self.surface = Some(surface);
        self
    }

    pub fn build(self, t: &Tokens) -> El {
        self.build_with(None, t)
    }

    /// The row as a list item that also holds `under`, what belongs to the row below it: the
    /// item keeps the row's key, role and name, and the row's own line gives them up.
    pub(crate) fn build_holding(self, under: El, t: &Tokens) -> El {
        self.build_with(Some(under), t)
    }

    fn build_with(self, holding: Option<El>, t: &Tokens) -> El {
        let size = Size::Compact;
        let two_lines = self.subtitle.is_some();
        let wraps = self.wrap;
        let on_its_line = wraps || (!two_lines && !self.tags.is_empty());
        let (height, band, kind) = match (two_lines, on_its_line) {
            (true, true) if wraps => (
                Self::two_line_height(),
                door::line_height(Type::Name),
                Type::Name,
            ),
            (true, _) => (Self::two_line_height(), Self::title_band(), Type::Name),
            (false, true) if wraps => (Self::height(), door::line_height(Type::Body), Type::Body),
            (false, true) => (
                Self::height(),
                door::line_height(Type::Body).max(super::tag::tag_height()),
                Type::Body,
            ),
            (false, false) => (Self::height(), Self::height(), Type::Body),
        };
        let below = self.surface.unwrap_or(t.bg_panel);
        let lit = self.mark != Mark::None;
        let (rest, fill) = match (self.tint, lit) {
            (Some(tint), lit) => {
                let fill = tint.alpha(if lit { TINT.1 } else { TINT.0 });
                (crate::colors::mix_srgb(below, tint, fill.a), fill)
            }
            (None, true) => {
                let lit = if below == t.bg_elevated {
                    t.bg_selected
                } else {
                    t.bg_elevated
                };
                (lit, lit)
            }
            (None, false) => (below, Color::TRANSPARENT),
        };
        let on_band = |e: El| row().h(band).items_center().child(e);
        let cut_middle = self.cut_middle;
        let one_line = |e: El| {
            if wraps {
                e.wrap().w_full()
            } else if cut_middle {
                e.single_line().truncate_middle().shrink()
            } else {
                e.single_line().truncate().shrink()
            }
        };
        let title_key = format!("{}.title", self.key);
        let title = self.title.map(|title| match title {
            Title::Built(el) => el,
            Title::Words(said, quiet) => {
                let ink = if (quiet && !self.current) || self.dimmed {
                    t.text_secondary
                } else {
                    t.text_primary
                };
                one_line(words(said, kind, ink).key(title_key))
            }
        });
        let title = title.map(|title| title.min_w(TITLE_FLOOR));
        let subtitle_key = self
            .subtitle_key
            .unwrap_or_else(|| format!("{}.subtitle", self.key));
        let subtitle = self
            .subtitle
            .map(|line| one_line(line.key(subtitle_key)).min_w(0.0));
        let has_lead = self.lead.is_some();
        let lead = self
            .lead
            .map(|lead| on_band(row().w(LEAD).justify_center().items_center().child(lead)));
        let hovering = !self.hover.is_empty();
        let controls_width = self.hover.iter().map(IconButton::width).sum::<f32>();
        let over_words = if self.inline.is_empty() {
            controls_width
        } else {
            (controls_width - LINE_GAP.pt()).max(0.0)
        };
        let tagged = !self.tags.is_empty();
        let under_title = tagged && (hovering || (wraps && !self.inline.is_empty()));
        let title_line = door::gap(row(), Space::S6).w_full().min_w(0.0);
        let (title_line, under) = match (wraps, tagged, under_title) {
            (true, _, true) => (
                title_line.items_start().children(title),
                Some(door::gap(row().items_center(), Space::S6).children(self.tags)),
            ),
            (true, _, false) => (
                title_line.items_start().children(title).children(self.tags),
                None,
            ),
            (false, true, true) => (
                title_line.h(band).items_center().children(title),
                Some(door::gap(row().items_center(), Space::S6).children(self.tags)),
            ),
            (false, true, false) => (
                title_line
                    .flex_wrap()
                    .items_center()
                    .children(title.map(|title| on_band(title).shrink().min_w(0.0)))
                    .children(self.tags),
                None,
            ),
            (false, false, _) => (title_line.h(band).items_center().children(title), None),
        };
        let gives_way = |e: El| {
            if hovering {
                e.group_opacity(1.0, 0.0)
            } else {
                e
            }
        };
        let asides = (!self.asides.is_empty()).then(|| {
            gives_way(
                on_band(
                    door::gap(row().items_center(), Space::S6)
                        .shrink()
                        .min_w(0.0)
                        .children(self.asides),
                )
                .max_w_frac(0.5)
                .shrink()
                .min_w(0.0),
            )
        });
        let badges = (!self.badges.is_empty()).then(|| {
            gives_way(on_band(
                door::gap(row().items_center(), Space::S6)
                    .key(format!("{}.badges", self.key))
                    .children(self.badges),
            ))
        });
        let lines = |head: El, under: Option<El>, subtitle: Option<El>| {
            door::gap(
                col(),
                if under.is_some() {
                    Space::S6
                } else {
                    Space::S2
                },
            )
            .grow(1.0)
            .min_w(0.0)
            .child(head)
            .children(under)
            .children(subtitle)
        };
        let (words, under, subtitle) = if wraps {
            (lines(title_line, under, subtitle), None, None)
        } else {
            (title_line.grow(1.0), under, subtitle)
        };
        let fading = door::gap(row(), Space::S8)
            .grow(1.0)
            .min_w(0.0)
            .items_start()
            .children(lead)
            .child(words)
            .children(asides)
            .children(badges);
        let fading = match (hovering, wraps) {
            (true, false) => door::fade_before_hover(fading, over_words),
            (true, true) => door::room_before_hover(fading, over_words),
            (false, _) => fading,
        };
        let controls_key = format!("{}.controls", self.key);
        let inline = (!self.inline.is_empty()).then(|| {
            let controls = on_band(
                door::gap(row(), Space::S4)
                    .key(controls_key.clone())
                    .items_center()
                    .children(self.inline),
            );
            if self.quiet && !lit {
                controls.group_opacity(QUIET, 1.0)
            } else {
                controls
            }
        });
        let over = hovering.then(|| {
            let key = if inline.is_some() {
                format!("{controls_key}.hover")
            } else {
                controls_key.clone()
            };
            row()
                .key(key)
                .group_opacity(0.0, 1.0)
                .pin(None, Some(0.0), Some(0.0), Some(0.0))
                .items_center()
                .children(self.hover.into_iter().map(|b| El::from(b.build(t))))
        });
        let (inline, anchor) = match (inline, over) {
            (Some(inline), over) => (
                Some(
                    row()
                        .h(band)
                        .items_center()
                        .children(over.map(|over| {
                            row()
                                .pin(Some(0.0), Some(0.0), None, Some(0.0))
                                .w(0.0)
                                .child(over)
                        }))
                        .child(inline),
                ),
                None,
            ),
            (None, over) => (
                None,
                over.map(|over| {
                    row()
                        .h(band)
                        .pin(None, Some(0.0), Some(0.0), None)
                        .child(over)
                }),
            ),
        };
        let band_line = door::gap(row(), LINE_GAP)
            .w_full()
            .min_w(0.0)
            .items_start()
            .child(fading)
            .children(anchor)
            .children(inline);
        let lower = (under.is_some() || subtitle.is_some()).then(|| {
            row()
                .w_full()
                .min_w(0.0)
                .children(has_lead.then(|| row().w(LEAD + Space::S8.pt())))
                .child(
                    door::gap(col(), Space::S2)
                        .grow(1.0)
                        .min_w(0.0)
                        .children(under)
                        .children(subtitle),
                )
        });
        let rows = door::gap(
            col(),
            if under_title && !wraps {
                Space::S6
            } else {
                Space::S2
            },
        )
        .grow(1.0)
        .min_w(0.0)
        .child(band_line)
        .children(lower);
        let bar = (self.mark == Mark::InView).then(|| {
            let top = (height - BAR.1) / 2.0;
            door::corner(
                el().key(format!("{}.in-view", self.key))
                    .absolute(0.0, top)
                    .size(BAR.0, BAR.1)
                    .fill(t.brand)
                    .inert(),
                Radius::Round,
            )
        });
        let item = self
            .select
            .is_some()
            .then(|| (self.key.clone(), self.role, self.label.clone()));
        let lifted = holding.is_some() && item.is_none() && self.role == "listitem";
        let (key, role, label, focusable, attach) = match self.select {
            Some((key, label, attach)) => (key, self.select_role, label, true, Some(attach)),
            None => (self.key, self.role, self.label, self.focusable, self.press),
        };
        let item_role = if role == "option" {
            "group"
        } else {
            "listitem"
        };
        let named = label.clone();
        let (line, held) = if lifted {
            (row(), Some((key, role, label)))
        } else {
            (row().key(key).role(role).label(label), None)
        };
        let line = line
            .focusable(focusable)
            .w_full()
            .min_w(0.0)
            .items_start()
            .fill(fill)
            .hover_group()
            .child(rows)
            .children(bar);
        let line = match self.expanded {
            Some(open) => line.expanded(open),
            None => line,
        };
        let line = match self.value {
            Some(value) => line.a11y_value(value),
            None => line,
        };
        let (line, value) = match (item.is_none() && held.is_none(), self.item_value) {
            (true, Some(value)) => (line.a11y_value(value), None),
            (_, value) => (line, value),
        };
        let line = match self.description {
            Some(description) => line.description(description),
            None => line,
        };
        let line = match self.checked {
            Some(on) => line.checked(on),
            None => line,
        };
        let line = match (wraps || tagged, two_lines) {
            (false, _) => line.h(height),
            (true, true) => line.min_h(height),
            (true, false) if wraps => door::on_band(line.min_h(height), Type::Body, Size::Regular),
            (true, false) => door::around_band(line, band, Size::Regular),
        };
        let line = door::sides(line, Space::S0, size.inset());
        let line = door::indented(line, size.inset(), Space::S12, self.depth);
        let line = if two_lines {
            door::rows(line, Space::S6, Space::S6)
        } else {
            line
        };
        let line = door::corner(door::gap(line, Space::S8), Radius::Control);
        let line = match attach {
            Some(attach) => attach(line),
            None => line,
        };
        let line = match (self.disabled, self.still, focusable, self.cursor) {
            (true, ..) => super::disabled::dim(line),
            (false, true, ..) => line,
            (false, false, _, true) => {
                focus::ring_on_edge(layers::pressable(line, rest, t).focus_selected(true), t)
            }
            (false, false, true, false) => focus::ring_on_edge(layers::pressable(line, rest, t), t),
            (false, false, false, false) => layers::pressable(line, rest, t),
        };
        let valued = |e: El| match &value {
            Some(value) => e.a11y_value(value.clone()),
            None => e,
        };
        match (item.or(held), holding) {
            (Some((key, role, label)), None) => valued(row())
                .key(key)
                .role(role)
                .label(label)
                .w_full()
                .min_w(0.0)
                .child(line),
            (Some((key, role, label)), Some(held_under)) => valued(col())
                .key(key)
                .role(role)
                .label(label)
                .child(line)
                .child(held_under),
            (None, Some(held_under)) => valued(col())
                .role(item_role)
                .label(named)
                .child(line)
                .child(held_under),
            (None, None) => line,
        }
    }
}

enum Title {
    Words(Said, bool),
    Built(El),
}
