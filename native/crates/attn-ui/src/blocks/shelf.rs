use rune::{Color, Curve, Easing, El, Spring, Transition, col, el, row};

use crate::colors::{MARK_READS, READS, mix_srgb, moved_until_it_reads, reads_furthest};
use crate::system::{Radius, Space, Type, door, focus, layers, words};
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

    pub(super) fn ink(&self, ground: &Ground, t: &Tokens) -> Ink {
        let ink = self.state.map_or(t.text_tertiary, |state| state.ink(t));
        ground.ink(ink, MARK_READS, t)
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
    height: 28.0,
    start: 8.0,
    end: 8.0,
    corner: 14.0,
};
const PILL: Shape = Shape {
    height: 28.0,
    start: 9.0,
    end: 10.0,
    corner: 14.0,
};

const BAR: f32 = 40.0;
const FRAME_TOP: f32 = 4.0;
const FRAME: f32 = 32.0;
const FRAME_CORNER: f32 = 16.0;
const FRAME_SECONDS: f32 = 0.2;
const MARK: f32 = 16.0;
const MARK_CORNER: f32 = 4.5;
const BARE_ICON: f32 = 14.0;
const CUT_ICON: f32 = 10.0;
const WORKING_WASH: f32 = 0.24;
const PILL_WASH_ON_SCREEN: f32 = 0.06;
const PILL_WASH_OFF_SCREEN: f32 = 0.05;
const DOT: f32 = 4.0;
const DASH: f32 = 10.0;
const DOT_GAP: f32 = 2.0;
const DOTS_START: f32 = PILL.corner;
const ENTRY_GAP: Space = Space::S2;
const NAME_GAP: Space = Space::S6;
const COUNT_GAP: Space = Space::S4;
const COUNT_INSET: Space = Space::S8;
const ASK: &str = "?";

const SCROLL: Curve = Curve::Tween {
    duration: 0.22,
    easing: Easing::cubic(0.22, 1.0, 0.36, 1.0),
};

/// `ink` moved toward whichever of the text and the panel reads further on `ground`, only as far
/// as `floor` asks.
pub(super) fn legible(ink: Color, ground: Color, floor: f32, t: &Tokens) -> Color {
    let toward = reads_furthest(ground, [t.text_primary, t.bg_panel]);
    moved_until_it_reads(ink, toward, ground, floor)
}

/// `e` taking a new colour at once, while its place and size still move with the row.
fn recolored_at_once(e: El) -> El {
    e.motion(Spring::NONE)
        .lead(Shelf::row_spring(), Shelf::row_spring())
}

fn cut(name: &str, chars: usize) -> String {
    if name.chars().count() <= chars {
        return name.to_owned();
    }
    let kept: String = name.chars().take(chars - 1).collect();
    format!("{}…", kept.trim_end())
}

fn laid_over(below: Color, layer: Color) -> Color {
    mix_srgb(below, layer.alpha(1.0), layer.a)
}

/// What is behind a control's inks, at rest and under the pointer.
#[derive(Clone, Copy)]
pub(super) struct Ground {
    rest: Color,
    hovered: Color,
    pressed: Color,
}

impl Ground {
    /// `rest` with the steps every control takes, each only as far as words on it still read.
    pub(super) fn of(rest: Color, t: &Tokens) -> Ground {
        let furthest = reads_furthest(rest, [t.text_primary, t.bg_panel]);
        let step = |below: Color, layer: Color| {
            moved_until_it_reads(laid_over(below, layer), below, furthest, READS)
        };
        let hovered = step(rest, layers::hover_layer(rest, t));
        let pressed = step(hovered, layers::pressed_layer(rest, t));
        Ground {
            rest,
            hovered,
            pressed,
        }
    }

    pub(super) fn ink(&self, ink: Color, floor: f32, t: &Tokens) -> Ink {
        let rest = legible(ink, self.rest, floor, t);
        let hovered = legible(rest, self.hovered, floor, t);
        Ink {
            rest,
            lit: legible(hovered, self.pressed, floor, t),
        }
    }

