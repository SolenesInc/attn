use attn_protocol::{
    PartyView, SeedAuthorization, SeedAuthorizationResultMessage, SeedAuthorizationUpdate,
};
use attn_testkit::{Request, Target, World};

use super::*;

fn authorized(update: Option<SeedAuthorizationUpdate>) -> Seed {
    with(seed("s-auth11", "Cache store migration"), |s| {
        s.authorization = Some(SeedAuthorization {
            text: "Land on main once the checks pass.".into(),
            set_by: ActorView {
                name: "the user".into(),
                r#ref: "user".into(),
            },
            set_at: "2026-11-13T09:00:00Z".into(),
            update,
        })
    })
}

fn waiting(typed_at: Option<&str>) -> SeedAuthorizationUpdate {
    SeedAuthorizationUpdate {
        recipient: PartyView {
            name: "Builder".into(),
            r#ref: "session:b1".into(),
            ..PartyView::default()
        },
        typed_at: typed_at.map(str::to_owned),
        needs_approval: Some(true),
    }
}

fn says(world: &World, key: &str) -> String {
    world
        .screen()
        .find(Target::key(key))
        .and_then(|n| n.name)
        .unwrap_or_default()
}

fn sent(world: &World) -> Vec<(Option<String>, Option<bool>)> {
    world
        .daemon
        .sent_of("seed_authorization")
        .iter()
        .map(|r| (r["text"].as_str().map(str::to_owned), r["clear"].as_bool()))
        .collect()
}

fn answer(world: &mut World, error: Option<&str>) {
    let request: Request = world
        .daemon
        .sent_of("seed_authorization")
        .last()
        .unwrap()
        .clone();
    world.daemon.reply_to(
        &request,
        SeedAuthorizationResultMessage {
            request_id: request["request_id"].as_str().unwrap().into(),
            success: error.is_none(),
            error: error.map(str::to_owned),
            ..SeedAuthorizationResultMessage::new()
        },
    );
    world.settle();
}

fn focused_key(world: &World) -> Option<String> {
    focused(world).and_then(|n| n.key)
}

#[test]
fn shows_the_words_who_set_them_and_whether_the_tender_has_them_as_pushes_come() {
    let mut world = open_garden(vec![authorized(Some(waiting(None)))]);
    open_row(&mut world, "Cache store migration");

    assert_eq!(
        says(&world, "garden.authorization.text"),
        "Land on main once the checks pass."
    );
    let meta = says(&world, "garden.authorization.meta");
    assert!(meta.starts_with("set by you "), "{meta}");
    assert!(
        meta.ends_with(" · waiting for Builder, which needs approval"),
        "{meta}"
    );

    push(
        &mut world,
        vec![with(
            authorized(Some(waiting(Some("2026-11-13T09:05:00Z")))),
            |s| s.rev = 2,
        )],
    );
    let meta = says(&world, "garden.authorization.meta");
    assert!(meta.contains(" · typed into Builder "), "{meta}");
    assert!(rests(&mut world));
}

#[test]
fn sets_the_words_from_the_keyboard_and_keeps_shift_enter_as_a_new_line() {
    let mut world = open_garden(vec![seed("s-auth11", "Cache store migration")]);
    open_row(&mut world, "Cache store migration");
    assert_eq!(
        says(&world, "garden.authorization.empty"),
        "No authorization from the user for this task."
    );

    click(&mut world, "Set");
    assert_eq!(
        focused_key(&world).as_deref(),
        Some("garden.authorization.field")
    );
    key(&mut world, Key::Enter);
    assert_eq!(
        says(&world, "garden.authorization.error"),
        "Write the steps agents take on this task beyond the project's guidance, and where they stop."
    );
    world.gesture(|app| app.type_text("Land on main."));
    key_with(
        &mut world,
        Key::Enter,
        Mods {
            shift: true,
            ..Mods::default()
        },
    );
    world.gesture(|app| app.type_text("Ask before releasing."));
    assert!(sent(&world).is_empty());
    key(&mut world, Key::Enter);
    assert_eq!(
        sent(&world),
        vec![(Some("Land on main.\nAsk before releasing.".into()), None)]
    );
    answer(&mut world, None);

    assert!(
        world
            .screen()
            .find(Target::key("garden.authorization.field"))
            .is_none()
    );
    assert_eq!(
        focused_key(&world).as_deref(),
        Some("garden.authorization.edit")
    );
    assert!(rests(&mut world));
}

#[test]
fn clears_the_words_and_leaves_the_keyboard_on_set() {
    let mut world = open_garden(vec![authorized(None)]);
    open_row(&mut world, "Cache store migration");

    click(&mut world, "Clear");
    assert_eq!(sent(&world), vec![(None, Some(true))]);
    answer(&mut world, None);
    push(
        &mut world,
        vec![with(seed("s-auth11", "Cache store migration"), |s| {
            s.rev = 2
        })],
    );

    assert_eq!(focused(&world).and_then(|n| n.name).as_deref(), Some("Set"));
    assert!(rests(&mut world));
}

#[test]
fn keeps_the_words_and_shows_a_refusal_waiting_for_it_and_escape_sends_nothing() {
    let mut world = open_garden(vec![authorized(None)]);
    open_row(&mut world, "Cache store migration");

    click(&mut world, "Change");
    world.gesture(|app| app.type_text("Ask me first."));
    key(&mut world, Key::Enter);
    key(&mut world, Key::Escape);
    key(&mut world, Key::Enter);
    assert_eq!(sent(&world).len(), 1);
    assert!(
        world
            .screen()
            .find(Target::key("garden.authorization.field"))
            .is_some()
    );
    answer(&mut world, Some("only the user sets an authorization"));
    assert_eq!(
        says(&world, "garden.authorization.error"),
        "only the user sets an authorization"
    );
    assert_eq!(
        world
            .screen()
            .get(Target::key("garden.authorization.field"))
            .value
            .as_deref(),
        Some("Ask me first.")
    );

    key(&mut world, Key::Escape);
    assert!(
        world
            .screen()
            .find(Target::key("garden.authorization.field"))
            .is_none()
    );
    assert_eq!(sent(&world).len(), 1);
    assert!(rests(&mut world));
}
