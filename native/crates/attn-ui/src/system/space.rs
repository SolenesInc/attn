/// A step of the spacing scale. Only `attn-ui` names a step: no view makes one or passes one.
#[derive(Clone, Copy, Debug, PartialEq)]
pub(crate) struct Space(f32);

#[allow(dead_code)]
impl Space {
    pub(crate) const S0: Space = Space(0.0);
    pub(crate) const S2: Space = Space(2.0);
    pub(crate) const S4: Space = Space(4.0);
    pub(crate) const S6: Space = Space(6.0);
    pub(crate) const S8: Space = Space(8.0);
    pub(crate) const S12: Space = Space(12.0);
    pub(crate) const S16: Space = Space(16.0);
    pub(crate) const S24: Space = Space(24.0);
    pub(crate) const S32: Space = Space(32.0);

    pub(crate) const fn pt(self) -> f32 {
        self.0
    }
}