    fn hover_ink(&self, ink: Color, floor: f32, t: &Tokens) -> Ink {
        let rest = legible(ink, self.rest, floor, t);
        Ink {
            rest,
            lit: legible(rest, self.hovered, floor, t),
        }
    }

    pub(super) fn words(&self, said: String, kind: Type, ink: Color, t: &Tokens) -> El {
        let ink = self.ink(ink, READS, t);
        words(said, kind, ink.rest).group_color(ink.lit)
    }

    pub(super) fn pressable(&self, e: El, corner: f32) -> El {
        door::round(e, corner)
            .hover(self.hovered)
            .press(self.pressed)
    }
}

/// A colour at rest, and while the pointer is over the control it is on.
#[derive(Clone, Copy)]
pub(super) struct Ink {
    pub(super) rest: Color,
    pub(super) lit: Color,
}

impl Ink {
    fn tint(self, e: El) -> El {
        e.color(self.rest).group_color(self.lit)
    }

    fn fill(self, e: El, corner: f32) -> El {
        let lit = (self.lit != self.rest).then(|| {
            door::round(el(), corner)
                .pin(Some(0.0), Some(0.0), Some(0.0), Some(0.0))
                .fill(self.lit)
                .group_opacity(0.0, 1.0)
                .inert()
        });
        door::round(e, corner).fill(self.rest).children(lit)
    }
}

enum MarkFace<'a> {
    Glyph(&'a Glyph),
    Source(rune::IconSource),
}

fn drawn(icon: MarkFace<'_>, points: f32, ink: Ink) -> El {
    ink.tint(match icon {
        MarkFace::Glyph(Glyph::Icon(icon)) => door::icon_at(*icon, points),
        MarkFace::Glyph(Glyph::Text(letter)) => {
            door::font(rune::text(letter.clone()), Type::Control)
        }
        MarkFace::Source(source) => door::icon_at(source, points),
    })
}

fn draw_mark(
    icon: MarkFace<'_>,
    state: Option<MemberState>,
    ink: Ink,
    rest_ground: Color,
    t: &Tokens,
) -> El {
    let square = || row().size(MARK, MARK).center().a11y_hidden();
    let cut_out = |icon: MarkFace<'_>, corner: f32| {
        let behind = Ink {
            rest: rest_ground,
            lit: rest_ground,
        };
        ink.fill(square(), corner)
            .child(drawn(icon, CUT_ICON, behind))
    };
    match state {
        Some(MemberState::Waiting) => cut_out(icon, MARK / 2.0),
        Some(MemberState::Approval) => cut_out(icon, MARK_CORNER),
        Some(MemberState::Working) => {
            let wash = mix_srgb(rest_ground, ink.rest, WORKING_WASH);
            let icon_ink = legible(ink.rest, wash, MARK_READS, t);
            let icon_ink = Ink {
                rest: icon_ink,
                lit: icon_ink,
            };
            door::round(square(), MARK / 2.0)
                .fill(wash)
                .child(drawn(icon, CUT_ICON, icon_ink))
        }
        _ => square().child(drawn(icon, BARE_ICON, ink)),
    }
}

/// An agent's mark drawn with the shelf's four-tier state language on `ground`: a filled circle
/// while waiting, a filled rounded square while asking approval, a washed circle while working,
/// and the bare icon otherwise. Hover contrast follows the hovered surface so quiet marks keep
/// their rest separation from one another under the pointer.
pub fn state_mark(
    icon: impl Into<rune::IconSource>,
    state: Option<MemberState>,
    ground: Color,
    t: &Tokens,
) -> El {
    let surface = Ground::of(ground, t);
    let base = state.map_or(t.text_tertiary, |state| state.ink(t));
    let ink = surface.hover_ink(base, MARK_READS, t);
    draw_mark(MarkFace::Source(icon.into()), state, ink, surface.rest, t)
}

