//! Settings → GitHub hosts: the hosts attn found authenticated, or why it does not look.

use attn_ui::Tokens;
use attn_ui::blocks::{Block, Notice, Prose, Section, Stack};
use attn_ui::system::{Type, words};

use crate::State;

pub(super) fn page(state: &State, t: &Tokens) -> Vec<Block> {
    let github = &state.facts.github;
    let section = Section::new("settings.block.github", "GitHub Hosts").description(
        "Authenticated hosts used by PR actions, review lookup, and repository metadata.",
    );
    let section = if let Some(reason) = github.polling_off_reason.as_deref() {
        section.push(Notice::warn("settings.github.polling-off", reason).build(t))
    } else if github.hosts.is_empty() {
        section
            .push(Prose::empty("settings.github.none", "No authenticated hosts detected.").build(t))
            .push(hint(t))
    } else {
        let hosts = Stack::tight()
            .key("settings.github.hosts")
            .role("list")
            .label("GitHub hosts")
            .children(
                github
                    .hosts
                    .iter()
                    .map(|host| words(host.clone(), Type::Mono, t.text_primary).role("listitem")),
            );
        section
            .push(Block::custom("settings.github.hosts.block", hosts))
            .push(hint(t))
    };
    vec![section.build(t)]
}

fn hint(t: &Tokens) -> Block {
    Prose::hint(
        "settings.github.hint",
        "Add hosts with `gh auth login --hostname <host>`.",
    )
    .build(t)
}
