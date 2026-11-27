#[rustfmt::skip]
mod generated;
#[rustfmt::skip]
pub mod go_values;
#[rustfmt::skip]
mod view_parts;
pub use view_parts::ViewParts;
pub mod session_id;

pub use generated::*;

impl Harness {
    /// Whether a launch may try the harness: installed, or still being checked.
    #[must_use]
    pub fn available(&self) -> bool {
        self.installation != "missing"
    }
}
