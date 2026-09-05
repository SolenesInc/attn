(() => {
  const key = "prompt-editor-theme";
  const system = window.matchMedia("(prefers-color-scheme: dark)");
  let preference = "system";
  try {
    const saved = localStorage.getItem(key);
    if (["light", "dark"].includes(saved)) preference = saved;
  } catch {
    // Appearance still works when the browser disables local storage.
  }
  function apply() {
    document.documentElement.dataset.theme = preference === "system"
      ? (system.matches ? "dark" : "light") : preference;
  }
  apply();
  system.addEventListener("change", apply);
  document.addEventListener("DOMContentLoaded", () => {
    const select = document.getElementById("theme");
    select.value = preference;
    select.addEventListener("change", () => {
      preference = select.value;
      apply();
      try {
        localStorage.setItem(key, preference);
      } catch {
        // Keep the selection for this page even when it cannot be saved.
      }
    });
  });
})();
