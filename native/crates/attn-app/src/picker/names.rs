const ADJECTIVES: [&str; 50] = [
    "amber", "bashful", "brisk", "chaotic", "chipper", "cosmic", "cranky", "crispy", "dapper",
    "dizzy", "drowsy", "feral", "fluffy", "frosty", "fussy", "gloomy", "grumpy", "hasty", "jaunty",
    "jolly", "lanky", "lucky", "moody", "nimble", "peppy", "plucky", "prickly", "quirky", "rowdy",
    "rugged", "rustic", "salty", "scruffy", "sleepy", "smug", "snappy", "sneaky", "soggy", "spicy",
    "sturdy", "sulky", "sunny", "surly", "tipsy", "unruly", "velvet", "wary", "whimsy", "wily",
    "zesty",
];

const NOUNS: [&str; 44] = [
    "alpaca",
    "axolotl",
    "badger",
    "beetle",
    "bison",
    "capybara",
    "chinchilla",
    "dingo",
    "ferret",
    "gecko",
    "gerbil",
    "gopher",
    "hedgehog",
    "heron",
    "ibex",
    "iguana",
    "kestrel",
    "lemur",
    "llama",
    "magpie",
    "manatee",
    "marmot",
    "meerkat",
    "mongoose",
    "moose",
    "narwhal",
    "newt",
    "ocelot",
    "opossum",
    "otter",
    "pangolin",
    "pelican",
    "pigeon",
    "platypus",
    "puffin",
    "quokka",
    "raccoon",
    "seal",
    "tapir",
    "toucan",
    "walrus",
    "weasel",
    "wombat",
    "yak",
];

/// Random draws in [0, 1), as the worktree name generator consumes them.
pub(crate) trait Draw {
    fn draw(&mut self) -> f64;
}

/// The process's own randomness, seeded by the standard library.
pub(crate) struct SystemDraw;

impl Draw for SystemDraw {
    fn draw(&mut self) -> f64 {
        use std::hash::{BuildHasher, Hasher};
        let bits = std::collections::hash_map::RandomState::new()
            .build_hasher()
            .finish();
        (bits >> 11) as f64 / (1u64 << 53) as f64
    }
}

fn pick<'a>(words: &[&'a str], random: &mut impl Draw) -> &'a str {
    words[(random.draw() * words.len() as f64) as usize % words.len()]
}

/// A git-safe `adjective-noun` branch name that is not in `taken`: fifty draws, then the
/// last draw with the first free numeric suffix.
pub(crate) fn worktree_name(taken: &[String], random: &mut impl Draw) -> String {
    let free = |name: &str| !taken.iter().any(|t| t == name);
    for _ in 0..50 {
        let name = format!("{}-{}", pick(&ADJECTIVES, random), pick(&NOUNS, random));
        if free(&name) {
            return name;
        }
    }
    let base = format!("{}-{}", pick(&ADJECTIVES, random), pick(&NOUNS, random));
    (2..)
        .map(|suffix| format!("{base}-{suffix}"))
        .find(|name| free(name))
        .expect("a suffix is free")
}

#[cfg(test)]
mod tests {
    use super::*;

    struct Sequence(Vec<f64>, usize);

    impl Draw for Sequence {
        fn draw(&mut self) -> f64 {
            let value = self.0[self.1 % self.0.len()];
            self.1 += 1;
            value
        }
    }

    #[test]
    fn a_name_is_a_git_safe_adjective_and_noun() {
        for _ in 0..100 {
            let name = worktree_name(&[], &mut SystemDraw);
            let (adjective, noun) = name.split_once('-').expect("adjective-noun");
            assert!(
                ADJECTIVES.contains(&adjective) && NOUNS.contains(&noun),
                "{name}"
            );
        }
    }

    #[test]
    fn a_name_is_never_one_already_taken() {
        let taken: Vec<String> = vec!["amber-alpaca".into()];
        let mut first_then_second = Sequence(vec![0.0, 0.0, 0.0, 0.03], 0);
        assert_eq!(
            worktree_name(&taken, &mut first_then_second),
            "amber-axolotl"
        );
    }

    #[test]
    fn draws_that_keep_colliding_fall_back_to_a_numeric_suffix() {
        let mut always_first = Sequence(vec![0.0], 0);
        assert_eq!(
            worktree_name(&["amber-alpaca".into()], &mut always_first),
            "amber-alpaca-2"
        );
        assert_eq!(
            worktree_name(
                &["amber-alpaca".into(), "amber-alpaca-2".into()],
                &mut always_first
            ),
            "amber-alpaca-3"
        );
    }

    #[test]
    fn names_spread_across_the_word_lists() {
        let names: std::collections::HashSet<String> = (0..200)
            .map(|_| worktree_name(&[], &mut SystemDraw))
            .collect();
        assert!(names.len() > 100, "{} distinct of 200", names.len());
    }
}
