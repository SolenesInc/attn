import { EditorState, Transaction, Annotation, StateField } from "@codemirror/state";
import { EditorView, Decoration, MatchDecorator, ViewPlugin, WidgetType, keymap, drawSelection } from "@codemirror/view";
import { history, historyKeymap, defaultKeymap } from "@codemirror/commands";
import { changes } from "./merge.js";

export const placeholder = /\{\{([a-zA-Z][a-zA-Z0-9_]*)\}\}/g;

const fromStore = Annotation.define();

class Chip extends WidgetType {
  constructor(name, { value, nested } = {}) {
    super();
    this.name = name;
    this.value = value;
    this.nested = nested;
  }
  eq(other) {
    return other.name === this.name && other.value === this.value && other.nested === this.nested;
  }
  toDOM() {
    const chip = document.createElement("span");
    chip.className = "chip";
    chip.dataset.name = this.name;
    const name = document.createElement("span");
    name.className = "chip-name";
    name.textContent = this.name;
    chip.append(name);
    if (this.nested) {
      chip.classList.add("nested");
      chip.title = "Filled by the block below";
    } else if (this.value !== undefined) {
      const value = document.createElement("span");
      value.className = "chip-value";
      const oneLine = this.value.replace(/\s+/g, " ").trim();
      value.textContent = oneLine.length > 80 ? oneLine.slice(0, 79) + "…" : oneLine || "empty";
      if (!oneLine) value.classList.add("empty");
      chip.append(value);
      chip.title = this.value;
    }
    return chip;
  }
  ignoreEvent() {
    return false;
  }
}

class Removed extends WidgetType {
  constructor(text) {
    super();
    this.text = text;
  }
  eq(other) {
    return other.text === this.text;
  }
  toDOM() {
    const removed = document.createElement("div");
    removed.className = "cm-removed";
    removed.textContent = this.text;
    return removed;
  }
}

const addedLine = Decoration.line({ class: "cm-added" });

function changeMarks(base, doc) {
  const hunks = changes(base, doc.toString());
  const marks = [];
  for (const { removed, added: [from, to] } of hunks) {
    const at = from < doc.lines ? doc.line(from + 1).from : doc.length;
    if (removed.length) marks.push(Decoration.widget({ widget: new Removed(removed.join("\n")), block: true, side: from < doc.lines ? -1 : 1 }).range(at));
    for (let line = from; line < to && line < doc.lines; line++) {
      const start = doc.line(line + 1).from;
      marks.push(addedLine.range(start));
    }
  }
  return Decoration.set(marks, true);
}

function diffAgainst(base) {
  return StateField.define({
    create: (state) => changeMarks(base, state.doc),
    update: (marks, transaction) => transaction.docChanged ? changeMarks(base, transaction.state.doc) : marks,
    provide: (field) => EditorView.decorations.from(field),
  });
}

function chips(chip) {
  const matcher = new MatchDecorator({
    regexp: placeholder,
    decoration: (match) => Decoration.replace({ widget: new Chip(match[1], chip(match[1])) }),
  });
  const plugin = ViewPlugin.fromClass(class {
    constructor(view) {
      this.chips = matcher.createDeco(view);
    }
    update(update) {
      this.chips = matcher.updateDeco(update, this.chips);
    }
  }, { decorations: (plugin) => plugin.chips });
  return [plugin, EditorView.atomicRanges.of((view) => view.plugin(plugin)?.chips ?? Decoration.none)];
}

export function fragmentEditor({ parent, text, chip, base, onChange, navigation = [], label }) {
  const view = new EditorView({
    parent,
    state: EditorState.create({
      doc: text,
      extensions: [
        history(),
        drawSelection({ cursorBlinkRate: 0 }),
        EditorView.lineWrapping,
        keymap.of([...navigation, ...historyKeymap, ...defaultKeymap]),
        chip ? chips(chip) : [],
        base === undefined ? [] : diffAgainst(base),
        EditorView.contentAttributes.of({ "aria-label": label, spellcheck: "false", "data-stop": "" }),
        EditorView.updateListener.of((update) => {
          if (update.docChanged && !update.transactions.some((t) => t.annotation(fromStore))) onChange?.(update.state.doc.toString());
        }),
      ],
    }),
  });
  return view;
}

export function replaceText(view, text) {
  const current = view.state.doc.toString();
  if (current === text) return;
  let start = 0;
  while (start < current.length && start < text.length && current[start] === text[start]) start++;
  let end = 0;
  while (end < current.length - start && end < text.length - start && current[current.length - 1 - end] === text[text.length - 1 - end]) end++;
  view.dispatch({
    changes: { from: start, to: current.length - end, insert: text.slice(start, text.length - end) },
    annotations: [fromStore.of(true), Transaction.addToHistory.of(false)],
  });
}
