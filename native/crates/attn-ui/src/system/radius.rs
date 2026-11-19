/// The corners a surface has, by what the surface is.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Radius {
    Tag,
    Control,
    Card,
    Panel,
    Round,
}

impl Radius {
    pub(crate) const fn pt(self) -> f32 {
        match self {
            Radius::Tag => 4.0,
            Radius::Control => 6.0,
            Radius::Card => 8.0,
            Radius::Panel => 12.0,
            Radius::Round => 999.0,
        }
    }
}
