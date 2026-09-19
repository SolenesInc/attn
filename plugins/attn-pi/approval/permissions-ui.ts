import { DynamicBorder, type ExtensionContext } from "@earendil-works/pi-coding-agent";
import { Container, type SelectItem, SelectList, Text } from "@earendil-works/pi-tui";
import { presetByID, presets, type Preset, type PresetID } from "./presets";

/** pi's own ctx.ui.select takes plain strings, so the presets get a SelectList,
 * which is the one list component that renders a description beside each label. */
export async function pickPreset(ctx: ExtensionContext, current: PresetID | undefined): Promise<Preset | undefined> {
  const items: SelectItem[] = presets.map((preset) => ({
    value: preset.id,
    label: preset.id === current ? `${preset.label} (current)` : preset.label,
    description: preset.description,
  }));
  const chosen = await ctx.ui.custom<string | undefined>((tui, theme, _keys, done) => {
    const container = new Container();
    container.addChild(new DynamicBorder((line) => theme.fg("border", line)));
    container.addChild(new Text(theme.fg("accent", theme.bold("Choose what the agent is allowed to do"))));
    const list = new SelectList(items, items.length, {
      selectedPrefix: (text) => theme.fg("accent", text),
      selectedText: (text) => theme.fg("accent", text),
      description: (text) => theme.fg("muted", text),
      scrollInfo: (text) => theme.fg("dim", text),
      noMatch: (text) => theme.fg("warning", text),
    });
    // Enter confirms whatever is selected, so the list must open on the current
    // preset or a confirming keypress silently switches the session to read-only.
    const at = items.findIndex((item) => item.value === current);
    if (at >= 0) list.setSelectedIndex(at);
    list.onSelect = (item) => done(item.value);
    list.onCancel = () => done(undefined);
    container.addChild(list);
    container.addChild(new Text(theme.fg("dim", "↑↓ · Enter apply · Esc cancel")));
    container.addChild(new DynamicBorder((line) => theme.fg("border", line)));
    return {
      render: (width: number) => container.render(width),
      invalidate: () => container.invalidate(),
      handleInput: (data: string) => { list.handleInput(data); tui.requestRender(); },
    };
  });
  return chosen === undefined ? undefined : presetByID(chosen);
}
