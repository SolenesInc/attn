import { expect, test } from "bun:test";
import type { Theme } from "@earendil-works/pi-coding-agent";
import { pickPreset } from "../approval/permissions-ui";
import { presets, type PresetID } from "../approval/presets";

const theme = { fg: (_color: string, text: string) => text, bold: (text: string) => text } as Theme;
// pi-tui binds tui.select.confirm to "enter" and tui.select.cancel to "escape".
const enter = "\r";
const escape = "\x1b";

/** Opens the picker and sends one key, the way a person pressing it would. */
function press(current: PresetID | undefined, key: string) {
  const ctx = {
    ui: {
      custom: <T>(make: (tui: unknown, palette: Theme, keys: unknown, done: (value: T) => void) => {
        handleInput?: (data: string) => void;
      }) => new Promise<T>((resolve) => {
        const component = make({ requestRender: () => {} }, theme, {}, resolve);
        component.handleInput!(key);
      }),
    },
  };
  return pickPreset(ctx as never, current);
}

test("the picker opens on the current preset, so confirming it changes nothing", async () => {
  for (const preset of presets) {
    expect(await press(preset.id, enter)).toBe(preset);
  }
});

test("the picker opens on read-only when the pair matches no preset, and escape picks nothing", async () => {
  expect((await press(undefined, enter))?.id).toBe("read-only");
  expect(await press("full-access", escape)).toBeUndefined();
});