pub(super) fn mark(member: &ShelfMember, ground: &Ground, t: &Tokens) -> El {
    let ink = member.ink(ground, t);
    draw_mark(
        MarkFace::Glyph(&member.icon),
        member.state,
        ink,
        ground.rest,
        t,
    )
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
            Place::Current => return t.bg_selected,
            _ if !self.stacked() => return bar,
            Place::OnScreen => PILL_WASH_ON_SCREEN,
            Place::OffScreen => PILL_WASH_OFF_SCREEN,
        };
        let washed = mix_srgb(bar, t.text_primary, wash);
        moved_until_it_reads(washed, bar, t.text_primary, READS)
    }

    fn slice(&self, member: &ShelfMember, name: f32, ground: &Ground, t: &Tokens) -> El {
        let (kind, ink) = match self.place {
            Place::Current => (Type::Control, t.text_primary),
            Place::OnScreen => (Type::Small, t.text_primary),
            Place::OffScreen => (Type::Small, t.text_tertiary),
        };
        let named = self.names(member, name).then(|| {
            let words = ground.words(self.name_of(member), kind, ink, t);
            recolored_at_once(words.single_line().truncate().w(name))
        });
        door::gap(row().items_center(), NAME_GAP)
            .h(self.shape().height)
            .child(recolored_at_once(mark(member, ground, t)))
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
        let ground = Ground::of(self.ground(t), t);
        let asks = asks(key, self.asking, &ground, t);
        let slices = MARK + self.name_room(name);
        let slices = if self.stacked() {
            let reel = col()
                .absolute(0.0, 0.0)
                .offset(0.0, -PILL.height * self.shown as f32)
                .offset_motion(SCROLL)
                .children(self.members.iter().enumerate().map(|(i, member)| {
                    let slice = self.slice(member, name, &ground, t);
                    let slice = slice.key(format!("{key}.slice.{i}"));
                    if i == self.shown {
                        slice
                    } else {
                        slice.a11y_hidden()
                    }
                }));
            el().w(slices).min_w(slices).h(PILL.height).child(reel)
        } else {
            let slice = self.slice(&self.members[0], name, &ground, t);
            slice.w(slices).min_w(slices)
        };
        let dots = self.dots().into_iter().map(|dot| {
            let member = &self.members[dot.member];
            let side = if dot.above { "above" } else { "below" };
            let rest = if dot.shows { 0.0 } else { -dot.width / 2.0 };
            member
                .ink(&ground, t)
                .fill(el().size(dot.width, DOT), DOT / 2.0)
                .key(format!("{key}.{side}.{}", dot.member))
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
            .motion(Shelf::row_spring())
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
        let entry = recolored_at_once(ground.pressable(entry, shape.corner));
        let entry = match self.place {
            Place::Current => entry.fill(ground.rest).border(1.0, t.border_hover),
            _ if self.stacked() => entry.fill(ground.rest),
            _ => entry,
        };
        if self.stacked() {
            door::clip_round(entry, shape.corner)
        } else {
            entry
        }
    }
}

fn asks(key: &str, count: usize, ground: &Ground, t: &Tokens) -> Vec<El> {
    (0..count)
        .map(|i| {
            recolored_at_once(ground.words(ASK.to_owned(), Type::Label, t.accent, t))
                .key(format!("{key}.ask.{i}"))
                .transition(Transition::none())
                .role("img")
                .label("Open ask")
        })
        .collect()
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

    /// The count keyed `key` and read as `label`.
    pub fn build(&self, key: impl Into<String>, label: impl Into<String>, t: &Tokens) -> El {
        let ground = Ground::of(t.bg_panel, t);
        let ink = if self.needs_user {
            t.text_primary
        } else {
            t.text_tertiary
        };
        let said = ground
            .words(self.caption(), self.kind(), ink, t)
            .single_line();
        let marks = self.marks.iter().map(|member| mark(member, &ground, t));
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
            .fill(ground.rest);
        focus::ring(ground.pressable(count, FLAT.corner), t)
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
    pub const FRAME_REACH: f32 = FRAME_CORNER - FLAT.corner;
    /// The room before the shelf's first entry and after its last.
    pub const START: f32 = Space::S4.pt();
    pub const END: f32 = Space::S4.pt();

    /// The spring a row's tiles and the shelf's entries move on.
    pub fn row_spring() -> Spring {
        Spring::from_stiffness_damping(630.0, 39.0, 1.0)
    }

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
        framing("shelf.frame.edge", x, width)
            .border(1.0, t.border)
            .paint_order(1)
            .inert()
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
