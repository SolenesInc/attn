use attn_testkit::World;

use super::harness_catalogs::{harnesses, serve};
use super::{accepting, open, press_action, rest, state, writes};

#[test]
fn each_opening_of_settings_reads_the_harnesses_once_and_writes_nothing() {
    let (daemon, stored) = accepting(&[]);
    serve(&daemon, &stored, harnesses());
    let mut world = World::start(daemon);
    open(&mut world);
    world.settle();
    assert_eq!(world.daemon.sent_of("delegation_preferences_get").len(), 1);
    assert!(world.daemon.sent_of("harness_models").is_empty());
    press_action(&mut world, attn_app::Action::UiOpenSettings);
    assert_eq!(state(&mut world)["open"], false);
    open(&mut world);
    world.settle();
    assert_eq!(world.daemon.sent_of("delegation_preferences_get").len(), 2);
    assert!(writes(&world).is_empty());
    rest(&mut world);
}
