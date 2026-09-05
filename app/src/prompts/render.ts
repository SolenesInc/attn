import catalog from "./catalog.generated.json";
import { renderCatalog } from "../../../internal/prompts/renderer";

export function renderPrompt(
  recipient: string,
  event: string,
  values: Record<string, string> = {},
): string {
  return renderCatalog(catalog, recipient, event, values);
}
