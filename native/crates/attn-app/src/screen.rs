use attn_term::Cells;
use attn_ui::colors::TerminalColors;
use rune::{Compositor, Frame, Runtime, RuntimeCore};

use crate::State;
use crate::markdown::ids;

/// Draws the window, with each terminal's cells in its tile's slot and, while the overview shows,
/// in its miniature's.
#[derive(Default)]
pub struct Screens {
    cells: Cells,
    small: Cells,
    miniatures: Option<Miniatures>,
}

/// The terminals the overview draws small, kept until the closed overview has left the screen.
struct Miniatures {
    terminals: Vec<String>,
    scale: f32,
}

impl Miniatures {
    fn marks(&self) -> impl Iterator<Item = u32> {
        self.terminals
            .iter()
            .map(|id| crate::terminal::miniature_mark(id))
    }
}

/// The terminals whose slots the last layout holds, fitted for a display scale other than the
/// window's. A shelved terminal keeps its fit until its tile returns and lays out again.
fn mounted_for_other_scale(rt: &Runtime<State>) -> impl Iterator<Item = &str> {
    rt.state()
        .terminals
        .fitted_for_other_scale(rt.env.scale)
        .filter(|id| rt.find(&crate::terminal::slot_key(id)).is_some())
}

/// Gives the terminals the window's theme and display scale, and returns the theme.
fn sync_terminals(rt: &mut Runtime<State>) -> TerminalColors {
    let theme = crate::colors(&rt.state().facts.settings, rt.env.system_dark).terminal;
    let scale = rt.env.scale;
    let pending_layout = rt.has_pending_input();
    let refit: Vec<String> = if pending_layout {
        Vec::new()
    } else {
        mounted_for_other_scale(rt).map(str::to_owned).collect()
    };
    let invalidate = {
        let state = rt.state_mut();
        state.terminals.set_theme(theme);
        if pending_layout {
            false
        } else {
            state.terminals.set_scale(scale, &refit);
            let px_per_col = state.terminals.metrics().cell_points(scale).0;
            match state.window.desk.reported_area.as_mut() {
                Some(area) if area.px_per_col != px_per_col => {
                    area.px_per_col = px_per_col;
                    true
                }
                _ => false,
            }
        }
    };
    if invalidate {
        rt.invalidate();
    }
    theme
}

fn screens_drawn(rt: &mut Runtime<State>, redrawn: bool) {
    rt.state().terminals.echoes_drawn();
    ids_drawn(rt);
    if redrawn {
        rt.a11y_text_changed();
    }
}

/// Follows what a frame the window drew shows of the ids attn knows: asks the daemon about those
/// nothing it holds names, and takes in where the ones in words are. Returns whether that changed
/// what the window draws.
pub fn ids_drawn(rt: &mut Runtime<State>) -> bool {
    if rt.state().terminals.has_unresolved_ids() {
        rt.update_if(|state, tx| {
            crate::terminal::ask_about_unresolved(state, tx);
            false
        });
    }
    if !ids::followed(rt.state()) {
        return false;
    }
    let links = rt.links(ids::HREF);
    rt.update_if(|state, _| ids::drawn(state, links))
}

impl Screens {
    /// A frame of a window with no GPU, as a wire test's world draws one: it takes the
    /// terminals' screens as `render` does and uploads nothing.
    pub fn draw_without_gpu(&mut self, rt: &mut Runtime<State>) {
        sync_terminals(rt);
        let redrawn = rt.state().terminals.undrawn();
        rt.state_mut().terminals.grids().for_each(drop);
        screens_drawn(rt, redrawn);
    }
}

impl Compositor<State> for Screens {
    fn features(&self) -> wgpu::Features {
        Cells::FEATURES
    }

    fn redraws(&self, rt: &Runtime<State>) -> bool {
        rt.state().terminals.undrawn()
            || (!rt.has_pending_input() && mounted_for_other_scale(rt).next().is_some())
    }

    fn render(&mut self, f: &mut Frame<'_, State>) {
        let (device, queue, key) = (f.device, f.queue, f.device_key());
        let scale = f.rt.env.scale;
        let theme = sync_terminals(f.rt);
        let redrawn = f.rt.state().terminals.undrawn();
        let terminals = &mut f.rt.state_mut().terminals;
        let metrics = terminals.metrics();
        self.cells.prepare(
            device,
            queue,
            key,
            scale,
            None,
            metrics,
            theme,
            terminals.grids(),
        );
        let open = crate::desktop::miniatures(f.rt.state());
        let closing = open.is_none();
        if let Some((terminals, scale)) = open {
            self.miniatures = Some(Miniatures { terminals, scale });
        }
        if let Some(miniatures) = &self.miniatures {
            let terminals = &f.rt.state().terminals;
            self.small.prepare(
                device,
                queue,
                key,
                scale,
                Some(miniatures.scale),
                metrics,
                theme,
                terminals.miniatures(&miniatures.terminals),
            );
        }
        let clear = f.rt.clear_color();
        let core: &mut RuntimeCore = f.rt;
        f.ui.prepare(device, queue, &mut core.text.atlas, &core.instances, f.size);
        let (cells, small) = (&mut self.cells, &mut self.small);
        let marks: Vec<u32> = self.miniatures.iter().flat_map(Miniatures::marks).collect();
        let mut drew_small = false;
        f.ui.draw_with(f.encoder, f.target, clear, &mut |pass, site| {
            if marks.contains(&site.mark) {
                drew_small = true;
                small.draw(device, pass, site);
            } else {
                cells.draw(device, pass, site);
            }
        });
        if closing && !drew_small && self.miniatures.take().is_some() {
            self.small = Cells::default();
        }
        screens_drawn(f.rt, redrawn);
    }
}
