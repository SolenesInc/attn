use attn_term::Cells;
use attn_ui::colors::TerminalColors;
use rune::{Compositor, Frame, Runtime, RuntimeCore};

use crate::State;

/// Draws the window, with each terminal's cells in its tile's slot.
#[derive(Default)]
pub struct Screens {
    cells: Cells,
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
        state.terminals.set_theme(theme, &state.daemon);
        if pending_layout {
            false
        } else {
            state.terminals.set_scale(scale, &refit, &state.daemon);
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
    if redrawn {
        rt.a11y_text_changed();
    }
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
        self.cells
            .prepare(device, queue, key, scale, metrics, theme, terminals.grids());
        let clear = f.rt.clear_color();
        let core: &mut RuntimeCore = f.rt;
        f.ui.prepare(device, queue, &mut core.text.atlas, &core.instances, f.size);
        let cells = &mut self.cells;
        f.ui.draw_with(f.encoder, f.target, clear, &mut |pass, site| {
            cells.draw(device, pass, site);
        });
        screens_drawn(f.rt, redrawn);
    }
}
