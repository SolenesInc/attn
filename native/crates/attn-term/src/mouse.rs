//! What the pointer sends a program that tracks the mouse, by the rules the Tauri terminal
//! follows (`app/src/utils/ghosttyScroll.ts`).

/// The modes a program set that decide what the pointer reports.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub struct Modes {
    /// Any of X10, normal, button-event or any-event tracking.
    pub tracking: bool,
    /// DECSET 1003: motion reports even with no button held.
    pub any_event: bool,
    /// DECSET 1002: motion reports while a button is held.
    pub drag: bool,
    /// DECSET 1006: SGR coordinates rather than X10 bytes.
    pub sgr: bool,
    pub alternate_screen: bool,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Action {
    Press,
    Move,
    Release,
}

/// The button code of a pointer button: the primary 0, the middle 1, the secondary 2.
pub const PRIMARY: u8 = 0;
pub const MIDDLE: u8 = 1;
pub const SECONDARY: u8 = 2;
/// The code motion carries with no button held, so a program does not read it as a drag.
const NO_BUTTON: u8 = 3;
const WHEEL_UP: u8 = 64;
const WHEEL_DOWN: u8 = 65;
/// The most reports one wheel event sends, however far it turned.
const WHEEL_REPORTS: u32 = 5;

/// The modifier bits a report carries.
pub fn modifiers(shift: bool, alt: bool, ctrl: bool) -> u8 {
    u8::from(shift) * 4 + u8::from(alt) * 8 + u8::from(ctrl) * 16
}

/// The report for `action` of `button` over the zero-based `cell`. A release carries no
/// button; motion with none held carries [`NO_BUTTON`].
pub fn report(
    action: Action,
    button: Option<u8>,
    (col, row): (u16, u16),
    sgr: bool,
    mods: u8,
) -> String {
    let (col, row) = (u32::from(col) + 1, u32::from(row) + 1);
    let button = match action {
        Action::Release => NO_BUTTON,
        _ => button.unwrap_or(NO_BUTTON),
    };
    let code = u32::from(button) + u32::from(mods) + if action == Action::Move { 32 } else { 0 };
    if sgr {
        let end = if action == Action::Release { 'm' } else { 'M' };
        return format!("\x1b[<{code};{col};{row}{end}");
    }
    let byte = |v: u32| char::from_u32((32 + v).min(255)).expect("at most 255 is a char");
    format!("\x1b[M{}{}{}", byte(code), byte(col), byte(row))
}

/// Whether motion reports to the program: with no button held only under any-event
/// tracking, and with one held only when its press was reported here.
pub fn reports_move(modes: Modes, pressed: Option<u8>, held: bool) -> bool {
    if !held {
        return pressed.is_none() && modes.any_event;
    }
    pressed.is_some() && (modes.any_event || modes.drag)
}

/// What `lines` rows of wheel send: button 64 or 65 reports to a program that tracks the
/// mouse, arrow keys to one on the alternate screen that doesn't. Negative lines turn up.
pub fn wheel(lines: i32, cell: (u16, u16), modes: Modes) -> String {
    let count = lines.unsigned_abs().min(WHEEL_REPORTS) as usize;
    if !modes.tracking {
        return if lines < 0 { "\x1b[A" } else { "\x1b[B" }.repeat(count);
    }
    let button = if lines < 0 { WHEEL_UP } else { WHEEL_DOWN };
    report(Action::Press, Some(button), cell, modes.sgr, 0).repeat(count)
}

/// Wheel travel in rows, whole rows taken and the part of a row left kept for the next.
#[derive(Clone, Copy, Debug, Default, PartialEq)]
pub struct WheelRows(f32);

impl WheelRows {
    /// Adds `rows` and takes the whole rows, toward zero.
    pub fn take(&mut self, rows: f32) -> i32 {
        let total = self.0 + rows;
        let lines = if total.abs() < 1.0 {
            0.0
        } else {
            total.trunc()
        };
        self.0 = total - lines;
        lines as i32
    }
}
