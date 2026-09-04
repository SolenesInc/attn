export type PromptField = { name: string; kind: string; trim_space?: boolean };
export type PromptNode = {
  kind: string;
  source?: string;
  field?: PromptField;
  condition?: { field: PromptField; test: string };
  children?: PromptNode[];
  bindings?: { name: string; node: PromptNode }[];
  keep_final_newline?: boolean;
  separator?: string;
};
export type PromptCatalog = {
  version: number;
  recipients: { id: string; events: { id: string; body: PromptNode }[] }[];
  sources: Record<string, string>;
};

// Match Go strings.TrimSpace, including NEL and excluding the JavaScript BOM.
function trim(text: string): string {
  return text.replace(
    /^[\u0009-\u000d \u0085\u00a0\u1680\u2000-\u200a\u2028\u2029\u202f\u205f\u3000]+|[\u0009-\u000d \u0085\u00a0\u1680\u2000-\u200a\u2028\u2029\u202f\u205f\u3000]+$/g,
    "",
  );
}

export function renderCatalog(
  catalog: PromptCatalog,
  recipient: string,
  event: string,
  values: Record<string, string>,
): string {
  if (catalog.version !== 1)
    throw new Error("Unsupported prompt catalog version");
  const definition = catalog.recipients
    .find((r) => r.id === recipient)
    ?.events.find((e) => e.id === event);
  if (!definition) throw new Error(`Unknown prompt: ${recipient}/${event}`);
  const own = (name: string) =>
    Object.prototype.hasOwnProperty.call(values, name);
  const known = new Set<string>();
  function fields(node: PromptNode): void {
    if (node.field) known.add(node.field.name);
    if (node.condition) {
      const field = node.condition.field;
      known.add(field.name);
      if (
        field.kind === "flag" &&
        own(field.name) &&
        !["true", "false"].includes(values[field.name]!)
      )
        throw new Error(`Flag ${field.name} needs true or false`);
    }
    for (const binding of node.bindings ?? []) fields(binding.node);
    for (const child of node.children ?? []) fields(child);
  }
  fields(definition.body);
  for (const name of Object.keys(values))
    if (!known.has(name)) throw new Error(`Unknown prompt input: ${name}`);
  function render(node: PromptNode): string {
    if (node.kind === "input" && node.field) {
      if (!own(node.field.name))
        throw new Error(`Missing prompt input: ${node.field.name}`);
      const value = values[node.field.name]!;
      return node.field.trim_space ? trim(value) : value;
    }
    if ((node.kind === "when" || node.kind === "choose") && node.condition) {
      const { field, test } = node.condition;
      const value = values[field.name] ?? "";
      const selected =
        test === "enabled" ? value === "true" : trim(value) !== "";
      if (node.kind === "when" && !selected) return "";
      return render(node.children![selected ? 0 : 1]!);
    }
    if (node.kind === "trim") return trim(render(node.children![0]!));
    if (node.kind === "compose" || node.kind === "join") {
      const children = (node.children ?? []).map(render);
      return node.kind === "compose"
        ? children.filter(Boolean).join("\n\n")
        : children.join(node.separator ?? "");
    }
    if (node.kind !== "text" || !node.source)
      throw new Error(`Unsupported prompt node: ${node.kind}`);
    let source = catalog.sources[node.source];
    if (source === undefined)
      throw new Error(`Missing prompt source: ${node.source}`);
    if (!node.keep_final_newline && source.endsWith("\n"))
      source = source.slice(0, -1);
    const bindings = Object.fromEntries(
      (node.bindings ?? []).map((b) => [b.name, render(b.node)]),
    );
    return source.replace(
      /\{\{([a-zA-Z][a-zA-Z0-9_]*)\}\}/g,
      (_, name: string) => {
        if (!Object.prototype.hasOwnProperty.call(bindings, name))
          throw new Error(`Missing prompt binding: ${name}`);
        return bindings[name]!;
      },
    );
  }
  return render(definition.body);
}
