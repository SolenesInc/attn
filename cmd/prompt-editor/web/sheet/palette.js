import { el } from "./dom.js";

export function palette({ items, open }) {
  const input = el("input", { type: "search", class: "palette-input", "aria-label": "Open a situation, scenario, event or file", placeholder: "Open a situation, scenario, event or file", autocomplete: "off", spellcheck: "false" });
  const list = el("ul", { class: "palette-list", role: "listbox" });
  const dialog = el("div", { class: "palette", role: "dialog", "aria-label": "Open", hidden: true },
    el("div", { class: "palette-box" }, input, list,
      el("div", { class: "palette-help" }, el("kbd", {}, "↑↓"), " move ", el("kbd", {}, "Enter"), " open ", el("kbd", {}, "Esc"), " close · unsaved edits stay when you switch")));
  let shown = [];
  let active = 0;
  let returnFocus = null;

  function draw() {
    const words = input.value.toLowerCase().split(/\s+/).filter(Boolean);
    shown = items().filter((item) => words.every((word) => item.search.includes(word))).slice(0, 60);
    active = Math.min(active, Math.max(shown.length - 1, 0));
    list.replaceChildren(...shown.map((item, index) => el("li", {
      role: "option",
      "aria-selected": String(index === active),
      onmousedown: (event) => { event.preventDefault(); choose(index); },
    }, el("span", { class: "palette-kind" }, item.kind), el("span", { class: "palette-title" }, item.id), el("span", { class: "palette-description" }, item.description ?? ""))));
    list.children[active]?.scrollIntoView({ block: "nearest" });
  }

  function choose(index) {
    const item = shown[index];
    if (!item) return;
    close(false);
    open(item);
  }

  function close(restore = true) {
    dialog.hidden = true;
    if (restore) returnFocus?.focus?.();
  }

  input.addEventListener("input", () => { active = 0; draw(); });
  input.addEventListener("keydown", (event) => {
    if (event.key === "ArrowDown" || event.key === "ArrowUp") {
      event.preventDefault();
      active = Math.max(0, Math.min(shown.length - 1, active + (event.key === "ArrowDown" ? 1 : -1)));
      draw();
    } else if (event.key === "Enter") {
      event.preventDefault();
      choose(active);
    } else if (event.key === "Escape") {
      event.preventDefault();
      close();
    }
  });
  dialog.addEventListener("mousedown", (event) => { if (event.target === dialog) close(); });

  return {
    element: dialog,
    show() {
      returnFocus = document.activeElement;
      dialog.hidden = false;
      input.value = "";
      active = 0;
      draw();
      input.focus();
    },
  };
}
