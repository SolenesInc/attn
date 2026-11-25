//! What the title bar's middle, such as the queue bar's desktop chips, gives up while its parts
//! would overlap. The middle's content and its box are measured before the frame paints, at the
//! text size in use, so a bar that changes shows what fits from its first frame. Each step is
//! taken only while something would overlap, and steps back once the bar is as wide as that
//! step's content last needed, for as long as what the bar holds is unchanged.

use rune::Tx;

use crate::State;

/// What the title bar has given up, each step on top of those before it.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq, PartialOrd, Ord)]
pub(crate) enum GivenUp {
    #[default]
    Nothing,
    /// The waiting pill's crumbs leave; it keeps its dot, its count and its caret.
    Crumbs,
    /// The critical notification leaves the bar; the bell keeps its dot.
    Strip,
    /// Every desktop but the window's gathers into the extras chip, and the runs chip leaves.
    Desktops,
    /// The profile pill keeps only its name's first letters and its caret.
    ProfileName,
    /// The window's chip and the extras leave; the desktop keys and the overview still reach
    /// every desktop, from the palette too.
    Chips,
    /// The shelf leaves; moving focus left and right still walks to the columns it held, and the
    /// palette still runs its tile commands.
    Shelf,
    /// The waiting pill says only its number, beside its dot.
    PillWord,
}

const STEPS: [GivenUp; 7] = [
    GivenUp::Crumbs,
    GivenUp::Strip,
    GivenUp::Desktops,
    GivenUp::ProfileName,
    GivenUp::Chips,
    GivenUp::Shelf,
    GivenUp::PillWord,
];

/// What the title bar holds that changes the room its parts need, and which steps free any.
#[derive(Clone, Debug, PartialEq)]
struct Contents {
    queue_bar: bool,
    desktop: Option<String>,
    desktops: usize,
    runs: bool,
    critical: bool,
    shelf: Option<(String, [usize; 3])>,
    profile: String,
    scale: u32,
    sidebar: u32,
}

fn contents(state: &State) -> Contents {
    let facts = &state.facts;
    let runs = crate::automations::runs::on_desktops(facts, state.window.now_ms)
        .iter()
        .any(|g| !g.runs.is_empty() || !g.needing_you.is_empty());
    Contents {
        queue_bar: super::actions::queue_flow(state) && state.window.sidebar_hidden,
        desktop: facts.current_desktop().map(|d| d.id.clone()),
        desktops: facts.desktops.len(),
        runs,
        critical: facts.notifications.unread_critical > 0,
        shelf: crate::desktop::shelf_columns(state),
        profile: facts.profiles.selected.clone(),
        scale: (crate::settings::text_scale(&facts.settings) * 100.0).round() as u32,
        sidebar: super::width::of(state).round() as u32,
    }
}

impl Contents {
    fn frees_room(&self, step: GivenUp) -> bool {
        match step {
            GivenUp::Nothing | GivenUp::ProfileName | GivenUp::Shelf => true,
            GivenUp::Strip => self.queue_bar && self.critical,
            GivenUp::Desktops => self.queue_bar && (self.desktops > 1 || self.runs),
            GivenUp::Crumbs | GivenUp::Chips | GivenUp::PillWord => self.queue_bar,
        }
    }
}

#[derive(Clone, Debug, PartialEq)]
struct Crowding {
    /// Each step taken, with the title bar's width at which what it showed before that step
    /// fits. The bar's width is the measure, not the middle's, as a step can widen the middle.
    taken: Vec<(GivenUp, f32)>,
    contents: Contents,
}

#[derive(Clone, Debug, Default, PartialEq)]
pub(crate) struct Room {
    bar_w: Option<f32>,
    middle: Option<[f32; 4]>,
    /// Where the content ended, and what the bar held and had given up then.
    content_right: Option<(f32, Contents, GivenUp)>,
    widths: Widths,
    crowding: Option<Crowding>,
    /// A step was taken, and the bar has not been measured since.
    unsettled: bool,
}

/// The last step the title bar has taken.
pub(crate) fn given_up(state: &State) -> GivenUp {
    let room = &state.window.queue_bar_room;
    match &room.crowding {
        Some(crowding) if crowding.contents == contents(state) => crowding
            .taken
            .last()
            .map_or(GivenUp::Nothing, |(step, _)| *step),
        _ => GivenUp::Nothing,
    }
}

/// How wide the title bar is laid out. It is heard before its middle.
pub(crate) fn bar_laid_out(state: &mut State, [_, _, w, _]: [f32; 4]) {
    state.window.queue_bar_room.bar_w = Some(w);
}

/// Where the title bar's middle is laid out. It is heard before what it holds. While the
/// content ends flush with the middle, something in it grows or shrinks to fill it, the end
/// moves with the middle and is heard then; otherwise where the content ends does not hang on
/// the middle's width, and the middle is measured against it now.
pub(crate) fn middle_laid_out(state: &mut State, rect: [f32; 4]) {
    let room = &mut state.window.queue_bar_room;
    let flush = match (room.middle, &room.content_right) {
        (Some([x, _, w, _]), Some((right, ..))) => (right - (x + w)).abs() <= 0.5,
        _ => true,
    };
    room.middle = Some(rect);
    if !flush {
        measure(state);
    }
}

