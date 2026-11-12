use attn_app::{State, view};
use rune::{Env, Runtime};

#[test]
fn the_window_shows_the_placeholder() {
    let env = Env {
        system_dark: true,
        scale: 2.0,
    };
    let mut runtime = Runtime::new(State, view, (960.0, 640.0), env);
    runtime.frame(0.0);
    assert!(runtime.find("placeholder").is_some());
}
