use rune::{Color, Curve, Easing, El, Spring, col, el, row};

use crate::colors::{MARK_READS, READS, mix_srgb, moved_until_it_reads, reads_furthest};
use crate::system::{Radius, Size, Space, Type, chip_button, door, focus, layers, words};
use crate::{Glyph, Tokens};

/// The title bar's shelf: an entry per column of the row, by name, with a frame behind the ones
/// on screen and a count for the ones folded away at either end.
pub struct Shelf;

/// What a tile of a column is doing, as its mark and its dot show it.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum MemberState {
    Idle,
    Working,
    Waiting,
    Approval,
    Scheduled,
    Unknown,
}

impl MemberState {
    fn ink(self, t: &Tokens) -> Color {
        match self {
            MemberState::Idle => t.state_idle,
            MemberState::Working => t.state_working,
            MemberState::Waiting => t.state_waiting,
            MemberState::Approval => t.state_approval,
            MemberState::Scheduled => t.state_scheduled,
            MemberState::Unknown => t.state_unknown,
        }
    }
}

/// One tile of a column: its name, its icon, and its state when it has one.
#[derive(Clone)]
pub struct ShelfMember {
    pub name: String,
    pub icon: Glyph,
    pub state: Option<MemberState>,
}

impl ShelfMember {
    pub fn needs_user(&self) -> bool {
        matches!(
            self.state,
            Some(MemberState::Waiting | MemberState::Approval)
        )
    }

    fn dot_width(&self) -> f32 {
        if self.needs_user() { DASH } else { DOT }
    }

    fn ink(&self, ground: Color, t: &Tokens) -> Color {
        let ink = self.state.map_or(t.text_tertiary, |state| state.ink(t));
        legible(ink, ground, MARK_READS, t)
    }
}

/// Where a column stands in the row.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Place {
    OffScreen,
    OnScreen,
    Current,
}

impl Place {
    fn name_chars(self) -> usize {
        match self {
            Place::OffScreen => 18,
            Place::OnScreen | Place::Current => 26,
        }
    }

    fn bar(self, t: &Tokens) -> Color {
        match self {
            Place::OffScreen => t.bg_panel,
            Place::OnScreen | Place::Current => t.bg_elevated,
        }
    }
}

/// A column's entry: flat for one tile, and for a stack a pill that shows the member in use.
#[derive(Clone)]
pub struct ShelfEntry {
    members: Vec<ShelfMember>,
    shown: usize,
    place: Place,
    asking: usize,
}

struct Shape {
    height: f32,
    start: f32,
    end: f32,
    corner: f32,
}

const FLAT: Shape = Shape {
    height: 22.0,
    start: 8.0,
    end: 8.0,
    corner: 6.0,
};
const PILL: Shape = Shape {
    height: 26.0,
    start: 7.0,
    end: 9.0,
    corner: 7.0,
};

const BAR: f32 = 32.0;
const FRAME_TOP: f32 = 1.0;
const FRAME: f32 = 30.0;
const FRAME_CORNER: f32 = 9.0;
const FRAME_SECONDS: f32 = 0.2;
const MARK: f32 = 16.0;
const MARK_CORNER: f32 = 4.5;
const BARE_ICON: f32 = 14.0;
const CUT_ICON: f32 = 10.0;
const WORKING_WASH: f32 = 0.24;
const PILL_WASH_ON_SCREEN: f32 = 0.14;
const PILL_WASH_OFF_SCREEN: f32 = 0.09;
const DOT: f32 = 4.0;
const DASH: f32 = 10.0;
const DOT_GAP: f32 = 2.0;
const DOTS_START: f32 = 8.0;
const ENTRY_GAP: Space = Space::S2;
const NAME_GAP: Space = Space::S6;
const MARK_GAP: Space = Space::S2;
const COUNT_GAP: Space = Space::S4;
const COUNT_INSET: Space = Space::S8;
const ASK: &str = "?";

