use rune::Runtime;

/// Draws a frame of `rt`, `dt` seconds on, as a window does, and fails the test when a backdrop
/// blur in it blurs only the layer it sits in: an ancestor that fades or draws as one layer
/// leaves it no window to blur, so the person sees a dim backdrop with no blur until the layer
/// goes.
pub(crate) fn draw<S: 'static>(rt: &mut Runtime<S>, dt: f64) -> bool {
    let awake = rt.frame(dt);
    assert!(
        rt.layered_backdrop_blurs.is_empty(),
        "a backdrop blur sits inside a layer or under a fading ancestor, so it blurs nothing behind it: {:?}. Draw the blurred element as a layer of its own, as attn_ui::Dialog draws its backdrop",
        rt.layered_backdrop_blurs
    );
    awake
}