/// The middle's `content` across the whole middle, its parts in a row whose end is measured
/// with what the bar held and had given up as `state`'s view built it. The end is its own
/// element for each set of steps drawn, so a drawing reports where it ends even where the last
/// one ended. A commit measures again only so many times, so while a step taken waits to be
/// measured, the end's layout asks for one more commit.
pub(crate) fn filled(state: &State, content: rune::El) -> rune::El {
    let held = contents(state);
    let drawn = given_up(state);
    content.grow(1.0).shrink().child(
        rune::el()
            .key(format!("titlebar.middle.end.{drawn:?}"))
            .w(0.0)
            .on_measure(move |state: &mut State, [x, ..]: [f32; 4]| {
                state.window.queue_bar_room.content_right = Some((x, held.clone(), drawn));
                measure(state);
            })
            .on_layout_tx(|state: &mut State, _, tx: &mut Tx| {
                if !state.window.queue_bar_room.unsettled {
                    tx.live_only();
                }
            }),
    )
}

fn measure(state: &mut State) {
    let room = &mut state.window.queue_bar_room;
    room.unsettled = false;
    let (Some(bar_w), Some([x, _, w, _]), Some((content_right, contents, drawn))) =
        (room.bar_w, room.middle, room.content_right.clone())
    else {
        return;
    };
    let crowding = room.crowding.get_or_insert_with(|| Crowding {
        taken: Vec::new(),
        contents: contents.clone(),
    });
    if crowding.contents != contents {
        *crowding = Crowding {
            taken: Vec::new(),
            contents: contents.clone(),
        };
    }
    let mut last = crowding.taken.last().map_or(GivenUp::Nothing, |(s, _)| *s);
    if drawn != last {
        room.unsettled = true;
        return;
    }
    let overflow = content_right - (x + w);
    if overflow > 0.5 {
        let mut left = overflow;
        while left > 0.5 {
            let Some(next) = STEPS
                .into_iter()
                .find(|step| *step > last && contents.frees_room(*step))
            else {
                break;
            };
            crowding.taken.push((next, bar_w + left));
            room.unsettled = true;
            last = next;
            match room.widths.freed_by(next, &contents) {
                Some(freed) => left -= freed,
                None => break,
            }
        }
    } else {
        while crowding
            .taken
            .last()
            .is_some_and(|(_, needed)| bar_w >= *needed)
        {
            crowding.taken.pop();
        }
    }
}

/// The title bar's parts that a step gives up, as wide as they were last laid out.
#[derive(Clone, Debug, Default, PartialEq)]
pub(crate) struct Widths {
    trail: f32,
    caret: f32,
    strip: f32,
    profile: f32,
    chips: f32,
    gathered: f32,
    runs: f32,
    shelf: f32,
}

/// A part of the title bar whose width a step frees.
#[derive(Clone, Copy)]
pub(crate) enum Part {
    /// What follows the waiting pill's count: its crumbs, if any, and its caret.
    Trail,
    Caret,
    Strip,
    Profile,
    /// The desktop chips, with how wide they stand gathered.
    Chips {
        gathered: f32,
    },
    Runs,
    Shelf,
}

impl Widths {
    /// The room `step` frees from a bar holding `contents`, when it is known before the step is
    /// taken: the part it removes and the gap before it, for the crumbs all of the pill's trail
    /// but its caret, and for gathering the desktops the runs chip and what the chips stand
    /// past their gathered width. Saying only the waiting number leaves something in the
    /// pill's place, so what it frees is measured after.
    fn freed_by(&self, step: GivenUp, contents: &Contents) -> Option<f32> {
        let gap = attn_ui::blocks::Inline::joined_gap();
        match step {
            GivenUp::Crumbs => Some((self.trail - self.caret).max(0.0)),
            GivenUp::Desktops => {
                let runs = if contents.runs { self.runs + gap } else { 0.0 };
                Some(runs + (self.chips - self.gathered).max(0.0))
            }
            GivenUp::Strip => Some(self.strip + gap),
            GivenUp::ProfileName => Some((self.profile - PROFILE_SHORT_W).max(0.0)),
            GivenUp::Chips => Some(self.chips + gap),
            GivenUp::Shelf => Some(self.shelf + gap),
            GivenUp::Nothing | GivenUp::PillWord => None,
        }
    }
}

/// `content` heard as `part` of the title bar, its width kept for the steps that give it up.
/// The middle's parts are heard before its end, in the same layout.
pub(crate) fn part(content: rune::El, part: Part) -> rune::El {
    content.on_measure(move |state: &mut State, [_, _, w, _]: [f32; 4]| {
        let widths = &mut state.window.queue_bar_room.widths;
        match part {
            Part::Trail => widths.trail = w,
            Part::Caret => widths.caret = w,
            Part::Strip => widths.strip = w,
            Part::Profile => widths.profile = w,
            Part::Chips { gathered } => {
                widths.chips = w;
                widths.gathered = gathered;
            }
            Part::Runs => widths.runs = w,
            Part::Shelf => widths.shelf = w,
        }
    })
}

/// The profile pill's widest while the title bar is crowded: its caret and the room around it,
/// with the name's first few letters.
pub(crate) const PROFILE_SHORT_W: f32 = 72.0;