const SCROLL: Curve = Curve::Tween {
    duration: 0.22,
    easing: Easing::cubic(0.22, 1.0, 0.36, 1.0),
};

/// `ink` moved toward whichever of the text and the panel reads further on `ground`, only as far
/// as `floor` asks.
fn legible(ink: Color, ground: Color, floor: f32, t: &Tokens) -> Color {
    let toward = reads_furthest(ground, [t.text_primary, t.bg_panel]);
    moved_until_it_reads(ink, toward, ground, floor)
}

fn cut(name: &str, chars: usize) -> String {
    if name.chars().count() <= chars {
        return name.to_owned();
    }
    let kept: String = name.chars().take(chars - 1).collect();
    format!("{}…", kept.trim_end())
}

fn drawn(icon: &Glyph, points: f32, ink: Color) -> El {
    match icon {
        Glyph::Icon(icon) => door::icon_at(*icon, points).color(ink),
        Glyph::Text(letter) => door::font(rune::text(letter.clone()), Type::Control).color(ink),
    }
}

fn mark(member: &ShelfMember, ground: Color, t: &Tokens) -> El {
    let ink = member.ink(ground, t);
    let face = |corner: f32, fill: Color, icon: El| {
        door::round(
            row().size(MARK, MARK).center().a11y_hidden().child(icon),
            corner,
        )
        .fill(fill)
    };
    match member.state {
        Some(MemberState::Waiting) => face(MARK / 2.0, ink, drawn(&member.icon, CUT_ICON, ground)),
        Some(MemberState::Approval) => {
            face(MARK_CORNER, ink, drawn(&member.icon, CUT_ICON, ground))
        }
        Some(MemberState::Working) => {
            let wash = mix_srgb(ground, ink, WORKING_WASH);
            let icon = legible(ink, wash, MARK_READS, t);
            face(MARK / 2.0, wash, drawn(&member.icon, CUT_ICON, icon))
        }
        _ => {
            row()
                .size(MARK, MARK)
                .center()
                .a11y_hidden()
                .child(drawn(&member.icon, BARE_ICON, ink))
        }
    }
}

fn pressable(e: El, ground: Color, corner: f32, t: &Tokens) -> El {
    layers::pressable(door::round(e, corner), ground, t)
}

impl ShelfEntry {
    /// `members` in the column's order, showing the one at `shown`, with a "?" for each of
    /// `asking` open asks.
    pub fn new(members: Vec<ShelfMember>, shown: usize, place: Place, asking: usize) -> ShelfEntry {
        ShelfEntry {
            shown: shown.min(members.len().saturating_sub(1)),
            members,
            place,
            asking,
        }
    }

    pub fn members(&self) -> &[ShelfMember] {
        &self.members
    }

    pub fn place(&self) -> Place {
        self.place
    }

    pub fn asking(&self) -> usize {
        self.asking
    }

    fn stacked(&self) -> bool {
        self.members.len() > 1
    }

