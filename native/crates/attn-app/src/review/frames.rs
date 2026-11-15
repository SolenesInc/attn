//! The frames a review document can open in, and the diagrams and arrows each shows.

use attn_protocol::{ReviewBundle, ReviewFlow, ReviewFlowMessage, ReviewStop};

#[derive(Clone, Copy, Debug, Default, PartialEq, Eq, Hash)]
pub(crate) enum Frame {
    #[default]
    Brief,
    Walkthrough,
    Map,
}

impl Frame {
    pub(crate) const ALL: [(Frame, &'static str, &'static str); 3] = [
        (Frame::Brief, "1", "Brief"),
        (Frame::Walkthrough, "2", "Walkthrough"),
        (Frame::Map, "3", "Map"),
    ];

    fn named(name: &str) -> Option<Frame> {
        match name {
            "brief" => Some(Frame::Brief),
            "walkthrough" => Some(Frame::Walkthrough),
            "map" => Some(Frame::Map),
            _ => None,
        }
    }
}

#[derive(Clone, Copy, Debug, Default, PartialEq, Eq, Hash)]
pub(crate) enum Side {
    Before,
    #[default]
    After,
}

impl Side {
    pub(crate) fn name(self) -> &'static str {
        match self {
            Side::Before => "before",
            Side::After => "after",
        }
    }

    pub(crate) fn other(self) -> Side {
        match self {
            Side::Before => Side::After,
            Side::After => Side::Before,
        }
    }
}

fn drawn(bundle: &ReviewBundle) -> impl Iterator<Item = &ReviewFlow> {
    bundle.flows.iter().filter(|f| f.drawn)
}

fn has_stop(flow: &ReviewFlow) -> bool {
    flow.messages.iter().any(|m| m.stop_line.is_some())
}

/// The frames a document can fill: Walkthrough needs a stop, Map a drawn sequence diagram with
/// a message tied to a stop. `built` leaves out the frames the reader cannot draw yet.
pub(crate) fn offered(bundle: &ReviewBundle, built: &[Frame]) -> Vec<Frame> {
    let mut frames = vec![Frame::Brief];
    if !bundle.stops.is_empty() {
        frames.push(Frame::Walkthrough);
    }
    if drawn(bundle).any(has_stop) {
        frames.push(Frame::Map);
    }
    frames.retain(|f| built.contains(f));
    frames
}

/// The frame the document opens in: the one it picks, when it can fill it.
pub(crate) fn opening(bundle: &ReviewBundle, built: &[Frame]) -> Frame {
    Frame::named(&bundle.frame)
        .filter(|f| offered(bundle, built).contains(f))
        .unwrap_or(Frame::Brief)
}

/// Whether the document has a diagram before the change and one after it, so `b` switches.
pub(crate) fn has_sides(bundle: &ReviewBundle) -> bool {
    let side = |name: &str| drawn(bundle).any(|f| f.side.as_deref() == Some(name));
    side("before") && side("after")
}

/// Whether the diagram `flow` shows on `side`: every drawn diagram does, except the other
/// side's when the document has both.
pub(crate) fn shows(bundle: &ReviewBundle, flow: &ReviewFlow, side: Side) -> bool {
    flow.drawn && (!has_sides(bundle) || flow.side.as_deref().is_none_or(|s| s == side.name()))
}

/// A message the Map's cursor stands on: one tied to a stop.
pub(crate) struct MapStop<'a> {
    pub(crate) flow: &'a ReviewFlow,
    pub(crate) message: &'a ReviewFlowMessage,
    pub(crate) stop: usize,
}

/// The messages tied to a stop in the diagrams `side` shows, in order.
pub(crate) fn map_stops(bundle: &ReviewBundle, side: Side) -> Vec<MapStop<'_>> {
    bundle
        .flows
        .iter()
        .filter(|f| shows(bundle, f, side))
        .flat_map(|flow| {
            flow.messages.iter().filter_map(move |message| {
                let line = message.stop_line?;
                let stop = bundle.stops.iter().position(|s| s.heading_line == line)?;
                Some(MapStop {
                    flow,
                    message,
                    stop,
                })
            })
        })
        .collect()
}

/// Where the Map stands, held by what the message says and the stop it shows, so an edit of the
/// diagram or the document above it keeps it.
#[derive(Clone, Debug, Default, PartialEq, Eq, Hash)]
pub(crate) struct MapPlace {
    text: String,
    stop: Option<(String, i64)>,
    index: usize,
}

fn stop_id(stop: &ReviewStop) -> (String, i64) {
    (stop.path.clone(), stop.from)
}

pub(crate) fn map_place(bundle: &ReviewBundle, list: &[MapStop], index: usize) -> MapPlace {
    let at = list.get(index);
    MapPlace {
        text: at.map(|a| a.message.text.clone()).unwrap_or_default(),
        stop: at.map(|a| stop_id(&bundle.stops[a.stop])),
        index,
    }
}

/// The index in `list` of the message `place` names: the nearest saying the same of the same
/// stop, else showing the same stop, else saying the same, else the same index.
pub(crate) fn map_index(bundle: &ReviewBundle, list: &[MapStop], place: &MapPlace) -> usize {
    let shows = |a: &MapStop| Some(stop_id(&bundle.stops[a.stop])) == place.stop;
    let says = |a: &MapStop| a.message.text == place.text;
    let nearest = |matches: &dyn Fn(&MapStop) -> bool| {
        list.iter()
            .enumerate()
            .filter(|(_, a)| matches(a))
            .map(|(i, _)| i)
            .min_by_key(|i| i.abs_diff(place.index))
    };
    nearest(&|a| says(a) && shows(a))
        .or_else(|| nearest(&shows))
        .or_else(|| nearest(&says))
        .unwrap_or(place.index.min(list.len().saturating_sub(1)))
}
