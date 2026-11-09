use super::table::{FLAGS, VERBS, Verb};
use attn::go::flag::{Flag, FlagDefault, FlagError, FlagSet, Value};

#[derive(Clone, Copy)]
pub enum FlagKind {
    Bool,
    Str,
    Int,
    Repeated,
}

pub struct Flags {
    pub verb: &'static Verb,
    fs: FlagSet<'static>,
    pub caller: Option<(String, String)>,
}

pub enum ParseStop {
    Help,
    Invalid(String),
}

pub fn verb_named(name: &str) -> &'static Verb {
    VERBS
        .iter()
        .find(|verb| verb.name == name)
        .unwrap_or_else(|| panic!("unknown seed verb: {name}"))
}

pub fn verb_has_flag(verb: &Verb, name: &str) -> bool {
    name == "profile" || name == "session" || verb.flags.contains(&name)
}

impl Flags {
    pub fn new(name: &str) -> Self {
        let verb = verb_named(name);
        let definitions: Vec<Flag<'static>> = FLAGS
            .iter()
            .filter(|definition| verb_has_flag(verb, definition.name))
            .map(|definition| Flag {
                name: definition.name,
                default: match definition.kind {
                    FlagKind::Bool => FlagDefault::Bool(false),
                    FlagKind::Str => FlagDefault::String(""),
                    FlagKind::Int => FlagDefault::Int(0),
                    FlagKind::Repeated => FlagDefault::Repeated,
                },
                usage: "",
            })
            .collect();
        Self {
            verb,
            fs: FlagSet::new("", &definitions),
            caller: None,
        }
    }

    pub fn parse(&mut self, args: &[String]) -> Result<Vec<String>, ParseStop> {
        let mut positionals = Vec::new();
        let mut rest = args.to_vec();
        loop {
            match self.fs.parse(&rest) {
                Ok(()) => {}
                Err(FlagError::Help) => return Err(ParseStop::Help),
                Err(FlagError::Invalid(message)) => return Err(ParseStop::Invalid(message)),
            }
            rest = self.fs.args().to_vec();
            if rest.is_empty() {
                return Ok(positionals);
            }
            positionals.push(rest.remove(0));
        }
    }

    pub fn string(&self, name: &str) -> &str {
        match self.fs.get(name) {
            Some(Value::String(value)) => value,
            _ => "",
        }
    }

    pub fn on(&self, name: &str) -> bool {
        matches!(self.fs.get(name), Some(Value::Bool(true)))
    }

    pub fn int(&self, name: &str) -> i64 {
        match self.fs.get(name) {
            Some(Value::Int(value)) => *value,
            _ => 0,
        }
    }

    pub fn repeated(&self, name: &str) -> &[String] {
        match self.fs.get(name) {
            Some(Value::Repeated(values)) => values,
            _ => &[],
        }
    }

    pub fn was_set(&self, name: &str) -> bool {
        self.fs.is_set(name)
    }
}