    fn shape(&self) -> &'static Shape {
        if self.stacked() { &PILL } else { &FLAT }
    }

    fn name_of(&self, member: &ShelfMember) -> String {
        cut(&member.name, self.place.name_chars())
    }

    fn asks_width(&self) -> f32 {
        self.asking as f32 * (NAME_GAP.pt() + door::width(ASK, Type::Label))
    }

    /// The room the entry's widest name takes, as wide as it stands while it is current.
    pub fn name_width(&self) -> f32 {
        self.members
            .iter()
            .map(|member| door::width(&self.name_of(member), Type::Control))
            .fold(0.0, f32::max)
    }

    fn unnamed_width(&self) -> f32 {
        let shape = self.shape();
        shape.start + MARK + shape.end + self.asks_width()
    }

    /// The room a pill's dots take on the edge that holds most of them, whichever member it shows.
    fn dots_width(&self) -> f32 {
        if !self.stacked() {
            return 0.0;
        }
        let edge = |members: &[ShelfMember]| {
            let dots: f32 = members.iter().map(ShelfMember::dot_width).sum();
            2.0 * DOTS_START + dots + DOT_GAP * (members.len() - 1) as f32
        };
        let last = self.members.len() - 1;
        edge(&self.members[1..]).max(edge(&self.members[..last]))
    }

    /// Whether `name` points draw the member's name, whole or cut short.
    fn names(&self, member: &ShelfMember, name: f32) -> bool {
        let whole = door::width(&self.name_of(member), Type::Control);
        name > 0.0 && name >= whole.min(door::width("…", Type::Control))
    }

    fn name_room(&self, name: f32) -> f32 {
        if self.members.iter().any(|m| self.names(m, name)) {
            NAME_GAP.pt() + name
        } else {
            0.0
        }
    }

    /// How wide the entry stands with `name` points for its name.
    pub fn width(&self, name: f32) -> f32 {
        (self.unnamed_width() + self.name_room(name)).max(self.dots_width())
    }

    /// The widest name that widens the entry by no more than `room`.
    pub fn name_within(&self, room: f32) -> f32 {
        let spare = self.width(0.0) - self.unnamed_width();
        (spare + room - NAME_GAP.pt()).floor().max(0.0)
    }

    fn ground(&self, t: &Tokens) -> Color {
        let bar = self.place.bar(t);
        let wash = match self.place {
            Place::Current => return t.text_primary,
            _ if !self.stacked() => return bar,
            Place::OnScreen => PILL_WASH_ON_SCREEN,
            Place::OffScreen => PILL_WASH_OFF_SCREEN,
        };
        let washed = mix_srgb(bar, t.text_primary, wash);
        moved_until_it_reads(washed, bar, t.text_primary, READS)
    }

    fn name_ink(&self, ground: Color, t: &Tokens) -> Color {
        let ink = match self.place {
            Place::Current => t.bg_panel,
            Place::OnScreen => t.text_primary,
            Place::OffScreen => t.text_tertiary,
        };
        legible(ink, ground, READS, t)
    }

    fn slice(&self, member: &ShelfMember, name: f32, t: &Tokens) -> El {
        let ground = self.ground(t);
        let kind = match self.place {
            Place::Current => Type::Control,
            _ => Type::Small,
        };
        let named = self.names(member, name).then(|| {
            words(self.name_of(member), kind, self.name_ink(ground, t))
                .single_line()
                .truncate()
                .w(name)
        });
        door::gap(row().items_center(), NAME_GAP)
            .h(self.shape().height)
            .child(mark(member, ground, t))
            .children(named)
    }

    /// The member whose dot is at (`x`, `y`) in the entry.
    pub fn dot_at(&self, x: f32, y: f32) -> Option<usize> {
        self.dots()
            .into_iter()
            .filter(|dot| dot.shows)
            .find(|dot| {
                let near = if dot.above {
                    y <= 2.0 * DOT
                } else {
                    y >= PILL.height - 2.0 * DOT
                };
                near && x >= dot.x && x < dot.x + dot.width + DOT_GAP
            })
            .map(|dot| dot.member)
    }

    fn dots(&self) -> Vec<Dot> {
        if !self.stacked() {
            return vec![];
        }
        let row = |above: bool| {
            let mut x = DOTS_START;
            self.members
                .iter()
                .enumerate()
                .map(|(member, m)| {
                    let width = m.dot_width();
                    let shows = if above {
                        member < self.shown
                    } else {
                        member > self.shown
                    };
                    let at = x;
                    if shows {
                        x += width + DOT_GAP;
                    }
                    Dot {
                        member,
                        above,
                        x: at,
                        width,
                        shows,
                    }
                })
                .collect::<Vec<_>>()
        };
        [row(true), row(false)].concat()
    }

    /// The entry keyed `key` and read as `label`, with `name` points for its name.
    pub fn build(&self, key: &str, label: &str, name: f32, t: &Tokens) -> El {
        let shape = self.shape();
        let ground = self.ground(t);
        let asks = Shelf::asks(key, self.asking, ground, t);
        let slices = MARK + self.name_room(name);
        let slices = if self.stacked() {
            let reel = col()
                .absolute(0.0, 0.0)
                .offset(0.0, -PILL.height * self.shown as f32)
                .offset_motion(SCROLL)
                .children(self.members.iter().enumerate().map(|(i, member)| {
                    let slice = self.slice(member, name, t);
                    let slice = slice.key(format!("{key}.slice.{i}"));
                    if i == self.shown {
                        slice
                    } else {
                        slice.a11y_hidden()
                    }
                }));
            el().w(slices).min_w(slices).h(PILL.height).child(reel)
        } else {
            let slice = self.slice(&self.members[0], name, t);
            slice.w(slices).min_w(slices)
        };
        let dots = self.dots().into_iter().map(|dot| {
            let member = &self.members[dot.member];
            let side = if dot.above { "above" } else { "below" };
            let rest = if dot.shows { 0.0 } else { -dot.width / 2.0 };
            door::round(el().size(dot.width, DOT), DOT / 2.0)
                .key(format!("{key}.{side}.{}", dot.member))
                .fill(member.ink(ground, t))
                .absolute(0.0, if dot.above { 0.0 } else { PILL.height - DOT })
                .offset(dot.x + rest, 0.0)
                .offset_motion(SCROLL)
                .scale(if dot.shows { 1.0 } else { 0.0 })
                .scale_motion(SCROLL)
                .inert()
                .a11y_hidden()
        });
        let width = self.width(name);
        let body = door::gap(row().items_center(), NAME_GAP)
            .absolute(shape.start, 0.0)
            .h(shape.height)
            .child(slices)
            .children(asks);
        let entry = el()
            .key(key.to_owned())
            .role("button")
            .label(label.to_owned())
            .w(width)
            .min_w(width)
            .h(shape.height)
            .child(body)
            .children(dots);
        let entry = pressable(entry, ground, shape.corner, t);
        let entry = if self.place == Place::Current || self.stacked() {
            entry.fill(ground)
        } else {
            entry
        };
        if self.stacked() {
            door::clip_round(entry, shape.corner)
        } else {
            entry
        }
    }
}

