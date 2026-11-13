use serde_json::Value;

/// An element a test names the way a person or an assistive tool would: by its key, or by
/// its role and accessible name.
#[derive(Clone, Debug, PartialEq, Eq)]
pub enum Target {
    Key(String),
    Role { role: String, name: Option<String> },
}

impl Target {
    pub fn key(key: impl Into<String>) -> Target {
        Target::Key(key.into())
    }

    /// Any element with this role, until `name` narrows it.
    pub fn role(role: impl Into<String>) -> Target {
        Target::Role {
            role: role.into(),
            name: None,
        }
    }

    pub fn name(self, name: impl Into<String>) -> Target {
        match self {
            Target::Role { role, .. } => Target::Role {
                role,
                name: Some(name.into()),
            },
            Target::Key(_) => panic!("a key names one element already; name narrows a role"),
        }
    }

    fn matches(&self, node: &Node) -> bool {
        match self {
            Target::Key(key) => node.key.as_deref() == Some(key),
            Target::Role { role, name } => {
                node.role == *role
                    && name
                        .as_ref()
                        .is_none_or(|name| node.name.as_ref() == Some(name))
            }
        }
    }
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Node {
    pub role: String,
    pub key: Option<String>,
    pub name: Option<String>,
    pub value: Option<String>,
    pub description: Option<String>,
    /// An outline row's depth, 1 at the top.
    pub level: Option<u64>,
    /// What assistive technology hears about the element's state: `focused`, `disabled`,
    /// `checked`, `selected` and the like.
    pub states: Vec<String>,
}

impl Node {
    pub fn is(&self, state: &str) -> bool {
        self.states.iter().any(|s| s == state)
    }
}

/// The window's accessibility tree at one moment.
pub struct Screen(pub(crate) Value);

impl Screen {
    pub fn find(&self, target: Target) -> Option<Node> {
        self.nodes().into_iter().find(|node| target.matches(node))
    }

    /// The element, or a panic that prints the whole tree.
    pub fn get(&self, target: Target) -> Node {
        self.find(target.clone())
            .unwrap_or_else(|| panic!("no element matches {target:?} on this screen:\n{self}"))
    }

    pub fn all(&self, target: Target) -> Vec<Node> {
        self.nodes()
            .into_iter()
            .filter(|node| target.matches(node))
            .collect()
    }

    /// The element's own children in order, or a panic that prints the whole tree.
    pub fn children(&self, target: Target) -> Vec<Node> {
        self.values()
            .into_iter()
            .find(|value| target.matches(&node(value)))
            .unwrap_or_else(|| panic!("no element matches {target:?} on this screen:\n{self}"))
            .get("children")
            .and_then(Value::as_array)
            .map(|children| children.iter().map(node).collect())
            .unwrap_or_default()
    }

    /// Every element under the element, in reading order, or a panic that prints the whole
    /// tree.
    pub fn descendants(&self, target: Target) -> Vec<Node> {
        let top = self
            .values()
            .into_iter()
            .find(|value| target.matches(&node(value)))
            .unwrap_or_else(|| panic!("no element matches {target:?} on this screen:\n{self}"));
        Screen(top.clone()).nodes().into_iter().skip(1).collect()
    }

    /// The element that has the keyboard, if any.
    pub fn focused(&self) -> Option<Node> {
        self.nodes().into_iter().find(|node| node.is("focused"))
    }

    fn nodes(&self) -> Vec<Node> {
        self.values().into_iter().map(node).collect()
    }

    fn values(&self) -> Vec<&Value> {
        let mut values = Vec::new();
        let mut stack = vec![&self.0];
        while let Some(value) = stack.pop() {
            values.push(value);
            if let Some(children) = value.get("children").and_then(Value::as_array) {
                stack.extend(children.iter().rev());
            }
        }
        values
    }
}

fn node(value: &Value) -> Node {
    let field = |name: &str| value.get(name).and_then(Value::as_str).map(str::to_owned);
    Node {
        role: field("role").unwrap_or_default(),
        key: field("key"),
        name: field("name"),
        value: field("value"),
        description: field("description"),
        level: value.get("level").and_then(Value::as_u64),
        states: value
            .get("states")
            .and_then(Value::as_array)
            .map(|states| {
                states
                    .iter()
                    .filter_map(Value::as_str)
                    .map(str::to_owned)
                    .collect()
            })
            .unwrap_or_default(),
    }
}

impl std::fmt::Display for Screen {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        write!(
            f,
            "{}",
            serde_json::to_string_pretty(&self.0).unwrap_or_default()
        )
    }
}