#[derive(Clone)]
struct Dot {
    member: usize,
    above: bool,
    x: f32,
    width: f32,
    shows: bool,
}

/// The end of the row a count stands at.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum CountSide {
    Before,
    After,
}

/// How many columns are folded at one end of the row, and the marks of the members in them that
/// need the user.
pub struct ShelfCount {
    pub side: CountSide,
    pub columns: usize,
    pub needs_user: bool,
    pub marks: Vec<ShelfMember>,
}

impl ShelfCount {
    fn caption(&self) -> String {
        match self.side {
            CountSide::Before => format!("‹ {}", self.columns),
            CountSide::After => format!("{} ›", self.columns),
        }
    }

    fn kind(&self) -> Type {
        if self.needs_user {
            Type::Control
        } else {
            Type::Small
        }
    }

    pub fn width(&self) -> f32 {
        door::width(&self.caption(), self.kind())
            + 2.0 * COUNT_INSET.pt()
            + self.marks.len() as f32 * (COUNT_GAP.pt() + MARK)
    }

    /// The count keyed `key` and read as `label`, `open` while its menu is.
    pub fn build(
        &self,
        key: impl Into<String>,
        label: impl Into<String>,
        open: bool,
        t: &Tokens,
    ) -> El {
        let ground = if open { t.bg_elevated } else { t.bg_panel };
        let ink = if self.needs_user {
            t.text_primary
        } else {
            t.text_tertiary
        };
        let ink = legible(ink, ground, READS, t);
        let said = words(self.caption(), self.kind(), ink).single_line();
        let marks = self.marks.iter().map(|member| mark(member, ground, t));
        let face = door::gap(
            door::inset(row().items_center(), COUNT_INSET, Space::S0),
            COUNT_GAP,
        );
        let face = match self.side {
            CountSide::Before => face.child(said).children(marks),
            CountSide::After => face.children(marks).child(said),
        };
        let width = self.width();
        let count = face
            .key(key.into())
            .role("button")
            .focusable(true)
            .label(label.into())
            .w(width)
            .min_w(width)
            .h(FLAT.height)
            .fill(ground);
        focus::ring(pressable(count, ground, FLAT.corner, t), t)
    }
}

fn framing(key: &str, x: f32, width: f32) -> El {
    door::round(el(), FRAME_CORNER)
        .key(key)
        .absolute(x, FRAME_TOP)
        .w(width)
        .h(FRAME)
        .motion(Spring::new(FRAME_SECONDS, 0.0))
}

impl Shelf {
    /// How far the frame runs past the first and the last entry on screen.
    pub const FRAME_REACH: f32 = 3.0;
    /// The room before the shelf's first entry and after its last.
    pub const START: f32 = Space::S4.pt();
    pub const END: f32 = Space::S4.pt();

    /// The shelf's entries one after another, keyed `key`.
    pub fn row(key: impl Into<String>) -> El {
        let row = row().key(key.into()).items_center().h(BAR);
        door::sides(door::gap(row, ENTRY_GAP), Space::S4, Space::S4)
    }

    /// The room between two of the shelf's entries.
    pub fn gap() -> f32 {
        ENTRY_GAP.pt()
    }

    /// The ground of the entries on screen, `width` wide from `x`.
    pub fn frame(x: f32, width: f32, t: &Tokens) -> El {
        framing("shelf.frame", x, width).fill(t.bg_elevated)
    }

    /// The edge of [`Shelf::frame`], drawn over the entry or the count beside it.
    pub fn frame_edge(x: f32, width: f32, t: &Tokens) -> El {
        let edge = legible(t.text_tertiary, t.bg_panel, MARK_READS, t);
        framing("shelf.frame.edge", x, width)
            .border(1.0, edge)
            .paint_order(1)
            .inert()
    }

    /// A "?" on `ground` for each of `count` open asks of the entry keyed `key`.
    pub fn asks(key: &str, count: usize, ground: Color, t: &Tokens) -> Vec<El> {
        let ink = legible(t.accent, ground, READS, t);
        (0..count)
            .map(|i| {
                words(ASK, Type::Label, ink)
                    .key(format!("{key}.ask.{i}"))
                    .role("img")
                    .label("Open ask")
            })
            .collect()
    }

    /// A tile's mark on `ground`.
    pub fn mark(member: &ShelfMember, ground: Color, t: &Tokens) -> El {
        mark(member, ground, t)
    }

    /// A folded column in a count's menu, keyed `key` and read as `label`: `marks`, the words
    /// `label` and `after`. The pointer moves the shelf's entries, so it takes no stop on Tab.
    pub fn folded(
        key: impl Into<String>,
        label: impl Into<String>,
        marks: impl IntoIterator<Item = El>,
        after: impl IntoIterator<Item = El>,
        t: &Tokens,
    ) -> El {
        let label = label.into();
        let face = door::gap(row().w_full().items_center(), NAME_GAP)
            .child(door::gap(row().items_center(), MARK_GAP).children(marks))
            .child(
                words(label.clone(), Type::Small, t.text_secondary)
                    .single_line()
                    .truncate()
                    .grow(1.0),
            )
            .children(after);
        chip_button(key, label, face, false, Size::Small, t)
            .focusable(false)
            .min_w(0.0)
            .w_full()
    }

    /// The accent ring over an entry while what it brings arrives, keyed `key`.
    pub fn flash(key: impl Into<String>, t: &Tokens) -> El {
        door::corner(
            el().key(key.into())
                .pin(Some(0.0), Some(0.0), Some(0.0), Some(0.0))
                .border(1.0, t.accent),
            Radius::Control,
        )
    }
}
